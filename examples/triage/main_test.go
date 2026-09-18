package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/bensyverson/goodall/examples/triage/mailbox"
)

// TestEverySourceNamesTheReaderItOpens guards the one cast in options.open: a
// -source is handed to the mailbox package as a Kind, so a name that drifted
// would refuse a real store at the moment somebody pointed the example at one.
func TestEverySourceNamesTheReaderItOpens(t *testing.T) {
	for source, kind := range map[sourceKind]mailbox.Kind{
		sourceApple:   mailbox.KindApple,
		sourceMaildir: mailbox.KindMaildir,
		sourceMbox:    mailbox.KindMbox,
	} {
		if string(source) != string(kind) {
			t.Errorf("-source %q names the reader %q", source, kind)
		}
	}
}

// TestAskingForHelpIsNotAFailure keeps -h out of the error path: the flag
// package hands back ErrHelp once it has printed the usage, and a command that
// passed that on would exit non-zero for a question it answered.
func TestAskingForHelpIsNotAFailure(t *testing.T) {
	if _, err := parseOptions([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parsing -h gave %v, want flag.ErrHelp", err)
	}
	if err := run([]string{"-h"}, io.Discard); err != nil {
		t.Errorf("asking for help failed the command: %v", err)
	}
}

// TestTheDefaultsRunOnTheBundledInbox is the promise the command makes: typed
// with no flags at all, it reads the sample that travels inside the binary.
func TestTheDefaultsRunOnTheBundledInbox(t *testing.T) {
	opts, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parsing no flags at all: %v", err)
	}
	if opts.Source != sourceSample {
		t.Errorf("the default source is %q, want %q", opts.Source, sourceSample)
	}
	if opts.Limit != defaultLimit || opts.Snippet != defaultSnippet {
		t.Errorf("the defaults are -n %d -snippet %d, want %d and %d", opts.Limit, opts.Snippet, defaultLimit, defaultSnippet)
	}
	if opts.Provider != anthropicProvider || opts.JudgeModel != defaultJudgeModel {
		t.Errorf("the defaults are -provider %q -judge-model %q", opts.Provider, opts.JudgeModel)
	}
	if opts.Instruction != defaultInstruction {
		t.Errorf("the default instruction is %q", opts.Instruction)
	}

	in, err := opts.open(t.Context())
	if err != nil {
		t.Fatalf("opening the bundled inbox: %v", err)
	}
	if len(in.Records) != sampleCount {
		t.Fatalf("the bundled inbox holds %d messages, want %d", len(in.Records), sampleCount)
	}
	if len(in.Skipped) != 0 {
		t.Errorf("the bundled inbox lost %d files: %+v", len(in.Skipped), in.Skipped)
	}
	for _, record := range in.Records {
		if len([]rune(record.Snippet)) > defaultSnippet {
			t.Errorf("%s carries a %d-rune snippet, over the %d the flag allows", record.ID, len([]rune(record.Snippet)), defaultSnippet)
		}
	}
	// The mail never enters the process whole: the snippet stops well
	// before the marker planted deep in the longest message.
	for _, record := range in.Records {
		if strings.Contains(record.Snippet, bodyMarker) || strings.Contains(record.Snippet, attachmentMarker) {
			t.Errorf("%s carries text from past the snippet limit or from an attachment", record.ID)
		}
	}
}

// TestTheInstructionIsThePositionalArgument keeps the everyday invocation one
// sentence after the command's name.
func TestTheInstructionIsThePositionalArgument(t *testing.T) {
	opts, err := parseOptions([]string{"-n", "5", "which of these can wait until Monday?"}, io.Discard)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	if opts.Instruction != "which of these can wait until Monday?" {
		t.Errorf("the instruction is %q", opts.Instruction)
	}
	if opts.Limit != 5 {
		t.Errorf("-n is %d, want 5", opts.Limit)
	}
}

func TestACommandLineThatCouldNotRunIsRefusedBeforeAnythingOpens(t *testing.T) {
	cases := map[string][]string{
		"an unknown source":     {"-source", "imap"},
		"a store with no path":  {"-source", "apple"},
		"an unknown provider":   {"-provider", "azure"},
		"no messages at all":    {"-n", "0"},
		"no snippet to judge":   {"-snippet", "0"},
		"an unpinned judge":     {"-judge-model", ""},
		"a maildir with a path": nil, // the control: this one must parse
	}
	for name, args := range cases {
		if args == nil {
			continue
		}
		if _, err := parseOptions(args, io.Discard); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := parseOptions([]string{"-source", "maildir", "-path", "/tmp/mail"}, io.Discard); err != nil {
		t.Errorf("a maildir with a path was refused: %v", err)
	}
}
