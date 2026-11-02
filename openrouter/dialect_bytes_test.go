package openrouter

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// updateChatGoldens rewrites the goldens below when set. They were written by the
// code as it stood before the OpenAI dialect left Chat Completions, so a diff here
// means a Chat server is being sent something new, which should be a deliberate act.
const updateChatGoldens = "GOODALL_UPDATE_CHAT_GOLDENS"

func TestTheChatDialectsSendTheBytesTheyAlwaysSent(t *testing.T) {
	for _, d := range []Dialect{OpenRouter, LMStudio, Generic} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", d, stream), func(t *testing.T) {
				client := New(WithDialect(d))
				got, _, err := client.requestBody(everyFeatureRequest(t), stream)
				if err != nil {
					t.Fatalf("requestBody: %v", err)
				}
				path := filepath.Join("testdata", fmt.Sprintf("chat_wire_%s_stream_%v.json", d, stream))
				if os.Getenv(updateChatGoldens) != "" {
					if err := os.WriteFile(path, got, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Errorf("the %s dialect's bytes changed:\n got %s\nwant %s", d, got, want)
				}
			})
		}
	}
}
