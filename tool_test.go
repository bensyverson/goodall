package goodall

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"strings"
	"testing"
)

// weatherInput is the everyday shape: a required string, an optional enum and
// an optional integer, in an order that is not alphabetical.
type weatherInput struct {
	City  string `json:"city" desc:"the city to look up"`
	Units string `json:"units,omitzero" desc:"temperature units" enum:"celsius,fahrenheit"`
	Days  int    `json:"days,omitzero" desc:"how many days to forecast"`
}

// alertInput exercises the nested and array cases of the parameter listing.
type alertInput struct {
	Query  string       `json:"query" desc:"what to search for"`
	Window windowInput  `json:"window" desc:"the time window"`
	Tags   []string     `json:"tags,omitzero" desc:"labels to match"`
	Points []pointInput `json:"points,omitzero" desc:"places to check"`
}

type windowInput struct {
	Start string `json:"start" desc:"ISO 8601 start"`
	End   string `json:"end,omitzero"`
}

type pointInput struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// emptyInput is a tool with no parameters at all.
type emptyInput struct{}

// mapInput cannot be expressed as a schema, so NewTool must refuse it.
type mapInput struct {
	Attrs map[string]string `json:"attrs"`
}

// newWeatherTool builds a tool that records the input it was handed.
func newWeatherTool(t *testing.T, got *weatherInput) Tool {
	t.Helper()
	tool, err := NewTool("get_weather", "Look up the weather.", func(_ context.Context, in weatherInput) (ToolResult, error) {
		if got != nil {
			*got = in
		}
		return TextResult("ok"), nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}

func newAlertTool(t *testing.T) Tool {
	t.Helper()
	tool, err := NewTool("set_alert", "Set an alert.", func(context.Context, alertInput) (ToolResult, error) {
		return TextResult("ok"), nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	return tool
}

func TestNewToolRejectsInvalidDefinitions(t *testing.T) {
	run := func(context.Context, weatherInput) (ToolResult, error) { return ToolResult{}, nil }
	cases := []struct {
		label string
		build func() (Tool, error)
		want  string
	}{
		{"empty name", func() (Tool, error) { return NewTool("", "d", run) }, "name"},
		{"over-long name", func() (Tool, error) { return NewTool(strings.Repeat("a", 65), "d", run) }, "64"},
		{"space in name", func() (Tool, error) { return NewTool("get weather", "d", run) }, `" "`},
		{"dot in name", func() (Tool, error) { return NewTool("get.weather", "d", run) }, `"."`},
		{"non-ascii name", func() (Tool, error) { return NewTool("wëather", "d", run) }, `"ë"`},
		{"empty description", func() (Tool, error) { return NewTool("get_weather", "", run) }, "description"},
		{"nil handler", func() (Tool, error) {
			return NewTool[weatherInput]("get_weather", "d", nil)
		}, "handler"},
		{"unrepresentable input", func() (Tool, error) {
			return NewTool("get_weather", "d", func(context.Context, mapInput) (ToolResult, error) { return ToolResult{}, nil })
		}, "map"},
		{"non-struct input", func() (Tool, error) {
			return NewTool("get_weather", "d", func(context.Context, string) (ToolResult, error) { return ToolResult{}, nil })
		}, "struct"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			tool, err := c.build()
			if err == nil {
				t.Fatalf("NewTool = %v, want an error", tool)
			}
			if tool != nil {
				t.Errorf("NewTool returned a tool alongside the error: %v", tool)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %s", err, c.want)
			}
		})
	}
}

// TestNewToolWrapsTheSchemaError keeps the typed schema error reachable, so an
// author can see which field is at fault without parsing the message.
func TestNewToolWrapsTheSchemaError(t *testing.T) {
	_, err := NewTool("get_weather", "d", func(context.Context, mapInput) (ToolResult, error) { return ToolResult{}, nil })
	if err == nil {
		t.Fatal("NewTool = nil, want an error")
	}
	serr, ok := errors.AsType[*SchemaError](err)
	if !ok {
		t.Fatalf("error %q does not wrap a *SchemaError", err)
	}
	if serr.Path != "mapInput.Attrs" {
		t.Errorf("SchemaError.Path = %q, want %q", serr.Path, "mapInput.Attrs")
	}
}

func TestNewToolReportsNameDescriptionAndSchema(t *testing.T) {
	tool := newWeatherTool(t, nil)
	if tool.Name() != "get_weather" {
		t.Errorf("Name = %q, want %q", tool.Name(), "get_weather")
	}
	if tool.Description() != "Look up the weather." {
		t.Errorf("Description = %q", tool.Description())
	}
	want, err := SchemaFor[weatherInput]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	gotJSON, err := json.Marshal(tool.Schema())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("Schema =\n%s\nwant\n%s", gotJSON, wantJSON)
	}
}

// TestToolSchemaIsTheSamePointerEveryCall matters because a provider marshals
// the schema into every request: a fresh copy per call would invite a caller
// to mutate one and see nothing change, and costs an allocation per turn.
func TestToolSchemaIsTheSamePointerEveryCall(t *testing.T) {
	tool := newWeatherTool(t, nil)
	if tool.Schema() != tool.Schema() {
		t.Error("Schema returned two different pointers")
	}
}

func TestExecuteCallsRunWithTheTypedValue(t *testing.T) {
	var got weatherInput
	tool := newWeatherTool(t, &got)
	res, err := tool.Execute(t.Context(), jsontext.Value(`{"city":"Paris","units":"celsius","days":3}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned an error result: %s", res.Text())
	}
	want := weatherInput{City: "Paris", Units: "celsius", Days: 3}
	if got != want {
		t.Errorf("run received %+v, want %+v", got, want)
	}
	if res.Text() != "ok" {
		t.Errorf("result text = %q, want %q", res.Text(), "ok")
	}
	if res.ToolUseID != "" {
		t.Errorf("ToolUseID = %q, want it left to the loop", res.ToolUseID)
	}
}

// TestExecuteTreatsAbsentInputAsAnEmptyObject covers the providers that send
// nothing at all for a tool with no parameters.
func TestExecuteTreatsAbsentInputAsAnEmptyObject(t *testing.T) {
	called := 0
	tool, err := NewTool("ping", "Ping.", func(context.Context, emptyInput) (ToolResult, error) {
		called++
		return TextResult("pong"), nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	for _, input := range []jsontext.Value{nil, jsontext.Value(""), jsontext.Value("null"), jsontext.Value("  null\n"), jsontext.Value("{}")} {
		res, err := tool.Execute(t.Context(), input)
		if err != nil {
			t.Fatalf("Execute(%q): %v", input, err)
		}
		if res.IsError {
			t.Errorf("Execute(%q) = error result %q, want success", input, res.Text())
		}
	}
	if called != 5 {
		t.Errorf("run called %d times, want 5", called)
	}
}

// TestExecuteDoesNotCallRunOnABadInput keeps a half-decoded value out of the
// handler: the model hears about the mistake instead.
func TestExecuteDoesNotCallRunOnABadInput(t *testing.T) {
	called := false
	tool, err := NewTool("get_weather", "d", func(context.Context, weatherInput) (ToolResult, error) {
		called = true
		return TextResult("ok"), nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	if _, err := tool.Execute(t.Context(), jsontext.Value(`{"city":"Paris","color":"red"}`)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if called {
		t.Error("run was called with an input that failed to decode")
	}
}

func TestExecutePassesRunResultAndErrorThrough(t *testing.T) {
	sentinel := errors.New("the service is down")
	want := ToolResult{IsError: true, Content: Blocks{Text{Text: "partial"}, Image{Source: BytesSource("image/png", []byte{1})}}}
	tool, err := NewTool("get_weather", "d", func(context.Context, weatherInput) (ToolResult, error) {
		return want, sentinel
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	got, err := tool.Execute(t.Context(), jsontext.Value(`{"city":"Paris"}`))
	if !errors.Is(err, sentinel) {
		t.Errorf("Execute error = %v, want %v", err, sentinel)
	}
	if len(got.Content) != len(want.Content) || got.Text() != "partial" || !got.IsError {
		t.Errorf("Execute = %+v, want %+v", got, want)
	}
}

func TestExecutePassesTheContextToRun(t *testing.T) {
	tool, err := NewTool("get_weather", "d", func(ctx context.Context, _ weatherInput) (ToolResult, error) {
		if err := ctx.Err(); err != nil {
			return ToolResult{}, err
		}
		return TextResult("ok"), nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := tool.Execute(ctx, jsontext.Value(`{"city":"Paris"}`)); !errors.Is(err, context.Canceled) {
		t.Errorf("Execute error = %v, want context.Canceled", err)
	}
}
