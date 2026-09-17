package typesafe

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"time"

	"github.com/bensyverson/goodall"
	"github.com/bensyverson/goodall/internal/transport"
)

// ModelCard is one entry of the model catalog: a name the model field accepts,
// with a description and a release date. The catalog says nothing about
// capability — every model on it answers the same three question types over
// the same endpoint — which is why this package implements no
// [goodall.ModelLister].
type ModelCard struct {
	// Name is the model id or alias, as the model field accepts it.
	Name string `json:"name"`
	// Description says what the model is for.
	Description string `json:"description,omitzero"`
	// ReleaseDate is when the model or alias was released, as the API's own
	// text. Recorded on 2026-09-17 it is an RFC 3339 timestamp with
	// microseconds, which [ModelCard.Released] parses; it is kept as a
	// string because a catalog that became unreadable when a format changed
	// would be worse than one a caller parses itself.
	ReleaseDate string `json:"release_date,omitzero"`
}

// Released parses [ModelCard.ReleaseDate] as an RFC 3339 timestamp. A date the
// API writes in another form is an error carrying the text, which is the whole
// of what went wrong.
func (m ModelCard) Released() (time.Time, error) {
	released, err := time.Parse(time.RFC3339, m.ReleaseDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("typesafe: %s has the release date %q, which is not an RFC 3339 timestamp",
			m.Name, m.ReleaseDate)
	}
	return released, nil
}

// wireModels is the body of GET /v1/models.
type wireModels struct {
	Models []ModelCard `json:"models,omitzero"`
}

// Models lists the names this account may send as the model, in the order the
// API returned them. It lists the aliases; a versioned id such as "jev-1.13.0"
// is accepted whether or not it appears here, so an absent name is not a
// refusal.
func (c *Client) Models(ctx context.Context) ([]ModelCard, error) {
	data, err := c.call(ctx, transport.Request{
		Method: http.MethodGet,
		URL:    c.endpoint(modelsPath),
		Header: c.headers(),
	})
	if err != nil {
		return nil, err
	}
	var w wireModels
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, &goodall.ProtocolError{Reason: "the model catalog could not be decoded: " + err.Error()}
	}
	return w.Models, nil
}
