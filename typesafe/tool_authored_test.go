package typesafe

import (
	json "encoding/json/v2"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bensyverson/goodall"
)

// authoredTool is the model-authored judgment tool built against the given
// client.
func authoredTool(t *testing.T, client *Client, opts ...ToolOption) goodall.Tool {
	t.Helper()
	tool, err := AuthoredTool(client, "judge", opts...)
	if err != nil {
		t.Fatalf("building the authored judgment tool: %v", err)
	}
	return tool
}

// authoredCall is a call a model could write: all three question types, in an
// order that is not the order this package declares them in, so a translation
// that reordered them would be caught.
const authoredCall = `{"state":{"ticket":"payouts failing"},"questions":[` +
	`{"id":"is_urgent","type":"noul","instructions":"Is work or money blocked right now?",` +
	`"true":"Blocked right now","false":"Can wait"},` +
	`{"id":"department","type":"choice","instructions":"Which team should handle this?",` +
	`"options":[{"key":"billing","description":"Payments, invoicing, refunds"},{"key":"other","description":"None of the above"}]},` +
	`{"id":"frustration","type":"score","instructions":"How frustrated is the customer?",` +
	`"levels":["Calm","Furious"]}]}`

// authoredBody is the request those questions must produce: the state as
// given, the questions as the questions object in the model's own order, and
// each question in the wire shape its type takes.
const authoredBody = `{"state":{"ticket":"payouts failing"},` +
	`"model":"jev-latest",` +
	`"questions":{` +
	`"is_urgent":{"type":"noul","instructions":"Is work or money blocked right now?",` +
	`"criteria":{"true":"Blocked right now","false":"Can wait"}},` +
	`"department":{"type":"choice","instructions":"Which team should handle this?",` +
	`"criteria":{"billing":"Payments, invoicing, refunds","other":"None of the above"}},` +
	`"frustration":{"type":"score","instructions":"How frustrated is the customer?",` +
	`"criteria":["Calm","Furious"]}}}`

// authoredAnswers answers the three questions above, so the result can be read
// back the way a consumer would read it.
const authoredAnswers = `{"model":"jev-1.13.0","answers":{` +
	`"is_urgent":{"type":"noul","noul":0.94},` +
	`"department":{"type":"choice","choice":"billing","confidence":0.9,"probabilities":{"billing":0.95,"other":0.05}},` +
	`"frustration":{"type":"score","score":0.8,"confidence":0.7,"legend":{"0":"Calm","1":"Furious"},"probabilities":{"0":0.2,"1":0.8}}},` +
	`"usage":{"input_tokens":120,"output_tokens":20}}`

// TestAuthoredToolSendsTheModelsQuestionsInTheModelsOrder is the round trip the
// authored tool exists for: the questions a model wrote become the request, and
// the typed answers come back as the result it reads.
func TestAuthoredToolSendsTheModelsQuestionsInTheModelsOrder(t *testing.T) {
	client, seen := serveJSON(t, http.StatusOK, authoredAnswers)
	result := judgeOneCall(t, authoredTool(t, client), authoredCall)

	if result.IsError {
		t.Fatalf("the tool result is an error: %q", result.Text())
	}
	if seen.Requests != 1 {
		t.Fatalf("the server saw %d requests, want one", seen.Requests)
	}
	if got := string(seen.Body); got != authoredBody {
		t.Errorf("the request body is\n%s\nwant\n%s", got, authoredBody)
	}
	var answers Answers
	if err := json.Unmarshal([]byte(result.Text()), &answers); err != nil {
		t.Fatalf("the tool result does not decode back into Answers: %v\n%s", err, result.Text())
	}
	if got, want := answers.IDs(), []string{"is_urgent", "department", "frustration"}; !slices.Equal(got, want) {
		t.Errorf("the answers carry %v, want %v in that order", got, want)
	}
}

// TestAuthoredToolRefusesAnUnaskableQuestionBeforeTheNetwork is the guard the
// authored tool needs and the fixed one does not: the questions are the model's,
// so they are checked before a call is paid for, and the model is told which
// question it was and what to write instead.
func TestAuthoredToolRefusesAnUnaskableQuestionBeforeTheNetwork(t *testing.T) {
	cases := map[string]struct {
		input string
		// names is what the refusal must say, so the model can act on it.
		names []string
	}{
		"an unmodeled type": {
			`{"state":"x","questions":[{"id":"where","type":"bounding_box","instructions":"Where is the logo?"}]}`,
			[]string{"where", "bounding_box", "noul", "choice", "score"},
		},
		"an unknown type": {
			`{"state":"x","questions":[{"id":"vibe","type":"vibe","instructions":"What is the vibe?"}]}`,
			[]string{"vibe", "noul", "choice", "score"},
		},
		"no questions at all": {
			`{"state":"x","questions":[]}`,
			[]string{"question"},
		},
		"a choice with no options": {
			`{"state":"x","questions":[{"id":"department","type":"choice","instructions":"Which team?"}]}`,
			[]string{"department", "option"},
		},
		"a score with one level": {
			`{"state":"x","questions":[{"id":"frustration","type":"score","instructions":"How angry?","levels":["Calm"]}]}`,
			[]string{"frustration", "level"},
		},
		"two questions sharing an id": {
			`{"state":"x","questions":[` +
				`{"id":"urgent","type":"noul","instructions":"Is it urgent?"},` +
				`{"id":"urgent","type":"noul","instructions":"Is it really urgent?"}]}`,
			[]string{"urgent"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := serveJSON(t, http.StatusOK, authoredAnswers)
			result := judgeOneCall(t, authoredTool(t, client), c.input)

			if !result.IsError {
				t.Fatalf("%s was accepted: %q", name, result.Text())
			}
			for _, want := range c.names {
				if !strings.Contains(result.Text(), want) {
					t.Errorf("the refusal %q does not name %q", result.Text(), want)
				}
			}
			if seen.Requests != 0 {
				t.Errorf("the server saw %d requests; a question set this package can refuse costs nothing", seen.Requests)
			}
		})
	}
}

// TestAuthoredToolDescriptionDefaultsAndCanBeReplaced is the guidance ruling in
// code: the tool carries the package's own description unless the consumer,
// whose prompt it competes with, replaces it.
func TestAuthoredToolDescriptionDefaultsAndCanBeReplaced(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, authoredAnswers)

	if got := authoredTool(t, client).Description(); got != DefaultAuthoredDescription {
		t.Errorf("the default description is %q, want DefaultAuthoredDescription", got)
	}
	const own = "Ask the judge about a node tree."
	if got := authoredTool(t, client, WithDescription(own)).Description(); got != own {
		t.Errorf("WithDescription left the description %q, want %q", got, own)
	}
}

// TestAuthoredToolSchemaNamesTheThreeTypes is what the model is looking at when
// it writes a question: two required members, and a type it can fill in without
// guessing.
func TestAuthoredToolSchemaNamesTheThreeTypes(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, authoredAnswers)
	schema := authoredTool(t, client).Schema()

	if got, want := strings.Join(schema.Required, ","), "state,questions"; got != want {
		t.Errorf("required = %q, want %q", got, want)
	}
	questions := propertyNamed(t, schema, "questions")
	if questions.Type != goodall.SchemaArray || questions.Items == nil {
		t.Fatalf("the questions property is %+v, want an array with items", questions)
	}
	typeMember := propertyNamed(t, questions.Items, "type")
	if got, want := typeMember.Enum, []string{"noul", "choice", "score"}; !slices.Equal(got, want) {
		t.Errorf("the type member's enum is %v, want exactly %v", got, want)
	}
	for _, name := range []string{"id", "type", "instructions"} {
		if p := propertyNamed(t, questions.Items, name); p.Description == "" {
			t.Errorf("the %s member has no description, so the model must guess what to write", name)
		}
	}
	if got, want := strings.Join(questions.Items.Required, ","), "id,type,instructions"; got != want {
		t.Errorf("a question requires %q, want %q", got, want)
	}
}

// propertyNamed reads one property out of an object schema.
func propertyNamed(t *testing.T, s *goodall.Schema, name string) *goodall.Schema {
	t.Helper()
	for i := range s.Properties {
		if s.Properties[i].Name == name {
			return &s.Properties[i].Schema
		}
	}
	t.Fatalf("the schema has no %q property: %+v", name, s.Properties)
	return nil
}

// TestAuthoredToolRefusesADefinitionItCannotServe keeps a registration mistake
// at construction, as [Tool] does.
func TestAuthoredToolRefusesADefinitionItCannotServe(t *testing.T) {
	client, _ := serveJSON(t, http.StatusOK, authoredAnswers)
	if tool, err := AuthoredTool(nil, "judge"); err == nil {
		t.Errorf("AuthoredTool accepted a nil client and returned %v", tool)
	}
	if tool, err := AuthoredTool(client, "judge this"); err == nil {
		t.Errorf("AuthoredTool accepted a name with a space and returned %v", tool)
	}
	if tool, err := AuthoredTool(client, "judge", WithDescription("")); err == nil {
		t.Errorf("AuthoredTool accepted an empty description and returned %v", tool)
	}
}
