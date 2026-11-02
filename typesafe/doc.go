// Package typesafe is a blocking client for TypeSafe's Jev, a judgment model. A
// [Client] implements [decision.Decider], so the questions, the answers and the
// [decision.Tool], [decision.AuthoredTool] and [decision.Route] adapters are all
// in [github.com/bensyverson/goodall/decision]; this package holds only what is
// TypeSafe's own: the endpoint, the key, the default model, the error bodies and
// the model catalog. Like every decision model it is deliberately not a
// [goodall.Provider].
//
// What it shares with the provider packages is everything below the API: the
// same HTTP transport with its retries and Retry-After handling, the same
// *[goodall.APIError] with the same cross-provider error kinds, and the same
// [goodall.Usage] counts. It imports goodall's own packages and nothing outside
// the standard library.
//
// # What Jev is bad at
//
// It reads instructions literally, cannot count, do arithmetic or compare
// dates, and degrades on state full of detail the question does not need.
// TypeSafe's own jaggedness notes also say it does not treat adversarial content
// in the state as hostile, which is why [decision] keeps instructions out of the
// state. See project/2026-09-17-typesafe-jev-findings.md for the whole list and
// for when a judgment model is the right tool at all.
//
// # Quick start
//
//	client := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))
//	answers, err := client.Decide(ctx, &decision.Request{
//		State: ticket,
//		Questions: decision.Questions{
//			{ID: "is_urgent", Question: decision.Predicate{Instructions: "Does this convey urgency?"}},
//		},
//	})
//
// Set [decision.Request.Model], or pin it with [decision.WithModel] on an
// adapter, wherever a threshold depends on the model; left empty it is
// [DefaultModel], an alias that moves.
package typesafe
