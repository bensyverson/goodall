package goodall

import (
	"encoding/json/v2"
	"fmt"
)

// TextResult is a successful result carrying one block of text, the shape most
// tools return.
func TextResult(text string) ToolResult {
	return ToolResult{Content: Blocks{Text{Text: text}}}
}

// ErrorResult is a failed result carrying one block of text. The text is
// written for the model, which reads it on its next turn and is expected to
// act on it, so it should say what went wrong and what a correct call looks
// like.
func ErrorResult(text string) ToolResult {
	return ToolResult{IsError: true, Content: Blocks{Text{Text: text}}}
}

// BlocksResult is a successful result carrying the given blocks in order. It
// is how a tool returns an image or a document alongside its text.
func BlocksResult(blocks ...Block) ToolResult {
	return ToolResult{Content: Blocks(blocks)}
}

// JSONResult marshals v with encoding/json/v2 and returns it as one block of
// text. Models read JSON well, so a struct is usually a better result than
// prose the tool formatted itself.
//
// A value that cannot be marshalled is the tool author's mistake, not the
// model's, so it comes back as an error rather than an error result.
func JSONResult(v any) (ToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return ToolResult{}, fmt.Errorf("goodall: marshalling a tool result: %w", err)
	}
	return TextResult(string(b)), nil
}

// Text is the result's text blocks joined together, mirroring Message.Text.
// Image and document blocks are skipped.
func (r ToolResult) Text() string {
	var b []byte
	for _, blk := range r.Content {
		if t, ok := blk.(Text); ok {
			b = append(b, t.Text...)
		}
	}
	return string(b)
}
