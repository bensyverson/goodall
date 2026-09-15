package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"

	"github.com/bensyverson/goodall"
)

// costCurrency is the currency OpenRouter charges in and reports in.
const costCurrency = "USD"

// translateResponse turns a blocking completion into a neutral response. The
// first choice is the answer; goodall asks for one and OpenRouter returns one.
//
// The blocks come back in the order they were produced: the reasoning_details
// entries first, then the message content, then the tool calls. A refusal is
// carried as a text block and sets the stop reason, because a model declining
// is an answer rather than an error.
//
// A body whose choices are empty is an error here: OpenRouter reports a
// failure inside a 200 response, and the caller reads the body's error object
// rather than this.
func translateResponse(body *chatResponse) (*goodall.Response, error) {
	if body == nil || len(body.Choices) == 0 {
		return nil, fmt.Errorf("openrouter: the response carried no choices")
	}
	first := body.Choices[0]
	blocks, err := messageBlocks(first.Message)
	if err != nil {
		return nil, err
	}
	usage, cost := translateUsage(body.Usage)
	stop := stopReason(first.FinishReason)
	if first.Message.Refusal != "" {
		stop = goodall.StopRefusal
	}
	return &goodall.Response{
		ID:               body.ID,
		Model:            body.Model,
		Message:          goodall.Message{Role: goodall.RoleAssistant, Content: blocks},
		StopReason:       stop,
		NativeStopReason: first.NativeFinishReason,
		Usage:            usage,
		Cost:             cost,
	}, nil
}

// messageBlocks turns one assistant message into neutral blocks.
func messageBlocks(m chatMessage) (goodall.Blocks, error) {
	var blocks goodall.Blocks
	for _, entry := range m.ReasoningDetails {
		block, ok := reasoningBlock(entry)
		if ok {
			blocks = append(blocks, block)
		}
	}
	if len(m.ReasoningDetails) == 0 && m.Reasoning != "" {
		// A server that sends the flat string and no entries, a Generic
		// or LM Studio endpoint, still had its thinking, just without
		// the provider bytes that would let it re-send verbatim.
		blocks = append(blocks, goodall.Thinking{Text: m.Reasoning})
	}
	switch m.Content.Kind {
	case contentString:
		if m.Content.Text != "" {
			blocks = append(blocks, goodall.Text{Text: m.Content.Text})
		}
	case contentParts:
		for _, part := range m.Content.Parts {
			if part.Type == partText {
				blocks = append(blocks, goodall.Text{Text: part.Text})
			}
		}
	}
	if m.Refusal != "" {
		blocks = append(blocks, goodall.Text{Text: m.Refusal})
	}
	for _, call := range m.ToolCalls {
		input, err := toolCallInput(call)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, goodall.ToolUse{ID: call.ID, Name: call.Function.Name, Input: input})
	}
	return blocks, nil
}

// reasoningBlock turns one reasoning_details entry into a thinking block,
// keeping the entry's own bytes so the next request re-sends it untouched.
//
// An entry of a type goodall does not know — OpenRouter has added
// reasoning.server_tool_call, and will add more — becomes a Thinking block
// with nothing but its raw bytes. That keeps one re-emission path for every
// entry and one place for a UI to skip, where a goodall.Unknown block would
// need the wire format's rules written a second time on the way out.
func reasoningBlock(entry jsontext.Value) (goodall.Block, bool) {
	if len(entry) == 0 {
		return nil, false
	}
	raw := jsontext.Value(entry.Clone())
	var detail reasoningDetail
	if err := json.Unmarshal(entry, &detail); err != nil {
		return goodall.Thinking{Raw: raw}, true
	}
	switch detail.Type {
	case reasoningText:
		return goodall.Thinking{Text: detail.Text, Signature: detail.Signature, Raw: raw}, true
	case reasoningSummary:
		return goodall.Thinking{Text: detail.Summary, Raw: raw}, true
	case reasoningEncrypted:
		return goodall.RedactedThinking{Data: detail.Data, Raw: raw}, true
	default:
		return goodall.Thinking{Raw: raw}, true
	}
}

// toolCallInput reads a tool call's arguments, which arrive as a JSON string
// and travel on as the unparsed object the tool decodes itself. Empty
// arguments are the wire's way of spelling a call with no parameters.
//
// The bytes are compacted, which is both how they are checked for validity
// and what keeps a re-sent tool call byte-identical whatever whitespace the
// provider used. Member order and number literals are preserved.
func toolCallInput(call toolCall) (jsontext.Value, error) {
	args := call.Function.Arguments
	if args == "" {
		return jsontext.Value("{}"), nil
	}
	value := jsontext.Value(args)
	if err := value.Compact(); err != nil {
		return nil, fmt.Errorf("openrouter: tool call %q to %q: arguments are not valid JSON: %w",
			call.ID, call.Function.Name, err)
	}
	return value, nil
}

// stopReason maps OpenRouter's normalized finish reason onto goodall's. A
// value goodall does not define travels on verbatim rather than being
// flattened into a catch-all; the upstream model's own string arrives
// separately as the response's NativeStopReason.
func stopReason(finish string) goodall.StopReason {
	switch finish {
	case "":
		return goodall.StopNone
	case "stop":
		return goodall.StopEndTurn
	case "length":
		return goodall.StopMaxTokens
	case "tool_calls":
		return goodall.StopToolUse
	case "content_filter":
		return goodall.StopRefusal
	default:
		return goodall.StopReason(finish)
	}
}

// translateUsage turns the wire's usage report into goodall's counts and the
// cost the provider charged. The stream leaf calls it on the trailing usage
// frame, which carries the same shape.
//
// The wire's prompt_tokens is the whole prompt, cached reads and cache writes
// included, so those are subtracted out of Input: goodall's Usage.TotalInput
// adds the three back up and must come to the same number.
//
// The cost decodes straight into a Decimal from the JSON number, never
// through a float64, which would round a fraction of a cent away. A body that
// reported no cost yields the zero Cost, which reads as "not known" rather
// than "free".
func translateUsage(u *usage) (goodall.Usage, goodall.Cost) {
	if u == nil {
		return goodall.Usage{}, goodall.Cost{}
	}
	out := goodall.Usage{Input: u.PromptTokens, Output: u.CompletionTokens}
	if d := u.PromptTokensDetails; d != nil {
		out.CacheRead = d.CachedTokens
		out.CacheWrite = d.CacheWriteTokens
		out.Input -= d.CachedTokens + d.CacheWriteTokens
	}
	if d := u.CompletionTokensDetails; d != nil {
		out.Reasoning = d.ReasoningTokens
	}
	if out.Input < 0 {
		out.Input = 0
	}
	var cost goodall.Cost
	if u.Cost != nil {
		cost = goodall.Cost{Amount: *u.Cost, Currency: costCurrency, Reported: true}
	}
	return out, cost
}
