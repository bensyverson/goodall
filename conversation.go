package goodall

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"iter"
)

// Conversation is the history of a thread: an ordered list of messages that
// only ever grows. The zero value is an empty conversation, ready to use.
//
// History is append-only because preserved thinking binds each thinking block
// to the exact prefix that produced it — editing, reordering or dropping an
// earlier turn invalidates every thinking block after it. So Conversation
// offers Append and read accessors and nothing that writes into a turn
// already sent. Append returns a new value rather than modifying the
// receiver, and every accessor hands back copies, so two conversations grown
// from the same value can never disturb each other.
type Conversation struct {
	messages []Message
}

// Append returns a conversation with the messages added at the end, leaving
// the receiver untouched. The result owns its storage outright, so appending
// to the same conversation twice yields two independent histories.
func (c Conversation) Append(msgs ...Message) Conversation {
	if len(msgs) == 0 {
		return c
	}
	out := make([]Message, 0, len(c.messages)+len(msgs))
	for _, m := range c.messages {
		out = append(out, m.clone())
	}
	for _, m := range msgs {
		out = append(out, m.clone())
	}
	return Conversation{messages: out}
}

// Len is the number of messages.
func (c Conversation) Len() int { return len(c.messages) }

// At is the message at index i, which panics if i is out of range, as an
// index into a slice does.
func (c Conversation) At(i int) Message { return c.messages[i].clone() }

// Last is the most recent message, and false when the conversation is empty.
func (c Conversation) Last() (Message, bool) {
	if len(c.messages) == 0 {
		return Message{}, false
	}
	return c.messages[len(c.messages)-1].clone(), true
}

// Messages is a copy of the history, safe to keep and to modify.
func (c Conversation) Messages() []Message {
	if len(c.messages) == 0 {
		return nil
	}
	out := make([]Message, len(c.messages))
	for i, m := range c.messages {
		out[i] = m.clone()
	}
	return out
}

// All iterates the messages in order, yielding a copy of each.
func (c Conversation) All() iter.Seq[Message] {
	return func(yield func(Message) bool) {
		for _, m := range c.messages {
			if !yield(m.clone()) {
				return
			}
		}
	}
}

// MarshalJSONTo writes the conversation as a JSON array of messages. An empty
// conversation is an empty array rather than null, so a persisted thread
// always has the same shape.
func (c Conversation) MarshalJSONTo(enc *jsontext.Encoder) error {
	if len(c.messages) == 0 {
		return enc.WriteValue(jsontext.Value("[]"))
	}
	return json.MarshalEncode(enc, c.messages)
}

// UnmarshalJSONFrom reads a JSON array of messages.
func (c *Conversation) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return json.UnmarshalDecode(dec, &c.messages)
}
