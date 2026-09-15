# goodall

A small Go library for programs where an AI model can use tools.

goodall is the agentic loop: a conversation with a model that can call the functions you give it, streamed as it happens, until the model has finished or a budget says stop. The core package has no dependencies beyond the standard library and talks to Anthropic and OpenRouter (and any OpenAI-compatible server) through provider packages that carry streaming, images and PDFs, extended thinking, tool schemas inferred from Go types, prompt caching and cost reporting. An optional `chat` package adds what a chat product needs on top: threads, a store, a service that owns runs so a phone can disconnect mid-answer, a redacted view for the front end, and SSE and NDJSON writers.

## Install

```sh
go get github.com/bensyverson/goodall
```

Go 1.27 or later. Keys are read from the environment; the repository's `.env` is in shell form so `source .env` loads them.

## Quick start: an agent with a tool

```go
weather, err := goodall.NewTool("get_weather", "Look up the current weather in a city.",
	func(ctx context.Context, in struct {
		City string `json:"city" desc:"the city to look up, such as Paris"`
	}) (goodall.ToolResult, error) {
		return goodall.TextResult(`{"temperature_c":18,"conditions":"light rain"}`), nil
	})
if err != nil {
	log.Fatal(err)
}

agent := &goodall.Agent{
	Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
	Model:    "claude-sonnet-5",
	System:   "You are a terse weather assistant.",
	Tools:    []goodall.Tool{weather},
	Thinking: goodall.ThinkingConfig{Effort: goodall.EffortLow, Display: goodall.DisplaySummarized},
}

var conv goodall.Conversation
for ev, _ := range agent.Run(ctx, conv, goodall.Text{Text: "Will it rain in Paris this afternoon?"}) {
	switch e := ev.(type) {
	case goodall.TextDelta:
		fmt.Print(e.Text)
	case goodall.ToolCallStart:
		fmt.Printf("\n[calling %s with %s]\n", e.ToolUse.Name, e.ToolUse.Input)
	case goodall.Done:
		conv = e.Result.Conversation // continue from here on the next Run
		fmt.Printf("\n(%d tokens)\n", goodall.TokensSpent(e.Result.Usage))
	case goodall.Stopped:
		fmt.Printf("\nstopped: %s\n", e.Message)
	}
}
```

The tool's JSON schema is inferred from the handler's input type. A run ends in exactly one `Done` or `Stopped` event carrying the conversation so far and the usage, on every path including cancellation. Swap `anthropic.New(...)` for `openrouter.New(openrouter.WithAPIKey(...))` and a model such as `anthropic/claude-sonnet-5` to run the same agent through OpenRouter; `openrouter.WithBaseURL` and `openrouter.WithDialect` point it at OpenAI or a local server. This example compiles as `ExampleAgent_Run` in the root package.

## Quick start: a chat back end

```go
agent := &goodall.Agent{
	Provider: openrouter.New(openrouter.WithAPIKey(os.Getenv("OPENROUTER_API_KEY"))),
	Model:    "anthropic/claude-sonnet-5",
	System:   "You are a friendly assistant.",
}
svc := chat.NewService(agent, chat.NewMemoryStore())
defer svc.Shutdown(ctx)

thread, err := svc.Create(ctx)
if err != nil {
	log.Fatal(err)
}
run, err := svc.Send(ctx, thread.ID, goodall.Text{Text: "Hello! What can you do?"})
if err != nil {
	log.Fatal(err)
}
for ev, _ := range run.Events {
	if d, ok := ev.(goodall.TextDelta); ok {
		fmt.Print(d.Text)
	}
}

view, err := svc.View(ctx, thread.ID) // no system prompt, no tool internals
```

The service owns the run: `Send` returns as soon as it starts, the run finishes and persists whether or not anyone is listening, `Subscribe` attaches a second client mid-answer and replays what it missed, `Stop` cancels and keeps the partial answer. An HTTP handler streams a thread with one line:

```go
chat.WriteSSE(w, svc.Subscribe(r.Context(), r.PathValue("id")))
```

Both compile as `ExampleService_Send` and `ExampleWriteSSE` in the `chat` package. Implement `chat.ThreadStore` to persist threads elsewhere; `chat/storetest` is the contract test.

## The examples

Two runnable previews live under `examples/`, both against the keys in `.env`:

```sh
source .env
go run ./examples/cli -provider anthropic      # a terminal chat with a dice tool
go run ./examples/web -provider openrouter     # a web chat on http://127.0.0.1:8080
```

In the terminal, type a line and press return. Ctrl-C while the answer is streaming stops it and keeps what had arrived, so the next line carries on from there; Ctrl-C at the prompt exits. `-model` names another model and `-thinking high` asks for more reasoning and shows its summary. The web page streams the answer over server-sent events and, reloaded mid-answer, catches up with the partial message and keeps streaming; it never receives the system prompt.

## What is in the box

| Package | What it is |
|---|---|
| `goodall` | Blocks, messages and conversations; tools with inferred schemas; the provider seam; the streaming event model; the agent loop with budgets, hooks, approval and resume; capability pre-flight against the model catalog |
| `goodall/anthropic` | The Messages API: streaming and blocking, thinking (adaptive and budget), images and PDFs, cache control, the models catalog |
| `goodall/openrouter` | Chat Completions with typed dialects for OpenRouter, OpenAI, LM Studio and generic servers: reasoning details round-tripped verbatim, cost reporting, the catalog with prices |
| `goodall/chat` | Threads, stores, the service that owns runs, the redacted view, SSE and NDJSON writers, a chat-safe Markdown subset |

## Reading further

- [project/2026-09-14-initial-vision.md](project/2026-09-14-initial-vision.md): the brief, what goodall is for and what it must do.
- [project/2026-09-14-research-findings.md](project/2026-09-14-research-findings.md): the verified facts about both provider APIs the code is built on.
- [project/2026-09-14-architecture-plan.md](project/2026-09-14-architecture-plan.md): the design, its invariants and every decision made while building, in place and dated.
- [project/backlog.md](project/backlog.md): what was decided against, and what would change that.
- `go test -short ./...` is the offline suite. With a `.env` present, a plain `go test ./...` also runs the live tests against both providers and spends real tokens.

## Author and license

Ben Syverson. MIT, see [LICENSE](LICENSE).
