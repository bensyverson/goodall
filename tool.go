package goodall

import (
	"context"
	"encoding/json/jsontext"
)

// Tool is something the model can call. NewTool builds one from a typed Go
// handler, inferring the schema from the handler's input type; a consumer
// with unusual needs implements the interface directly.
//
// Execute receives the model's argument object exactly as produced. It
// returns a ToolResult on every path the model should hear about, including
// a bad input, with IsError set so the model can correct itself; the error
// return is for failures of the tool itself, which the loop also turns into
// an error result so that every tool_use still gets a tool_result. The loop
// fills in the result's ToolUseID.
type Tool interface {
	Name() string
	Description() string
	Schema() *Schema
	Execute(ctx context.Context, input jsontext.Value) (ToolResult, error)
}
