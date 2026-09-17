package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/bensyverson/goodall/typesafe"
)

// The TypeSafe recordings. Jev is a judgment model, so an exchange here is one
// state plus a handful of typed questions, chosen so that the answers are
// unambiguous enough to assert on offline: a ticket that plainly belongs to
// billing, is plainly urgent and is plainly written by an unhappy person.
//
// A TypeSafe client is neither a goodall.Provider nor a goodall.Completer — Jev
// has no conversation to stream or collect — so the recorder's stream, complete
// and failing helpers cannot serve it. These exchanges drive the real client
// through rec.httpClient() and call rec.save themselves, which is all those
// helpers do.

// recordedTicket is the state the mixed recording evaluates. It is an object
// rather than a string so the fixture shows a structured state, which is the
// shape a caller with records to judge will send.
type recordedTicket struct {
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Plan    string `json:"plan"`
}

// recordedComplaint is the text both recordings judge. It is written to be
// plainly urgent and plainly unhappy, so an offline assertion about the answer
// is a statement about the model rather than a coin toss.
const recordedComplaint = "Help! My payouts have been failing for 3 days and I have staff to pay."

// unknownQuestionType is a request body the API refuses. It is the cheapest
// route to a real 422 envelope — validation rejects it before a token is spent
// — and it cannot be built through the client at all, since the Question
// interface is sealed to the three types that exist, so the recorder posts it
// by hand.
const unknownQuestionType = `{"state":"Help! My payouts have been failing for 3 days.",` +
	`"model":"jev-1.13.0","questions":{"vibe":{"type":"vibe","instructions":"What is the vibe?"}}}`

// typeSafeExchanges builds the TypeSafe recordings against one model.
func typeSafeExchanges(rec *recorder, key, model string) ([]exchange, error) {
	client := typesafe.New(key, typesafe.WithHTTPClient(rec.httpClient()))

	ticket := recordedTicket{
		Subject: "Payouts failing since Tuesday",
		Body:    recordedComplaint,
		Plan:    "growth",
	}
	mixed := typesafe.Questions{
		{ID: "department", Question: typesafe.Choice{
			Instructions: "Which team should handle this ticket?",
			Options: []typesafe.Option{
				{Key: "billing", Description: "Payments, payouts, invoicing, refunds"},
				{Key: "technical", Description: "Bugs, outages, integrations"},
				{Key: "sales", Description: "Pricing, upgrades, new accounts"},
			},
		}},
		{ID: "is_urgent", Question: typesafe.Noul{
			Instructions: "Does this ticket convey urgency?",
		}},
		{ID: "frustration", Question: typesafe.Score{
			Instructions: "How frustrated is the customer?",
			Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
		}},
	}
	criteria := typesafe.Questions{
		{ID: "is_urgent", Question: typesafe.Noul{
			Instructions: "Does this message convey urgency?",
			True:         "Explicitly time-sensitive, or money or work is blocked right now",
			False:        "No urgency expressed; the sender can wait",
		}},
	}

	return []exchange{
		{
			name: "mixed",
			what: "one noul, one choice and one score over an object state, in one call",
			run: func(ctx context.Context) error {
				return askAndSave(ctx, rec, client, "mixed", ticket, mixed, model)
			},
		},
		{
			name: "noul_criteria",
			what: "a noul with true and false criteria over a plain string state",
			run: func(ctx context.Context) error {
				return askAndSave(ctx, rec, client, "noul_criteria", recordedComplaint, criteria, model)
			},
		},
		{
			name: "models",
			what: "the model catalog, so the offline test decodes real cards",
			run: func(ctx context.Context) error {
				cards, err := client.Models(ctx)
				if err != nil {
					return err
				}
				if err := rec.saveResponse("models", ".json"); err != nil {
					return err
				}
				for _, card := range cards {
					fmt.Printf("    models: %s (%s) %s\n", card.Name, card.ReleaseDate, card.Description)
				}
				return nil
			},
		},
		{
			name: "refused",
			what: "an unknown question type, so a real 422 envelope is on disk",
			run: func(ctx context.Context) error {
				return recordRefusal(ctx, rec, key, "refused", unknownQuestionType)
			},
		},
	}, nil
}

// askAndSave makes one judgment call, records it and prints what it cost.
func askAndSave(ctx context.Context, rec *recorder, client *typesafe.Client, name string, state any, questions typesafe.Questions, model string) error {
	answers, callErr := client.Ask(ctx, state, questions, typesafe.WithModel(model))
	if err := rec.refused(name, ".json", callErr); err != nil {
		return err
	}
	if callErr != nil {
		return callErr
	}
	if err := rec.save(name, ".json"); err != nil {
		return err
	}
	reportJudgment(name, answers, questions)
	return nil
}

// recordRefusal posts a body the client cannot build and records the error
// envelope the API answers with. The call is made by hand because the point of
// the fixture is a body the typed API refuses to produce.
func recordRefusal(ctx context.Context, rec *recorder, key, name, body string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		typesafe.DefaultBaseURL+systemOnePath, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := rec.httpClient().Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		rec.transport.taken()
		return fmt.Errorf("%s: the request meant to be refused was accepted, so there is no error body to record", name)
	}
	if err := rec.save(name, ".json"); err != nil {
		return err
	}
	fmt.Printf("    %s: status=%d\n", name, resp.StatusCode)
	return nil
}

// systemOnePath is the judgment endpoint, needed here only by the hand-built
// refusal above; every other call goes through the client, which owns the path.
const systemOnePath = "/v1/systemone"

// saveResponse records the last round trip's response and nothing else. The
// catalog is a GET with no body, and recorder.save would leave an empty
// live_models.request.json beside it that a reader would have to wonder about.
func (r *recorder) saveResponse(name, ext string) error {
	taken := r.transport.taken()
	if len(taken.Response) == 0 {
		return fmt.Errorf("%s: no response was captured, so there is nothing to record", name)
	}
	return r.writeFile(fixturePrefix+name+ext, taken.Response)
}

// reportJudgment prints what one judgment call cost and what it decided, so a
// run's output is enough to write the offline assertions from without opening
// the fixture.
func reportJudgment(name string, answers *typesafe.Answers, questions typesafe.Questions) {
	if answers == nil {
		fmt.Printf("    %s: no answers\n", name)
		return
	}
	u := answers.Usage
	fmt.Printf("    %s: model=%s questions=%d input=%d output=%d%s\n",
		name, answers.Model, len(questions), u.Input, u.Output, reportedCost(answers.Cost))
	for _, nq := range questions {
		fmt.Printf("      %s: %s\n", nq.ID, describeAnswer(answers, nq))
	}
}

// describeAnswer renders one answer for the run's output, read through the
// accessor for the type the question asked.
func describeAnswer(answers *typesafe.Answers, nq typesafe.NamedQuestion) string {
	switch nq.Question.QuestionType() {
	case typesafe.TypeNoul:
		answer, err := answers.Noul(nq.ID)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("noul=%.4f", answer.Noul)
	case typesafe.TypeChoice:
		answer, err := answers.Choice(nq.ID)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("choice=%s confidence=%.4f ranked=%v",
			answer.Choice, answer.Confidence, answer.Ranked())
	case typesafe.TypeScore:
		answer, err := answers.Score(nq.ID)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("score=%.4f confidence=%.4f levels=%q ranked=%v",
			answer.Score, answer.Confidence, answer.Levels(), answer.Ranked())
	}
	return "a question type the recorder does not describe"
}
