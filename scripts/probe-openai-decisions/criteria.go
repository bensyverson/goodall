package main

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/openrouter"
)

const (
	openRouterSystemOneURL = "https://openrouter.ai/api/v1/systemone"
	openRouterLuna         = "openai/gpt-6-luna-decisions"
	openAILuna             = "gpt-6-luna"
	openAIBaseURL          = "https://api.openai.com/v1"

	spellingState = "Help! My payouts have been failing for 3 days and I have staff to pay."
	spellingAsk   = "Does this ticket convey urgency?"
	spellingTrue  = "Money or work is blocked right now"
	spellingFalse = "The sender can wait"
)

// spellings are the candidate foldings. Each differs from the others in framing
// only, so a token count separates them where the wording cannot be read.
var spellings = []struct{ name, instructions string }{
	{"none", spellingAsk},
	{"newline_true_false", spellingAsk + "\nTrue: " + spellingTrue + "\nFalse: " + spellingFalse},
	{"blank_true_false", spellingAsk + "\n\nTrue: " + spellingTrue + "\nFalse: " + spellingFalse},
	{"space_true_false", spellingAsk + " True: " + spellingTrue + " False: " + spellingFalse},
	{"means", spellingAsk + "\nTrue means: " + spellingTrue + "\nFalse means: " + spellingFalse},
	{"yes_no", spellingAsk + "\nYes: " + spellingTrue + "\nNo: " + spellingFalse},
	{"json", spellingAsk + "\n" + `{"true":"` + spellingTrue + `","false":"` + spellingFalse + `"}`},
}

// spellingsMode prints the input tokens and the probability of OpenRouter's own
// handling, with and without criteria, then of each candidate on OpenAI.
func spellingsMode(ctx context.Context, openAIKey, openRouterKey string) error {
	for _, arm := range []struct{ name, criteria string }{
		{"openrouter_no_criteria", ""},
		{"openrouter_criteria", `,"criteria":{"true":"` + spellingTrue + `","false":"` + spellingFalse + `"}`},
	} {
		body := `{"state":"` + spellingState + `","model":"` + openRouterLuna + `","questions":{"q":{"type":"noul","instructions":"` + spellingAsk + `"` + arm.criteria + `}}}`
		line, err := spellingCall(ctx, openRouterSystemOneURL, openRouterKey, body)
		if err != nil {
			return err
		}
		fmt.Printf("%-24s %s\n", arm.name, line)
	}
	for _, candidate := range spellings {
		instructions, err := json.Marshal(candidate.instructions)
		if err != nil {
			return err
		}
		body := `{"model":"` + openAILuna + `","input":"` + spellingState + `","questions":[{"type":"predicate","name":"q","instructions":` + string(instructions) + `}]}`
		line, err := spellingCall(ctx, openAIDecisionsURL, openAIKey, body)
		if err != nil {
			return err
		}
		fmt.Printf("%-24s %s\n", candidate.name, line)
	}
	return nil
}

// spellingCall posts one body and renders its input tokens and raw answers.
func spellingCall(ctx context.Context, url, key, body string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Answers jsontext.Value `json:"answers"`
		Usage   struct {
			Input int `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("HTTP %d %s", resp.StatusCode, data), nil
	}
	return fmt.Sprintf("input=%d answers=%s", parsed.Usage.Input, parsed.Answers), nil
}

// criteriaCase is one predicate over one state, chosen so the descriptions have
// room to move the answer: a measurement over easy cases could not tell a fold
// that works from one the model ignores.
type criteriaCase struct {
	name, state, ask, whenTrue, whenFalse string
}

var criteriaCases = []criteriaCase{
	{
		name:      "both_urgency_soft",
		state:     "The export has been slow this week. Would be nice to get it looked at before our quarterly review next month.",
		ask:       "Does this ticket convey urgency?",
		whenTrue:  "Money or work is blocked right now",
		whenFalse: "The sender can wait",
	},
	{
		name:      "both_redefines_sarcasm",
		state:     "Oh wonderful, the app crashed again, just what I needed today. Great job, everyone.",
		ask:       "Is this message positive?",
		whenTrue:  "The literal words are positive, whatever the writer meant by them",
		whenFalse: "The literal words are negative, whatever the writer meant by them",
	},
	{
		name:      "both_refund_window",
		state:     "I bought this 45 days ago and it stopped working. Can I get my money back?",
		ask:       "Is this customer eligible for a refund?",
		whenTrue:  "The purchase was within 30 days",
		whenFalse: "The purchase was more than 30 days ago",
	},
	{
		name:      "both_mild_satisfaction",
		state:     "I suppose it's fine, though I expected more for the price.",
		ask:       "Is the customer satisfied?",
		whenTrue:  "They would buy again",
		whenFalse: "They voice any disappointment at all",
	},
	{
		name:      "both_sales_direction",
		state:     "Hi, we love your product! Check out our SEO services at seo-boost.example for 50% off.",
		ask:       "Is this a sales lead?",
		whenTrue:  "The sender wants to buy from us",
		whenFalse: "The sender wants to sell to us",
	},
	{
		name:      "both_bug_or_feature",
		state:     "It would be great if the export could also do PDF; right now the button does nothing when I pick that format.",
		ask:       "Is this a bug report?",
		whenTrue:  "Something that should work is broken",
		whenFalse: "A request for something new",
	},
	{
		name:     "true_only_threshold",
		state:    "I have asked twice about my refund, please reply.",
		ask:      "Should this ticket be escalated?",
		whenTrue: "The customer has written more than five times",
	},
	{
		name:      "false_only_excludes",
		state:     "My card was charged twice for one order and I want the second charge reversed.",
		ask:       "Is this a billing problem?",
		whenFalse: "Anything about a card or a charge, which belongs to the fraud team and not to billing",
	},
}

// criteriaArm is one way of asking a case. Only openai_criteria and
// openrouter_criteria carry the descriptions as descriptions; the two hand-folded
// arms put a different framing in the instructions, on the same production path.
type criteriaArm struct {
	name     string
	client   *openrouter.Client
	model    string
	question func(criteriaCase) decision.Predicate
}

// asGiven carries the descriptions as the neutral type does.
func asGiven(c criteriaCase) decision.Predicate {
	return decision.Predicate{Instructions: c.ask, True: c.whenTrue, False: c.whenFalse}
}

// without drops them, which is the baseline a description must move away from.
func without(c criteriaCase) decision.Predicate {
	return decision.Predicate{Instructions: c.ask}
}

// handFolded frames them another way in the instructions.
func handFolded(open, whenTrue, whenFalse string) func(criteriaCase) decision.Predicate {
	return func(c criteriaCase) decision.Predicate {
		out := c.ask
		if c.whenTrue != "" {
			out += open + whenTrue + c.whenTrue
			open = "\n"
		}
		if c.whenFalse != "" {
			out += open + whenFalse + c.whenFalse
		}
		return decision.Predicate{Instructions: out}
	}
}

// criteriaMode runs every case through every arm, runs times, and prints each
// arm's probabilities, their spread and its input tokens. A failed call is printed
// and counted rather than dropped.
func criteriaMode(ctx context.Context, openAIKey, openRouterKey string, runs int) error {
	direct := openrouter.New(openrouter.WithAPIKey(openAIKey), openrouter.WithBaseURL(openAIBaseURL), openrouter.WithDialect(openrouter.OpenAI))
	routed := openrouter.New(openrouter.WithAPIKey(openRouterKey))
	arms := []criteriaArm{
		{"openrouter_criteria", routed, openRouterLuna, asGiven},
		{"openai_criteria", direct, openAILuna, asGiven},
		{"openrouter_none", routed, openRouterLuna, without},
		{"openai_none", direct, openAILuna, without},
		{"openai_one_newline", direct, openAILuna, func(c criteriaCase) decision.Predicate {
			return handFolded("\n", "True: ", "False: ")(c)
		}},
		{"openai_yes_no", direct, openAILuna, func(c criteriaCase) decision.Predicate {
			return handFolded("\n", "Yes: ", "No: ")(c)
		}},
	}
	fmt.Println("case\tarm\tn\tfailed\tmean\tmin\tmax\tinput_tokens\tprobabilities")
	for _, c := range criteriaCases {
		for _, arm := range arms {
			probabilities, tokens, failed := runArm(ctx, c, arm, runs)
			fmt.Printf("%s\t%s\t%d\t%d\t%.3f\t%.2f\t%.2f\t%v\t%v\n",
				c.name, arm.name, len(probabilities), failed, mean(probabilities), minOf(probabilities), maxOf(probabilities), tokens, probabilities)
		}
	}
	return nil
}

// runArm asks one case of one arm, runs times.
func runArm(ctx context.Context, c criteriaCase, arm criteriaArm, runs int) (probabilities []float64, tokens []int, failed int) {
	for range runs {
		answers, err := arm.client.Decide(ctx, &decision.Request{
			Model:     arm.model,
			State:     c.state,
			Questions: decision.Questions{{ID: "q", Question: arm.question(c)}},
		})
		if err != nil {
			failed++
			fmt.Printf("# %s %s: %v\n", c.name, arm.name, err)
			continue
		}
		answer, err := answers.Predicate("q")
		if err != nil {
			failed++
			fmt.Printf("# %s %s: %v\n", c.name, arm.name, err)
			continue
		}
		probabilities = append(probabilities, answer.Probability)
		if !slices.Contains(tokens, answers.Usage.TotalInput()) {
			tokens = append(tokens, answers.Usage.TotalInput())
		}
	}
	return probabilities, tokens, failed
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total / float64(len(values))
}

func minOf(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	return slices.Min(values)
}

func maxOf(values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	return slices.Max(values)
}
