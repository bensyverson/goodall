package main

import (
	"context"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
)

// The OpenAI decision recordings, on api.openai.com's own wire. They name their
// model themselves, since -model defaults to a conversational one, and they ask
// the question set the OpenRouter recording asks, so the offline test can hold the
// two wires' answers side by side.

// openAIDecisionExchanges builds the recordings. Run them with -only, one at a
// time, since a plain run would also re-record the conversational fixtures.
func openAIDecisionExchanges(rec *recorder, client *openrouter.Client) []exchange {
	ticket := recordedTicket{
		Subject: "Payouts failing since Tuesday",
		Body:    recordedComplaint,
		Plan:    "growth",
	}
	deep := any("leaf")
	for range 33 {
		deep = map[string]any{"a": deep}
	}
	return []exchange{
		{
			name: "openai_decision_mixed",
			what: "one predicate, one choice and one score over an object state, the OpenRouter recording's question set",
			run: func(ctx context.Context) error {
				return askAndSave(ctx, rec, client, "openai_decision_mixed", ticket, decisionQuestions, livemodel.OpenAIDecision)
			},
		},
		{
			name: "openai_decision_criteria",
			what: "a predicate with true and false descriptions over a string state, which this wire folds into the instructions",
			run: func(ctx context.Context) error {
				return askAndSave(ctx, rec, client, "openai_decision_criteria", recordedComplaint, decision.Questions{
					{ID: "is_urgent", Question: decision.Predicate{
						Instructions: "Does this ticket convey urgency?",
						True:         "Money or work is blocked right now",
						False:        "The sender can wait",
					}},
				}, livemodel.OpenAIDecision)
			},
		},
		{
			name: "openai_decision_one_option",
			what: "a choice with one option, which the neutral type allows and this server does not",
			run: func(ctx context.Context) error {
				return recordDecisionRefusal(ctx, rec, client, "openai_decision_one_option", &decision.Request{
					Model: livemodel.OpenAIDecision,
					State: recordedComplaint,
					Questions: decision.Questions{{ID: "department", Question: decision.Choice{
						Instructions: "Which team should handle this ticket?",
						Options:      []decision.Option{{Key: "billing"}},
					}}},
				})
			},
		},
		{
			name: "openai_decision_too_deep",
			what: "a state past the documented 32 levels, whose error carries a null code",
			run: func(ctx context.Context) error {
				return recordDecisionRefusal(ctx, rec, client, "openai_decision_too_deep", &decision.Request{
					Model:     livemodel.OpenAIDecision,
					State:     deep,
					Questions: decisionQuestions,
				})
			},
		},
	}
}
