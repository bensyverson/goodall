package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/examples/triage/mailbox"
	"github.com/bensyverson/goodall/openrouter"
	"github.com/bensyverson/goodall/typesafe"
)

// TestTheJudgeStaysOnItsPinnedJevByDefault is the example's calibration: every
// threshold in questions.go was tuned on one version, so a run that names nothing
// must not move.
func TestTheJudgeStaysOnItsPinnedJevByDefault(t *testing.T) {
	opts, err := parseOptions(nil, io.Discard)
	if err != nil {
		t.Fatalf("parsing no flags: %v", err)
	}
	if opts.JudgeBackend != judgeTypeSafe || opts.JudgeModel != defaultJudgeModel {
		t.Errorf("the default judge is %q on %q, want %q on %q", opts.JudgeModel, opts.JudgeBackend, defaultJudgeModel, judgeTypeSafe)
	}
	if note := opts.calibrationNote(); note != "" {
		t.Errorf("the default judge carries a calibration warning: %q", note)
	}
}

func TestAJudgeIsNamedByBackendAndModel(t *testing.T) {
	for name, tc := range map[string]struct {
		args      []string
		backend   judgeBackend
		model     string
		wantError string
	}{
		"a model on OpenRouter": {
			args:    []string{"-judge-backend", "openrouter", "-judge-model", "some/model"},
			backend: judgeOpenRouter, model: "some/model",
		},
		"Luna on OpenAI": {
			args:    []string{"-judge-backend", "openai", "-judge-model", "gpt-6-luna"},
			backend: judgeOpenAI, model: "gpt-6-luna",
		},
		"whatever a local server has loaded": {
			args:    []string{"-judge-backend", "lmstudio"},
			backend: judgeLMStudio, model: "",
		},
		"another Jev": {
			args:    []string{"-judge-model", "jev-1.14.0"},
			backend: judgeTypeSafe, model: "jev-1.14.0",
		},
		"OpenRouter with no model": {
			args:      []string{"-judge-backend", "openrouter"},
			wantError: "-judge-model",
		},
		"OpenAI with no model": {
			args:      []string{"-judge-backend", "openai"},
			wantError: "-judge-model",
		},
		"Jev with an empty model": {
			args:      []string{"-judge-model", ""},
			wantError: "-judge-model",
		},
		"a backend that does not exist": {
			args:      []string{"-judge-backend", "azure"},
			wantError: "-judge-backend",
		},
	} {
		t.Run(name, func(t *testing.T) {
			opts, err := parseOptions(tc.args, io.Discard)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("parsing %v gave %v, want an error naming %s", tc.args, err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsing %v: %v", tc.args, err)
			}
			if opts.JudgeBackend != tc.backend || opts.JudgeModel != tc.model {
				t.Errorf("the judge is %q on %q, want %q on %q", opts.JudgeModel, opts.JudgeBackend, tc.model, tc.backend)
			}
		})
	}
}

// TestAnyOtherJudgeIsReportedAsNotCalibrated: the report is what a person reads,
// and a triage that looks authoritative is the failure this guards against.
func TestAnyOtherJudgeIsReportedAsNotCalibrated(t *testing.T) {
	for _, args := range [][]string{
		{"-judge-backend", "openrouter", "-judge-model", "some/model"},
		{"-judge-backend", "lmstudio"},
		{"-judge-model", "jev-1.14.0"},
	} {
		opts, err := parseOptions(args, io.Discard)
		if err != nil {
			t.Fatalf("parsing %v: %v", args, err)
		}
		note := opts.calibrationNote()
		if !strings.Contains(note, defaultJudgeModel) || !strings.Contains(note, "thresholds") {
			t.Errorf("%v: the note is %q, want it to say the thresholds are tuned to %s", args, note, defaultJudgeModel)
		}
		var out strings.Builder
		writeReport(&out, opts, newInbox("a source", mailbox.Listing{}, defaultSnippet), newStore())
		if !strings.Contains(out.String(), note) {
			t.Errorf("%v: the report does not carry the note:\n%s", args, out.String())
		}
	}
}

func TestEachBackendBuildsItsOwnClient(t *testing.T) {
	t.Setenv(typeSafeKeyEnv, "k")
	t.Setenv(openRouterKeyEnv, "k")
	t.Setenv(openAIKeyEnv, "k")
	for backend, dialect := range map[judgeBackend]openrouter.Dialect{
		judgeOpenRouter: openrouter.OpenRouter,
		judgeOpenAI:     openrouter.OpenAI,
		judgeLMStudio:   openrouter.LMStudio,
	} {
		client, err := newJudgeClient(backend)
		if err != nil {
			t.Fatalf("%s: %v", backend, err)
		}
		if got, ok := client.(*openrouter.Client); !ok || got.Dialect() != dialect {
			t.Errorf("%s built %T, want the %s dialect", backend, client, dialect)
		}
	}
	client, err := newJudgeClient(judgeTypeSafe)
	if err != nil {
		t.Fatalf("typesafe: %v", err)
	}
	if _, ok := client.(*typesafe.Client); !ok {
		t.Errorf("typesafe built %T", client)
	}
}

// costlyJudge is the fake judge on a server that states what a call cost.
type costlyJudge struct{ *judge }

// judgeCallCost is the amount each call reports.
const judgeCallCost = "0.25"

// Decide answers as the fake does and adds the reported cost.
func (c costlyJudge) Decide(ctx context.Context, r *decision.Request) (*decision.Answers, error) {
	answers, err := c.judge.Decide(ctx, r)
	if err != nil {
		return nil, err
	}
	amount, err := goodall.ParseDecimal(judgeCallCost)
	if err != nil {
		return nil, errors.New("the fake's cost does not parse")
	}
	answers.Cost = goodall.Cost{Amount: amount, Currency: "USD", Reported: true}
	return answers, nil
}

// TestAJudgesReportedCostReachesTheCall: the books of a judge that states its
// price were being dropped on the way to the report, reading as not known.
func TestAJudgesReportedCostReachesTheCall(t *testing.T) {
	in := sampleInbox(t)
	costly := costlyJudge{newJudge(t)}

	triaged, err := triageMessages(t.Context(), costly, "a-model", in, newStore(), nil)
	if err != nil {
		t.Fatalf("triage: %v", err)
	}
	amount, _ := goodall.ParseDecimal(judgeCallCost)
	if want := amount.MulInt(int64(len(in.Records))); !triaged.Cost.Reported || triaged.Cost.Amount.Cmp(want) != 0 {
		t.Errorf("the triage call declared %+v, want a reported %v for %d judgments", triaged.Cost, want, len(in.Records))
	}

	st := newStore()
	checked, err := checkDraft(t.Context(), costly, "a-model", in, st, checkRequest{MessageID: in.Records[0].ID, Draft: "Thanks, looking into it."})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !checked.Cost.Reported || checked.Cost.Amount.Cmp(amount) != 0 {
		t.Errorf("the check call declared %+v, want a reported %v", checked.Cost, amount)
	}
}

// TestTheReportSaysWhatTheJudgeCostOnlyWhenItWasTold keeps an unreported cost from
// reading as free.
func TestTheReportSaysWhatTheJudgeCostOnlyWhenItWasTold(t *testing.T) {
	amount, _ := goodall.ParseDecimal(judgeCallCost)
	opts := options{JudgeBackend: judgeOpenRouter, JudgeModel: "a-model"}
	end := goodall.ToolCallEnd{ToolUse: goodall.ToolUse{Name: toolTriage}, Cost: goodall.Cost{Amount: amount, Currency: "USD", Reported: true}}

	st := newStore()
	st.observe(end)
	var told strings.Builder
	writeSpend(&told, opts, st)
	if !strings.Contains(told.String(), judgeCallCost+" USD") {
		t.Errorf("a reported cost is missing from the spend:\n%s", told.String())
	}

	end.Cost = goodall.Cost{}
	st = newStore()
	st.observe(end)
	var untold strings.Builder
	writeSpend(&untold, opts, st)
	if strings.Contains(untold.String(), "USD") {
		t.Errorf("an unreported cost was printed:\n%s", untold.String())
	}
}
