package goodall

import (
	"context"
	"encoding/json/jsontext"
	"strings"
	"testing"
)

func TestExecuteReportsMissingRequiredParameters(t *testing.T) {
	cases := []struct {
		label string
		tool  func(*testing.T) Tool
		input jsontext.Value
		want  string
	}{
		{"absent input", func(t *testing.T) Tool { return newWeatherTool(t, nil) }, nil, `missing required parameter "city"`},
		{"empty object", func(t *testing.T) Tool { return newWeatherTool(t, nil) }, jsontext.Value(`{}`), `missing required parameter "city"`},
		{"json null", func(t *testing.T) Tool { return newWeatherTool(t, nil) }, jsontext.Value(`null`), `missing required parameter "city"`},
		{"two missing", newAlertTool, jsontext.Value(`{}`), `missing required parameters "query" and "window"`},
		{"missing inside an object", newAlertTool, jsontext.Value(`{"query":"q","window":{}}`), `missing required parameter "window.start"`},
		{"missing inside an array element", newAlertTool, jsontext.Value(`{"query":"q","window":{"start":"s"},"points":[{"lat":1,"lon":2},{"lat":3}]}`), `missing required parameter "points[1].lon"`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			res, err := c.tool(t).Execute(t.Context(), c.input)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !res.IsError {
				t.Fatalf("Execute = %+v, want an error result", res)
			}
			if first := problemLine(res.Text()); first != c.want {
				t.Errorf("first line = %q, want %q", first, c.want)
			}
		})
	}
}

func TestExecuteReportsABadInput(t *testing.T) {
	cases := []struct {
		label string
		tool  func(*testing.T) Tool
		input jsontext.Value
		want  string
	}{
		{"unknown parameter", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":"Paris","colour":"red"}`), `unknown parameter "colour"`},
		{"unknown nested parameter", newAlertTool,
			jsontext.Value(`{"query":"q","window":{"start":"s","stop":"e"}}`), `unknown parameter "window.stop"`},
		// json/v2 keeps the offending literal only when it rejected the
		// value's content (3.5 into an int); a plain kind mismatch reports
		// the kind alone, which is what the model needs anyway.
		{"wrong type, number sent", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":3}`), `parameter "city" takes a string, but the value sent was a number`},
		{"wrong type, boolean sent", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":true}`), `parameter "city" takes a string, but the value sent was a boolean`},
		{"wrong type, object sent", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":{"name":"Paris"}}`), `parameter "city" takes a string, but the value sent was an object`},
		{"fractional integer", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":"Paris","days":3.5}`), `parameter "days" takes an integer, but the value sent was 3.5`},
		{"wrong type inside an object", newAlertTool,
			jsontext.Value(`{"query":"q","window":{"start":7}}`), `parameter "window.start" takes a string, but the value sent was a number`},
		{"wrong type inside an array", newAlertTool,
			jsontext.Value(`{"query":"q","window":{"start":"s"},"points":[{"lat":1,"lon":2},{"lat":"x","lon":0}]}`),
			`parameter "points[1].lat" takes a number, but the value sent was a string`},
		{"not an object", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`[1,2]`), `the input must be a JSON object, but the value sent was an array`},
		{"truncated json", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":`), `the input is not valid JSON: unexpected EOF`},
		{"trailing comma", func(t *testing.T) Tool { return newWeatherTool(t, nil) },
			jsontext.Value(`{"city":"Paris",}`), `the input is not valid JSON: invalid character ',' at start of value`},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			res, err := c.tool(t).Execute(t.Context(), c.input)
			if err != nil {
				t.Fatalf("Execute = error %v, want a nil error and an error result", err)
			}
			if !res.IsError {
				t.Fatalf("Execute = %+v, want an error result", res)
			}
			if first := problemLine(res.Text()); first != c.want {
				t.Errorf("first line = %q, want %q", first, c.want)
			}
		})
	}
}

// TestErrorResultListsEveryParameterInSchemaOrder is the whole point of the
// message: a model that fixes one parameter should not have to guess the rest.
func TestErrorResultListsEveryParameterInSchemaOrder(t *testing.T) {
	res, err := newWeatherTool(t, nil).Execute(t.Context(), jsontext.Value(`{"colour":"red"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	const want = `Parameters:
  city (string, required): the city to look up
  units (string, optional, one of "celsius", "fahrenheit"): temperature units
  days (integer, optional): how many days to forecast`
	if got := parameterListing(res.Text()); got != want {
		t.Errorf("listing =\n%s\nwant\n%s", got, want)
	}
}

func TestErrorResultListsNestedAndArrayParameters(t *testing.T) {
	res, err := newAlertTool(t).Execute(t.Context(), jsontext.Value(`{"nope":1}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	const want = `Parameters:
  query (string, required): what to search for
  window (object, required): the time window
  window.start (string, required): ISO 8601 start
  window.end (string, optional)
  tags (array of string, optional): labels to match
  points (array of object, optional): places to check
  points[].lat (number, required)
  points[].lon (number, required)`
	if got := parameterListing(res.Text()); got != want {
		t.Errorf("listing =\n%s\nwant\n%s", got, want)
	}
}

func TestErrorResultListingAppearsInEveryFailure(t *testing.T) {
	tool := newWeatherTool(t, nil)
	for _, input := range []jsontext.Value{nil, jsontext.Value(`{"colour":"red"}`), jsontext.Value(`{"city":3}`), jsontext.Value(`{"city":`), jsontext.Value(`[1,2]`)} {
		res, err := tool.Execute(t.Context(), input)
		if err != nil {
			t.Fatalf("Execute(%q): %v", input, err)
		}
		if !res.IsError {
			t.Fatalf("Execute(%q) = %+v, want an error result", input, res)
		}
		text := res.Text()
		if !strings.Contains(text, "\n\nParameters:\n") {
			t.Errorf("Execute(%q) text has no parameter listing:\n%s", input, text)
		}
		if !strings.Contains(text, "city (string, required)") {
			t.Errorf("Execute(%q) listing omits city:\n%s", input, text)
		}
	}
}

// TestErrorResultSaysWhenThereAreNoParameters keeps the listing honest for a
// tool that takes nothing: an empty "Parameters:" heading would read as a
// truncated message.
func TestErrorResultSaysWhenThereAreNoParameters(t *testing.T) {
	tool, err := NewTool("ping", "Ping.", func(context.Context, emptyInput) (ToolResult, error) {
		return TextResult("pong"), nil
	})
	if err != nil {
		t.Fatalf("NewTool: %v", err)
	}
	res, err := tool.Execute(t.Context(), jsontext.Value(`{"colour":"red"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, want := parameterListing(res.Text()), "This tool takes no parameters."; got != want {
		t.Errorf("listing = %q, want %q", got, want)
	}
}

// problemLine is the problem statement at the head of an error result.
func problemLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// parameterListing is everything after the blank line that follows the problem
// statement.
func parameterListing(s string) string {
	_, rest, ok := strings.Cut(s, "\n\n")
	if !ok {
		return ""
	}
	return rest
}
