// Command triage is goodall's mail example: an agent that reads a mailbox,
// has a judgment model sort every message, delegates reply drafts to a cheaper
// model, checks each draft with four small inspectors, sends a flagged draft
// back to a stronger model once, escalates a still-flagged one to you, and
// prints a report of where everything goes and what it would say.
//
// It moves nothing. No message is flagged, filed, deleted or sent: the output
// is a report on standard output and that is all.
//
// What leaves the process is a filtered record of each message — sender,
// subject, date and a bounded snippet of the text, with quoted replies and
// markup stripped and attachments never decoded — because two third parties
// see this mail and because a judgment model degrades on detail no question
// asks about.
//
// Usage:
//
//	go run ./examples/triage
//	go run ./examples/triage -source apple -path ~/Library/Mail/V10 -n 30
//	go run ./examples/triage "which of these can wait until Monday?"
//
// With no flags it runs on a synthetic inbox bundled in the binary, so the
// example works with no mail on the machine. The keys come from the
// environment or from the repository's .env: ANTHROPIC_API_KEY or
// OPENROUTER_API_KEY for the conversation, and TYPESAFE_API_KEY for the judge.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/openrouter"
	"github.com/bensyverson/goodall/typesafe"
)

// sampleFS carries the bundled inbox, so the example runs from any directory
// with no mail on the machine and no flags.
//
//go:embed testdata/sample.mbox
var sampleFS embed.FS

// sampleFile is the bundled inbox's path inside sampleFS.
const sampleFile = "testdata/sample.mbox"

// sourceKind is where the mail is read from. It is typed because it decides
// both the reader and what -path means, and a misspelt string should reach
// neither.
type sourceKind string

const (
	// sourceApple is Apple Mail's on-disk store.
	sourceApple sourceKind = "apple"
	// sourceMaildir is a Maildir.
	sourceMaildir sourceKind = "maildir"
	// sourceMbox is a single mbox file, which is also what a Gmail Takeout
	// export is.
	sourceMbox sourceKind = "mbox"
	// sourceSample is the synthetic inbox bundled in the binary.
	sourceSample sourceKind = "sample"
)

// providerName is which API the conversation talks to, following
// examples/cli: one name decides the client, the key and the models.
type providerName string

const (
	// anthropicProvider is the Anthropic Messages API.
	anthropicProvider providerName = "anthropic"
	// openRouterProvider is OpenRouter's Chat Completions API.
	openRouterProvider providerName = "openrouter"
)

// The environment variables the keys are read from. A key is never a flag: a
// key on a command line is a key in the shell history.
const (
	anthropicKeyEnv  = "ANTHROPIC_API_KEY"
	openRouterKeyEnv = "OPENROUTER_API_KEY"
	typeSafeKeyEnv   = "TYPESAFE_API_KEY"
)

// The flag defaults. The message limit is what one run of this example is
// meant to cover, and the snippet limit is the privacy line: the judge and the
// models see this many runes of a message's text and nothing more.
const (
	defaultLimit   = 50
	defaultSnippet = 400
)

// defaultInstruction is what the run is asked to do when the command line
// carries no instruction of its own.
const defaultInstruction = "Triage my inbox: say where each message goes and which ones are waiting on me, " +
	"and draft a reply for the ones that need one."

// options are the settled command line.
type options struct {
	// Source is which reader opens the mail.
	Source sourceKind
	// Path is the store, folder or file, as that reader means it.
	Path string
	// Limit is how many messages are read, newest first.
	Limit int
	// Snippet is how many runes of each message's text travel.
	Snippet int
	// Provider is which API the conversation talks to.
	Provider providerName
	// JudgeModel is the pinned Jev version, since thresholds are tuned
	// against one.
	JudgeModel string
	// Instruction is what the person asked for.
	Instruction string
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "triage:", err)
		os.Exit(1)
	}
}

// run is main with errors and a writer, so every failure leaves through one
// place and a test can read the report.
func run(args []string, out io.Writer) error {
	opts, err := parseOptions(args, os.Stderr)
	if err != nil {
		// -h is a question the flag package has already answered by
		// printing the usage, so passing it on would exit non-zero for
		// a command that did what was asked.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	d, err := newDeps(opts)
	if err != nil {
		return err
	}
	// One run, so Ctrl-C cancels it rather than stopping a prompt: the
	// terminal event still carries everything that arrived, and the report
	// is printed from it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return execute(ctx, opts, d, out)
}

// parseOptions reads the command line, writing the usage and any flag
// complaint to usageOut. The instruction is the positional argument, so the
// everyday invocation is the command's name and a sentence.
func parseOptions(args []string, usageOut io.Writer) (options, error) {
	fs := flag.NewFlagSet("triage", flag.ContinueOnError)
	fs.SetOutput(usageOut)
	source := fs.String("source", string(sourceSample), "where to read mail from: apple, maildir, mbox or sample")
	path := fs.String("path", "", "the store, folder or file to read; not used by -source sample")
	limit := fs.Int("n", defaultLimit, "how many messages to read, newest first")
	snippet := fs.Int("snippet", defaultSnippet, "how many runes of each message's text the models and the judge see")
	provider := fs.String("provider", string(anthropicProvider), "which API to talk to: anthropic or openrouter")
	judgeModel := fs.String("judge-model", defaultJudgeModel, "the Jev version the judgments are asked of")
	fs.Usage = func() { usage(fs) }
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	opts := options{
		Source:      sourceKind(*source),
		Path:        *path,
		Limit:       *limit,
		Snippet:     *snippet,
		Provider:    providerName(*provider),
		JudgeModel:  *judgeModel,
		Instruction: defaultInstruction,
	}
	if fs.NArg() > 0 {
		opts.Instruction = strings.Join(fs.Args(), " ")
	}
	return opts, opts.validate()
}

// validate reports a command line that could not run, before anything is
// opened or any key is read.
func (o options) validate() error {
	switch o.Source {
	case sourceApple, sourceMaildir, sourceMbox:
		if o.Path == "" {
			return fmt.Errorf("-source %s needs a -path to read", o.Source)
		}
	case sourceSample:
	default:
		return fmt.Errorf("unknown -source %q; it is one of %s, %s, %s or %s",
			o.Source, sourceApple, sourceMaildir, sourceMbox, sourceSample)
	}
	switch o.Provider {
	case anthropicProvider, openRouterProvider:
	default:
		return fmt.Errorf("unknown -provider %q; it is %s or %s", o.Provider, anthropicProvider, openRouterProvider)
	}
	if o.Limit < 1 {
		return fmt.Errorf("-n is %d; read at least one message", o.Limit)
	}
	if o.Snippet < 1 {
		return fmt.Errorf("-snippet is %d; the judge needs some text to judge", o.Snippet)
	}
	if o.JudgeModel == "" {
		return errors.New("-judge-model is empty; pin the version your thresholds were tuned against")
	}
	return nil
}

// open reads the mail. The listing is done once, before the agent starts: the
// filtered records it produces are the only view of the mail that anything
// downstream ever gets.
func (o options) open(ctx context.Context) (*inbox, error) {
	if o.Source == sourceSample {
		return o.openSample(ctx)
	}
	src, err := mailbox.Open(mailbox.Kind(o.Source), expandHome(o.Path))
	if err != nil {
		return nil, err
	}
	listing, err := src.List(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	return newInbox(fmt.Sprintf("%s %s", o.Source, o.Path), listing, o.Snippet), nil
}

// openSample reads the bundled inbox. It is written to a temporary file
// because the readers take a path — they are built for real stores, where
// nothing is in memory — and it is removed as soon as the listing has read it.
func (o options) openSample(ctx context.Context) (*inbox, error) {
	data, err := sampleFS.ReadFile(sampleFile)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp("", "goodall-triage-sample-*.mbox")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	src, err := mailbox.OpenMbox(file.Name())
	if err != nil {
		return nil, err
	}
	listing, err := src.List(ctx, o.Limit)
	if err != nil {
		return nil, err
	}
	return newInbox("the bundled sample inbox", listing, o.Snippet), nil
}

// expandHome turns a leading ~ into the home directory, because an Apple Mail
// path is one a person types with a tilde and the shell does not expand it
// inside a quoted flag.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// newDeps builds what the run talks to: one provider client for all three
// agents, and the judge. The three agents differ by model, not by client, so
// switching provider moves the whole cascade at once.
func newDeps(o options) (deps, error) {
	judgeKey, err := apiKey(typeSafeKeyEnv)
	if err != nil {
		return deps{}, err
	}
	provider, models, err := newProvider(o.Provider)
	if err != nil {
		return deps{}, err
	}
	return deps{
		Parent:     provider,
		Strong:     provider,
		Cheap:      provider,
		Models:     models,
		Judge:      typesafe.New(judgeKey),
		JudgeModel: o.JudgeModel,
	}, nil
}

// newProvider builds the client for a provider name and reports the models
// that provider spells the three roles with.
func newProvider(name providerName) (goodall.Provider, modelSet, error) {
	switch name {
	case anthropicProvider:
		key, err := apiKey(anthropicKeyEnv)
		if err != nil {
			return nil, modelSet{}, err
		}
		return anthropic.New(key), anthropicModels, nil
	case openRouterProvider:
		key, err := apiKey(openRouterKeyEnv)
		if err != nil {
			return nil, modelSet{}, err
		}
		return openrouter.New(openrouter.WithAPIKey(key)), openRouterModels, nil
	}
	return nil, modelSet{}, fmt.Errorf("unknown provider %q; it is %s or %s", name, anthropicProvider, openRouterProvider)
}

// apiKey reads one key from the environment, falling back to the repository's
// .env so that the example runs the way the live tests do. The error says
// which variable is empty and which file was looked in, because that is the
// whole of what a person needs to fix it.
func apiKey(variable string) (string, error) {
	if key := os.Getenv(variable); key != "" {
		return key, nil
	}
	path, err := dotenv.RepoEnvFile()
	if err != nil {
		return "", fmt.Errorf("%s is not set, and there is no repository .env to read: %w", variable, err)
	}
	if key, ok := dotenv.Lookup(path, variable); ok {
		return key, nil
	}
	return "", fmt.Errorf("%s is not set and %s does not carry it; export the key or add it there", variable, path)
}

// usage is the -h text, which names the environment the example needs.
func usage(fs *flag.FlagSet) {
	out := fs.Output()
	fmt.Fprintf(out, "Usage: go run ./examples/triage [flags] [instruction]\n\n")
	fmt.Fprintf(out, "Triage a mailbox with a judgment model, draft replies with a cheaper one,\n")
	fmt.Fprintf(out, "check every draft, and print a report. Nothing is moved, flagged or sent.\n\n")
	fmt.Fprintf(out, "The keys come from %s or %s and from %s,\n", anthropicKeyEnv, openRouterKeyEnv, typeSafeKeyEnv)
	fmt.Fprintf(out, "read from the environment or from the repository's .env.\n\n")
	fmt.Fprintf(out, "Flags:\n")
	fs.PrintDefaults()
	fmt.Fprintf(out, "\nWith no flags it runs on a synthetic inbox bundled in the binary.\n")
}
