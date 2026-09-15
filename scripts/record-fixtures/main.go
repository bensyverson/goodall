// Command record-fixtures refreshes a provider's test fixtures from the live
// API, so the offline suite parses bytes the API really sent rather than
// bytes a person believed it would send.
//
// It reads the API key from a .env file (see internal/dotenv) and refuses to
// run without one, naming the key and the file it looked in. Every exchange
// writes two files under -out: the response, as live_<name>.sse for a
// streamed call or live_<name>.json for a blocking one, and the request body
// that produced it as live_<name>.request.json. Nothing but the bytes the API
// returned reaches the response file — no headers, so no key can land in a
// fixture.
//
// Usage:
//
//	go run ./scripts/record-fixtures -env /path/to/.env
//	go run ./scripts/record-fixtures -provider openrouter
//	go run ./scripts/record-fixtures -provider openrouter -only thinking_tool_use
//	go run ./scripts/record-fixtures -provider openai -list
//
// -out defaults to the chosen provider's own testdata directory, so a run that
// names a provider and nothing else writes where that provider's tests read.
//
// Adding a provider is one new file in this package: define a providerSpec
// with the provider's key, its default model, the testdata directory its
// fixtures belong in and a function that builds its exchanges against the
// recorder's http.Client, then add it to the providers table below. Nothing
// else in this package is provider-specific.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"

	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
)

// exchange is one recorded conversation. Running it makes one or more live
// calls and writes a fixture for each; its name is what -only selects.
type exchange struct {
	// name is the fixture name, without the live_ prefix and the
	// extension.
	name string
	// what says in one line which path through the provider it exercises,
	// printed as the run goes and by -list.
	what string
	// run makes the calls and saves the fixtures.
	run func(ctx context.Context) error
}

// providerSpec is everything this command needs to know about one provider.
type providerSpec struct {
	// key is the name of the .env entry holding the provider's API key.
	key string
	// defaultModel is the -model default: the newest model the live
	// catalogue confirms, shared with that provider's live tests through
	// internal/livemodel so the two cannot drift.
	defaultModel string
	// outDir is the -out default: the testdata directory of the package
	// whose tests read these fixtures, relative to the repository root. It
	// lives here rather than in the flag so that choosing a provider
	// chooses the right directory, and a run with no -out cannot write one
	// provider's recordings over another's.
	outDir string
	// exchanges builds the provider's recordings. It is given the
	// recorder, which owns the recording http.Client and writes the
	// files, the API key and the model to record against.
	exchanges func(rec *recorder, key, model string) ([]exchange, error)
}

// providers is the -provider table. One entry per provider; see the package
// comment for what adding one takes.
var providers = map[string]providerSpec{
	"anthropic": {
		key:          "ANTHROPIC_API_KEY",
		defaultModel: livemodel.Anthropic,
		outDir:       "anthropic/testdata",
		exchanges:    anthropicExchanges,
	},
	"openrouter": {
		key:          "OPENROUTER_API_KEY",
		defaultModel: livemodel.OpenRouterClaude,
		outDir:       "openrouter/testdata",
		exchanges:    openRouterExchanges,
	},
	"openai": {
		key:          "OPENAI_API_KEY",
		defaultModel: livemodel.OpenAI,
		outDir:       "openrouter/testdata",
		exchanges:    openAIExchanges,
	},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "record-fixtures:", err)
		os.Exit(1)
	}
}

// run is main with an error return, so every failure leaves by one path.
func run() error {
	provider := flag.String("provider", "anthropic", "which provider to record ("+strings.Join(providerNames(), ", ")+")")
	env := flag.String("env", ".env", "the .env file holding the provider's API key, relative to the working directory")
	out := flag.String("out", "", "the directory to write fixtures into (default: the chosen provider's own testdata directory)")
	model := flag.String("model", "", "the model to record against (default: the provider's newest confirmed model)")
	only := flag.String("only", "", "record just this one exchange, by name")
	list := flag.Bool("list", false, "print the exchange names this provider records and exit")
	flag.Parse()

	spec, ok := providers[*provider]
	if !ok {
		return fmt.Errorf("unknown provider %q; known providers are %s", *provider, strings.Join(providerNames(), ", "))
	}
	if *model == "" {
		*model = spec.defaultModel
	}
	if *out == "" {
		*out = spec.outDir
	}

	// The key is looked up before anything else so a run with no key fails
	// on the first line rather than after the first HTTP 401. -list needs
	// no key, since it makes no call.
	key, haveKey := dotenv.Lookup(*env, spec.key)
	if !haveKey && !*list {
		return fmt.Errorf("no %s in %s; recording needs a live key, and nothing was written", spec.key, *env)
	}

	rec := newRecorder(*out)
	built, err := spec.exchanges(rec, key, *model)
	if err != nil {
		return err
	}
	if *list {
		for _, ex := range built {
			fmt.Printf("%-26s %s\n", ex.name, ex.what)
		}
		return nil
	}

	if info, err := os.Stat(*out); err != nil || !info.IsDir() {
		return fmt.Errorf("-out %s is not a directory; run this from the repository root", *out)
	}
	selected, err := selectExchanges(built, *only)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("recording %s against %s into %s\n", *provider, *model, *out)
	for _, ex := range selected {
		fmt.Printf("  %s — %s\n", ex.name, ex.what)
		if err := ex.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", ex.name, err)
		}
	}

	fmt.Println("wrote:")
	for _, name := range rec.summary() {
		fmt.Println("  " + name)
	}
	return nil
}

// selectExchanges applies -only, failing loudly on a name that is not one of
// them rather than recording nothing and reporting success.
func selectExchanges(all []exchange, only string) ([]exchange, error) {
	if only == "" {
		return all, nil
	}
	for _, ex := range all {
		if ex.name == only {
			return []exchange{ex}, nil
		}
	}
	names := make([]string, 0, len(all))
	for _, ex := range all {
		names = append(names, ex.name)
	}
	return nil, fmt.Errorf("no exchange named %q; this provider records %s", only, strings.Join(names, ", "))
}

// providerNames lists the -provider values, in a stable order.
func providerNames() []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
