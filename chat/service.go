package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bensyverson/goodall"
)

// ErrThreadBusy is a second run asked for on a thread that already has one.
// A thread has one active run (invariant 12), because two runs appending to
// one history would interleave turns that the model must see in order.
var ErrThreadBusy = errors.New("chat: the thread already has a run in flight")

// ErrThreadIdle is [Service.Stop] on a thread with nothing to stop. It is an
// error rather than a silent success so that a caller can tell "I stopped it"
// from "it had already finished" — the second means the answer is in the
// store.
var ErrThreadIdle = errors.New("chat: the thread has no run in flight")

// ErrServiceClosed is work asked for after [Service.Shutdown]. Reading a
// thread still works; starting a run does not.
var ErrServiceClosed = errors.New("chat: the service is shut down")

// ErrNotResolvable is [Service.Resolve] on a thread that is not waiting for
// tool results: its conversation does not end in a model turn that asked for
// them. The wrapped message names what the thread does end in.
var ErrNotResolvable = errors.New("chat: the thread is not waiting for tool results")

// ErrSubscriberOverflow ends a subscription that stopped reading for long
// enough to fill its buffer. The run is never blocked by a subscriber, so a
// client that cannot keep up is dropped instead; it subscribes again, which
// replays the run so far.
var ErrSubscriberOverflow = errors.New("chat: the subscriber fell too far behind and was dropped")

// DefaultSubscriberBuffer is how many events a subscriber may fall behind by
// before it is dropped. It is generous, because the cost of buffering an
// event is a pointer and the cost of dropping a client is a visible stutter.
const DefaultSubscriberBuffer = 256

// Service runs an [goodall.Agent] against the threads of a [ThreadStore]. It
// is the chat back end: one value, shared by every request, that owns the runs
// in flight.
//
// A Service is safe for concurrent use once constructed. It holds no per-run
// state beyond the table of runs in flight, and the Agent it was given holds
// none at all, so one agent serves every thread.
type Service struct {
	agent  *goodall.Agent
	store  ThreadStore
	logger *slog.Logger
	buffer int

	// ctx is the lifetime of every run the service starts. A run is
	// deliberately not derived from the context of the call that started
	// it: the caller's context governs the call, and the run outlives it.
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	runs   map[string]*activeRun
	closed bool
	wg     sync.WaitGroup
}

// ServiceOption configures a [Service] at construction.
type ServiceOption func(*serviceConfig)

// serviceConfig is what the options write into, so NewService can apply them
// before deciding what the service ends up holding.
type serviceConfig struct {
	logger    *slog.Logger
	buffer    int
	budget    goodall.Budget
	hasBudget bool
}

// WithLogger records what the service does to its runs: a run started, a run
// ended, a thread that could not be persisted. A service with no logger logs
// nothing, which is the default.
func WithLogger(logger *slog.Logger) ServiceOption {
	return func(c *serviceConfig) { c.logger = logger }
}

// WithBudget overrides the agent's own budget for every run the service
// starts, which is how a chat back end caps a single user message without
// giving the agent a second identity. The agent itself is left untouched.
func WithBudget(budget goodall.Budget) ServiceOption {
	return func(c *serviceConfig) { c.budget, c.hasBudget = budget, true }
}

// WithSubscriberBuffer sets how many events a subscriber may fall behind by
// before it is dropped with [ErrSubscriberOverflow]. A value below one leaves
// [DefaultSubscriberBuffer] in place.
func WithSubscriberBuffer(events int) ServiceOption {
	return func(c *serviceConfig) { c.buffer = events }
}

// NewService is a chat back end over an agent and a store. Both are required;
// passing nil for either is a programming error and panics, because a service
// that discovers it at the first request has already accepted work it cannot
// do.
func NewService(agent *goodall.Agent, store ThreadStore, opts ...ServiceOption) *Service {
	if agent == nil {
		panic("chat: NewService needs an agent")
	}
	if store == nil {
		panic("chat: NewService needs a store")
	}
	cfg := serviceConfig{buffer: DefaultSubscriberBuffer}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.buffer < 1 {
		cfg.buffer = DefaultSubscriberBuffer
	}
	if cfg.hasBudget {
		// An Agent holds no per-run state, so a copy with a different
		// budget is a whole agent and costs nothing to make.
		bounded := *agent
		bounded.Budget = cfg.budget
		agent = &bounded
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{
		agent:  agent,
		store:  store,
		logger: cfg.logger,
		buffer: cfg.buffer,
		ctx:    ctx,
		cancel: cancel,
		runs:   make(map[string]*activeRun),
	}
}

// Create is a new empty thread, persisted. Its id is a uuid v7, so a listing
// ordered by id is a listing ordered by age.
func (s *Service) Create(ctx context.Context) (*Thread, error) {
	thread := NewThread()
	if err := s.store.Put(ctx, thread); err != nil {
		return nil, err
	}
	return thread, nil
}

// Get is the stored thread, straight from the store. A thread with a run in
// flight reads as it was before the run started: the service persists once,
// when the run ends, and [Service.Subscribe] is how a caller watches the
// answer arrive.
func (s *Service) Get(ctx context.Context, threadID string) (*Thread, error) {
	return s.store.Get(ctx, threadID)
}

// Send appends the input to the thread and starts a run to answer it,
// returning the run as soon as it is registered: its id, and a stream of its
// events that was attached before the run began.
//
// The run is the service's, not the caller's: it is started on a goroutine
// with a context derived from the service's own lifetime, so it survives the
// request, the CLI invocation or the app that started it. ctx governs only
// this call — loading the thread and registering the run.
//
// The returned stream is what a caller on one goroutine reads. It is a
// subscription like any other — leaving it detaches and changes nothing about
// the run — but because it was attached before the first event, it sees the
// whole run however quickly the run finishes. A [Service.Subscribe] made after
// Send returns can miss a run that ended in between, since a finished run's
// events are not retained; that is the shape for a second request that
// attaches to a run somebody else started.
//
// A thread that already has a run in flight is refused with [ErrThreadBusy];
// a thread the store does not hold is refused with [ErrThreadNotFound]. When
// the run ends the service persists the thread — the conversation the run
// produced, and its usage and cost added to the thread's totals — whether or
// not anyone read the stream.
func (s *Service) Send(ctx context.Context, threadID string, input ...goodall.Block) (*Run, error) {
	if s.isClosed() {
		return nil, ErrServiceClosed
	}
	thread, err := s.store.Get(ctx, threadID)
	if err != nil {
		return nil, err
	}
	conv := thread.Conversation
	return s.begin(thread, func(runCtx context.Context) goodall.Stream {
		return s.agent.Run(runCtx, conv, input...)
	})
}

// Resolve continues a run that paused for approval, answering the tool calls
// of the thread's last model turn with the given results. It is [Send] in
// every other respect: same ownership, same [ErrThreadBusy], same
// persistence.
//
// A thread whose conversation does not end in a model turn that asked for
// tools is refused with [ErrNotResolvable] rather than starting a run that
// could only fail. Which calls are waiting is on the terminal event's
// Result.Pending, and in the thread's last message.
func (s *Service) Resolve(ctx context.Context, threadID string, results ...goodall.ToolResult) (*Run, error) {
	if s.isClosed() {
		return nil, ErrServiceClosed
	}
	thread, err := s.store.Get(ctx, threadID)
	if err != nil {
		return nil, err
	}
	if err := resolvable(thread); err != nil {
		return nil, err
	}
	conv := thread.Conversation
	return s.begin(thread, func(runCtx context.Context) goodall.Stream {
		return s.agent.Resume(runCtx, conv, results...)
	})
}

// resolvable reports why a thread cannot be resumed, in the terms the caller
// needs: what it ends in, rather than that it is wrong.
func resolvable(thread *Thread) error {
	last, ok := thread.Conversation.Last()
	if !ok {
		return fmt.Errorf("chat: thread %q has no messages: %w", thread.ID, ErrNotResolvable)
	}
	if last.Role != goodall.RoleAssistant {
		return fmt.Errorf("chat: thread %q ends in a %s message: %w", thread.ID, last.Role, ErrNotResolvable)
	}
	if len(last.ToolUses()) == 0 {
		return fmt.Errorf("chat: thread %q ends in a model turn that asked for no tools: %w", thread.ID, ErrNotResolvable)
	}
	return nil
}

// Stop cancels the thread's run. The loop's cancellation path keeps the
// partial answer, gives every tool call a result and ends in one terminal
// event, so the thread persists whole and can be sent to again.
//
// It returns as soon as the cancellation is delivered, not when the run has
// ended: a caller that needs to know when the thread is settled subscribes,
// or reads the thread once the subscription ends. A thread with nothing in
// flight — including one that has never run, or one that finished a moment
// ago — answers [ErrThreadIdle].
func (s *Service) Stop(threadID string) error {
	s.mu.Lock()
	run := s.runs[threadID]
	s.mu.Unlock()
	if run == nil {
		return ErrThreadIdle
	}
	run.cancel()
	return nil
}

// Shutdown cancels every run in flight and returns when they have all ended
// and persisted what they produced, so a server can drain before it exits.
// After it is called, Send and Resolve answer [ErrServiceClosed]; Create, Get
// and Subscribe still reach the store and the runs that are winding down.
//
// It is idempotent. A ctx that expires first ends the wait and returns the
// context's error, leaving the runs to finish on their own.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()

	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// begin registers a run for the thread, attaches the starter's subscription
// and starts the run. start is called on the run's own goroutine with the
// run's context, so the stream is created there and belongs to nobody else.
//
// The subscription is attached before the goroutine exists, which is the
// whole point of returning it: nothing the run does can happen before the
// starter is listening.
func (s *Service) begin(thread *Thread, start func(context.Context) goodall.Stream) (*Run, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServiceClosed
	}
	if _, busy := s.runs[thread.ID]; busy {
		s.mu.Unlock()
		return nil, fmt.Errorf("chat: thread %q: %w", thread.ID, ErrThreadBusy)
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	run := newActiveRun(thread.ID, cancel, s.buffer)
	s.runs[thread.ID] = run
	s.wg.Add(1)
	s.mu.Unlock()

	sub := &subscriber{ch: make(chan goodall.Event, run.buffer)}
	backlog, live := run.attach(sub)
	go s.drive(runCtx, run, thread, start)
	return &Run{ID: run.id, Events: follow(context.Background(), run, sub, backlog, live)}, nil
}

// isClosed reports whether Shutdown has been called, which is what turns
// every request for new work away before it touches the store.
func (s *Service) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// log writes to the service's logger when it has one.
func (s *Service) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if s.logger == nil {
		return
	}
	s.logger.Log(ctx, level, msg, args...)
}
