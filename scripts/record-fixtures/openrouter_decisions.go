package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// The OpenRouter decision recordings. They name their models themselves rather
// than taking -model, because -model defaults to a conversational one and the
// refusal is the property of one particular model.

// decisionQuestions is one question of each type, about a ticket chosen so the
// answers are not a coin toss.
var decisionQuestions = decision.Questions{
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
}

// decisionsRoute is the System One route under OpenRouter's default base URL,
// needed only by the hand-built refusal below.
const decisionsRoute = openrouter.DefaultBaseURL + "/systemone"

// decisionExchanges builds the recordings of the decision wire. Run them with
// -only, one at a time, since a plain run would also re-record every
// conversational fixture.
func decisionExchanges(rec *recorder, client *openrouter.Client, key string) []exchange {
	ticket := recordedTicket{
		Subject: "Payouts failing since Tuesday",
		Body:    recordedComplaint,
		Plan:    "growth",
	}
	return []exchange{
		{
			name: "decision_mixed",
			what: "one predicate, one choice and one score on a pinned non-Jev decision model",
			run: func(ctx context.Context) error {
				return askAndSave(ctx, rec, client, "decision_mixed", ticket, decisionQuestions, livemodel.OpenRouterDecision)
			},
		},
		{
			name: "decision_model_limit",
			what: "a model's own limit: an object state a string-only model refuses with a 400",
			run: func(ctx context.Context) error {
				return recordDecisionRefusal(ctx, rec, client, "decision_model_limit", &decision.Request{
					Model:     livemodel.OpenRouterDecisionLimited,
					State:     ticket,
					Questions: decisionQuestions,
				})
			},
		},
		{
			name: "decision_schema_error",
			what: "an unknown question type, so the envelope of a schema failure is on disk",
			run: func(ctx context.Context) error {
				return recordRefusal(ctx, rec, key, decisionsRoute, "decision_schema_error",
					`{"state":"Help!","model":"`+livemodel.OpenRouterDecision+`","questions":{"vibe":{"type":"vibe","instructions":"What is the vibe?"}}}`)
			},
		},
		{
			name: "decision_models",
			what: "the decision catalog, so the offline test decodes real rows",
			run: func(ctx context.Context) error {
				models, err := client.DecisionModels(ctx)
				if err != nil {
					return err
				}
				if err := rec.saveResponse("decision_models", ".json"); err != nil {
					return err
				}
				fmt.Printf("    decision_models: %d rows\n", len(models))
				return nil
			},
		},
	}
}

// recordDecisionRefusal makes one call expected to be refused and records the
// error envelope. A call that succeeds is itself a failure, since a fixture
// holding an answer would make the refusal test assert nothing.
func recordDecisionRefusal(ctx context.Context, rec *recorder, client *openrouter.Client, name string, req *decision.Request) error {
	_, callErr := client.Decide(ctx, req)
	if callErr == nil {
		rec.transport.taken()
		return fmt.Errorf("%s: the request meant to be refused was accepted, so there is no error body to record", name)
	}
	apiErr, ok := errors.AsType[*goodall.APIError](callErr)
	if !ok {
		rec.transport.taken()
		return fmt.Errorf("%s: the call failed without an API error, so there is no envelope to record: %w", name, callErr)
	}
	if err := rec.save(name, ".json"); err != nil {
		return err
	}
	fmt.Printf("    %s: status=%d kind=%s message=%q\n", name, apiErr.Status, apiErr.Kind, apiErr.Message)
	return nil
}
