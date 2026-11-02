// The wire structs of OpenAI's /responses, in both directions. Everything spelled
// this wire's way stops at the responses_*.go files. Nothing here is a Go map but
// the metadata, which marshals its keys in sorted order, so two identical requests
// are identical bytes (invariant 8).

package openrouter

import (
	"encoding/json/jsontext"

	"github.com/bensyverson/goodall"
)

// responsesItemType is the type of one input or output item.
type responsesItemType string

const (
	// itemMessage is a turn of text and media, in either direction.
	itemMessage responsesItemType = "message"
	// itemReasoning is the model's thinking, which goes back whole.
	itemReasoning responsesItemType = "reasoning"
	// itemFunctionCall is a tool call.
	itemFunctionCall responsesItemType = "function_call"
	// itemFunctionCallOutput is the answer to one tool call.
	itemFunctionCallOutput responsesItemType = "function_call_output"
)

// responsesPartType is the type of one part of a message or a tool's output.
type responsesPartType string

const (
	partInputText  responsesPartType = "input_text"
	partInputImage responsesPartType = "input_image"
	partInputFile  responsesPartType = "input_file"
	partOutputText responsesPartType = "output_text"
	partRefusal    responsesPartType = "refusal"
)

// The values the wire's status members take that goodall treats specially.
const (
	responsesStatusCompleted  = "completed"
	responsesStatusIncomplete = "incomplete"
	responsesStatusFailed     = "failed"
)

// The reasons an incomplete response gives. content_filter was seen only in a
// documentation sample.
const (
	incompleteMaxOutputTokens = "max_output_tokens"
	incompleteContentFilter   = "content_filter"
)

// reasoningSummaryAuto asks for a readable summary at the level the model picks.
// Every recording that set it had the response report "detailed".
const reasoningSummaryAuto = "auto"

// responsesRequest is the body of POST /responses. The input is last, so a
// conversation's next turn extends this turn's bytes (invariant 8). Store is
// always false and has no omitzero: goodall owns the conversation, a pause can
// outlive the process, and the server's default is to keep it.
type responsesRequest struct {
	Model             string               `json:"model"`
	Instructions      string               `json:"instructions,omitzero"`
	Tools             []responsesTool      `json:"tools,omitzero"`
	ToolChoice        *responsesToolChoice `json:"tool_choice,omitzero"`
	ParallelToolCalls *bool                `json:"parallel_tool_calls,omitzero"`
	Reasoning         *responsesReasoning  `json:"reasoning,omitzero"`
	MaxOutputTokens   int                  `json:"max_output_tokens,omitzero"`
	Metadata          metadata             `json:"metadata,omitzero"`
	SafetyIdentifier  string               `json:"safety_identifier,omitzero"`
	Store             bool                 `json:"store"`
	Stream            bool                 `json:"stream,omitzero"`
	// Input holds marshaled items, because a reasoning item is the provider's
	// own bytes and the others are built to sit beside it.
	Input []jsontext.Value `json:"input"`
}

// responsesTool is one tool, flat where the Chat shape nests a function. Strict
// has no omitzero: the strict_absent recording shows a tool that leaves it out
// having its schema rewritten, with optional properties moved into required.
type responsesTool struct {
	Type        functionType    `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitzero"`
	Parameters  *goodall.Schema `json:"parameters,omitzero"`
	Strict      bool            `json:"strict"`
}

// responsesToolChoice is a mode as a bare string, or a function by name as an
// object, the forms the tool_choice_* recordings show.
type responsesToolChoice struct {
	Mode string
	Name string
}

// MarshalJSONTo writes whichever form the choice is.
func (c responsesToolChoice) MarshalJSONTo(enc *jsontext.Encoder) error {
	if c.Name == "" {
		return enc.WriteToken(jsontext.String(c.Mode))
	}
	for _, tok := range []jsontext.Token{
		jsontext.BeginObject,
		jsontext.String("type"), jsontext.String(string(functionTool)),
		jsontext.String("name"), jsontext.String(c.Name),
		jsontext.EndObject,
	} {
		if err := enc.WriteToken(tok); err != nil {
			return err
		}
	}
	return nil
}

// responsesReasoning is how hard to think and whether to summarize it.
type responsesReasoning struct {
	Effort  string `json:"effort,omitzero"`
	Summary string `json:"summary,omitzero"`
}

// responsesMessage is a message input item.
type responsesMessage struct {
	Type    responsesItemType `json:"type"`
	Role    role              `json:"role"`
	Content []responsesPart   `json:"content"`
}

// responsesPart is one part of a message or a tool's output, carrying only the
// members of its own type.
type responsesPart struct {
	Type     responsesPartType `json:"type"`
	Text     string            `json:"text,omitzero"`
	ImageURL string            `json:"image_url,omitzero"`
	Filename string            `json:"filename,omitzero"`
	FileData string            `json:"file_data,omitzero"`
	FileURL  string            `json:"file_url,omitzero"`
}

// responsesFunctionCall is a function_call input item, rebuilt from a neutral
// block. The minimal_call recording shows these three members are enough.
type responsesFunctionCall struct {
	Type      responsesItemType `json:"type"`
	CallID    string            `json:"call_id"`
	Name      string            `json:"name"`
	Arguments string            `json:"arguments"`
}

// responsesFunctionOutput is a function_call_output input item. Output is a string
// or a list of parts, and has no omitzero because an empty one is still an answer.
type responsesFunctionOutput struct {
	Type   responsesItemType `json:"type"`
	CallID string            `json:"call_id"`
	Output jsontext.Value    `json:"output"`
}

// responsesBody is a whole response: a blocking body, or the response member of a
// stream's terminal event. Output stays raw so a reasoning item can keep its bytes.
type responsesBody struct {
	ID                string               `json:"id,omitzero"`
	Model             string               `json:"model,omitzero"`
	Status            string               `json:"status,omitzero"`
	Error             *wireError           `json:"error,omitzero"`
	IncompleteDetails *responsesIncomplete `json:"incomplete_details,omitzero"`
	Output            []jsontext.Value     `json:"output,omitzero"`
	Usage             *responsesUsage      `json:"usage,omitzero"`
}

// responsesIncomplete says why a response stopped early.
type responsesIncomplete struct {
	Reason string `json:"reason,omitzero"`
}

// responsesUsage is the wire's usage report. Input is the whole prompt, cached
// reads and cache writes included.
type responsesUsage struct {
	InputTokens        int `json:"input_tokens,omitzero"`
	InputTokensDetails struct {
		CachedTokens     int `json:"cached_tokens,omitzero"`
		CacheWriteTokens int `json:"cache_write_tokens,omitzero"`
	} `json:"input_tokens_details"`
	OutputTokens        int `json:"output_tokens,omitzero"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens,omitzero"`
	} `json:"output_tokens_details"`
}

// responsesOutputItem is the part of an output item goodall reads. An item of a
// type it does not know is read no further than its type.
type responsesOutputItem struct {
	Type      responsesItemType     `json:"type,omitzero"`
	CallID    string                `json:"call_id,omitzero"`
	Name      string                `json:"name,omitzero"`
	Arguments string                `json:"arguments,omitzero"`
	Content   []responsesOutputPart `json:"content,omitzero"`
	Summary   []responsesOutputPart `json:"summary,omitzero"`
}

// responsesOutputPart is one part of an output message, or one summary part of a
// reasoning item. A refusal part holds its text in a member of its own.
type responsesOutputPart struct {
	Type    responsesPartType `json:"type,omitzero"`
	Text    string            `json:"text,omitzero"`
	Refusal string            `json:"refusal,omitzero"`
}
