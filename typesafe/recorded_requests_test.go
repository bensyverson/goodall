package typesafe

import (
	"encoding/json/jsontext"
	"net/http"
	"testing"

	"github.com/bensyverson/goodall/decision"
)

// TestRecordedRequestsAreStillTheBytesSent is the guard on the codec's encode
// side. The two requests below are the ones scripts/record-fixtures makes, and
// each .request.json beside the answer is what the client sent when that answer
// was recorded, indented for reading. Sending the same request now and indenting
// it the same way must reproduce the recording exactly, so a change in member
// order, in the wire's spelling or in how a state is marshaled fails here.
func TestRecordedRequestsAreStillTheBytesSent(t *testing.T) {
	const complaint = "Help! My payouts have been failing for 3 days and I have staff to pay."
	type ticket struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
		Plan    string `json:"plan"`
	}
	cases := map[string]*decision.Request{
		"live_mixed": {
			Model: "jev-1.13.0",
			State: ticket{Subject: "Payouts failing since Tuesday", Body: complaint, Plan: "growth"},
			Questions: decision.Questions{
				{ID: "department", Question: decision.Choice{
					Instructions: "Which team should handle this ticket?",
					Options: []decision.Option{
						{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
						{Key: "technical", Description: "Bugs, outages, integrations"},
						{Key: "sales", Description: "Pricing, upgrades, new accounts"},
					},
				}},
				{ID: "is_urgent", Question: decision.Predicate{
					Instructions: "Does this ticket convey urgency?",
				}},
				{ID: "frustration", Question: decision.Score{
					Instructions: "How frustrated is the customer?",
					Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
				}},
			},
		},
		"live_noul_criteria": {
			Model: "jev-1.13.0",
			State: complaint,
			Questions: decision.Questions{
				{ID: "is_urgent", Question: decision.Predicate{
					Instructions: "Does this message convey urgency?",
					True:         "Explicitly time-sensitive, or money or work is blocked right now",
					False:        "No urgency expressed; the sender can wait",
				}},
			},
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			client, seen := serveJSON(t, http.StatusOK, string(fixture(t, name+".json")))
			if _, err := client.Decide(t.Context(), req); err != nil {
				t.Fatalf("Decide: %v", err)
			}
			sent := jsontext.Value(seen.Body)
			if err := sent.Indent(jsontext.WithIndent("  ")); err != nil {
				t.Fatalf("indenting what was sent: %v\n%s", err, seen.Body)
			}
			if got, want := string(sent)+"\n", string(fixture(t, name+".request.json")); got != want {
				t.Errorf("the request is no longer the recorded bytes\n got: %s\nwant: %s", got, want)
			}
		})
	}
}
