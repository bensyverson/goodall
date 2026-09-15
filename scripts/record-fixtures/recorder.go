package main

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/bensyverson/goodall"
)

// fixturePrefix marks every file this tool writes, so a recorded fixture is
// distinguishable at a glance from the hand-authored ones beside it.
const fixturePrefix = "live_"

// capture is what one HTTP round trip left behind: the body that was sent,
// the status that came back and the bytes of the response.
//
// Headers are deliberately absent. The request's headers carry the API key,
// and a recorder that held them could write one into a file that is committed;
// keeping the struct free of them makes that impossible rather than unlikely.
type capture struct {
	Request  []byte
	Status   int
	Response []byte
}

// recordingTransport is the http.RoundTripper the recorder puts under the
// real provider client. It passes every call through to the network and keeps
// the bytes of the last one.
//
// It buffers the whole response before the client sees any of it, which
// defeats streaming — for a recorder that is the point, since the fixture is
// the complete byte stream and nothing here cares when it arrives. On a
// retried call the last attempt wins, which is the attempt whose answer the
// client actually returned.
type recordingTransport struct {
	base http.RoundTripper
	mu   sync.Mutex
	last capture
}

// RoundTrip sends the request and records it.
func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	sent, err := requestBody(req)
	if err != nil {
		return nil, err
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	t.mu.Lock()
	t.last = capture{Request: sent, Status: resp.StatusCode, Response: body}
	t.mu.Unlock()
	return resp, nil
}

// taken returns the last round trip and clears it, so a caller that finds an
// empty capture knows no call was made rather than reading a stale one.
func (t *recordingTransport) taken() capture {
	t.mu.Lock()
	defer t.mu.Unlock()
	last := t.last
	t.last = capture{}
	return last
}

// requestBody reads the body about to be sent without consuming it. The
// transport rebuilds the body for each attempt through GetBody, which is the
// copy to read; a request without one is read and replaced.
func requestBody(req *http.Request) ([]byte, error) {
	switch {
	case req.Body == nil:
		return nil, nil
	case req.GetBody != nil:
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		defer body.Close()
		return io.ReadAll(body)
	default:
		body, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		return body, nil
	}
}

// recorder writes captures into the fixture directory and reports what it
// wrote. One recorder serves a whole run.
type recorder struct {
	out       string
	transport *recordingTransport
	written   []string
}

// newRecorder builds a recorder writing under dir.
func newRecorder(dir string) *recorder {
	return &recorder{out: dir, transport: &recordingTransport{}}
}

// httpClient is the client to hand a provider. Its transport records; its
// zero timeout is what a long stream needs.
func (r *recorder) httpClient() *http.Client {
	return &http.Client{Transport: r.transport}
}

// save writes the last round trip as the named fixture. ext is the extension
// the response body earns — ".sse" for a streamed call, ".json" for a
// blocking one — and the request body is written beside it so a reader can
// see what produced the answer.
//
// Nothing but the bytes the API returned reaches the response file: no
// header, no status line, no key.
func (r *recorder) save(name, ext string) error {
	taken := r.transport.taken()
	if len(taken.Response) == 0 {
		return fmt.Errorf("%s: no response was captured, so there is nothing to record", name)
	}
	if err := r.writeFile(fixturePrefix+name+ext, taken.Response); err != nil {
		return err
	}
	return r.writeFile(fixturePrefix+name+".request.json", readable(taken.Request))
}

// writeFile puts one fixture on disk and remembers it for the summary.
func (r *recorder) writeFile(name string, data []byte) error {
	path := filepath.Join(r.out, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	r.written = append(r.written, name)
	return nil
}

// summary lists what the run wrote, in name order.
func (r *recorder) summary() []string {
	out := slices.Clone(r.written)
	slices.Sort(out)
	return out
}

// readable indents a request body so the committed file can be read without a
// tool. Member order is preserved, so the file still shows the byte-stable
// prefix the translation built; only the whitespace differs from what was
// sent. A body that is not JSON — there are none today — is written verbatim.
func readable(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	value := jsontext.Value(body)
	if err := value.Indent(jsontext.WithIndent("  ")); err != nil {
		return body
	}
	return append(value, '\n')
}

// stream makes one streamed call, records the server-sent event stream as
// live_<name>.sse and returns the collected response.
//
// The fixture is written whether or not the collection succeeded: a stream
// that broke halfway is exactly the sort of bytes the offline tests should
// have, and losing it would mean making the call again to see it.
func (r *recorder) stream(ctx context.Context, p goodall.Provider, name string, req *goodall.Request) (*goodall.Response, error) {
	resp, collectErr := p.Stream(ctx, req).Collect()
	if err := r.save(name, ".sse"); err != nil {
		return resp, err
	}
	reportUsage(name, resp)
	return resp, collectErr
}

// complete makes one blocking call and records the response body as
// live_<name>.json.
func (r *recorder) complete(ctx context.Context, c goodall.Completer, name string, req *goodall.Request) (*goodall.Response, error) {
	resp, callErr := c.Complete(ctx, req)
	if err := r.save(name, ".json"); err != nil {
		return resp, err
	}
	reportUsage(name, resp)
	return resp, callErr
}

// failing makes one blocking call that is expected to fail and records the
// error envelope as live_<name>.json. A call that succeeds is itself a
// failure: the recording exists to put a real error body on disk, and a
// fixture holding a successful answer would make the error tests assert
// nothing.
func (r *recorder) failing(ctx context.Context, c goodall.Completer, name string, req *goodall.Request) error {
	_, callErr := c.Complete(ctx, req)
	if callErr == nil {
		return fmt.Errorf("%s: the request meant to fail succeeded, so there is no error body to record", name)
	}
	if err := r.save(name, ".json"); err != nil {
		return err
	}
	apiErr, ok := errors.AsType[*goodall.APIError](callErr)
	if !ok {
		return fmt.Errorf("%s: the call failed with %v, which is not an API error and carries no envelope", name, callErr)
	}
	fmt.Printf("    %s: status=%d kind=%s type=%s\n", name, apiErr.Status, apiErr.Kind, apiErr.Type)
	return nil
}

// reportUsage prints what one call cost in tokens. Money is not printed:
// Anthropic reports no price on this API, and a figure this tool invented
// would be worse than none.
func reportUsage(name string, resp *goodall.Response) {
	if resp == nil {
		fmt.Printf("    %s: no response\n", name)
		return
	}
	u := resp.Usage
	fmt.Printf("    %s: stop=%s input=%d output=%d cache_read=%d cache_write=%d\n",
		name, resp.StopReason, u.Input, u.Output, u.CacheRead, u.CacheWrite)
}
