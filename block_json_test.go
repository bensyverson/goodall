package goodall

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// blockCase pairs a block value with the exact bytes goodall must produce for
// it. Both directions are asserted, so the table is the specification of the
// neutral block shape.
type blockCase struct {
	name  string
	block Block
	want  string
}

func blockCases() []blockCase {
	return []blockCase{
		{
			name:  "text",
			block: Text{Text: "hello"},
			want:  `{"type":"text","text":"hello"}`,
		},
		{
			name:  "text with a default cache marker",
			block: Text{Text: "hi", Cache: &CacheControl{}},
			want:  `{"type":"text","text":"hi","cache":{}}`,
		},
		{
			name:  "text with a one-hour cache marker",
			block: Text{Text: "hi", Cache: &CacheControl{TTL: CacheTTL1h}},
			want:  `{"type":"text","text":"hi","cache":{"ttl":"1h"}}`,
		},
		{
			name:  "image from bytes",
			block: Image{Source: BytesSource("image/png", []byte("PNGDATA"))},
			want:  `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"UE5HREFUQQ=="}}`,
		},
		{
			name:  "image from a url with a cache marker",
			block: Image{Source: URLSource("https://example.com/cat.png"), Cache: &CacheControl{TTL: CacheTTL5m}},
			want:  `{"type":"image","source":{"type":"url","url":"https://example.com/cat.png"},"cache":{"ttl":"5m"}}`,
		},
		{
			name:  "document with title and context",
			block: Document{Source: FileSource("file_123"), Title: "Q3 report", Context: "quarterly numbers"},
			want:  `{"type":"document","source":{"type":"file","file_id":"file_123"},"title":"Q3 report","context":"quarterly numbers"}`,
		},
		{
			name:  "document without title or context",
			block: Document{Source: BytesSource("application/pdf", []byte("PNGDATA"))},
			want:  `{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"UE5HREFUQQ=="}}`,
		},
		{
			name:  "tool use",
			block: ToolUse{ID: "toolu_1", Name: "get_weather", Input: jsontext.Value(`{"city":"Paris"}`)},
			want:  `{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}`,
		},
		{
			name:  "tool use without input",
			block: ToolUse{ID: "toolu_1", Name: "now"},
			want:  `{"type":"tool_use","id":"toolu_1","name":"now"}`,
		},
		{
			name:  "tool result with text",
			block: ToolResult{ToolUseID: "toolu_1", Content: Blocks{Text{Text: "18C"}}},
			want:  `{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"18C"}]}`,
		},
		{
			name: "tool result carrying an image and a document",
			block: ToolResult{ToolUseID: "toolu_2", Content: Blocks{
				Text{Text: "here"},
				Image{Source: BytesSource("image/png", []byte("PNGDATA"))},
				Document{Source: URLSource("https://example.com/a.pdf"), Title: "A"},
			}},
			want: `{"type":"tool_result","tool_use_id":"toolu_2","content":[` +
				`{"type":"text","text":"here"},` +
				`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"UE5HREFUQQ=="}},` +
				`{"type":"document","source":{"type":"url","url":"https://example.com/a.pdf"},"title":"A"}]}`,
		},
		{
			name:  "tool result that is an error",
			block: ToolResult{ToolUseID: "toolu_3", IsError: true, Content: Blocks{Text{Text: "boom"}}},
			want:  `{"type":"tool_result","tool_use_id":"toolu_3","is_error":true,"content":[{"type":"text","text":"boom"}]}`,
		},
		{
			name: "thinking with a signature and the provider block",
			block: Thinking{
				Text:      "Let me count.",
				Signature: "ErUBCkYIBx",
				Raw:       jsontext.Value(`{"type":"thinking","thinking":"Let me count.","signature":"ErUBCkYIBx"}`),
			},
			want: `{"type":"thinking","text":"Let me count.","signature":"ErUBCkYIBx","raw":{"type":"thinking","thinking":"Let me count.","signature":"ErUBCkYIBx"}}`,
		},
		{
			name: "thinking with no display text",
			block: Thinking{
				Signature: "sig",
				Raw:       jsontext.Value(`{"type":"thinking","thinking":"","signature":"sig"}`),
			},
			want: `{"type":"thinking","signature":"sig","raw":{"type":"thinking","thinking":"","signature":"sig"}}`,
		},
		{
			name:  "redacted thinking",
			block: RedactedThinking{Data: "EroBCkYIBxgCKkBc"},
			want:  `{"type":"redacted_thinking","data":"EroBCkYIBxgCKkBc"}`,
		},
		{
			name: "redacted thinking with the provider block",
			block: RedactedThinking{
				Data: "EroB",
				Raw:  jsontext.Value(`{"type":"redacted_thinking","data":"EroB"}`),
			},
			want: `{"type":"redacted_thinking","data":"EroB","raw":{"type":"redacted_thinking","data":"EroB"}}`,
		},
		{
			name: "an unrecognized block keeps its bytes",
			block: Unknown{
				Type: "server_tool_use",
				Raw:  jsontext.Value(`{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"go"}}`),
			},
			want: `{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"go"}}`,
		},
	}
}

// TestBlockMarshalBytes asserts the exact JSON goodall writes for every block
// type, with plain json.Marshal and no options.
func TestBlockMarshalBytes(t *testing.T) {
	for _, c := range blockCases() {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.block)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal =\n\t%s\nwant\n\t%s", got, c.want)
			}
		})
	}
}

// TestBlockRoundTrip asserts every block decodes back to an equal value and
// re-encodes byte-for-byte, with plain json.Unmarshal and no options.
func TestBlockRoundTrip(t *testing.T) {
	for _, c := range blockCases() {
		t.Run(c.name, func(t *testing.T) {
			var got Blocks
			if err := json.Unmarshal([]byte("["+c.want+"]"), &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("decoded %d blocks, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0], c.block) {
				t.Fatalf("decoded %#v, want %#v", got[0], c.block)
			}
			again, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(again) != "["+c.want+"]" {
				t.Errorf("re-encoded %s, want [%s]", again, c.want)
			}
		})
	}
}

// TestBlocksMarshalAsPlainSlice proves a bare []Block encodes without options,
// so a consumer can hand a slice of blocks to any encoder.
func TestBlocksMarshalAsPlainSlice(t *testing.T) {
	got, err := json.Marshal([]Block{Text{Text: "a"}, Text{Text: "b"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`
	if string(got) != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

// TestUnknownBlockFromUnrecognizedType is the guard for "unknown values are
// surfaced, never dropped": a type tag the library does not know becomes an
// Unknown carrying the original bytes.
func TestUnknownBlockFromUnrecognizedType(t *testing.T) {
	const in = `[{"type":"container_upload","file_id":"f1"}]`
	var got Blocks
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	u, ok := got[0].(Unknown)
	if !ok {
		t.Fatalf("decoded %T, want Unknown", got[0])
	}
	if u.Type != "container_upload" {
		t.Errorf("Type = %q, want container_upload", u.Type)
	}
	if string(u.Raw) != `{"type":"container_upload","file_id":"f1"}` {
		t.Errorf("Raw = %s", u.Raw)
	}
}

// TestUnknownBlockRawIsNotAliased guards against retaining the decoder's
// scratch buffer: the bytes of one block must not be overwritten by the next.
func TestUnknownBlockRawIsNotAliased(t *testing.T) {
	var (
		want  []string
		parts []string
	)
	// Long enough, and read a byte at a time, so a streaming decoder must
	// refill and reuse its buffer while these blocks are still held.
	for i := range 40 {
		b := fmt.Sprintf(`{"type":"alpha%d","padding":%q}`, i, strings.Repeat("x", 64))
		parts = append(parts, b)
		want = append(want, b)
	}
	in := "[" + strings.Join(parts, ",") + "]"

	for _, tc := range []struct {
		name   string
		decode func(*Blocks) error
	}{
		{"from bytes", func(b *Blocks) error { return json.Unmarshal([]byte(in), b) }},
		{"from a reader", func(b *Blocks) error {
			return json.UnmarshalRead(iotest.OneByteReader(strings.NewReader(in)), b)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got Blocks
			if err := tc.decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			for i, w := range want {
				u, ok := got[i].(Unknown)
				if !ok {
					t.Fatalf("block %d is %T, want Unknown", i, got[i])
				}
				if string(u.Raw) != w {
					t.Fatalf("block %d Raw = %s, want %s", i, u.Raw, w)
				}
			}
		})
	}
}

// TestMalformedBlockIsAnError draws the line between "a type goodall does not
// know", which becomes an Unknown, and "a block of a known type whose body is
// wrong", which is a decode error naming the block.
func TestMalformedBlockIsAnError(t *testing.T) {
	var got Blocks
	err := json.Unmarshal([]byte(`[{"type":"text","text":123}]`), &got)
	if err == nil {
		t.Fatalf("Unmarshal succeeded and gave %#v, want an error", got)
	}
	if !strings.Contains(err.Error(), "goodall.Text") {
		t.Errorf("error %q does not name the block type", err)
	}
}

// TestUnknownBlockWithoutRaw covers a hand-built Unknown: it still writes a
// well-formed block rather than a JSON null.
func TestUnknownBlockWithoutRaw(t *testing.T) {
	got, err := json.Marshal(Unknown{Type: "widget"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{"type":"widget"}` {
		t.Errorf("Marshal = %s, want {\"type\":\"widget\"}", got)
	}
}

// TestThinkingRawSurvivesNesting checks the invariant that matters most for
// preserved thinking: a thinking block nested in a message keeps the
// provider's bytes, member order included.
func TestThinkingRawSurvivesNesting(t *testing.T) {
	raw := `{"type":"thinking","signature":"zz","thinking":"a\nb","extra":{"b":1,"a":2}}`
	m := AssistantMessage(Thinking{Text: "a\nb", Signature: "zz", Raw: jsontext.Value(raw)})
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Message
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	th, ok := back.Content[0].(Thinking)
	if !ok {
		t.Fatalf("decoded %T, want Thinking", back.Content[0])
	}
	if string(th.Raw) != raw {
		t.Errorf("Raw = %s, want %s", th.Raw, raw)
	}
}
