package redact

import "testing"

func TestAccountReplacesTheIDAndNothingElse(t *testing.T) {
	for in, want := range map[string]string{
		`{"error":{"message":"nope"},"user_id":"user_abc123"}`: `{"error":{"message":"nope"},"user_id":"<redacted>"}`,
		`{"user_id": "user_abc123"}`:                           `{"user_id":"<redacted>"}`,
		`{"message":"user_id is a word here"}`:                 `{"message":"user_id is a word here"}`,
	} {
		if got := string(Account([]byte(in))); got != want {
			t.Errorf("Account(%s) = %s, want %s", in, got, want)
		}
	}
}
