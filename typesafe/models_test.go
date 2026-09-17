package typesafe

import (
	"errors"
	"net/http"
	"testing"

	"github.com/bensyverson/goodall"
)

func TestModelsReadsTheCatalogInOrder(t *testing.T) {
	const body = `{"models":[` +
		`{"name":"jev-latest","description":"The most recent stable release.","release_date":"2026-09-01"},` +
		`{"name":"jev-preview","description":"The most recent release, official or not.","release_date":"2026-09-01"}]}`
	client, seen := serveJSON(t, http.StatusOK, body)
	cards, err := client.Models(t.Context())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if seen.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", seen.Method)
	}
	if seen.Path != "/v1/models" {
		t.Errorf("path = %s, want /v1/models", seen.Path)
	}
	if seen.Auth != "Bearer "+testKey {
		t.Errorf("Authorization = %q, want a bearer token", seen.Auth)
	}
	want := []ModelCard{
		{Name: "jev-latest", Description: "The most recent stable release.", ReleaseDate: "2026-09-01"},
		{Name: "jev-preview", Description: "The most recent release, official or not.", ReleaseDate: "2026-09-01"},
	}
	if len(cards) != len(want) {
		t.Fatalf("got %d cards, want %d: %+v", len(cards), len(want), cards)
	}
	for i := range want {
		if cards[i] != want[i] {
			t.Errorf("card %d = %+v, want %+v", i, cards[i], want[i])
		}
	}
}

func TestModelsReportsAFailingStatus(t *testing.T) {
	client, _ := serveJSON(t, http.StatusUnauthorized, `{"detail":"Missing or invalid API key."}`, WithMaxRetries(-1))
	_, err := client.Models(t.Context())
	if err == nil {
		t.Fatal("an unauthorized catalog read came back as success")
	}
	if !errors.Is(err, goodall.KindUnauthorized) {
		t.Errorf("error is not KindUnauthorized: %v", err)
	}
}
