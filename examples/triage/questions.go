package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bensyverson/goodall/typesafe"
)

// Every question the judge is ever asked, and every threshold that reads an
// answer, lives in this file. That is the shape TypeSafe's own guidance asks
// for and the reason the triage plan named the file: a judgment pipeline is
// only reviewable if the semantic part and the policy part can be read side by
// side, and a threshold buried at the call site that reads it is a policy
// nobody can find. Nothing here calls the API; tools.go and route.go do that.

// questionID is the key one question is sent under and its answer comes back
// under. It is typed because every accessor takes one and a misspelt string
// would read as a missing answer rather than as a mistake.
type questionID string

// The questions asked about one message, and the routing question asked about
// the person's own turn.
const (
	// qDepartment is which part of the business a message belongs to.
	qDepartment questionID = "department"
	// qNeedsReply is whether the message is waiting on an answer.
	qNeedsReply questionID = "needs_reply"
	// qIsUrgent is whether it is waiting on one today.
	qIsUrgent questionID = "is_urgent"
	// qIntent is what the person asked this run for.
	qIntent questionID = "intent"
)

// The four inspectors a draft is checked by. Each is one defect a person could
// rule on in a second, because four specific questions combined in code beat
// one vague "is this draft good": a single score gives nothing to act on,
// while a named defect tells the redraft what to fix.
const (
	// qAnswersTheQuestion is whether the draft answers what was asked.
	qAnswersTheQuestion questionID = "answers_the_question"
	// qInventsAFact is whether the draft states something the message
	// does not contain.
	qInventsAFact questionID = "invents_a_fact"
	// qUnaskedPromise is whether the draft promises something nobody
	// asked for.
	qUnaskedPromise questionID = "unasked_promise"
	// qToneMismatch is whether the draft's register fits the sender's.
	qToneMismatch questionID = "tone_mismatch"
)

// department is where a message is proposed to go. It is a typed constant
// because the judge's options, the report's vocabulary and any routing a
// consumer builds on top must be one set: a department the report knows and
// the question does not offer is a route nothing can take.
type department string

// The departments a message is sorted into. Five is the number the plan
// settled on: enough to be useful, few enough that each earns a description
// the judge can tell apart.
const (
	departmentBilling   department = "billing"
	departmentTechnical department = "technical"
	departmentSales     department = "sales"
	departmentPersonal  department = "personal"
	departmentOther     department = "other"
)

// departments is the set in the order it is sent. The order is fixed because
// jev-1.13.0 favours whichever option is listed first on a close call
// (measured by scripts/probe-option-order, recorded in typesafe.Option), so
// two runs are only comparable while it stays put.
var departments = []department{
	departmentBilling,
	departmentTechnical,
	departmentSales,
	departmentPersonal,
	departmentOther,
}

// known reports whether the judge's pick is a department this code has a
// policy for. An answer outside the set is a question and a report that have
// drifted apart, which is worth saying out loud.
func (d department) known() bool {
	return slices.Contains(departments, d)
}

// The thresholds that turn a probability into a decision. They are inclusive:
// a noul exactly at its threshold counts as a yes.
const (
	// needsReplyThreshold is where "waiting on an answer" starts. It is
	// low because a message wrongly listed as needing a reply costs a
	// glance, while one wrongly dropped costs a customer.
	needsReplyThreshold = 0.60
	// urgentThreshold is where "today" starts. It is higher than
	// needsReplyThreshold: an inbox where a third of the messages are
	// urgent has no urgent messages.
	urgentThreshold = 0.70
	// departmentFloor is the probability a department needs before the
	// report states it plainly. Below it the message is still routed to
	// the leading option, and the report says the call was close.
	departmentFloor = 0.55
	// routeFloor is the confidence a routing judgment needs before it
	// narrows a turn. Below it the turn keeps the agent's own model and
	// its whole tool set, because narrowing on a guess takes away a tool
	// the person may have been asking for.
	routeFloor = 0.60
)

// triageQuestions is what the judge is asked about one message: where it
// belongs, whether it is waiting on an answer, and whether it is waiting
// today. All three are asked in one call over one message's record, which is
// what the judge is good at — several independent judgments over one small
// state.
func triageQuestions() typesafe.Questions {
	options := make([]typesafe.Option, 0, len(departments))
	for _, dept := range departments {
		options = append(options, typesafe.Option{Key: string(dept), Description: departmentDescriptions[dept]})
	}
	return typesafe.Questions{
		{ID: string(qDepartment), Question: typesafe.Choice{
			Instructions: "Which part of a small software company should handle this message? Decide from what the sender is asking for, not from the address it came from.",
			Options:      options,
		}},
		{ID: string(qNeedsReply), Question: typesafe.Noul{
			Instructions: "Is this message waiting on a written answer from the person who received it?",
			True:         "The sender asked a question, asked for a decision, or asked for something only the recipient can give, and nothing in the message says it was already answered.",
			False:        "The message is a receipt, a notification, a newsletter, an automated report, or a note that closes a thread; a reply would add nothing.",
		}},
		{ID: string(qIsUrgent), Question: typesafe.Noul{
			Instructions: "Would a day's delay in answering this message cost the sender or the recipient something real?",
			True:         "Money, a deadline, an outage, a legal date or a customer's decision turns on an answer arriving today.",
			False:        "The matter keeps until the end of the week, or the pressure is the sender's own framing rather than a consequence: a marketing deadline, a countdown in a message that asks for a password or a payment.",
		}},
	}
}

// departmentDescriptions is the rubric for each option. Jev reads them
// literally, so each says what the department is for in the words the sender
// would use rather than naming the team that owns it.
var departmentDescriptions = map[department]string{
	departmentBilling:   "Invoices, payments, purchase orders, refunds, plan changes and anything about what something costs.",
	departmentTechnical: "How the product works or fails: a feature question, a bug, an outage, an integration, an export, an account that will not do what it should.",
	departmentSales:     "Someone who is not yet a customer, or a customer asking about buying more: pricing for a new size, a trial, a demo, a renewal being weighed up.",
	departmentPersonal:  "A message to this person rather than to the business: a colleague's note, a friend, an invitation, something with no commercial content at all.",
	departmentOther:     "Everything the four above do not cover, including automated mail, newsletters, notifications and messages whose purpose is to deceive the reader.",
}

// triageVerdict is one message's triage after the thresholds have been
// applied: what the report prints and what the agent reads back. The
// probabilities travel with the decisions because a close call and a certain
// one deserve different weight from the person reading the report.
type triageVerdict struct {
	// Department is the leading option.
	Department department `json:"department"`
	// DepartmentProbability is that option's probability.
	DepartmentProbability float64 `json:"department_probability"`
	// DepartmentConfidence is how concentrated the whole distribution is.
	DepartmentConfidence float64 `json:"department_confidence"`
	// Unsure is set when the leading option is under departmentFloor, so
	// a reader is told the call was close rather than shown a number to
	// interpret.
	Unsure bool `json:"unsure,omitzero"`
	// NeedsReply is whether the message is waiting on an answer.
	NeedsReply bool `json:"needs_reply"`
	// NeedsReplyProbability is the judgment behind it.
	NeedsReplyProbability float64 `json:"needs_reply_probability"`
	// Urgent is whether it is waiting today.
	Urgent bool `json:"urgent"`
	// UrgentProbability is the judgment behind it.
	UrgentProbability float64 `json:"urgent_probability"`
}

// readTriage applies the thresholds to one message's answers. An answer that
// is missing or of the wrong type is an error naming the question: a judgment
// that did not arrive must not read as a no.
func readTriage(answers *typesafe.Answers) (triageVerdict, error) {
	dept, err := answers.Choice(string(qDepartment))
	if err != nil {
		return triageVerdict{}, err
	}
	if !department(dept.Choice).known() {
		return triageVerdict{}, fmt.Errorf("the judge answered %q, which is not one of the departments this report knows", dept.Choice)
	}
	reply, err := answers.Noul(string(qNeedsReply))
	if err != nil {
		return triageVerdict{}, err
	}
	urgent, err := answers.Noul(string(qIsUrgent))
	if err != nil {
		return triageVerdict{}, err
	}
	probability := dept.Probabilities[dept.Choice]
	return triageVerdict{
		Department:            department(dept.Choice),
		DepartmentProbability: probability,
		DepartmentConfidence:  dept.Confidence,
		Unsure:                probability < departmentFloor,
		NeedsReply:            reply.Yes(needsReplyThreshold),
		NeedsReplyProbability: reply.Noul,
		Urgent:                urgent.Yes(urgentThreshold),
		UrgentProbability:     urgent.Noul,
	}, nil
}

// polarity is which end of a noul an inspector fires at. It is a typed
// constant rather than a bool because the questions read better as plain
// questions — "does the draft answer what was asked?" — and which end is the
// defect is then a fact about the inspector that a reader should find named.
type polarity int

const (
	// firesAbove is an inspector whose question describes the defect, so a
	// high probability is the flag.
	firesAbove polarity = iota
	// firesBelow is an inspector whose question describes the virtue, so a
	// low probability is the flag.
	firesBelow
)

// inspector is one check on a draft: the question the judge answers, the
// threshold that reads it, and the words a person and the redrafting model
// both see when it fires.
type inspector struct {
	// ID is the question's id.
	ID questionID
	// Label says what fired, written to be read in a report and pasted
	// into the redraft's task.
	Label string
	// Threshold is where this defect starts.
	Threshold float64
	// Fires is which end of the noul the defect is at.
	Fires polarity
	// Question is what the judge is asked.
	Question typesafe.Noul
}

// fired reports whether this inspector's answer is over its threshold.
func (ins inspector) fired(answer typesafe.NoulAnswer) bool {
	if ins.Fires == firesBelow {
		return answer.Noul < ins.Threshold
	}
	return answer.Yes(ins.Threshold)
}

// inspectors are the four checks, in the order they are asked and reported.
// Each threshold is its own because the defects are not equally cheap: an
// invented fact is the one that costs a customer's trust, so it fires on the
// faintest sign, while a tone that reads as a poor fit is a matter of taste
// and needs the judge to be fairly sure.
var inspectors = []inspector{
	{
		ID:        qAnswersTheQuestion,
		Label:     "it does not answer what the sender asked",
		Threshold: 0.50,
		Fires:     firesBelow,
		Question: typesafe.Noul{
			Instructions: "Does the draft answer the question the sender actually asked?",
			True:         "Every question the sender asked is addressed, even if the answer is that it cannot be done or that more information is needed.",
			False:        "The draft acknowledges the message, changes the subject, or answers a question that was not asked, leaving the sender's own question open.",
		},
	},
	{
		ID:        qInventsAFact,
		Label:     "it states a fact the message does not contain",
		Threshold: 0.40,
		Fires:     firesAbove,
		Question: typesafe.Noul{
			Instructions: "Does the draft state a fact about the account, the product, a price or a date that the sender's message does not contain?",
			True:         "The draft asserts something specific — a figure, a date, a setting, a plan's contents, what happened before — that nothing in the sender's message supports.",
			False:        "Everything the draft asserts comes from the sender's message, or is offered as a question, an offer to check, or a plainly general statement.",
		},
	},
	{
		ID:        qUnaskedPromise,
		Label:     "it promises something the sender did not ask for",
		Threshold: 0.50,
		Fires:     firesAbove,
		Question: typesafe.Noul{
			Instructions: "Does the draft commit the recipient to an action, a date, a refund or a discount that the sender did not ask for?",
			True:         "The draft offers a deadline, a credit, a call, a feature or a change of plan that nobody requested, in words that would bind the recipient.",
			False:        "The draft answers what was asked and offers nothing beyond it, or names a next step the sender themselves proposed.",
		},
	},
	{
		ID:        qToneMismatch,
		Label:     "its tone does not fit the sender's",
		Threshold: 0.65,
		Fires:     firesAbove,
		Question: typesafe.Noul{
			Instructions: "Would the sender find the draft's tone a poor fit for their own message?",
			True:         "A brisk, worried or formal message is answered with jokes, marketing warmth or stiffness that reads as a form letter.",
			False:        "The draft meets the sender roughly where they are: as plain, as warm and as formal as the message it answers.",
		},
	},
}

// inspectorQuestions is the four checks as one request. They are independent
// judgments over one state, which is the shape that makes a batch cheap.
func inspectorQuestions() typesafe.Questions {
	questions := make(typesafe.Questions, 0, len(inspectors))
	for _, ins := range inspectors {
		questions = append(questions, typesafe.NamedQuestion{ID: string(ins.ID), Question: ins.Question})
	}
	return questions
}

// flagged is which inspectors fired, in the order they are asked. Combining
// the four answers is code's job and not the judge's: the judge answers four
// small questions it can answer well, and the policy that turns them into
// "show this draft" or "spend the expensive model on it" stays here where it
// can be read and changed.
//
// An answer that is missing or of the wrong type is an error naming the
// inspector, because a check that silently lost a question would pass every
// draft it could not read.
func flagged(answers *typesafe.Answers) ([]inspector, error) {
	var fired []inspector
	for _, ins := range inspectors {
		answer, err := answers.Noul(string(ins.ID))
		if err != nil {
			return nil, fmt.Errorf("the check for %q could not be read: %w", ins.ID, err)
		}
		if ins.fired(answer) {
			fired = append(fired, ins)
		}
	}
	return fired, nil
}

// labels is what fired, in words, for a report line and for the task the
// redrafting model is given.
func labels(fired []inspector) []string {
	out := make([]string, 0, len(fired))
	for _, ins := range fired {
		out = append(out, ins.Label)
	}
	return out
}

// sentence joins labels into one readable clause.
func sentence(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// intent is what the person's turn is asking the agent for. The two are the
// jobs this agent does, and they want different models and different tools:
// reading the inbox is cheap and needs two tools, while writing replies runs
// the whole cascade.
type intent string

const (
	// intentSummary is a question a report of the inbox answers.
	intentSummary intent = "summary"
	// intentDrafts is a request for replies to be written.
	intentDrafts intent = "drafts"
)

// routingQuestions is the one question asked about the person's turn, before
// anything is sent to the conversational model. It is deliberately one
// question over one short state: the turn's own words.
func routingQuestions() typesafe.Questions {
	return typesafe.Questions{
		{ID: string(qIntent), Question: typesafe.Choice{
			Instructions: "What is this person asking their mail assistant to do?",
			Options: []typesafe.Option{
				{Key: string(intentSummary), Description: "They want to know what is in the inbox: what arrived, where it belongs, what is waiting on them, what is urgent. A report answers them."},
				{Key: string(intentDrafts), Description: "They want replies written for them, to messages that need one, whether or not they also want the inbox summarized."},
			},
		}},
	}
}

// readIntent is the routing answer and how concentrated it was. An intent the
// code has no policy for is an error rather than a default, so a question and
// a router that have drifted apart are noticed on the first turn.
func readIntent(answers *typesafe.Answers) (typesafe.ChoiceAnswer, error) {
	answer, err := answers.Choice(string(qIntent))
	if err != nil {
		return typesafe.ChoiceAnswer{}, err
	}
	switch intent(answer.Choice) {
	case intentSummary, intentDrafts:
		return answer, nil
	}
	return typesafe.ChoiceAnswer{}, fmt.Errorf("the router answered %q, which is neither %q nor %q", answer.Choice, intentSummary, intentDrafts)
}
