// Package redact removes what must not leave the machine from a response body that
// is about to be saved or printed.
package redact

import "regexp"

// accountID matches the user_id member OpenRouter adds to an error envelope, which
// names the account behind the key.
var accountID = regexp.MustCompile(`"user_id":\s*"[^"]*"`)

// Account returns the body with any account id replaced. Nothing reads the value.
func Account(body []byte) []byte {
	return accountID.ReplaceAll(body, []byte(`"user_id":"<redacted>"`))
}
