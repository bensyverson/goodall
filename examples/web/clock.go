package main

import (
	"context"
	"fmt"
	"time"

	"github.com/bensyverson/goodall"
)

// clockInput is what the model passes the clock tool. The struct tags are the
// tool's schema: goodall derives it from the type, and the desc tag is what
// the model reads about each field.
type clockInput struct {
	OffsetHours int `json:"offset_hours,omitzero" desc:"whole hours to add to UTC, 0 for UTC itself"`
}

// newClockTool is the preview's one built-in tool: the current time, which a
// model cannot know and which needs no network, no key and no state. It is
// here so the page has something real to render a tool chip for.
func newClockTool() (goodall.Tool, error) {
	return goodall.NewTool("clock", "Report the current date and time in UTC, optionally offset by whole hours.",
		func(ctx context.Context, in clockInput) (goodall.ToolResult, error) {
			if in.OffsetHours < -12 || in.OffsetHours > 14 {
				// A refusal the model can act on: it is a result,
				// not an error, so the run carries on.
				return goodall.ErrorResult(fmt.Sprintf("offset_hours is %d; real offsets run from -12 to +14", in.OffsetHours)), nil
			}
			at := time.Now().UTC().Add(time.Duration(in.OffsetHours) * time.Hour)
			return goodall.TextResult(fmt.Sprintf("%s (UTC%+d)", at.Format(time.RFC1123), in.OffsetHours)), nil
		})
}
