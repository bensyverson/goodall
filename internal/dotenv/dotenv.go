// Package dotenv reads the API keys the live tests and the fixture recorder
// need out of a .env file at the repository root.
//
// It is deliberately tiny and is not a dotenv implementation: there is no
// variable interpolation, no escape processing inside quotes, no multi-line
// value and no export into the process environment. It reads the handful of
// forms this project's .env actually uses — `export KEY=value`, `KEY=value`,
// either one quoted — because the alternative was a dependency for forty
// lines of string handling, and a value it misreads would be a key that fails
// to authenticate rather than a key that leaks.
//
// No function here logs, prints or wraps a value into an error message. A
// value read out of this file is a secret, and the only thing a caller should
// do with it is hand it to a provider client.
package dotenv

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

// Load reads path and returns the assignments in it, last one winning.
//
// A file that is not there yields an empty map and no error: a working tree
// with no .env is the normal state of a clone, and the callers answer it by
// skipping loudly rather than by failing. Any other read error — a directory,
// a permission denial, an unreadable device — is returned, because it means
// something is wrong that the operator wants to hear about.
//
// The parser skips blank lines, lines whose first non-space character is `#`,
// and any line with no `=` in it. A leading `export ` is dropped, the key and
// the value are trimmed of surrounding space, and one matching pair of single
// or double quotes around the value is removed. Everything else, including a
// `#` after the value and an unmatched quote, is part of the value.
func Load(path string) (map[string]string, error) {
	out := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for line := range strings.Lines(string(data)) {
		key, value, ok := parseLine(line)
		if !ok {
			continue
		}
		out[key] = value
	}
	return out, nil
}

// Lookup reads one key out of the file at path. It reports false for a key
// that is absent, a key whose value is empty, and a file that could not be
// read at all: every caller of this package treats those the same way — skip
// the live call and say which key in which file was missing — and an empty
// string cannot authenticate a request in any case.
//
// A caller that needs to tell a broken file from a missing one calls Load.
func Lookup(path, key string) (string, bool) {
	values, err := Load(path)
	if err != nil {
		return "", false
	}
	value := values[key]
	return value, value != ""
}

// parseLine reads one line of the file. The bool is false for a line that
// carries no assignment.
func parseLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, value, ok = strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	return key, unquote(strings.TrimSpace(value)), true
}

// unquote removes one matching pair of surrounding quotes. An unmatched quote
// is left alone: guessing at what the author meant would silently change a
// secret, and a value that is visibly wrong is easier to diagnose than one
// that is subtly wrong.
func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	quote := value[0]
	if (quote == '"' || quote == '\'') && value[len(value)-1] == quote {
		return value[1 : len(value)-1]
	}
	return value
}
