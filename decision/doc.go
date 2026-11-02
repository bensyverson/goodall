// Package decision is goodall's neutral vocabulary for decision models: one
// request carries a state, which may be text, an object or an array, and a list
// of typed questions, and the answer is one typed value per question, all
// evaluated over the same state in parallel. There is no conversation, no tool
// call, no thinking and no stream, so a decision model is deliberately not a
// [goodall.Provider]; it sits beside them as the thing an agent asks when the
// question is a judgment rather than a turn.
//
// The package holds the questions ([Predicate], [Choice] and [Score]), the
// answers, the [Decider] seam every server implements, and the adapters written
// over that seam. It makes no HTTP call and its code names no vendor: a client package
// implements Decider and translates to its own wire, and everything here works
// over whichever one it is handed. This package has no default model, and a
// threshold tuned on one model does not carry to another, so pin one with
// [WithModel] wherever a threshold depends on it.
//
// # State is data, not instructions
//
// The state is marshaled as JSON exactly as it is given and is the material the
// model works from. Do not count on a decision model to treat adversarial content
// in it as hostile: a transcript, a web page or a user message placed in the state can
// carry text that reads as an instruction, and the questions are the only thing
// that decides what is asked. Keep the questions and their thresholds in the
// calling code, where they can be reviewed, and put untrusted text only in the
// state.
//
// # A state means what the server makes of it
//
// The servers on this wire turn an object or an array into model input each their
// own way: per their docs as read on 2026-11-02, Ollama serializes either as JSON
// text and does not read it as chat messages, LocalAI renders an object as labeled
// lines, and llama.cpp reads an array as a list of chat messages, so an array of
// records may not mean there what its author intended. goodall passes the state
// through unnormalized, because rewriting it would hide a real difference in what
// the model reads. Where the form matters, build the text yourself and send a
// string, so the rendering is yours and not the server's.
//
// Confidence differs in the same way, as an entropy measure on Ollama and a
// normalized margin for some models on LocalAI, so a threshold tuned on one server
// need not carry to another even on the same model.
//
// # What a judge is bad at
//
// Filter and compute in code and ask the model only the semantic part: a judge
// reads instructions literally and degrades on state full of detail the question
// does not need, and counting, arithmetic and date comparisons belong to the
// caller. Convert numbers into named buckets before asking about them.
//
// # Quick start
//
//	answers, err := decider.Decide(ctx, &decision.Request{
//		Model: model,
//		State: ticket,
//		Questions: decision.Questions{
//			{ID: "department", Question: decision.Choice{
//				Instructions: "Which team should handle this?",
//				Options: []decision.Option{
//					{Key: "billing", Description: "Payments, invoicing, refunds"},
//					{Key: "technical", Description: "Bugs, outages, integrations"},
//				},
//			}},
//			{ID: "is_urgent", Question: decision.Predicate{Instructions: "Does this convey urgency?"}},
//		},
//	})
//	if err != nil {
//		return err
//	}
//	dept, err := answers.Choice("department")
//
// A question the model declines comes back as a [RefusalAnswer] beside the
// others, and its typed accessor returns an error naming the id rather than a
// zero value, which would read as a confident no.
//
// # Reaching the judge from an agent
//
// Three adapters put a judgment in front of, or inside, a [goodall.Agent], and
// none of them needs anything of the core loop that a tool and a hook do not
// already offer:
//
//   - [Tool] is a tool whose questions the developer fixes: the model chooses
//     what to judge and reads back the typed answers. Reach for this one first.
//   - [AuthoredTool] is a tool whose questions the model writes at call time,
//     for judgments that cannot be enumerated in advance. Its input schema
//     carries the guidance about writing a question, and it refuses a question
//     set no server could answer before paying for the call.
//   - [Route] is a [goodall.Hooks] BeforeSend hook: it judges each new turn and
//     hands the answers to a callback that picks the model, narrows the tools,
//     adds a system line or refuses the turn.
package decision
