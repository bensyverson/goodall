package openrouter

import (
	"encoding/json/jsontext"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
)

// openAIAnswersBody answers decideQuestions with one more, a score, the way
// api.openai.com spells it: an array in question order, with the distributions as
// arrays too. Every usage detail is non-zero and they differ, so a subtraction
// that goes wrong cannot land on the right figure by accident.
func openAIAnswersBody() string {
	return `{"model":"gpt-6-luna","answers":[` +
		`{"type":"choice","name":"department","choice":"billing","confidence":0.9,"probabilities":[{"value":"billing","probability":0.95},{"value":"technical","probability":0.05}]},` +
		`{"type":"predicate","name":"is_urgent","probability":0.25},` +
		`{"type":"score","name":"frustration","score":1.25,"confidence":0.7,"probabilities":[{"value":0,"label":"Calm","probability":0.1},{"value":1,"label":"Annoyed","probability":0.55},{"value":2,"label":"Furious","probability":0.35}]}` +
		`],"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":10},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":5},"total_tokens":107}}`
}

// openAIQuestions is what openAIAnswersBody answers.
func openAIQuestions() decision.Questions {
	return append(decideQuestions(), decision.NamedQuestion{ID: "frustration", Question: decision.Score{
		Instructions: "How frustrated is the customer?",
		Levels:       []string{"Calm", "Annoyed", "Furious"},
	}})
}

// openAIDecide asks one request of a test server under the OpenAI dialect.
func openAIDecide(t *testing.T, body string, req *decision.Request) (*decision.Answers, *seenRequest, error) {
	t.Helper()
	client, seen := decideServer(t, http.StatusOK, body, WithDialect(OpenAI))
	answers, err := client.Decide(t.Context(), req)
	return answers, seen, err
}

// openAIRequest is a request over a string state, the simplest one this wire takes.
func openAIRequest() *decision.Request {
	return &decision.Request{Model: "gpt-6-luna", State: "Help!", Questions: openAIQuestions()}
}

// TestOpenAIDecideSendsItsOwnWire is the path and the bytes. Everything the System
// One wire spells as an object keyed by id is an array here, and the member order
// is pinned because two identical requests must be identical bytes (invariant 8).
func TestOpenAIDecideSendsItsOwnWire(t *testing.T) {
	_, seen, err := openAIDecide(t, openAIAnswersBody(), openAIRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if seen.method != http.MethodPost || seen.path != "/decisions" {
		t.Errorf("%s %s, want POST /decisions", seen.method, seen.path)
	}
	if seen.auth != "Bearer test-key" {
		t.Errorf("Authorization = %q", seen.auth)
	}
	want := `{"model":"gpt-6-luna","input":"Help!","questions":[` +
		`{"type":"choice","name":"department","instructions":"Which team should handle this?","choices":[{"value":"billing","description":"Payments, invoicing, refunds"},{"value":"technical"}]},` +
		`{"type":"predicate","name":"is_urgent","instructions":"Does this convey urgency?"},` +
		`{"type":"score","name":"frustration","instructions":"How frustrated is the customer?","levels":[{"label":"Calm"},{"label":"Annoyed"},{"label":"Furious"}]}` +
		`]}`
	if seen.body != want {
		t.Errorf("body\n got: %s\nwant: %s", seen.body, want)
	}
}

// TestOpenAIDecideMapsEachStateShape is the state rule's accepting half: text is
// input, and an object or a list of objects is an input_json part, decided by
// what the state marshals to rather than by its Go type.
func TestOpenAIDecideMapsEachStateShape(t *testing.T) {
	type ticket struct {
		Body string `json:"body"`
	}
	type label string
	jsonInput := func(data string) string {
		return `[{"type":"message","role":"user","content":[{"type":"input_json","data":` + data + `}]}]`
	}
	for name, tc := range map[string]struct {
		state any
		input string
	}{
		"a string":                       {"Help!", `"Help!"`},
		"an empty string":                {"", `""`},
		"a named string":                 {label("Help!"), `"Help!"`},
		"JSON that is a string":          {jsontext.Value(`"Help!"`), `"Help!"`},
		"a struct":                       {ticket{Body: "Help!"}, jsonInput(`{"body":"Help!"}`)},
		"a pointer to a struct":          {&ticket{Body: "Help!"}, jsonInput(`{"body":"Help!"}`)},
		"a map, in sorted order":         {map[string]int{"b": 2, "a": 1}, jsonInput(`{"a":1,"b":2}`)},
		"a slice of structs":             {[]ticket{{Body: "one"}, {Body: "two"}}, jsonInput(`[{"body":"one"},{"body":"two"}]`)},
		"JSON that is a list of objects": {jsontext.Value(`[{"a":1},{"b":[1,"x"]}]`), jsonInput(`[{"a":1},{"b":[1,"x"]}]`)},
		"an empty object":                {map[string]int{}, jsonInput(`{}`)},
		"an empty list":                  {[]ticket{}, jsonInput(`[]`)},
	} {
		t.Run(name, func(t *testing.T) {
			req := openAIRequest()
			req.State = tc.state
			req.Questions = decision.Questions{{ID: "q", Question: decision.Predicate{Instructions: "Urgent?"}}}
			_, seen, err := openAIDecide(t, `{"model":"m","answers":[{"type":"predicate","name":"q","probability":1}]}`, req)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			want := `{"model":"gpt-6-luna","input":` + tc.input + `,"questions":`
			if !strings.HasPrefix(seen.body, want) {
				t.Errorf("body\n got: %s\nwant it to start: %s", seen.body, want)
			}
		})
	}
}

// TestOpenAIDecideRefusesAStateTheWireCannotCarry is the state rule's refusing
// half. The server answers each of these with a 400 naming an inner path of a
// request the caller never wrote (observed 2026-11-02, probe-openai-decisions
// -mode states), so a refusal here costs no call and can name the way out, which
// matters most when the reader is a model that wrote the state.
func TestOpenAIDecideRefusesAStateTheWireCannotCarry(t *testing.T) {
	var nilTicket *struct{ Body string }
	for name, tc := range map[string]struct {
		state any
		says  string
	}{
		"a list of strings": {[]string{"one", "two"}, "item 0 is a string"},
		"a mixed list":      {jsontext.Value(`[{"a":1},"two"]`), "item 1 is a string"},
		"a list of lists":   {[][]int{{1}}, "item 0 is an array"},
		"a list of numbers": {[]int{1}, "item 0 is a number"},
		"a number":          {42, "a number"},
		"a boolean":         {true, "a boolean"},
		"a nil pointer":     {nilTicket, "null"},
	} {
		t.Run(name, func(t *testing.T) {
			req := openAIRequest()
			req.State = tc.state
			_, seen, err := openAIDecide(t, openAIAnswersBody(), req)
			if !errors.Is(err, goodall.KindInvalidRequest) {
				t.Fatalf("error is %v, want KindInvalidRequest", err)
			}
			if seen.calls != 0 {
				t.Errorf("the client made %d calls for a state it refuses", seen.calls)
			}
			for _, words := range []string{tc.says, "object", "nothing was sent"} {
				if !strings.Contains(err.Error(), words) {
					t.Errorf("error %q does not say %q", err, words)
				}
			}
		})
	}
}

// TestOpenAIDecideRefusesAStateThatWillNotMarshal mirrors the System One wire.
func TestOpenAIDecideRefusesAStateThatWillNotMarshal(t *testing.T) {
	req := openAIRequest()
	req.State = make(chan int)
	_, seen, err := openAIDecide(t, openAIAnswersBody(), req)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Errorf("error is %v, want KindInvalidRequest", err)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls", seen.calls)
	}
}

// TestOpenAIDecideFoldsPredicateCriteria is the criteria rule. The wire has no
// member for them, and the spelling is the one measured against OpenRouter in the
// findings doc, so a change to it invalidates those figures.
func TestOpenAIDecideFoldsPredicateCriteria(t *testing.T) {
	for name, tc := range map[string]struct {
		question decision.Predicate
		want     string
	}{
		"neither": {
			decision.Predicate{Instructions: "Urgent?"},
			"Urgent?",
		},
		"both": {
			decision.Predicate{Instructions: "Urgent?", True: "Money is blocked", False: "It can wait"},
			"Urgent?\n\nTrue: Money is blocked\nFalse: It can wait",
		},
		"only true": {
			decision.Predicate{Instructions: "Urgent?", True: "Money is blocked"},
			"Urgent?\n\nTrue: Money is blocked",
		},
		"only false": {
			decision.Predicate{Instructions: "Urgent?", False: "It can wait"},
			"Urgent?\n\nFalse: It can wait",
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := openAIRequest()
			req.Questions = decision.Questions{{ID: "q", Question: tc.question}}
			_, seen, err := openAIDecide(t, `{"model":"m","answers":[{"type":"predicate","name":"q","probability":1}]}`, req)
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			instructions, err := jsontext.AppendQuote(nil, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"type":"predicate","name":"q","instructions":` + string(instructions) + `}`
			if !strings.Contains(seen.body, want) {
				t.Errorf("body\n got: %s\nwant it to hold: %s", seen.body, want)
			}
		})
	}
}

// TestOpenAIDecideReadsEveryAnswerShape holds this wire to the neutral forms: a
// score's array is keyed by level index, and a choice's by option.
func TestOpenAIDecideReadsEveryAnswerShape(t *testing.T) {
	answers, _, err := openAIDecide(t, openAIAnswersBody(), openAIRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers.Model != "gpt-6-luna" {
		t.Errorf("Model = %q", answers.Model)
	}
	if got, want := answers.IDs(), []string{"department", "is_urgent", "frustration"}; !slices.Equal(got, want) {
		t.Errorf("IDs = %v, want %v", got, want)
	}
	choice, err := answers.Choice("department")
	if err != nil {
		t.Fatalf("Choice: %v", err)
	}
	if choice.Choice != "billing" || choice.Confidence != 0.9 ||
		choice.Probabilities["billing"] != 0.95 || choice.Probabilities["technical"] != 0.05 {
		t.Errorf("choice = %+v", choice)
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("Predicate: %v", err)
	}
	if predicate.Probability != 0.25 {
		t.Errorf("Probability = %v", predicate.Probability)
	}
	score, err := answers.Score("frustration")
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if score.Score != 1.25 || score.Confidence != 0.7 {
		t.Errorf("score = %+v", score)
	}
	if score.Probabilities["0"] != 0.1 || score.Probabilities["1"] != 0.55 || score.Probabilities["2"] != 0.35 {
		t.Errorf("Probabilities = %v, want them keyed by level index", score.Probabilities)
	}
	if got, want := score.Levels(), []string{"Calm", "Annoyed", "Furious"}; !slices.Equal(got, want) {
		t.Errorf("Levels = %v, want %v", got, want)
	}
	if ranked := score.Ranked(); ranked[0].Key != "1" {
		t.Errorf("Ranked = %v, want level 1 first", ranked)
	}
}

// TestOpenAIRefusalLeavesItsSiblingsReadable: the refusal carries only a type and a
// name, the model declined that one question, and a zero would read as a confident
// no.
func TestOpenAIRefusalLeavesItsSiblingsReadable(t *testing.T) {
	body := `{"model":"gpt-6-luna","answers":[` +
		`{"type":"refusal","name":"department"},` +
		`{"type":"predicate","name":"is_urgent","probability":0.25},` +
		`{"type":"refusal","name":"frustration"}` +
		`],"usage":{"input_tokens":10,"output_tokens":0}}`
	answers, _, err := openAIDecide(t, body, openAIRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if got, ok := answers.Lookup("department"); !ok || got.AnswerType() != decision.TypeRefusal {
		t.Errorf("Lookup = %v, %v, want a refusal", got, ok)
	}
	_, err = answers.Choice("department")
	answerErr, ok := errors.AsType[*decision.AnswerError](err)
	if !ok {
		t.Fatalf("error is %T, want *decision.AnswerError: %v", err, err)
	}
	if answerErr.Got != decision.TypeRefusal || answerErr.ID != "department" {
		t.Errorf("AnswerError = %+v", answerErr)
	}
	if _, err := answers.Score("frustration"); err == nil {
		t.Error("a declined score read as an answer")
	}
	predicate, err := answers.Predicate("is_urgent")
	if err != nil {
		t.Fatalf("the sibling of a refusal did not read: %v", err)
	}
	if predicate.Probability != 0.25 {
		t.Errorf("Probability = %v", predicate.Probability)
	}
}

// TestOpenAIUsageDoesNotDoubleCountTheCache: OpenAI's input_tokens includes the
// cached and cache-write counts, and goodall's Input is the uncached part, so the
// three must add back up to what the server reported.
func TestOpenAIUsageDoesNotDoubleCountTheCache(t *testing.T) {
	answers, _, err := openAIDecide(t, openAIAnswersBody(), openAIRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	want := goodall.Usage{Input: 60, Output: 7, CacheRead: 30, CacheWrite: 10, Reasoning: 5}
	if answers.Usage != want {
		t.Errorf("Usage = %+v, want %+v", answers.Usage, want)
	}
	if got := answers.Usage.TotalInput(); got != 100 {
		t.Errorf("TotalInput = %d, want the 100 the server reported", got)
	}
}

// TestOpenAICostIsNotKnown: this server states no cost, so the zero is "not known"
// and never "free". A cost member appearing later is left unread as well, since
// the dialect says this wire does not carry one.
func TestOpenAICostIsNotKnown(t *testing.T) {
	for name, body := range map[string]string{
		"as documented": openAIAnswersBody(),
		"a stray cost":  strings.Replace(openAIAnswersBody(), `"total_tokens":107`, `"total_tokens":107,"cost":0.5`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			answers, _, err := openAIDecide(t, body, openAIRequest())
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if answers.Cost.Reported {
				t.Errorf("Cost = %+v, want it not reported", answers.Cost)
			}
			if !answers.Cost.Amount.IsZero() {
				t.Errorf("Amount = %s, want zero", answers.Cost.Amount)
			}
		})
	}
}

// TestOpenAIProtocolSurprisesAreLoud: each of these is a body this client's own
// request could not have drawn, so reading it as a value would state a judgment
// nobody made.
func TestOpenAIProtocolSurprisesAreLoud(t *testing.T) {
	wrap := func(answer string) string { return `{"model":"m","answers":[` + answer + `]}` }
	for name, body := range map[string]string{
		"a boolean pick":             wrap(`{"type":"choice","name":"q","choice":true,"confidence":1,"probabilities":[{"value":true,"probability":1}]}`),
		"a boolean in the spread":    wrap(`{"type":"choice","name":"q","choice":"a","confidence":1,"probabilities":[{"value":false,"probability":1}]}`),
		"a null name":                wrap(`{"type":"predicate","name":null,"probability":1}`),
		"no name":                    wrap(`{"type":"predicate","probability":1}`),
		"a refusal with a null name": wrap(`{"type":"refusal","name":null}`),
		"a predicate with no value":  wrap(`{"type":"predicate","name":"q"}`),
		"a choice with no pick":      wrap(`{"type":"choice","name":"q","confidence":1}`),
		"a score with no value":      wrap(`{"type":"score","name":"q","confidence":1}`),
		"a level with no index":      wrap(`{"type":"score","name":"q","score":1,"probabilities":[{"label":"a","probability":1}]}`),
		"answers keyed by id":        `{"model":"m","answers":{"q":{"type":"predicate","probability":1}}}`,
		"no answers at all":          `{"model":"m"}`,
		"not JSON":                   `<html>`,
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := openAIDecide(t, body, openAIRequest())
			if _, ok := errors.AsType[*goodall.ProtocolError](err); !ok {
				t.Errorf("error is %T, want *goodall.ProtocolError: %v", err, err)
			}
		})
	}
}

// TestOpenAIBooleanSurpriseSaysWhatItFound names the boolean, since the reader's
// next step is to wonder how a boolean got there.
func TestOpenAIBooleanSurpriseSaysWhatItFound(t *testing.T) {
	_, _, err := openAIDecide(t, `{"model":"m","answers":[{"type":"choice","name":"dept","choice":true,"confidence":1}]}`, openAIRequest())
	if err == nil || !strings.Contains(err.Error(), "boolean") || !strings.Contains(err.Error(), "dept") {
		t.Errorf("error = %v, want it to name the boolean and the question", err)
	}
}

// TestOpenAIAnUnknownAnswerTypeIsKept: the judgment was paid for, as on the other
// wire, so an answer this version cannot model stays whole.
func TestOpenAIAnUnknownAnswerTypeIsKept(t *testing.T) {
	raw := `{"type":"box","name":"where","x":1}`
	answers, _, err := openAIDecide(t, `{"model":"m","answers":[`+raw+`]}`, openAIRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	got, ok := answers.Lookup("where")
	if !ok {
		t.Fatal("the answer was dropped")
	}
	unknown, ok := got.(decision.UnknownAnswer)
	if !ok {
		t.Fatalf("answer is %T", got)
	}
	if unknown.Type != "box" || string(unknown.Raw) != raw {
		t.Errorf("unknown = %+v", unknown)
	}
}

// TestOpenAIGenerationIsTheRequestHeader: the body names no id on this server
// (observed 2026-11-02), and the header is what its logs and support go by.
func TestOpenAIGenerationIsTheRequestHeader(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req_abc")
		io.WriteString(w, openAIAnswersBody())
	}, WithDialect(OpenAI))
	answers, err := client.Decide(t.Context(), openAIRequest())
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if answers.Generation != "req_abc" {
		t.Errorf("Generation = %q, want the request id", answers.Generation)
	}
	if answers.ServedBy != "" {
		t.Errorf("ServedBy = %q, and this server routes to no other host", answers.ServedBy)
	}
}

// TestOpenAIADecisionErrorInsideASuccessIsAnError holds the rule the client keeps
// on every wire: HTTP 200 is not success if the body says otherwise.
func TestOpenAIADecisionErrorInsideASuccessIsAnError(t *testing.T) {
	_, _, err := openAIDecide(t, `{"error":{"message":"no","type":"server_error","param":null,"code":null}}`, openAIRequest())
	apiErr, ok := errors.AsType[*goodall.APIError](err)
	if !ok {
		t.Fatalf("error is %T, want *goodall.APIError: %v", err, err)
	}
	if apiErr.Message != "no" {
		t.Errorf("Message = %q", apiErr.Message)
	}
}

// TestOpenAIDecisionNeedsAModel: "Only gpt-6-luna is supported" today, and picking
// it silently would pin a caller's thresholds to a model they did not choose. The
// error must not send them to DecisionModels, which this dialect refuses.
func TestOpenAIDecisionNeedsAModel(t *testing.T) {
	req := openAIRequest()
	req.Model = ""
	_, seen, err := openAIDecide(t, openAIAnswersBody(), req)
	if !errors.Is(err, goodall.KindInvalidRequest) {
		t.Fatalf("error is %v, want KindInvalidRequest", err)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls", seen.calls)
	}
	if strings.Contains(err.Error(), "DecisionModels") {
		t.Errorf("error %q points at a call this dialect refuses", err)
	}
}

// TestAToolOverOpenAITellsTheModelWhyAStateWasRefused is the state rule through an
// adapter: an AuthoredTool's state is the model's to write, so a list of strings is
// a live case, and the model has to read a sentence it can act on rather than the
// run ending. Criteria on the same question must not be what stops it.
func TestAToolOverOpenAITellsTheModelWhyAStateWasRefused(t *testing.T) {
	client, seen := decideServer(t, http.StatusOK, `{"model":"m","answers":[{"type":"predicate","name":"q","probability":1}]}`, WithDialect(OpenAI))
	tool, err := decision.AuthoredTool(client, "judge", decision.WithModel("gpt-6-luna"))
	if err != nil {
		t.Fatalf("AuthoredTool: %v", err)
	}
	questions := `[{"id":"q","type":"predicate","instructions":"Urgent?","true":"Money is blocked","false":"It can wait"}]`

	result, err := tool.Execute(t.Context(), jsontext.Value(`{"state":["one","two"],"questions":`+questions+`}`))
	if err != nil {
		t.Fatalf("a refused state ended the run: %v", err)
	}
	if !result.IsError {
		t.Errorf("result = %+v, want an error result", result)
	}
	if seen.calls != 0 {
		t.Errorf("the client made %d calls", seen.calls)
	}
	if text := result.Text(); !strings.Contains(text, "object") {
		t.Errorf("the model read %q, which names no way out", text)
	}

	result, err = tool.Execute(t.Context(), jsontext.Value(`{"state":{"body":"Help!"},"questions":`+questions+`}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.IsError {
		t.Errorf("result = %+v, want the predicate with criteria answered", result)
	}
	if !strings.Contains(seen.body, `True: Money is blocked`) {
		t.Errorf("body = %s, want the criteria folded", seen.body)
	}
}
