package typesafe_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/typesafe"
)

// The three ways an agent reaches the judge. Each compiles with the suite and
// is not run, because each needs keys.

// triageQuestions is the question set the fixed-questions example asks. It is a
// package-level value because that is where a reviewable question set belongs:
// one place, beside the thresholds that read its answers.
var triageQuestions = typesafe.Questions{
	{ID: "department", Question: typesafe.Choice{
		Instructions: "Which team should handle this ticket?",
		Options: []typesafe.Option{
			{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
			{Key: "technical", Description: "Bugs, outages, integrations"},
			{Key: "other", Description: "Anything the two above do not cover"},
		},
	}},
	{ID: "is_urgent", Question: typesafe.Noul{
		Instructions: "Is work or money blocked right now?",
		True:         "Blocked right now",
		False:        "The sender can wait",
	}},
}

// A judgment tool whose questions are fixed by the developer: the model decides
// what to judge, and the questions — and the thresholds that read them — stay in
// the calling code where they can be reviewed.
func ExampleTool() {
	client := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))

	triage, err := typesafe.Tool(client, "triage_ticket",
		"Decide which team a support ticket belongs to and whether it is blocking the sender. "+
			"Send the ticket as an object with its subject and body.",
		triageQuestions, typesafe.WithToolModel("jev-1.13.0"))
	if err != nil {
		log.Fatal(err)
	}

	assistant := &goodall.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-sonnet-5",
		System:   "You triage incoming support tickets and say where each one goes.",
		Tools:    []goodall.Tool{triage},
	}

	for ev, err := range assistant.Run(context.Background(), goodall.Conversation{},
		goodall.Text{Text: `{"subject":"Payouts failing","body":"Three days now and I have staff to pay."}`}) {
		if err != nil {
			log.Fatal(err)
		}
		if delta, ok := ev.(goodall.TextDelta); ok {
			fmt.Print(delta.Text)
		}
	}
}

// A judgment tool whose questions the model writes at call time: the delegation
// shape, for the judgments a developer cannot enumerate in advance. The
// description defaults to typesafe.DefaultAuthoredDescription, and the guidance
// about each part of a question travels in the input schema.
func ExampleAuthoredTool() {
	client := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))

	judge, err := typesafe.AuthoredTool(client, "judge", typesafe.WithToolModel("jev-1.13.0"))
	if err != nil {
		log.Fatal(err)
	}

	assistant := &goodall.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-sonnet-5",
		System:   "You sort incoming work. Judge in batches rather than one item at a time.",
		Tools:    []goodall.Tool{judge},
	}

	for ev, err := range assistant.Run(context.Background(), goodall.Conversation{},
		goodall.Text{Text: "Here are five tickets as JSON. Decide which team each one goes to and which are urgent."}) {
		if err != nil {
			log.Fatal(err)
		}
		if start, ok := ev.(goodall.ToolCallStart); ok {
			fmt.Printf("judging: %s\n", start.ToolUse.Input)
		}
	}
}

// Routing: a cheap judgment in front of an expensive conversation. The hook
// sees each new turn before it is sent and picks the model it goes to; the
// judge never sees the history.
func ExampleRoute() {
	client := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))

	routing := typesafe.Questions{
		{ID: "intent", Question: typesafe.Choice{
			Instructions: "What does this turn need?",
			Options: []typesafe.Option{
				{Key: "chat", Description: "Small talk, or a question a short answer settles"},
				{Key: "hard", Description: "A problem that needs careful reasoning or a plan"},
			},
		}},
	}

	assistant := &goodall.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-haiku-4-5-20251001",
		Hooks: goodall.Hooks{
			BeforeSend: typesafe.Route(client, routing,
				func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message, answers *typesafe.Answers) error {
					intent, err := answers.Choice("intent")
					if err != nil {
						return err
					}
					if intent.Choice == "hard" && intent.Confidence > 0.8 {
						req.Model = "claude-opus-5"
					}
					return nil
				}),
		},
	}

	for ev, err := range assistant.Run(context.Background(), goodall.Conversation{},
		goodall.Text{Text: "My payouts have been failing for three days. What should I do?"}) {
		if err != nil {
			log.Fatal(err)
		}
		if delta, ok := ev.(goodall.TextDelta); ok {
			fmt.Print(delta.Text)
		}
	}
}
