package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
)

// maxLineBytes is the longest line the loop will read. A pasted document is a
// reasonable thing to type at a chat prompt, so the limit is generous.
const maxLineBytes = 1 << 20

// Options are what the read-eval loop needs beyond the service it talks to.
type Options struct {
	// ThreadID is the thread every line is sent to, so the whole session
	// is one conversation.
	ThreadID string
	// Prompt is written before each line is read.
	Prompt string
	// Interrupt is Ctrl-C. A value that arrives while an answer is
	// streaming stops the run and returns to the prompt; a value that
	// arrives at the prompt ends the loop. A nil channel is a session
	// that cannot be interrupted, which is what a test that only types
	// wants.
	Interrupt <-chan struct{}
}

// Loop reads a line at a time from in, sends each as a turn on
// opts.ThreadID and writes the answer to out as it streams: text as it
// arrives, thinking marked, every tool call as it starts and ends, and a
// summary of what the turn cost.
//
// It returns when the input ends, when an interrupt arrives at the prompt, or
// when ctx is canceled. Stopping a run is not an error and does not end the
// loop: the partial answer stays in the thread and the next line continues
// from it. The service is not shut down here — the caller owns it.
func Loop(ctx context.Context, svc *chat.Service, in io.Reader, out io.Writer, opts Options) error {
	lines := readLines(ctx, in)
	for {
		fmt.Fprint(out, opts.Prompt)
		select {
		case <-ctx.Done():
			fmt.Fprintln(out)
			return nil
		case <-opts.Interrupt:
			fmt.Fprintln(out)
			return nil
		case line, ok := <-lines:
			if !ok {
				fmt.Fprintln(out)
				return nil
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			if err := turn(ctx, svc, out, opts, line); err != nil {
				return err
			}
		}
	}
}

// turn sends one line and prints the run that answers it, reading the stream
// to its end. The service persists the thread and frees it before it delivers
// the terminal event, so the next line can be sent the moment this returns.
func turn(ctx context.Context, svc *chat.Service, out io.Writer, opts Options, line string) error {
	run, err := svc.Send(ctx, opts.ThreadID, goodall.Text{Text: line})
	if err != nil {
		return fmt.Errorf("sending the line: %w", err)
	}

	// Ctrl-C during an answer stops the run rather than the program. The
	// watcher is released when the stream ends, so the next interrupt is
	// the one that leaves the prompt.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-opts.Interrupt:
			// ErrThreadIdle here means the run ended in the moment
			// between the key and the cancellation, which is the
			// answer arriving rather than a failure.
			_ = svc.Stop(opts.ThreadID)
		case <-finished:
		}
	}()

	p := newPrinter(out)
	for ev, err := range run.Events {
		if err != nil {
			p.fail(err)
			continue
		}
		p.event(ev)
	}
	return nil
}

// readLines reads in on its own goroutine, because the loop must stay able to
// answer an interrupt while a read is blocked on a terminal that nobody is
// typing at. The channel closes at the end of the input.
func readLines(ctx context.Context, in io.Reader) <-chan string {
	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLineBytes)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()
	return lines
}
