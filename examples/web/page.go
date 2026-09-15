package main

import (
	_ "embed"
	"net/http"
)

// pageHTML is the whole front end: one file, plain JavaScript, no external
// assets, embedded in the binary so the example is a single artifact to copy
// and run.
//
//go:embed page.html
var pageHTML []byte

// page serves the front end.
func (s *server) page(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The page is the binary's, so it changes only when the binary does;
	// the browser is told not to hold on to it while the example is being
	// edited.
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(pageHTML)
}
