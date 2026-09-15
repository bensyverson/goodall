// Package openrouter talks to OpenRouter and to any other server that speaks
// the OpenAI Chat Completions format. The wire format is shared; the quirks
// are not, so a typed [Dialect] decides which of OpenRouter's own members a
// request may carry and LM Studio, OpenAI and other compatible servers work
// by configuration rather than by a second client.
//
// The wire structs, here for what goes out and in wire_response.go for what
// comes back, are deliberately unexported: the package's public surface is
// the dialect, the request extensions and the client, and a caller who wants
// to shape a request does it through [goodall.Request]. Their fields are
// declared in the order they are written, so the bytes of a request are
// deterministic and its prefix stays byte-identical between the turns of a
// run.
package openrouter

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"maps"
	"slices"

	"github.com/bensyverson/goodall"
)

// ProviderName is the name goodall knows this provider by. It is what an
// [Extensions] value answers to goodall.Extension.Provider and what appears on
// the errors this package raises.
const ProviderName = "openrouter"

// role is a chat message's role. OpenRouter also accepts "developer", which
// goodall has no neutral concept for and so never sends.
type role string

const (
	roleSystem    role = "system"
	roleUser      role = "user"
	roleAssistant role = "assistant"
	roleTool      role = "tool"
)

// partType is the discriminator of a content part.
type partType string

const (
	partText     partType = "text"
	partImageURL partType = "image_url"
	partFile     partType = "file"
)

// functionType is the only tool type Chat Completions defines.
type functionType string

const functionTool functionType = "function"

// cacheType is the only cache-control type OpenRouter defines.
type cacheType string

const cacheEphemeral cacheType = "ephemeral"

// chatRequest is the body of POST /chat/completions. Every member after the
// first two is optional; the ones OpenRouter added to the OpenAI format are
// gated by the dialect's [Quirks] so a plain OpenAI-compatible server never
// sees them.
type chatRequest struct {
	Model             string           `json:"model"`
	Messages          []chatMessage    `json:"messages"`
	Tools             []wireTool       `json:"tools,omitzero"`
	ToolChoice        *toolChoice      `json:"tool_choice,omitzero"`
	ParallelToolCalls *bool            `json:"parallel_tool_calls,omitzero"`
	MaxTokens         int              `json:"max_tokens,omitzero"`
	Stop              []string         `json:"stop,omitzero"`
	Stream            bool             `json:"stream"`
	Reasoning         *reasoningConfig `json:"reasoning,omitzero"`
	ReasoningEffort   string           `json:"reasoning_effort,omitzero"`
	CacheControl      *cacheControl    `json:"cache_control,omitzero"`
	Provider          *ProviderRouting `json:"provider,omitzero"`
	Models            []string         `json:"models,omitzero"`
	Plugins           []Plugin         `json:"plugins,omitzero"`
	SessionID         string           `json:"session_id,omitzero"`
	Debug             *Debug           `json:"debug,omitzero"`
	User              string           `json:"user,omitzero"`
	Metadata          metadata         `json:"metadata,omitzero"`
}

// chatMessage is one message in either direction. Request and response share
// one struct because they share one wire shape: the members a request never
// sets are the ones a response never returns, and two definitions would drift
// where the type checker cannot see across the encode/decode seam.
type chatMessage struct {
	Role             role             `json:"role"`
	Content          content          `json:"content"`
	ToolCallID       string           `json:"tool_call_id,omitzero"`
	ToolCalls        []toolCall       `json:"tool_calls,omitzero"`
	Reasoning        string           `json:"reasoning,omitzero"`
	ReasoningDetails []jsontext.Value `json:"reasoning_details,omitzero"`
	Refusal          string           `json:"refusal,omitzero"`
	Annotations      []jsontext.Value `json:"annotations,omitzero"`
}

// contentKind says which of the three shapes a message's content takes on the
// wire: a JSON null, a bare string, or an array of parts. It is a typed
// constant rather than a pair of "is this set" booleans because the three
// states are mutually exclusive and the encoder must pick exactly one.
type contentKind uint8

const (
	// contentNull is the zero value, written as JSON null. An assistant
	// message that only carries tool calls has no content.
	contentNull contentKind = iota
	// contentString is a bare JSON string, which is what an assistant
	// message and a tool result use.
	contentString
	// contentParts is an array of typed parts, which is what carries
	// images, files and per-part cache markers.
	contentParts
)

// content is a message's content in whichever of the three wire shapes it
// takes.
type content struct {
	Kind  contentKind
	Text  string
	Parts []contentPart
}

// stringContent is content written as a bare JSON string.
func stringContent(s string) content { return content{Kind: contentString, Text: s} }

// partsContent is content written as an array of typed parts.
func partsContent(parts []contentPart) content {
	return content{Kind: contentParts, Parts: parts}
}

// MarshalJSONTo writes the content in the shape its kind names.
func (c content) MarshalJSONTo(enc *jsontext.Encoder) error {
	switch c.Kind {
	case contentString:
		return enc.WriteToken(jsontext.String(c.Text))
	case contentParts:
		return json.MarshalEncode(enc, c.Parts)
	default:
		return enc.WriteToken(jsontext.Null)
	}
}

// UnmarshalJSONFrom reads whichever of the three shapes arrived.
func (c *content) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	switch dec.PeekKind() {
	case '"':
		var s string
		if err := json.UnmarshalDecode(dec, &s); err != nil {
			return err
		}
		*c = stringContent(s)
		return nil
	case '[':
		var parts []contentPart
		if err := json.UnmarshalDecode(dec, &parts); err != nil {
			return err
		}
		*c = partsContent(parts)
		return nil
	default:
		if _, err := dec.ReadValue(); err != nil {
			return err
		}
		*c = content{}
		return nil
	}
}

// contentPart is one typed part of a message's content.
type contentPart struct {
	Type         partType      `json:"type"`
	Text         string        `json:"text,omitzero"`
	ImageURL     *imageURL     `json:"image_url,omitzero"`
	File         *filePart     `json:"file,omitzero"`
	CacheControl *cacheControl `json:"cache_control,omitzero"`
}

// imageURL is an image part's payload: a fetchable URL or a data: URL.
type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitzero"`
}

// filePart is a file part's payload. FileData is a data: URL for inline
// bytes, or a fetchable URL, which OpenRouter accepts for PDFs.
type filePart struct {
	Filename string `json:"filename,omitzero"`
	FileData string `json:"file_data"`
}

// cacheControl is a prompt-cache breakpoint, at the top level of a request or
// on one content part.
type cacheControl struct {
	Type cacheType        `json:"type"`
	TTL  goodall.CacheTTL `json:"ttl,omitzero"`
}

// toolCall is the model's request to call a tool. Arguments is a JSON string
// on the wire, not a JSON object.
type toolCall struct {
	ID       string           `json:"id"`
	Type     functionType     `json:"type"`
	Function toolCallFunction `json:"function"`
}

// toolCallFunction names the tool and carries its arguments.
type toolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// wireTool is one tool definition.
type wireTool struct {
	Type     functionType `json:"type"`
	Function toolFunction `json:"function"`
}

// toolFunction is a tool's name, prose and parameter schema. Parameters is
// goodall's own Schema, which marshals its properties in declaration order.
type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitzero"`
	Parameters  *goodall.Schema `json:"parameters,omitzero"`
}

// toolChoice constrains the model's tool use. It is a JSON string for the
// three modes and an object for a named tool, so it carries its own encoder.
type toolChoice struct {
	Mode string
	Name string
}

// MarshalJSONTo writes the mode string, or the named-function object.
func (c toolChoice) MarshalJSONTo(enc *jsontext.Encoder) error {
	if c.Name == "" {
		return enc.WriteToken(jsontext.String(c.Mode))
	}
	return json.MarshalEncode(enc, namedToolChoice{
		Type:     functionTool,
		Function: namedFunction{Name: c.Name},
	})
}

// namedToolChoice is the object form of a tool choice.
type namedToolChoice struct {
	Type     functionType  `json:"type"`
	Function namedFunction `json:"function"`
}

// namedFunction names the tool a named choice requires.
type namedFunction struct {
	Name string `json:"name"`
}

// reasoningConfig is OpenRouter's reasoning object. Only the two members
// goodall's ThinkingConfig can express are sent.
type reasoningConfig struct {
	Effort  string `json:"effort,omitzero"`
	Exclude bool   `json:"exclude,omitzero"`
}

// metadata is a request's free-form metadata. It marshals its keys in sorted
// order: a Go map iterates randomly, and invariant 8 wants the same request
// to produce the same bytes every time.
type metadata map[string]string

// MarshalJSONTo writes the map as a JSON object with its keys sorted.
func (m metadata) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if err := enc.WriteToken(jsontext.String(k)); err != nil {
			return err
		}
		if err := enc.WriteToken(jsontext.String(m[k])); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndObject)
}
