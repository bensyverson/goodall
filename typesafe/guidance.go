package typesafe

// The prose a model reads when it is given a judgment tool. It is product
// surface, so it lives in one file where it can be reviewed as text rather than
// scattered through the code that uses it.
//
// Two rules decide what goes here and what does not. Guidance about a *field*
// belongs in that field's schema description — the `desc` tags in
// tool_authored.go — because that is what the model is looking at when it fills
// the field in; a rule several thousand tokens away in a system prompt enforces
// nothing. And guidance about the *tool* is as short as it can be, because every
// sentence of it competes for attention with the consumer's own prompt.
//
// Nothing here goes into a system prompt. A consumer who wants the judge
// described differently replaces the text with [WithDescription].

// DefaultAuthoredDescription is the description an [AuthoredTool] carries
// unless [WithDescription] replaces it. It says what the judge is for, the
// habit that makes it cheap — everything in one call — and the work to do
// before asking, since the judge reads and interprets rather than computes.
const DefaultAuthoredDescription = "Judge material against questions you write yourself: one call sends the material once and answers " +
	"every question about it in parallel, so ask everything you want to know in a single call rather than one question at a time. " +
	"Each answer comes back typed and ready to act on: a probability for a yes-or-no question, a named option with its whole " +
	"distribution for a choice, a level on your own rubric for a score. " +
	"The judge reads each question literally and answers only what its words ask, so state the exact condition you mean and name " +
	"the options you want back. " +
	"It reads and interprets; work out anything that needs counting, arithmetic, a date comparison or a look at an image yourself, " +
	"and ask the judge the part that needs reading."
