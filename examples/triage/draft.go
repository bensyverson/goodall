package main

import (
	"time"

	"github.com/bensyverson/goodall"
)

// The two delegates. Writing is the work this agent hands off: the cheap model
// writes every first draft, and the strong one is spent only on a draft the
// judge flagged, which is the whole shape of the cascade.
const (
	toolDraft   = "draft_reply"
	toolRedraft = "redraft_reply"
)

// draftBudget bounds a delegated run. A delegate has no tools and one thing to
// write, so a single turn is the shape of the work and the budget says so; the
// timeout is there because a hung delegate would otherwise hold the parent's
// call open for as long as the provider let it.
var draftBudget = goodall.Budget{MaxTurns: 1, MaxTokens: 20000, Timeout: 2 * time.Minute}

// draftSystem is the cheap model's standing instruction.
//
// It carries the two facts that shape every draft — nothing is sent, and the
// writer sees only what the task quotes — because a model that knows why it
// cannot look something up asks for it in the draft instead of inventing it.
const draftSystem = "You write short reply drafts for one person's mail, in plain text, ready for them to read and send themselves. " +
	"Nothing you write is sent: it goes into a report for that person to approve, so the draft is the whole of your answer, with no preamble about it.\n\n" +
	"You are given one message and what it asks for, and that is all you can see: no account, no order history, no calendar. " +
	"A draft that stays inside what the message itself contains is one the person can send unchanged, so where an answer needs a fact you were not given, " +
	"write the sentence that asks for it or says it will be checked.\n\n" +
	"Meet the sender where they are: as plain, as warm and as formal as the message you are answering, and as short as it can be while answering everything they asked."

// redraftSystem is the strong model's standing instruction.
//
// A redraft is a repair, so the task it is given names the draft, the message
// and which inspectors fired on it; this says what those names mean and what
// to do with them. The inspectors are the four in questions.go, described here
// as the defects they are so that fixing one is a concrete edit rather than a
// rewrite.
const redraftSystem = "You rewrite reply drafts that a set of inspectors flagged, for one person's mail, in plain text. " +
	"Nothing you write is sent: it goes into a report for that person to approve, so the rewritten draft is the whole of your answer.\n\n" +
	"The task names the message, the draft as it stands, and which of four inspectors fired on it: that it does not answer what the sender asked, " +
	"that it states a fact the message does not contain, that it promises something the sender did not ask for, or that its tone does not fit the sender's. " +
	"Each names a specific defect, so repair those and leave the rest of the draft alone — the flagged problems are why this draft reached you, " +
	"and a wholesale rewrite loses the parts that were already right.\n\n" +
	"Where the draft asserted something the message does not contain, the repair is to ask for it or to say it will be checked, " +
	"because the person reading this report can only send a draft whose every claim they can stand behind."

// draftTool is the delegate that writes a first draft: a child agent on the
// cheap model, with its own system prompt, its own budget and no tools.
//
// It is a goodall.AgentTool, so the parent's model sees one tool call while
// every event of the child's run reaches the parent's stream nested under it,
// and what the child spent is declared on that call rather than folded into
// the parent's own totals.
func draftTool(provider goodall.Provider, model string) (goodall.Tool, error) {
	child := &goodall.Agent{
		Provider: provider,
		Model:    model,
		System:   draftSystem,
		Budget:   draftBudget,
	}
	return goodall.AgentTool(child, toolDraft,
		"Hand one reply to a cheaper writing model. The prompt is the whole brief: quote the sender, the subject and what they asked, "+
			"and say what the reply should do. It sees nothing of this conversation and cannot look anything up, so a brief that leaves a fact out gets a draft that asks for it.")
}

// redraftTool is the delegate a flagged draft earns: a child agent on the
// strong model, called once, before the person is troubled.
//
// The judge sits between the two — the cheap model writes, the inspectors
// decide which drafts are worth the expensive one, and only a draft that is
// still flagged after this reaches the person.
func redraftTool(provider goodall.Provider, model string) (goodall.Tool, error) {
	child := &goodall.Agent{
		Provider: provider,
		Model:    model,
		System:   redraftSystem,
		Budget:   draftBudget,
	}
	return goodall.AgentTool(child, toolRedraft,
		"Hand a flagged draft to the stronger model, once. The prompt carries the sender's message, the draft as it stands and the problems "+
			"check_draft named, in its own words, so the rewrite repairs those rather than starting again. Check the result with check_draft.")
}
