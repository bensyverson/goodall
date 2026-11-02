package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAFixtureCarriesNoAccountID: OpenRouter's error envelopes name the account
// behind the key, and a recording is committed to the repository.
func TestAFixtureCarriesNoAccountID(t *testing.T) {
	rec := newRecorder(t.TempDir())
	body := `{"error":{"message":"nope","code":400},"user_id":"user_abc123"}`
	if err := rec.writeFile("live_x.json", []byte(body)); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(rec.out, "live_x.json"))
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if strings.Contains(string(got), "user_abc123") {
		t.Errorf("the fixture carries the account id: %s", got)
	}
	if want := `{"error":{"message":"nope","code":400},"user_id":"<redacted>"}`; string(got) != want {
		t.Errorf("the fixture is %s, want everything else untouched: %s", got, want)
	}
}
