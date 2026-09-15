package goodall

// Role says who a message is from.
type Role string

const (
	// RoleUser is the person, and also the carrier for tool results.
	RoleUser Role = "user"
	// RoleAssistant is the model.
	RoleAssistant Role = "assistant"
	// RoleSystem is an instruction placed mid-conversation, which both
	// supported providers accept; the standing system prompt is a request
	// field rather than a message.
	RoleSystem Role = "system"
)

// Message is one turn: a role and its content blocks. Partial marks an
// assistant turn that was interrupted before the model finished, so a
// consumer can render it as cut off rather than complete.
type Message struct {
	Role    Role   `json:"role"`
	Content Blocks `json:"content"`
	Partial bool   `json:"partial,omitzero"`
}

// UserMessage is a turn from the person, or the carrier for tool results.
func UserMessage(blocks ...Block) Message {
	return Message{Role: RoleUser, Content: blocks}
}

// AssistantMessage is a turn from the model.
func AssistantMessage(blocks ...Block) Message {
	return Message{Role: RoleAssistant, Content: blocks}
}

// SystemMessage is an instruction inserted mid-conversation.
func SystemMessage(blocks ...Block) Message {
	return Message{Role: RoleSystem, Content: blocks}
}

// Text is the message's text blocks concatenated, which is what a plain-text
// consumer wants from a turn. Blocks of other kinds are ignored.
func (m Message) Text() string {
	var b []byte
	for _, blk := range m.Content {
		if t, ok := blk.(Text); ok {
			b = append(b, t.Text...)
		}
	}
	return string(b)
}

// ToolUses are the tool calls in the message, in order. It returns nil when
// the model asked for none.
func (m Message) ToolUses() []ToolUse {
	var out []ToolUse
	for _, blk := range m.Content {
		if t, ok := blk.(ToolUse); ok {
			out = append(out, t)
		}
	}
	return out
}

// clone returns a copy sharing no slice with m, so history cannot be edited
// through a message handed out or handed in.
func (m Message) clone() Message {
	m.Content = m.Content.clone()
	return m
}
