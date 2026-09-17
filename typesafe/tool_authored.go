package typesafe

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"strconv"

	"github.com/bensyverson/goodall"
)

// The model-authored judgment tool. Every `desc` tag below is prose a model
// reads at the moment it fills that member in, which is the only place a rule
// about a field is reliably in front of it; see guidance.go for why the tool's
// own description carries so little.

// authoredJudgment is the input schema of an [AuthoredTool]: the material to
// judge and the questions to ask about it, both written by the model.
type authoredJudgment struct {
	// State is the material to judge, as the model wrote it.
	State jsontext.Value `json:"state" desc:"the material to judge: a string of text, or a JSON object or array of records. Send only what the questions need — judgment degrades on detail that no question asks about"`
	// Questions are the questions to ask about that state, in the order
	// the model wrote them.
	Questions []authoredQuestion `json:"questions" desc:"every question to ask about this state, judged in parallel in one call. Ask all of them here rather than calling again"`
}

// authoredQuestion is one question as a model writes it. The three type-specific
// members are flat rather than nested because a model fills a flat object more
// reliably than a discriminated union, and each one says which type it belongs
// to.
type authoredQuestion struct {
	// ID is the key the answer comes back under.
	ID string `json:"id" desc:"a short name for this question, such as is_urgent; the answer comes back under it and the judge never sees it"`
	// Type is the question's type.
	Type QuestionType `json:"type" enum:"noul,choice,score" desc:"noul for a yes-or-no question, choice for one of a named set, score for a level on a rubric"`
	// Instructions is the question itself.
	Instructions string `json:"instructions" desc:"the question to judge, as the exact condition you mean: the judge reads this literally and answers only what these words ask"`
	// True describes what a yes means, on a noul question.
	True string `json:"true,omitzero" desc:"noul only, optional: what a yes means here"`
	// False describes what a no means, on a noul question.
	False string `json:"false,omitzero" desc:"noul only, optional: what a no means here"`
	// Options are the options to choose between, on a choice question.
	Options []authoredOption `json:"options,omitzero" desc:"choice only: name every option the state could fall into, and add a none-of-the-above option when the list might not cover it"`
	// Levels are the rubric, on a score question.
	Levels []string `json:"levels,omitzero" desc:"score only: at least two levels, lowest first, each describing a concrete situation rather than a grade"`
}

// authoredOption is one option of a model-written choice question.
type authoredOption struct {
	// Key is the option's name, which the answer reports.
	Key string `json:"key" desc:"the option's name, which is what the answer reports"`
	// Description is the rubric for this option.
	Description string `json:"description,omitzero" desc:"what belongs in this option, in enough detail to tell it from the others"`
}

// AuthoredTool wraps a [Client] as a [goodall.Tool] that lets the model write
// both the state and the questions at call time. It is the delegation shape: a
// conversational model hands a batch of judgments to a model that answers them
// in parallel for a fraction of the price, and reads back typed answers.
//
// It is a separate constructor from [Tool], and the second one to reach for,
// because the questions are the part a judgment pipeline wants under review:
// the judge reads them literally, so a vague condition or an unnamed option
// produces an answer that is confidently about the wrong thing, and questions
// written per call cannot be inspected before they are asked. Where the
// questions can be known in advance, [Tool] is the better trade.
//
// The description defaults to [DefaultAuthoredDescription] and
// [WithDescription] replaces it; the guidance about each member of a question
// travels in that member's schema description, which is what the model is
// reading as it writes one.
//
// A question set this package can refuse costs nothing: an empty list, a type
// other than noul, choice or score — the API has a fourth, bounding_box, that
// goodall does not model — a choice with no options, a score with fewer than
// two levels and two questions sharing an id all come back as a result with
// IsError set, naming the question and what to write instead, before any call
// is made. A judgment the API refuses carries the API's own message. The model
// reads either one and writes a better question set.
//
// It returns an error for a nil client and for a definition no provider would
// accept — the naming rules are [goodall.NewTool]'s:
//
//	judge, err := typesafe.AuthoredTool(client, "judge")
func AuthoredTool(client *Client, name string, opts ...ToolOption) (goodall.Tool, error) {
	if client == nil {
		return nil, &toolError{name: name, err: errNoClient}
	}
	cfg, ask := settleTool(opts)
	description := DefaultAuthoredDescription
	if cfg.described {
		description = cfg.description
	}
	return goodall.NewTool(name, description, func(ctx context.Context, in authoredJudgment) (goodall.ToolResult, error) {
		questions, err := authoredQuestions(in.Questions)
		if err != nil {
			return goodall.ErrorResult(err.Error()), nil
		}
		return judge(ctx, client, in.State, questions, ask...)
	})
}

// authoredQuestions translates what the model wrote into a [Questions],
// refusing the sets this package knows the API cannot answer. Every message is
// written for the model, which reads it and is expected to write a better
// question, so each one names the question and what to write instead.
func authoredQuestions(written []authoredQuestion) (Questions, error) {
	if len(written) == 0 {
		return nil, fmt.Errorf("the questions list is empty; ask at least one question about the state, and ask every question you have in this one call")
	}
	out := make(Questions, 0, len(written))
	for _, w := range written {
		question, err := w.question()
		if err != nil {
			return nil, err
		}
		out = append(out, NamedQuestion{ID: w.ID, Question: question})
	}
	return out, nil
}

// question builds the typed question one written question describes.
func (w authoredQuestion) question() (Question, error) {
	switch w.Type {
	case TypeNoul:
		return Noul{Instructions: w.Instructions, True: w.True, False: w.False}, nil
	case TypeChoice:
		if len(w.Options) == 0 {
			return nil, w.problem("is a choice and names no options; list every option the state could fall into, each with a key and a description")
		}
		options := make([]Option, 0, len(w.Options))
		for _, opt := range w.Options {
			options = append(options, Option{Key: opt.Key, Description: opt.Description})
		}
		return Choice{Instructions: w.Instructions, Options: options}, nil
	case TypeScore:
		if len(w.Levels) < 2 {
			return nil, w.problem("is a score with %d %s; list at least two levels, lowest first, each describing a concrete situation",
				len(w.Levels), plural(len(w.Levels), "level"))
		}
		return Score{Instructions: w.Instructions, Levels: w.Levels}, nil
	}
	return nil, w.problem("asks for the type %q; ask for %s, %s or %s, and work anything else out yourself",
		w.Type, TypeNoul, TypeChoice, TypeScore)
}

// problem is one refusal, naming the question it is about. A call carrying
// twenty questions gives the model nothing else to go on.
func (w authoredQuestion) problem(format string, args ...any) error {
	return fmt.Errorf("question %s "+format, append([]any{strconv.Quote(w.ID)}, args...)...)
}

// plural is the singular or plural of a count, so that a refusal never reads
// "1 levels", which looks to a model like a bug in the tool.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
