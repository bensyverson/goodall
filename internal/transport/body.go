package transport

import (
	"context"
	"io"
	"sync"
)

// contextBody binds a streaming response body to the context the request was
// made with. net/http gives no guarantee here that survives an injected
// RoundTripper: a recording or replaying transport has no connection to tear
// down, so a cancelled caller could otherwise read on forever from a body
// nobody is going to close. Wrapping makes the promise the transport's own.
type contextBody struct {
	ctx  context.Context
	body io.ReadCloser
	// stop releases the watcher when the caller closes the body first.
	stop chan struct{}
	once sync.Once
	err  error
}

// newContextBody wraps body so that reads honour ctx and cancellation closes
// it. A context that can never be cancelled needs no watcher, and gets none:
// an idle goroutine per stream is a cost worth avoiding, and a goroutine
// parked on a nil channel is not durably blocked, which would stall a
// synctest bubble.
func newContextBody(ctx context.Context, body io.ReadCloser) io.ReadCloser {
	done := ctx.Done()
	if done == nil {
		return body
	}
	b := &contextBody{ctx: ctx, body: body, stop: make(chan struct{})}
	go func() {
		select {
		case <-done:
			b.Close()
		case <-b.stop:
		}
	}()
	return b
}

// Read reports the context's error in place of anything the underlying body
// has to say once ctx is done, so a caller that cancelled sees why rather
// than the shrapnel — an unexpected EOF, a reset — that the cancellation
// produced downstream.
func (b *contextBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := b.body.Read(p)
	if err != nil && err != io.EOF {
		if ctxErr := b.ctx.Err(); ctxErr != nil {
			return n, ctxErr
		}
	}
	return n, err
}

// Close is idempotent: the caller and the cancellation watcher both call it,
// often at once, and either may be first.
func (b *contextBody) Close() error {
	b.once.Do(func() {
		close(b.stop)
		b.err = b.body.Close()
	})
	return b.err
}
