package main

import (
	json "encoding/json/v2"
	"errors"
	"log"
	"net/http"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/chat"
)

// maxSendBytes caps the body of a send. A chat message is a line of text; a
// megabyte is far past generous and keeps a stray upload from being buffered.
const maxSendBytes = 1 << 20

// server is the example's HTTP surface over a [chat.Service]. It holds
// nothing else: the service owns the threads, the runs and the agent, so a
// handler is a translation from a request to one call on it and back.
type server struct {
	svc *chat.Service
}

// newHandler is the whole web preview as an [net/http.Handler], so the same
// routes a browser talks to are what a test drives through httptest.
//
// The routes are the four verbs of a chat back end plus the page:
//
//	GET  /                      the page, embedded in the binary
//	POST /threads               create a thread, answering its view
//	GET  /threads/{id}          the thread's redacted view
//	POST /threads/{id}/send     start a run, answering its id
//	GET  /threads/{id}/events   the run's events, as Server-Sent Events
//	POST /threads/{id}/stop     cancel the run in flight
//
// Nothing a route answers is a [chat.Thread] or a [goodall.Conversation]: the
// page sees the redacted [chat.ThreadView] and the run's events, which is the
// back-end-owns-threads shape the chat layer was built for.
func newHandler(svc *chat.Service) http.Handler {
	s := &server{svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("POST /threads", s.create)
	mux.HandleFunc("GET /threads/{id}", s.view)
	mux.HandleFunc("POST /threads/{id}/send", s.send)
	mux.HandleFunc("GET /threads/{id}/events", s.events)
	mux.HandleFunc("POST /threads/{id}/stop", s.stop)
	return mux
}

// create makes a thread and answers its view, so the page has the shape it
// will keep rendering from the moment it has an id.
func (s *server) create(w http.ResponseWriter, r *http.Request) {
	thread, err := s.svc.Create(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	view, err := s.svc.View(r.Context(), thread.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// view answers the thread's redacted view: what is stored, which is every
// finished run. A run in flight is watched on the events route instead.
func (s *server) view(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.View(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// sendRequest is the page's message: one line of text. Media would arrive
// here too, as a second member; the view already has a place to render it.
type sendRequest struct {
	Text string `json:"text"`
}

// sendResponse is what starting a run answers: its id, for a log or a future
// route that acts on one run.
type sendResponse struct {
	RunID string `json:"run_id"`
}

// send appends the page's line to the thread and starts a run to answer it.
func (s *server) send(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := json.UnmarshalRead(http.MaxBytesReader(w, r.Body, maxSendBytes), &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "the body must be {\"text\": \"…\"}"})
		return
	}
	if req.Text == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "the message is empty"})
		return
	}

	run, err := s.svc.Send(r.Context(), r.PathValue("id"), goodall.Text{Text: req.Text})
	if err != nil {
		writeError(w, err)
		return
	}
	// The run's own event stream is deliberately not read here. The run
	// belongs to the service and outlives this request; the browser
	// attaches to it through the events route, which replays what it
	// missed. Leaving this subscription unread costs one bounded buffer,
	// which the service drops as soon as it fills.
	writeJSON(w, http.StatusOK, sendResponse{RunID: run.ID})
}

// events streams the thread's run as Server-Sent Events: what the run has
// already produced, then its live tail, ending with the run's one terminal
// event — which is what tells the page to close its EventSource, since an
// EventSource reconnects to any stream that merely ends.
//
// The stream is redacted on the way out, as the view route's thread is: the
// browser is handed which tool ran and whether it failed, never what was
// passed to it, what it returned, or the conversation the terminal event
// carries.
//
// A thread with no run in flight, and a thread the store does not hold, both
// stream nothing and end at once: a subscription cannot tell them apart, and
// the page has already asked the view route about the id.
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// Send the headers before the first event, so a browser knows the
	// stream is open even while the model is still thinking. A writer that
	// cannot be flushed buffers instead, which is slow but not wrong.
	_ = http.NewResponseController(w).Flush()

	stream := chat.Redact(s.svc.Subscribe(r.Context(), r.PathValue("id")), s.svc.ViewOptions())
	if err := chat.WriteSSE(w, stream); err != nil {
		// The only error is a failure to write: the client has gone.
		// The run is untouched and keeps going.
		log.Printf("the event stream for thread %s ended: %v", r.PathValue("id"), err)
	}
}

// stop cancels the thread's run. The loop keeps the partial answer, so the
// thread persists whole and the page's next reload shows how far it got.
func (s *server) stop(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Stop(r.PathValue("id")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// errorResponse is what every failing route answers, so the page has one
// shape to read.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSON answers one value.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, value); err != nil {
		// The status line is already out, so there is nowhere left to
		// report this but the log.
		log.Printf("writing a %d response: %v", status, err)
	}
}

// writeError maps the chat layer's sentinels onto status codes. Anything else
// is a fault of this server rather than of the request, and is logged here
// and reported as a bare 500: a message from an unexpected error is written
// by code that never meant it for a browser.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chat.ErrThreadNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such thread"})
	case errors.Is(err, chat.ErrThreadBusy):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the thread is already answering"})
	case errors.Is(err, chat.ErrThreadIdle):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the thread has nothing to stop"})
	case errors.Is(err, chat.ErrServiceClosed):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "the server is shutting down"})
	default:
		log.Printf("unexpected failure: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "something went wrong"})
	}
}
