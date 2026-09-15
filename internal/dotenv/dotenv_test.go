package dotenv

import (
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// write puts content in a temporary file and returns its path.
func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return path
}

func TestLoadReadsTheFormsAnEnvFileUses(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name:    "a bare assignment",
			content: "KEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "the shell export form this project's .env uses",
			content: "export KEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "double quotes are stripped",
			content: "export KEY=\"value\"\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "single quotes are stripped",
			content: "KEY='value'\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "blank lines and comments are skipped",
			content: "\n# a comment\n\n   # an indented comment\nKEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "surrounding whitespace is trimmed",
			content: "  KEY   =   value  \n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "a value may contain equals signs",
			content: "KEY=a=b=c\n",
			want:    map[string]string{"KEY": "a=b=c"},
		},
		{
			name:    "a hash inside a value is part of the value",
			content: "KEY=sk-ant-a#b\n",
			want:    map[string]string{"KEY": "sk-ant-a#b"},
		},
		{
			name:    "an empty value is a key that is present and empty",
			content: "KEY=\n",
			want:    map[string]string{"KEY": ""},
		},
		{
			name:    "quotes preserve interior whitespace",
			content: "KEY=\"  spaced  \"\n",
			want:    map[string]string{"KEY": "  spaced  "},
		},
		{
			name:    "an unmatched quote is part of the value",
			content: "KEY=\"value\n",
			want:    map[string]string{"KEY": "\"value"},
		},
		{
			name:    "the last assignment of a key wins",
			content: "KEY=first\nKEY=second\n",
			want:    map[string]string{"KEY": "second"},
		},
		{
			name:    "a line with no equals sign is skipped",
			content: "not an assignment\nKEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "a line with no key is skipped",
			content: "=orphan\nexport =orphan\nKEY=value\n",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "carriage returns from a CRLF file are not part of the value",
			content: "KEY=value\r\nOTHER=second\r\n",
			want:    map[string]string{"KEY": "value", "OTHER": "second"},
		},
		{
			name:    "a file with no trailing newline still yields its last line",
			content: "KEY=value",
			want:    map[string]string{"KEY": "value"},
		},
		{
			name:    "several keys, which is what the project's .env holds",
			content: "export ANTHROPIC_API_KEY=one\nexport OPENROUTER_API_KEY=two\nexport OPENAI_API_KEY=three\n",
			want: map[string]string{
				"ANTHROPIC_API_KEY":  "one",
				"OPENROUTER_API_KEY": "two",
				"OPENAI_API_KEY":     "three",
			},
		},
		{
			name:    "an empty file is an empty map",
			content: "",
			want:    map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(write(t, tt.content))
			if err != nil {
				t.Fatalf("Load: unexpected error: %v", err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("Load = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadTreatsAMissingFileAsEmptyRatherThanAnError(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent", ".env"))
	if err != nil {
		t.Fatalf("Load on a missing file: %v, want no error", err)
	}
	if got == nil {
		t.Fatal("Load on a missing file returned a nil map; callers range over it")
	}
	if len(got) != 0 {
		t.Errorf("Load on a missing file = %v, want an empty map", got)
	}
}

func TestLoadReportsAReadErrorThatIsNotAbsence(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil {
		t.Fatal("Load on a directory returned no error; a real read failure must be reported")
	}
}

func TestLookupFindsAKeyAndReportsItsAbsence(t *testing.T) {
	path := write(t, "export ANTHROPIC_API_KEY=secret-value\n")

	got, ok := Lookup(path, "ANTHROPIC_API_KEY")
	if !ok {
		t.Fatal("Lookup did not find a key that is in the file")
	}
	if got != "secret-value" {
		t.Errorf("Lookup returned the wrong value for the key")
	}

	if _, ok := Lookup(path, "OPENROUTER_API_KEY"); ok {
		t.Error("Lookup found a key that is not in the file")
	}
	if _, ok := Lookup(filepath.Join(t.TempDir(), ".env"), "ANTHROPIC_API_KEY"); ok {
		t.Error("Lookup found a key in a file that does not exist")
	}
}

func TestLookupReportsAnEmptyValueAsMissing(t *testing.T) {
	path := write(t, "ANTHROPIC_API_KEY=\n")
	if _, ok := Lookup(path, "ANTHROPIC_API_KEY"); ok {
		t.Error("Lookup reported an empty value as present; a blank key cannot authenticate a call")
	}
}
