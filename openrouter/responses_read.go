// A /responses body as neutral blocks, a stop reason and usage. The stream reuses
// the stop reason, the usage and the error, so Collect and Complete agree.

package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"strings"

	"github.com/bensyverson/goodall"
)

// summarySeparator joins a reasoning item's summary parts in Thinking.Text, where
// they read as paragraphs. Raw keeps them apart.
const summarySeparator = "\n\n"

// responsesErrorKinds places the error codes OpenAI documents for a failed
// response, which arrives with no HTTP status to read a kind from. None of them was
// observed, since a failure could not be provoked, and a code not listed stays
// unknown.
var responsesErrorKinds = map[string]goodall.ErrorKind{
	"server_error":        goodall.KindServer,
	"rate_limit_exceeded": goodall.KindRateLimited,
	"invalid_prompt":      goodall.KindInvalidRequest,
	"invalid_image":       goodall.KindUnsupportedInput,
}

// readResponsesBody turns the bytes of a 200 into a neutral response. HTTP 200 is
// not success (invariant 6): a body naming an error, a failed status, or no status
// at all is an error.
func readResponsesBody(raw []byte, header http.Header) (*goodall.Response, error) {
	var body responsesBody
	if err := json.Unmarshal(raw, &body); err != nil {
		if apiErr := decodeError(0, header, raw); apiErr != nil {
			return nil, apiErr
		}
		return nil, fmt.Errorf("openrouter: decoding the response: %w", err)
	}
	if body.Status == "" || body.Status == responsesStatusFailed || body.Error.said() {
		return nil, responsesFailure(body.Error, header, raw)
	}
	blocks, refused, called, err := responsesBlocks(body.Output)
	if err != nil {
		return nil, err
	}
	stop, native := responsesStop(&body, refused, called)
	return &goodall.Response{
		ID:               body.ID,
		Model:            body.Model,
		Message:          goodall.Message{Role: goodall.RoleAssistant, Content: blocks},
		StopReason:       stop,
		NativeStopReason: native,
		Usage:            responsesUsageOf(body.Usage),
	}, nil
}

// responsesFailure is the typed error for a failed response or an error event. A
// failure that says nothing is still reported, with the body kept as Raw.
func responsesFailure(we *wireError, header http.Header, raw []byte) *goodall.APIError {
	if !we.said() {
		we = &wireError{Message: "the response failed and gave no reason"}
	}
	out := inBodyError(we, header, raw)
	if out.Kind == goodall.KindUnknown {
		if kind, ok := responsesErrorKinds[out.Type]; ok {
			out.Kind = kind
		}
	}
	return out
}

// responsesBlocks reads output items into blocks, in order. It also reports
// whether a part refused and whether any item called a tool, which the stop reason
// depends on.
func responsesBlocks(items []jsontext.Value) (blocks goodall.Blocks, refused, called bool, err error) {
	for _, raw := range items {
		var item responsesOutputItem
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, false, false, fmt.Errorf("openrouter: decoding an output item: %w", err)
		}
		switch item.Type {
		case itemReasoning:
			blocks = append(blocks, goodall.Thinking{Text: summaryText(item.Summary), Raw: compactItem(raw)})
		case itemFunctionCall:
			input, err := toolCallInput(toolCall{
				ID:       item.CallID,
				Function: toolCallFunction{Name: item.Name, Arguments: item.Arguments},
			})
			if err != nil {
				return nil, false, false, err
			}
			called = true
			blocks = append(blocks, goodall.ToolUse{ID: item.CallID, Name: item.Name, Input: input})
		case itemMessage:
			for _, part := range item.Content {
				switch {
				case part.Type == partRefusal:
					refused = true
					if part.Refusal != "" {
						blocks = append(blocks, goodall.Text{Text: part.Refusal})
					}
				case part.Text != "":
					blocks = append(blocks, goodall.Text{Text: part.Text})
				}
			}
		default:
			blocks = append(blocks, goodall.Unknown{Type: goodall.BlockType(item.Type), Raw: compactItem(raw)})
		}
	}
	return blocks, refused, called, nil
}

// compactItem is an output item's bytes with the whitespace gone: OpenAI indents a
// blocking body and not a stream, and the two must give one Raw. Compacting keeps
// member order, string escapes and every other byte.
func compactItem(raw jsontext.Value) jsontext.Value {
	out := jsontext.Value(raw.Clone())
	if err := out.Compact(); err != nil {
		// It was read out of a body that decoded, so this cannot happen
		// short of a bug, and the bytes as they came are the best answer.
		return jsontext.Value(raw.Clone())
	}
	return out
}

// summaryText joins a reasoning item's summary parts.
func summaryText(parts []responsesOutputPart) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, summarySeparator)
}

// responsesStop maps a finished response onto goodall's stop reason and the
// provider's own string. A completed response is a tool call or the end of a turn;
// an incomplete one maps its reason; a value goodall does not define travels on
// verbatim (invariant 6).
func responsesStop(body *responsesBody, refused, called bool) (goodall.StopReason, string) {
	switch body.Status {
	case responsesStatusCompleted:
		switch {
		case refused:
			return goodall.StopRefusal, body.Status
		case called:
			return goodall.StopToolUse, body.Status
		default:
			return goodall.StopEndTurn, body.Status
		}
	case responsesStatusIncomplete:
		reason := ""
		if body.IncompleteDetails != nil {
			reason = body.IncompleteDetails.Reason
		}
		switch reason {
		case incompleteMaxOutputTokens:
			return goodall.StopMaxTokens, reason
		case incompleteContentFilter:
			return goodall.StopRefusal, reason
		case "":
			return goodall.StopReason(body.Status), body.Status
		default:
			return goodall.StopReason(reason), reason
		}
	default:
		return goodall.StopReason(body.Status), body.Status
	}
}

// responsesUsageOf turns the wire's report into goodall's counts. The wire's input
// is the whole prompt, so the cached parts come out of it: goodall's
// Usage.TotalInput adds them back up.
func responsesUsageOf(u *responsesUsage) goodall.Usage {
	if u == nil {
		return goodall.Usage{}
	}
	out := goodall.Usage{
		Input:      u.InputTokens - u.InputTokensDetails.CachedTokens - u.InputTokensDetails.CacheWriteTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.InputTokensDetails.CachedTokens,
		CacheWrite: u.InputTokensDetails.CacheWriteTokens,
		Reasoning:  u.OutputTokensDetails.ReasoningTokens,
	}
	if out.Input < 0 {
		out.Input = 0
	}
	return out
}
