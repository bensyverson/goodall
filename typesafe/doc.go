// Package typesafe is a blocking client for TypeSafe's Jev, a judgment model:
// one request carries a state — text, an object or an array — and a list of
// typed questions, and the answer is one typed value per question, all
// evaluated over the same state in parallel. There is no conversation, no tool
// call, no thinking and no stream, so a Client is deliberately not a
// [goodall.Provider] and not a [goodall.Completer]; it sits beside them as the
// thing an agent asks when the question is a judgment rather than a turn.
//
// What it shares with the provider packages is everything below the API: the
// same HTTP transport with its retries and Retry-After handling, the same
// *[goodall.APIError] with the same cross-provider error kinds, and the same
// [goodall.Usage] counts. It imports the root package and nothing outside the
// standard library.
//
// # State is data, not instructions
//
// The state is marshaled as JSON exactly as it is given and is the material
// the model works from. Jev does not treat adversarial content in it as
// hostile: TypeSafe's own jaggedness notes say so, so a transcript, a web page
// or a user message placed in the state can carry text that reads as an
// instruction, and the questions are the only thing that decides what is
// asked. Keep the questions and their thresholds in the calling code, where
// they can be reviewed, and put untrusted text only in the state.
//
// # What Jev is bad at
//
// It reads instructions literally, cannot count, do arithmetic or compare
// dates, and degrades on state full of detail the question does not need.
// Filter and compute in code, ask the model only the semantic part, and
// convert numbers into named buckets before asking about them. See
// project/2026-09-17-typesafe-jev-findings.md for the whole list and for when
// a judgment model is the right tool at all.
//
// # Quick start
//
//	client := typesafe.New(os.Getenv("TYPESAFE_API_KEY"))
//	answers, err := client.Ask(ctx, ticket, typesafe.Questions{
//		{ID: "department", Question: typesafe.Choice{
//			Instructions: "Which team should handle this?",
//			Options: []typesafe.Option{
//				{Key: "billing", Description: "Payments, invoicing, refunds"},
//				{Key: "technical", Description: "Bugs, outages, integrations"},
//			},
//		}},
//		{ID: "is_urgent", Question: typesafe.Noul{Instructions: "Does this convey urgency?"}},
//	})
//	if err != nil {
//		return err
//	}
//	dept, err := answers.Choice("department")
package typesafe
