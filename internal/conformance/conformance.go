// Package conformance holds what the decision conformance suite and its
// recordings share: the one question set, the one state in its two forms, and the
// table of backends. The suite is this package's test; scripts/record-fixtures
// records from the same values, so a fixture cannot drift from what the suite asks.
//
// A new decision wire earns its place by adding one row to [Backends] and
// recording its fixtures with
//
//	go run ./scripts/record-fixtures -provider conformance -env .env
//
// The suite asserts structure and accessors, never one model's judgment. Models
// disagree about the same ticket and thresholds do not travel, so a test that
// asserted an answer would be a test of the model.
package conformance

import (
	"net/http"

	"github.com/bensyverson/goodall/decision"
	"github.com/bensyverson/goodall/internal/livemodel"
	"github.com/bensyverson/goodall/openrouter"
	"github.com/bensyverson/goodall/typesafe"
)

// Ticket is the object form of the state, a record of the kind a caller with
// tickets to judge would send.
type Ticket struct {
	// Subject is the ticket's subject line.
	Subject string `json:"subject"`
	// Body is what the customer wrote.
	Body string `json:"body"`
	// Plan is the customer's plan.
	Plan string `json:"plan"`
}

// Complaint is the text form of the state, and the body of [ObjectState].
const Complaint = "Help! My payouts have been failing for 3 days and I have staff to pay."

// ObjectState is the state in its object form. Both wires take an object, whereas
// OpenAI's refuses a state that is not a string, an object or a list of objects.
var ObjectState = Ticket{Subject: "Payouts failing since Tuesday", Body: Complaint, Plan: "growth"}

// UnknownModel is the model that provokes each server's own refusal, the one
// mistake a request can carry that every backend has to judge for itself.
const UnknownModel = "no-such-model"

// Questions cover every type each wire has. The predicate's descriptions are
// two-sided because OpenRouter refuses one-sided ones, and the choice has more
// than one option because OpenAI refuses fewer than two.
var Questions = decision.Questions{
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
		True:         "Money or work is blocked right now",
		False:        "The sender can wait",
	}},
	{ID: "frustration", Question: decision.Score{
		Instructions: "How frustrated is the customer?",
		Levels:       []string{"Calm", "Mildly annoyed", "Frustrated", "Furious"},
	}},
}

// Form names one recorded exchange of a backend.
type Form string

const (
	// FormObject asks [Questions] over [ObjectState].
	FormObject Form = "object"
	// FormText asks [Questions] over [Complaint].
	FormText Form = "text"
	// FormRefused asks [Questions] over [ObjectState] of [UnknownModel].
	FormRefused Form = "refused"
)

// Forms lists every form a backend records, in recording order.
var Forms = []Form{FormObject, FormText, FormRefused}

// Fixture is the name of one recording, without the live_ prefix and the
// extension.
func (b Backend) Fixture(form Form) string { return b.Name + "_" + string(form) }

// Backend is one row of the suite: a server and how to reach it.
type Backend struct {
	// Name is what the suite's output and the fixtures call this backend.
	Name string
	// KeyEnv is the .env entry holding the key, empty for a server that takes
	// none, which is also what says this backend is on this machine.
	KeyEnv string
	// Model is the pinned model, empty to leave the choice to the server.
	Model string
	// Path is the path a request must reach, so that a client posting to the
	// wrong route cannot pass on a replay that ignores it.
	Path string
	// ReportsCost is whether this server states what a call cost.
	ReportsCost bool
	// RefusedStatus is the status [FormRefused] was recorded with, since a
	// fixture holds a body and no status line.
	RefusedStatus int
	// New builds the client under test, with retries left as a consumer gets them.
	New func(key string, client *http.Client) decision.Decider
}

// Backends is the table the suite runs and the recorder records.
var Backends = []Backend{
	{
		Name:          "typesafe",
		KeyEnv:        "TYPESAFE_API_KEY",
		Model:         livemodel.TypeSafe,
		Path:          "/v1/systemone",
		RefusedStatus: http.StatusBadRequest,
		New: func(key string, client *http.Client) decision.Decider {
			return typesafe.New(key, typesafe.WithHTTPClient(client))
		},
	},
	{
		Name:          "openrouter",
		KeyEnv:        "OPENROUTER_API_KEY",
		Model:         livemodel.OpenRouterDecision,
		Path:          "/api/v1/systemone",
		ReportsCost:   true,
		RefusedStatus: http.StatusBadRequest,
		New: func(key string, client *http.Client) decision.Decider {
			return openrouter.New(openrouter.WithAPIKey(key), openrouter.WithHTTPClient(client))
		},
	},
	{
		Name:          "openai",
		KeyEnv:        "OPENAI_API_KEY",
		Model:         livemodel.OpenAIDecision,
		Path:          "/v1/decisions",
		RefusedStatus: http.StatusNotFound,
		New: func(key string, client *http.Client) decision.Decider {
			return openrouter.New(openrouter.WithAPIKey(key), openrouter.WithHTTPClient(client),
				openrouter.WithBaseURL("https://api.openai.com/v1"), openrouter.WithDialect(openrouter.OpenAI))
		},
	},
	{
		// No model is named: the server answers with what is loaded, which the
		// run's output then names.
		Name:          "lmstudio",
		Path:          "/v1/systemone",
		RefusedStatus: http.StatusBadRequest,
		New: func(_ string, client *http.Client) decision.Decider {
			return openrouter.New(openrouter.WithHTTPClient(client),
				openrouter.WithBaseURL(livemodel.LMStudioBase()), openrouter.WithDialect(openrouter.LMStudio))
		},
	},
}

// Request is the request one form sends through a bare [decision.Decider], which
// is what the recorder makes and what every adapter must reproduce.
func (b Backend) Request(form Form) *decision.Request {
	switch form {
	case FormText:
		return &decision.Request{Model: b.Model, State: Complaint, Questions: Questions}
	case FormRefused:
		return &decision.Request{Model: UnknownModel, State: ObjectState, Questions: Questions}
	}
	return &decision.Request{Model: b.Model, State: ObjectState, Questions: Questions}
}
