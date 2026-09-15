// Command web is goodall's web chat preview: a small net/http server over a
// [chat.Service], and one embedded page that streams an answer as it arrives,
// shows thinking and tool calls, stops a run, and catches up after a reload.
//
// It is a preview in the sense the project means: every event and every state
// renders somewhere a human can look. It is also the shape a real back end
// takes — the browser is handed a redacted view and a stream of events, never
// a thread, a conversation or the system prompt.
//
// Run it with a key in the environment:
//
//	source .env
//	go run ./examples/web
//	go run ./examples/web -provider openrouter -addr 127.0.0.1:9000
//
// The keys are ANTHROPIC_API_KEY and OPENROUTER_API_KEY. The repository's
// .env is written in shell form for exactly this: source it and run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/openrouter"
)

// The model each provider answers with when -model is not given.
const (
	// defaultAnthropicModel is Anthropic's newest model as of 2026-09-15,
	// confirmed against GET /v1/models/claude-sonnet-5 that day: adaptive
	// thinking, a 1,000,000-token context, a 128,000-token output limit.
	defaultAnthropicModel = "claude-sonnet-5"
	// defaultOpenRouterModel is the same model on OpenRouter, in
	// OpenRouter's own "vendor/model" spelling as its catalog listed it
	// on 2026-09-15, so that switching providers changes the route and not
	// the correspondent; pass -model to name any other of OpenRouter's
	// models. An example imports only the public packages, so this is a
	// deliberate copy of what internal/livemodel records.
	defaultOpenRouterModel = "anthropic/claude-sonnet-5"
)

// defaultSystem is the standing instruction, which the page never sees: the
// view announces a placeholder where it would be, and nothing carries the
// text. Overriding it with -system is how to watch that hold.
const defaultSystem = "You are a helpful assistant in a small web chat. Keep answers short. " +
	"Call the clock tool whenever an answer depends on the current date or time."

// shutdownGrace is how long an interrupted server waits for the runs in
// flight to end and persist, and then for the responses to finish.
const shutdownGrace = 20 * time.Second

// providerName is the -provider flag's vocabulary.
type providerName string

const (
	// providerAnthropic talks to the Anthropic Messages API.
	providerAnthropic providerName = "anthropic"
	// providerOpenRouter talks to OpenRouter's Chat Completions API.
	providerOpenRouter providerName = "openrouter"
)

func main() {
	log.SetFlags(log.Ltime)
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run is main with an error return, so every failure leaves through one door.
func run() error {
	provider := flag.String("provider", string(providerAnthropic), "which model API to use: anthropic or openrouter")
	model := flag.String("model", "", "the provider's model identifier; empty means the provider's default")
	system := flag.String("system", defaultSystem, "the agent's standing instruction, which the page never receives")
	addr := flag.String("addr", "127.0.0.1:8080", "the address to listen on")
	flag.Usage = usage
	flag.Parse()

	agent, err := newAgent(providerName(*provider), *model, *system)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	svc := chat.NewService(agent, chat.NewMemoryStore(), chat.WithLogger(logger))

	// No WriteTimeout: an event stream is a response that lasts as long as
	// the answer does, and a write deadline would cut it off mid-run.
	srv := &http.Server{
		Addr:              *addr,
		Handler:           newHandler(svc),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return listenAndDrain(srv, svc, agent)
}

// listenAndDrain runs the server until it fails or the process is interrupted, then
// drains: the runs in flight end and persist first, so a browser watching an
// answer sees it finish, and the responses are closed after them.
func listenAndDrain(srv *http.Server, svc *chat.Service, agent *goodall.Agent) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	log.Printf("goodall web chat on %s, answering with %s", srv.Addr, agent.Model)

	select {
	case err := <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// A second interrupt reaches the process itself, so an
		// impatient person is never stuck waiting for the drain.
		stop()
		log.Print("interrupted: ending the runs in flight")
	}

	drain, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := svc.Shutdown(drain); err != nil {
		log.Printf("the runs did not all finish: %v", err)
	}
	return srv.Shutdown(drain)
}

// newAgent builds the agent the whole server shares: one provider, one model,
// one standing instruction and the preview's one tool. An Agent holds no
// per-run state, so this one value answers every thread.
func newAgent(name providerName, model, system string) (*goodall.Agent, error) {
	provider, model, err := newProvider(name, model)
	if err != nil {
		return nil, err
	}
	clock, err := newClockTool()
	if err != nil {
		return nil, err
	}
	return &goodall.Agent{
		Provider: provider,
		Model:    model,
		System:   system,
		Tools:    []goodall.Tool{clock},
	}, nil
}

// newProvider builds the named provider from the environment, and answers the
// model to use with it: the one asked for, or that provider's default.
func newProvider(name providerName, model string) (goodall.Provider, string, error) {
	switch name {
	case providerAnthropic:
		key, err := apiKey("ANTHROPIC_API_KEY")
		if err != nil {
			return nil, "", err
		}
		return anthropic.New(key), orDefault(model, defaultAnthropicModel), nil
	case providerOpenRouter:
		key, err := apiKey("OPENROUTER_API_KEY")
		if err != nil {
			return nil, "", err
		}
		return openrouter.New(openrouter.WithAPIKey(key)), orDefault(model, defaultOpenRouterModel), nil
	default:
		return nil, "", fmt.Errorf("unknown provider %q: the choices are %s and %s", name, providerAnthropic, providerOpenRouter)
	}
}

// apiKey reads one key from the environment, saying how to supply it when it
// is missing rather than failing at the first request.
func apiKey(name string) (string, error) {
	key := os.Getenv(name)
	if key == "" {
		return "", fmt.Errorf("%s is not set: run `source .env` first, or export the key yourself", name)
	}
	return key, nil
}

// orDefault is the value, or the fallback when it is empty.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// usage is the -h text, which says how the keys get here.
func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "web is goodall's web chat preview: a chat service behind six HTTP routes and one page.\n\n")
	fmt.Fprintf(out, "Usage:\n  source .env && go run ./examples/web [flags]\n\n")
	fmt.Fprintf(out, "The key comes from the environment: ANTHROPIC_API_KEY or OPENROUTER_API_KEY.\n")
	fmt.Fprintf(out, "The repository's .env is shell-form, so `source .env` is all it takes.\n\n")
	fmt.Fprintf(out, "Flags:\n")
	flag.PrintDefaults()
}
