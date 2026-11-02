package main

import (
	"fmt"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/openrouter"
	"github.com/bensyverson/goodall/typesafe"
)

// judgeBackend is which server answers the judgments. Swapping it is the seam
// this flag shows: the questions and the policy that reads them do not change.
type judgeBackend string

const (
	// judgeTypeSafe is TypeSafe's own API, and the default.
	judgeTypeSafe judgeBackend = "typesafe"
	// judgeOpenRouter is OpenRouter's catalog of decision models.
	judgeOpenRouter judgeBackend = "openrouter"
	// judgeOpenAI is OpenAI's decisions route.
	judgeOpenAI judgeBackend = "openai"
	// judgeLMStudio is a decision model on this machine, with no key.
	judgeLMStudio judgeBackend = "lmstudio"
)

const (
	// openAIJudgeURL is where the OpenAI judge is reached.
	openAIJudgeURL = "https://api.openai.com/v1"
	// lmStudioJudgeURL is LM Studio's documented default address.
	lmStudioJudgeURL = "http://localhost:1234/v1"
)

// newJudgeClient builds the client for a backend, reading its key where it has one.
func newJudgeClient(backend judgeBackend) (decision.Decider, error) {
	switch backend {
	case judgeTypeSafe:
		key, err := apiKey(typeSafeKeyEnv)
		if err != nil {
			return nil, err
		}
		return typesafe.New(key), nil
	case judgeOpenRouter:
		key, err := apiKey(openRouterKeyEnv)
		if err != nil {
			return nil, err
		}
		return openrouter.New(openrouter.WithAPIKey(key)), nil
	case judgeOpenAI:
		key, err := apiKey(openAIKeyEnv)
		if err != nil {
			return nil, err
		}
		return openrouter.New(openrouter.WithAPIKey(key), openrouter.WithBaseURL(openAIJudgeURL), openrouter.WithDialect(openrouter.OpenAI)), nil
	case judgeLMStudio:
		return openrouter.New(openrouter.WithBaseURL(lmStudioJudgeURL), openrouter.WithDialect(openrouter.LMStudio)), nil
	}
	return nil, fmt.Errorf("unknown -judge-backend %q; it is %s, %s, %s or %s", backend, judgeTypeSafe, judgeOpenRouter, judgeOpenAI, judgeLMStudio)
}

// judgeLabel names the judge for a person reading the report. No model named on a
// local server means whatever it has loaded.
func (o options) judgeLabel() string {
	if o.JudgeModel == "" {
		return "the model loaded on " + string(o.JudgeBackend)
	}
	return o.JudgeModel
}

// calibrationNote is what the report says about a judge the thresholds were not
// tuned on, empty for the one they were. Each model's probabilities mean something
// of their own, so the same cut-offs on another model sort differently without
// either being wrong, and a report that looked authoritative would hide that.
func (o options) calibrationNote() string {
	if o.JudgeBackend == judgeTypeSafe && o.JudgeModel == defaultJudgeModel {
		return ""
	}
	return fmt.Sprintf("The judge is %s on %s. The thresholds in this example are tuned to %s and do not carry to another model, so this run shows that a judge can be swapped and says nothing about how well this one judges.",
		o.judgeLabel(), o.JudgeBackend, defaultJudgeModel)
}
