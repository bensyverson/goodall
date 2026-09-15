// Command cli is goodall's terminal chat preview: one thread, one provider,
// and every event the agent loop produces rendered somewhere a human can look
// at it — streamed text, the model's thinking, each tool call as it starts and
// ends, and what the turn cost.
//
// It is also the smallest complete consumer of the library, so it imports only
// goodall's public packages and keeps its own logic to two things: this file,
// which is flags, a provider and the signal wiring, and the read-eval loop in
// repl.go, which takes a reader and a writer and can therefore be tested.
//
// Usage:
//
//	source .env                       # ANTHROPIC_API_KEY, OPENROUTER_API_KEY
//	go run ./examples/cli -provider anthropic
//	go run ./examples/cli -provider openrouter -thinking high
//
// Type a line and press return to send it. Ctrl-C while the answer is
// streaming stops it and keeps what had arrived; Ctrl-C at the prompt, or
// end-of-file, exits.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/chat"
	"github.com/bensyverson/goodall/openrouter"
)

// providerName is which API the session talks to. It is a typed constant
// because it decides three things at once — the client, the key and the
// default model — and a misspelt string should not reach any of them.
type providerName string

const (
	// anthropicProvider is the Anthropic Messages API.
	anthropicProvider providerName = "anthropic"
	// openRouterProvider is OpenRouter's Chat Completions API.
	openRouterProvider providerName = "openrouter"
)

// The default model for each provider, as of 2026-09-15.
//
// Anthropic's is claude-sonnet-5, the model the live tests and the fixture
// recorder use (internal/livemodel). OpenRouter's is the same model under the
// name the OpenRouter catalogue listed it by on 2026-09-15, so that switching
// providers changes the route and not the correspondent. An example may not
// import internal/livemodel, so these are deliberate copies.
const (
	defaultAnthropicModel  = "claude-sonnet-5"
	defaultOpenRouterModel = "anthropic/claude-sonnet-5"
)

// The environment variables each provider's key is read from. The keys are
// never flags: a key on a command line is a key in the shell history.
const (
	anthropicKeyEnv  = "ANTHROPIC_API_KEY"
	openRouterKeyEnv = "OPENROUTER_API_KEY"
)

// defaultSystem is the standing instruction. It is short on purpose: the
// example is about the loop, and a long prompt would be the part a reader
// copied. It says why the tool exists rather than ordering the model to use
// it, because a rule that carries its reason survives the cases it does not
// name.
const defaultSystem = "You are answering in a terminal chat, so keep replies to a few plain-text sentences. " +
	"Random numbers have to come from a real roll rather than from you, which is what the roll_dice tool is for."

// defaultEffort is the -thinking flag's default, spelled the way
// goodall.Effort spells its zero value.
const defaultEffort = "default"

// shutdownGrace is how long the service is given to persist the run in flight
// when the session ends.
const shutdownGrace = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cli:", err)
		os.Exit(1)
	}
}

// run is main with errors, so every failure leaves through one place.
func run() error {
	provider := flag.String("provider", string(anthropicProvider), "which API to talk to: anthropic or openrouter")
	model := flag.String("model", "", "the model id; the default depends on -provider")
	system := flag.String("system", defaultSystem, "the system prompt")
	thinking := flag.String("thinking", defaultEffort, "how hard the model should think: default, off, low, medium, high, xhigh or max")
	flag.Usage = usage
	flag.Parse()

	effort, err := parseEffort(*thinking)
	if err != nil {
		return err
	}
	client, defaultModel, err := newProvider(providerName(*provider))
	if err != nil {
		return err
	}
	if *model == "" {
		*model = defaultModel
	}
	tool, err := newDiceTool()
	if err != nil {
		return err
	}

	agent := &goodall.Agent{
		Provider: client,
		Model:    *model,
		System:   *system,
		Tools:    []goodall.Tool{tool},
		Thinking: thinkingFor(effort),
		// Warnings are the ones worth a terminal: a model catalogue the
		// run could not read, which lets the run go ahead unchecked.
		Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	svc := chat.NewService(agent, chat.NewMemoryStore())
	ctx := context.Background()
	thread, err := svc.Create(ctx)
	if err != nil {
		return fmt.Errorf("creating the thread: %w", err)
	}

	interrupt, stopSignals := interrupts()
	defer stopSignals()

	fmt.Printf("goodall chat: %s, %s, thinking %s. Ctrl-C stops an answer; Ctrl-C at the prompt exits.\n",
		*provider, *model, effort)
	loopErr := Loop(ctx, svc, os.Stdin, os.Stdout, Options{ThreadID: thread.ID, Prompt: "> ", Interrupt: interrupt})

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := svc.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting the service down: %w", err)
	}
	return loopErr
}

// newProvider builds the client for a provider name, with the key from the
// environment, and reports that provider's default model. A missing key says
// which variable is empty and how the repository's own keys are loaded.
func newProvider(name providerName) (goodall.Provider, string, error) {
	switch name {
	case anthropicProvider:
		key := os.Getenv(anthropicKeyEnv)
		if key == "" {
			return nil, "", missingKey(anthropicKeyEnv)
		}
		return anthropic.New(key), defaultAnthropicModel, nil
	case openRouterProvider:
		key := os.Getenv(openRouterKeyEnv)
		if key == "" {
			return nil, "", missingKey(openRouterKeyEnv)
		}
		return openrouter.New(openrouter.WithAPIKey(key)), defaultOpenRouterModel, nil
	}
	return nil, "", fmt.Errorf("unknown provider %q; it is %s or %s", name, anthropicProvider, openRouterProvider)
}

// missingKey is the error for an empty key variable, which says how to fill
// it: the repository keeps its keys in a shell-form .env for exactly this.
func missingKey(variable string) error {
	return fmt.Errorf("%s is not set; run `source .env` first, or export the key", variable)
}

// parseEffort reads the -thinking flag as a goodall.Effort, spelling the zero
// value "default" the way the type does.
func parseEffort(name string) (goodall.Effort, error) {
	effort := goodall.Effort(name)
	if name == defaultEffort {
		effort = goodall.EffortDefault
	}
	if !effort.Known() {
		return "", fmt.Errorf("unknown -thinking value %q; it is one of default, off, low, medium, high, xhigh or max", name)
	}
	return effort, nil
}

// thinkingFor is the thinking configuration for an effort: a session that is
// thinking at all asks for the summaries too, because thinking nobody can see
// is not worth a pane in a terminal.
func thinkingFor(effort goodall.Effort) goodall.ThinkingConfig {
	cfg := goodall.ThinkingConfig{Effort: effort}
	if effort != goodall.EffortOff {
		cfg.Display = goodall.DisplaySummarized
	}
	return cfg
}

// interrupts turns Ctrl-C into the loop's interrupt channel and hands back
// the func that stops listening. The channel holds one signal: a second
// Ctrl-C while the first is still being acted on is the same request, not a
// second one.
func interrupts() (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	interrupt := make(chan struct{}, 1)
	go func() {
		for range signals {
			select {
			case interrupt <- struct{}{}:
			default:
			}
		}
	}()
	// Closing after Stop ends the forwarding goroutine; the runtime sends
	// nothing more once the channel is unregistered.
	return interrupt, func() { signal.Stop(signals); close(signals) }
}

// usage is the -h text, which names the environment the example needs.
func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, "Usage: go run ./examples/cli [flags]\n\n")
	fmt.Fprintf(out, "A one-thread chat in the terminal, over goodall's chat service.\n")
	fmt.Fprintf(out, "The key comes from %s or %s; `source .env` loads them.\n\n", anthropicKeyEnv, openRouterKeyEnv)
	fmt.Fprintf(out, "Flags:\n")
	flag.PrintDefaults()
	fmt.Fprintf(out, "\nDefault models: %s for %s, %s for %s.\n",
		defaultAnthropicModel, anthropicProvider, defaultOpenRouterModel, openRouterProvider)
}
