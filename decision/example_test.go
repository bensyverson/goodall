package decision_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/anthropic"
	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/openrouter"
	"github.com/bensyverson/goodall/typesafe"
)

// The three ways an agent reaches the judge. Each compiles with the suite and
// is not run, because each needs keys.

// triageQuestions is the question set the fixed-questions example asks. It is a
// package-level value because that is where a reviewable question set belongs:
// one place, beside the thresholds that read its answers.
var triageQuestions = decision.Questions{
	{ID: "department", Question: decision.Choice{
		Instructions: "Which team should handle this ticket?",
		Options: []decision.Option{
			{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
			{Key: "technical", Description: "Bugs, outages, integrations"},
			{Key: "other", Description: "Anything the two above do not cover"},
		},
	}},
	{ID: "is_urgent", Question: decision.Predicate{
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

	triage, err := decision.Tool(client, "triage_ticket",
		"Decide which team a support ticket belongs to and whether it is blocking the sender. "+
			"Send the ticket as an object with its subject and body.",
		triageQuestions, decision.WithModel("jev-1.13.0"))
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
// description defaults to decision.DefaultAuthoredDescription, and the guidance
// about each part of a question travels in the input schema.
func ExampleAuthoredTool() {
	client := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))

	judge, err := decision.AuthoredTool(client, "judge", decision.WithModel("jev-1.13.0"))
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

	routing := decision.Questions{
		{ID: "intent", Question: decision.Choice{
			Instructions: "What does this turn need?",
			Options: []decision.Option{
				{Key: "chat", Description: "Small talk, or a question a short answer settles"},
				{Key: "hard", Description: "A problem that needs careful reasoning or a plan"},
			},
		}},
	}

	assistant := &goodall.Agent{
		Provider: anthropic.New(os.Getenv("ANTHROPIC_API_KEY")),
		Model:    "claude-haiku-4-5-20251001",
		Hooks: goodall.Hooks{
			BeforeSend: decision.Route(client, routing,
				func(ctx context.Context, req *goodall.Request, newTurn *goodall.Message, answers *decision.Answers) error {
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

// The same questions asked of a model that is not Jev. A decision model is chosen
// by a client and a model, so nothing about the questions changes, and because the
// thresholds that read an answer do not travel between models, this client has no
// default: the call names one, read from the catalog rather than remembered.
func ExampleDecider() {
	client := openrouter.New(openrouter.WithAPIKey(os.Getenv("OPENROUTER_API_KEY")))

	catalog, err := client.DecisionModels(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(catalog), "decision models on offer")

	answers, err := client.Decide(context.Background(), &decision.Request{
		Model:     "openai/gpt-6-luna-decisions",
		State:     "Help! My payouts have been failing for 3 days and I have staff to pay.",
		Questions: triageQuestions,
	})
	if err != nil {
		log.Fatal(err)
	}
	department, err := answers.Choice("department")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(department.Choice, "answered by", answers.Model, "at", answers.Cost.Amount, answers.Cost.Currency)
}

// The other two ways to reach a model that is not Jev: OpenAI's own wire and a
// server on this machine. Each is the same client under another dialect, and a
// tool or a route takes any of them, so the adapters do not change either.
func ExampleDecider_otherServers() {
	openAI := openrouter.New(
		openrouter.WithAPIKey(os.Getenv("OPENAI_API_KEY")),
		openrouter.WithBaseURL("https://api.openai.com/v1"),
		openrouter.WithDialect(openrouter.OpenAI),
	)
	if _, err := decision.Tool(openAI, "triage_ticket", "Decide which team a support ticket belongs to.",
		triageQuestions, decision.WithModel("gpt-6-luna")); err != nil {
		log.Fatal(err)
	}

	// No key and no model: the server answers with what is loaded.
	local := openrouter.New(
		openrouter.WithBaseURL("http://localhost:1234/v1"),
		openrouter.WithDialect(openrouter.LMStudio),
	)
	if _, err := decision.Tool(local, "triage_ticket", "Decide which team a support ticket belongs to.",
		triageQuestions); err != nil {
		log.Fatal(err)
	}
}
