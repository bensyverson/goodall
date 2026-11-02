package main

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"os"
	"strings"
)

// MarshalText writes a section on one line, so accepting a baseline shows up in
// a commit as the headings that changed.
func (s section) MarshalText() ([]byte, error) {
	return []byte(s.Digest + " " + s.Heading), nil
}

// UnmarshalText reads what MarshalText wrote.
func (s *section) UnmarshalText(text []byte) error {
	digest, heading, ok := strings.Cut(string(text), " ")
	if !ok {
		return fmt.Errorf("section %q has no digest and heading", text)
	}
	s.Digest, s.Heading = digest, heading
	return nil
}

// readBaseline loads the accepted state. A missing file is an error: an empty
// baseline would report the whole world as drift, or, worse, as nothing.
func readBaseline(path string) (baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return baseline{}, err
	}
	var b baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return baseline{}, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// writeBaseline saves the accepted state with stable bytes.
func writeBaseline(path string, b baseline) error {
	data, err := json.Marshal(b, json.Deterministic(true), jsontext.Multiline(true))
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
