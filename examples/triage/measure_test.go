package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"fmt"
	"io"
	"io/fs"
	"net/mail"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
)

// TestMeasureNeedsReply compares wordings of the needs_reply question over a fixed
// sample of a real mail store. It is a measurement, not a test of anything, and it
// skips unless pointed at a store. It lives here because this package is main: only
// from inside can it ask the shipping question set over the shipping state.
//
//	TRIAGE_MEASURE_STORE=$HOME/Library/Mail/V10 TRIAGE_MEASURE_OUT=$PWD/local/needs-reply TRIAGE_MEASURE_RUNS=1 \
//	  go test -v -count=1 -timeout 60m -run TestMeasureNeedsReply ./examples/triage
//
// The sample's ids are kept in the out directory, so a later run asks about the same
// messages however the inbox has moved on. Only counts are printed. The out directory
// holds message ids, senders and subjects, so it must stay under the gitignored local/,
// and is absolute because a test runs from its package's directory. jev-1.13.0 answered
// identically on 3 runs of 140 messages, so one run is enough there.
func TestMeasureNeedsReply(t *testing.T) {
	store, out := os.Getenv("TRIAGE_MEASURE_STORE"), os.Getenv("TRIAGE_MEASURE_OUT")
	if store == "" || out == "" {
		t.Skip("TRIAGE_MEASURE_STORE and TRIAGE_MEASURE_OUT are not set, so no mail is read")
	}
	if !filepath.IsAbs(out) {
		t.Fatalf("TRIAGE_MEASURE_OUT is %q; it must be absolute, since a test runs from its package's directory", out)
	}
	runs := 3
	if v, err := strconv.Atoi(os.Getenv("TRIAGE_MEASURE_RUNS")); err == nil && v > 0 {
		runs = v
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	sample, records := loadSample(ctx, t, store, filepath.Join(out, "sample.json"))
	judge, err := newJudgeClient(judgeTypeSafe)
	if err != nil {
		t.Fatal(err)
	}

	type key struct {
		id  string
		arm int
	}
	var mu sync.Mutex
	scores := map[key][]float64{}
	failures := 0
	var input, output int
	var wg sync.WaitGroup
	slots := make(chan struct{}, judgeConcurrency)
	for id := range sample {
		record, ok := records[id]
		if !ok {
			continue
		}
		for armIndex, arm := range needsReplyArms {
			questions := withNeedsReply(arm)
			for range runs {
				wg.Go(func() {
					slots <- struct{}{}
					defer func() { <-slots }()
					answers, err := judge.Decide(ctx, &decision.Request{Model: defaultJudgeModel, State: stateOf(record), Questions: questions})
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						failures++
						return
					}
					input += answers.Usage.Input
					output += answers.Usage.Output
					reply, err := answers.Predicate(string(qNeedsReply))
					if err != nil {
						failures++
						return
					}
					scores[key{id, armIndex}] = append(scores[key{id, armIndex}], reply.Probability)
				})
			}
		}
	}
	wg.Wait()

	strata := []stratum{stratumReplied, stratumBulk, stratumAutomated, stratumOther}
	fmt.Printf("model %s, threshold %.2f, %d runs each, %d failed calls, %d input and %d output tokens\n",
		defaultJudgeModel, needsReplyThreshold, runs, failures, input, output)
	fmt.Printf("%-12s %-10s %8s %8s %8s %8s\n", "arm", "stratum", "messages", "yes", "mean", "unstable")
	for armIndex, arm := range needsReplyArms {
		for _, s := range strata {
			var messages, yes, calls, unstable int
			var sum float64
			for id, in := range sample {
				got := scores[key{id, armIndex}]
				if in != s || len(got) == 0 {
					continue
				}
				messages++
				said := 0
				for _, p := range got {
					calls++
					sum += p
					if p >= needsReplyThreshold {
						said++
					}
				}
				yes += said
				if said != 0 && said != len(got) {
					unstable++
				}
			}
			if calls > 0 {
				fmt.Printf("%-12s %-10s %8d %7.0f%% %8.2f %8d\n", arm.name, s, messages, 100*float64(yes)/float64(calls), sum/float64(calls), unstable)
			}
		}
	}

	var review bytes.Buffer
	for id, s := range sample {
		record, ok := records[id]
		if !ok {
			continue
		}
		fmt.Fprintf(&review, "%s\t", s)
		for armIndex := range needsReplyArms {
			fmt.Fprintf(&review, "%.2f\t", meanOf(scores[key{id, armIndex}]))
		}
		fmt.Fprintf(&review, "%s\t%s\n", addressText(record.From.Name, record.From.Address), record.Subject)
	}
	if err := os.WriteFile(filepath.Join(out, "review.tsv"), review.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// needsReplyArm is one wording.
type needsReplyArm struct {
	name     string
	question decision.Predicate
}

// needsReplyArms are the wordings compared. The baseline is the text that scored
// mass mail 0.60 to 0.82 on 2026-09-17, frozen here since the shipping one moves.
// Each later arm changes one thing from the one before it.
var needsReplyArms = []needsReplyArm{
	{"baseline", decision.Predicate{
		Instructions: "Is this message waiting on a written answer from the person who received it?",
		True:         "The sender asked a question, asked for a decision, or asked for something only the recipient can give, and nothing in the message says it was already answered.",
		False:        "The message is a receipt, a notification, a newsletter, an automated report, or a note that closes a thread; a reply would add nothing.",
	}},
	{"criteria", decision.Predicate{
		Instructions: "Is this message waiting on a written answer from the person who received it?",
		True:         "A particular person wrote to this recipient and is waiting on their words: a question, a decision or a request that only a reply from them can settle, not yet answered.",
		False:        "It went out to many people or from a company's system, so no one is waiting on this recipient's words: a receipt, a notification, a newsletter, a solicitation, an appeal, a survey, or any call to action answered by clicking, paying or ignoring it. Or it is a note that closes a thread.",
	}},
	{"question", decision.Predicate{
		Instructions: "Is a particular person waiting for this recipient to write back in their own words?",
		True:         "A particular person wrote to this recipient and is waiting on their words: a question, a decision or a request that only a reply from them can settle, not yet answered.",
		False:        "It went out to many people or from a company's system, so no one is waiting on this recipient's words: a receipt, a notification, a newsletter, a solicitation, an appeal, a survey, or any call to action answered by clicking, paying or ignoring it. Or it is a note that closes a thread.",
	}},
}

// withNeedsReply is the shipping question set with one wording swapped in, so the
// judge is asked what it is asked in production: all three questions in one call.
func withNeedsReply(arm needsReplyArm) decision.Questions {
	questions := slices.Clone(triageQuestions())
	for i := range questions {
		if questions[i].ID == string(qNeedsReply) {
			questions[i].Question = arm.question
		}
	}
	return questions
}

func meanOf(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(max(len(values), 1))
}

func stableHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// The sample's shape: a rate near 5% needs a few hundred messages to read, and the
// pool it is drawn from reaches back months rather than days.
const (
	samplePool     = 6000
	sentHeaderRead = 64 << 10
)

var sampleWant = map[stratum]int{stratumReplied: 103, stratumBulk: 200, stratumAutomated: 100, stratumOther: 200}

// loadSample reads the store and returns the sample, drawing it the first time.
func loadSample(ctx context.Context, t *testing.T, store, path string) (map[string]stratum, map[string]mailbox.Record) {
	t.Helper()
	source, err := mailbox.OpenApple(store)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := source.List(ctx, samplePool)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("inbox pool: %d messages, %d files skipped\n", len(listing.Messages), len(listing.Skipped))

	sample := map[string]stratum{}
	if saved, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(saved, &sample); err != nil {
			t.Fatal(err)
		}
	} else {
		replied, err := sentReplies(store)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("sent mail answers %d message ids\n", len(replied))
		byStratum := map[stratum][]string{}
		seen := map[string]bool{}
		for _, msg := range listing.Messages {
			if seen[msg.ID] {
				continue
			}
			seen[msg.ID] = true
			s := classify(msg.ID, msg.Header, msg.Record(1).From.Address, replied)
			byStratum[s] = append(byStratum[s], msg.ID)
		}
		for s, ids := range byStratum {
			fmt.Printf("pool %-10s %d\n", s, len(ids))
		}
		sample = pickSample(byStratum, sampleWant)
		saved, err := json.Marshal(sample)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, saved, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	records := map[string]mailbox.Record{}
	for _, msg := range listing.Messages {
		if _, ok := sample[msg.ID]; ok {
			records[msg.ID] = msg.Record(defaultSnippet)
		}
	}
	fmt.Printf("sample: %d messages, %d found in the pool\n", len(sample), len(records))
	return sample, records
}

// sentReplies walks every Sent folder and reads only each message's header, since a
// Sent folder is years of attachments and the In-Reply-To is all this needs.
func sentReplies(store string) (map[string]bool, error) {
	replied := map[string]bool{}
	err := filepath.WalkDir(store, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".emlx") || !isSentPath(path) {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()
		head, err := io.ReadAll(io.LimitReader(file, sentHeaderRead))
		if err != nil {
			return nil
		}
		if _, rest, ok := bytes.Cut(head, []byte("\n")); ok {
			if end := bytes.Index(rest, []byte("\n\n")); end >= 0 {
				rest = rest[:end+2]
			}
			parsed, err := mail.ReadMessage(io.MultiReader(bytes.NewReader(rest), strings.NewReader("\r\n\r\n")))
			if err == nil {
				if id := repliedTo(parsed.Header.Get("In-Reply-To")); id != "" {
					replied[id] = true
				}
			}
		}
		return nil
	})
	return replied, err
}

// isSentPath recognizes the names Apple Mail gives a Sent folder.
func isSentPath(path string) bool {
	for _, name := range []string{"Sent Messages.mbox", "Sent Mail.mbox", "Sent.mbox"} {
		if strings.Contains(path, string(filepath.Separator)+name+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
