package main

// The models the cascade runs on, in one place, because the whole point of the
// example is which work goes to which of them: the cheap one writes, the judge
// checks, and the strong one only sees what the judge flagged.
//
// They are spelled per provider so that -provider moves all three at once
// rather than leaving a run talking to one company's parent and another's
// delegate. An example may not import internal/livemodel, so these are
// deliberate copies of the ids the repository uses elsewhere, as of
// 2026-09-17.

// modelSet is the three roles one provider's names for them.
type modelSet struct {
	// Parent is the agent that reads the inbox and decides what to
	// delegate. It is the middle model: the orchestration is not hard, and
	// the expensive one is kept for the drafts that earned it.
	Parent string
	// Strong is the model a flagged draft is rewritten by, and the model a
	// turn that asks for drafts is run on.
	Strong string
	// Cheap is the model that writes the first draft of every reply.
	Cheap string
}

// The parent and strong ids are the ones the repository's live tests and
// recordings use (claude-sonnet-5 and claude-opus-5, both confirmed against the
// live catalogs on 2026-09-15). The cheap id is Haiku 4.5, the newest Haiku
// either catalog listed on 2026-09-17: GET /v1/models on the Anthropic API
// named claude-haiku-4-5-20251001 and no Haiku 5, and OpenRouter's catalog
// named anthropic/claude-haiku-4.5. A run that is refused for an unknown
// model changes one line here.

// anthropicModels is the three roles as the Anthropic API names them.
var anthropicModels = modelSet{
	Parent: "claude-sonnet-5",
	Strong: "claude-opus-5",
	Cheap:  "claude-haiku-4-5-20251001",
}

// openRouterModels is the same three as the OpenRouter catalog names them, so
// switching provider changes the route and not the correspondent.
var openRouterModels = modelSet{
	Parent: "anthropic/claude-sonnet-5",
	Strong: "anthropic/claude-opus-5",
	Cheap:  "anthropic/claude-haiku-4.5",
}

// defaultJudgeModel is the Jev version every judgment is asked of. It is the
// versioned id rather than the jev-latest alias because TypeSafe tunes each
// version's confidence separately, and every threshold in questions.go was
// written against this one.
const defaultJudgeModel = "jev-1.13.0"
