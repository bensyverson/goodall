package typesafe_test

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"testing"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/internal/dotenv"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/typesafe"
)

// This is the live check the adapters leaf exists to make: one real
// conversational model, given the authored judgment tool and a realistic sorting
// task, writing its own questions for the judge. It costs money on two APIs, so
// there is one run in the file, and it skips loudly without either key or under
// -short.
//
// What it asserts is only that the delegation worked — the tool was called and
// came back with answers rather than a refusal. What it is *for* is the log: the
// questions the model wrote, verbatim, so a reader can hold them against the
// jaggedness list in project/2026-09-17-typesafe-jev-findings.md and see whether
// a model left to itself asks a judgment model things a judgment model can
// answer. An assertion on the wording would be an assertion about a model's
// prose, which is not this package's to make.

// liveAgentKeyName is the .env entry holding the conversational model's key,
// named in the skip message so a reader knows what to add.
const liveAgentKeyName = "ANTHROPIC_API_KEY"

// liveAgentKey reads the conversational provider's key from the repository's
// .env, or skips the test saying which key in which file was missing.
func liveAgentKey(t *testing.T) string {
	t.Helper()
	path, err := dotenv.RepoEnvFile()
	if err != nil {
		t.Skipf("skipping the live delegated judgment: %v", err)
	}
	key, ok := dotenv.Lookup(path, liveAgentKeyName)
	if !ok {
		t.Skipf("skipping the live delegated judgment: no %s in %s, so no call was made", liveAgentKeyName, path)
	}
	return key
}

// liveTickets is the material the model sorts: five tickets with the fields a
// real queue carries, one of which fits none of the obvious teams.
const liveTickets = `[
 {"id":"T-1041","subject":"Payouts failing since Tuesday","body":"Three payout batches have failed with error PAYOUT_DECLINED and I have staff to pay on Friday."},
 {"id":"T-1042","subject":"How do I add a second seat?","body":"We hired someone. Where do I add them, and does it change our monthly price?"},
 {"id":"T-1043","subject":"Webhook retries are hammering us","body":"Since yesterday your webhooks retry every 200ms and our endpoint is falling over. Please stop them."},
 {"id":"T-1044","subject":"Invoice VAT number wrong","body":"Our VAT number on last month's invoice is missing two digits. We need a corrected copy for our accountant, no rush."},
 {"id":"T-1045","subject":"Thanks!","body":"Just wanted to say the new dashboard is lovely. No reply needed."}
]`

// TestLiveAuthoredToolWritesItsOwnQuestions runs one real delegated judgment and
// logs every question the model wrote.
func TestLiveAuthoredToolWritesItsOwnQuestions(t *testing.T) {
	client := liveClient(t)
	key := liveAgentKey(t)

	judge, err := typesafe.AuthoredTool(client, "judge", typesafe.WithToolModel(livemodel.TypeSafe))
	if err != nil {
		t.Fatalf("building the authored judgment tool: %v", err)
	}

	assistant := &goodall.Agent{
		Provider:  anthropic.New(key),
		Model:     livemodel.Anthropic,
		System:    "You sort incoming support tickets. Judge in batches rather than one ticket at a time, then report what you found in a short table.",
		Tools:     []goodall.Tool{judge},
		MaxTokens: 4096,
		Budget:    goodall.Budget{MaxTurns: 4, Timeout: 3 * time.Minute},
	}

	var calls, judgments, refusals int
	var terminal goodall.Event
	var streamErr error
	for ev, err := range assistant.Run(t.Context(), goodall.Conversation{}, goodall.Text{
		Text: "Here are five support tickets as JSON. Decide which team each one should go to and which of them are urgent, then summarize.\n\n" + liveTickets,
	}) {
		if err != nil {
			streamErr = err
			break
		}
		switch e := ev.(type) {
		case goodall.ToolCallStart:
			calls++
			logAuthoredCall(t, e.ToolUse.Input)
		case goodall.ToolCallEnd:
			if e.Result.IsError {
				refusals++
				t.Logf("the judgment was refused: %s", e.Result.Text())
				break
			}
			judgments++
			logJudgment(t, e.Result.Text())
		case goodall.Done:
			terminal = e
			t.Logf("final answer:\n%s", e.Result.Response.Message.Text())
		case goodall.Stopped:
			terminal = e
			t.Logf("the run stopped (%s): %s", e.Cause, e.Message)
		}
	}
	if streamErr != nil {
		t.Fatalf("the run stream yielded an error, which it must never do: %v", streamErr)
	}
	if _, ok := terminal.(goodall.Done); !ok {
		t.Fatalf("the run ended with %T, want Done", terminal)
	}
	if calls == 0 {
		t.Error("the model never called the judgment tool, so nothing was delegated")
	}
	// A refusal is logged rather than failed: it is a question set this
	// package would not send, the model reads the reason and writes a better
	// one, and one observed run did exactly that before succeeding. What
	// would be a failure is a delegation that never produced a judgment at
	// all.
	if judgments == 0 {
		t.Errorf("none of the %d calls produced a judgment; %d came back as a refusal", calls, refusals)
	}
	t.Logf("%d calls, %d judgments, %d refusals", calls, judgments, refusals)
}

// logAuthoredCall writes the call the model wrote, indented, which is the
// observation this test exists to make. It is logged as the JSON the model
// actually sent rather than decoded into a copy of the tool's input type: a
// hand-written twin of that struct would drift from it, and what a reader wants
// here is the model's own words.
func logAuthoredCall(t *testing.T, input jsontext.Value) {
	t.Helper()
	pretty := input.Clone()
	if err := pretty.Indent(); err != nil {
		t.Logf("the model's call is not well-formed JSON (%v); raw:\n%s", err, input)
		return
	}
	t.Logf("the questions the model wrote:\n%s", pretty)
}

// logJudgment writes what came back, read through the same decoder a consumer
// would use.
func logJudgment(t *testing.T, result string) {
	t.Helper()
	var answers typesafe.Answers
	if err := json.Unmarshal([]byte(result), &answers); err != nil {
		t.Logf("the tool result did not decode back into Answers (%v); raw:\n%s", err, result)
		return
	}
	t.Logf("%s answered %d questions for %d input tokens", answers.Model, len(answers.IDs()), answers.Usage.Input)
	for _, entry := range answers.All() {
		switch answer := entry.Answer.(type) {
		case typesafe.NoulAnswer:
			t.Logf("  %s: noul=%.3f", entry.ID, answer.Noul)
		case typesafe.ChoiceAnswer:
			t.Logf("  %s: %s confidence=%.3f ranked=%v", entry.ID, answer.Choice, answer.Confidence, answer.Ranked())
		case typesafe.ScoreAnswer:
			t.Logf("  %s: score=%.3f confidence=%.3f levels=%q", entry.ID, answer.Score, answer.Confidence, answer.Levels())
		default:
			t.Logf("  %s: %s", entry.ID, entry.Answer.AnswerType())
		}
	}
}
