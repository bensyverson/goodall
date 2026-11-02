// Command probe-agent-loop runs goodall's real agent loop against named models
// and reports whether each can do what an agent needs: think, call a tool, take
// the result back on a second turn and finish.
//
// The second turn is the point. Models that carry signed reasoning refuse a
// follow-up whose history lost it, so a single-turn check passes while every real
// agent run fails. This goes through [goodall.Agent] and the provider packages,
// the production path, and never hand-builds a request.
//
// Every run spends real money, a fraction of a cent per model at a low effort,
// which is why this is a script and not a test:
//
//	go run ./scripts/probe-agent-loop -env /abs/path/.env -models google/gemini-4-argon,google/gemini-3.8-flash
//	go run ./scripts/probe-agent-loop -env /abs/path/.env -dialect openai -models gpt-6-astra,gpt-6-luna
//	go run ./scripts/probe-agent-loop -env /abs/path/.env -dialect openai -effort off -models gpt-6-luna
//
// The figures it produced are in project/2026-11-02-model-currency-findings.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/liveprobe"
	"github.com/bensyverson/goodall/openrouter"
)

// runTimeout bounds one model's run.
const runTimeout = 3 * time.Minute

func main() {
	envPath := flag.String("env", ".env", "path to the .env holding the API keys")
	models := flag.String("models", "", "comma-separated model ids (required)")
	dialect := flag.String("dialect", "openrouter", "openrouter or openai")
	effort := flag.String("effort", string(goodall.EffortLow), "thinking effort")
	flag.Parse()
	if *models == "" {
		fatal(fmt.Errorf("-models is required"))
	}
	keys, err := dotenv.Load(*envPath)
	if err != nil {
		fatal(err)
	}
	var provider goodall.Provider
	switch *dialect {
	case "openrouter":
		provider = openrouter.New(openrouter.WithAPIKey(keys["OPENROUTER_API_KEY"]))
	case "openai":
		provider = openrouter.New(openrouter.WithAPIKey(keys["OPENAI_API_KEY"]),
			openrouter.WithBaseURL("https://api.openai.com/v1"), openrouter.WithDialect(openrouter.OpenAI))
	default:
		fatal(fmt.Errorf("unknown dialect %q", *dialect))
	}
	for model := range strings.SplitSeq(*models, ",") {
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		fmt.Printf("%-32s %s\n", model, liveprobe.AgentLoop(ctx, provider, model, goodall.Effort(*effort)))
		cancel()
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "probe-agent-loop:", err)
	os.Exit(1)
}
