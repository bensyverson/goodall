package goodall

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The types below are the inference fixtures. Each supported one is paired
// with a sample document in inferCases, decoded with RejectUnknownMembers so
// that a member name the schema and json/v2 disagree about fails the test.

type greetIn struct {
	Name string `json:"name" desc:"who to greet"`
}

type numbersIn struct {
	Count  int     `json:"count"`
	Size   uint16  `json:"size"`
	Ratio  float32 `json:"ratio"`
	Amount float64 `json:"amount"`
	On     bool    `json:"on"`
}

// mode is a named string type: inference keys off the kind, not the type.
type mode string

type enumIn struct {
	Mode mode `json:"mode" desc:"how hard to try" enum:"fast, slow"`
}

type listIn struct {
	Tags  []string `json:"tags"`
	Sizes [2]int   `json:"sizes"`
}

type point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// nestedIn mentions point twice on two different paths, which a global
// "seen this type" recursion guard would wrongly reject.
type nestedIn struct {
	Origin point   `json:"origin"`
	Path   []point `json:"path"`
}

type optionalIn struct {
	Zero  string `json:"zero,omitzero"`
	Empty string `json:"empty,omitempty"`
	Ptr   *int   `json:"ptr"`
	Req   string `json:"req"`
}

type ident struct {
	ID string `json:"id"`
}

type embedIn struct {
	Lead string `json:"lead"`
	ident
	Note string `json:"note"`
}

// embedShadowIn relies on json/v2's depth rule: the shallower field wins and
// keeps its own position.
type embedShadowIn struct {
	ID string `json:"id" desc:"the outer one wins"`
	ident
}

// embedNamedIn gives the embedded struct a JSON name, which stops json/v2
// flattening it.
type embedNamedIn struct {
	ident `json:"ident"`
}

type rawIn struct {
	Payload jsontext.Value `json:"payload" desc:"any JSON"`
}

type timeIn struct {
	At    time.Time  `json:"at"`
	Until *time.Time `json:"until"`
}

type smallByte uint8

type bytesIn struct {
	Data   []byte      `json:"data"`
	Digest [4]byte     `json:"digest"`
	Codes  []smallByte `json:"codes"`
}

type tagIn struct {
	Name   string `desc:"no json tag, so the Go field name is the member name"`
	Hidden string `json:"-"`
	secret string
	Kept   string `json:"kept"`
}

type stringTagIn struct {
	Big int64 `json:"big,string"`
}

type textIn struct {
	Addr netip.Addr `json:"addr"`
}

type emptyIn struct{}

var inferCases = []struct {
	name   string
	typ    reflect.Type
	want   string
	sample string
}{
	{
		"string with a description",
		reflect.TypeFor[greetIn](),
		`{"type":"object","properties":{"name":{"type":"string","description":"who to greet"}},"required":["name"],"additionalProperties":false}`,
		`{"name":"ada"}`,
	},
	{
		"integers, numbers and booleans",
		reflect.TypeFor[numbersIn](),
		`{"type":"object","properties":{"count":{"type":"integer"},"size":{"type":"integer"},"ratio":{"type":"number"},"amount":{"type":"number"},"on":{"type":"boolean"}},"required":["count","size","ratio","amount","on"],"additionalProperties":false}`,
		`{"count":1,"size":2,"ratio":1.5,"amount":2.5,"on":true}`,
	},
	{
		"enum",
		reflect.TypeFor[enumIn](),
		`{"type":"object","properties":{"mode":{"type":"string","description":"how hard to try","enum":["fast","slow"]}},"required":["mode"],"additionalProperties":false}`,
		`{"mode":"fast"}`,
	},
	{
		"slice and array",
		reflect.TypeFor[listIn](),
		`{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}},"sizes":{"type":"array","items":{"type":"integer"}}},"required":["tags","sizes"],"additionalProperties":false}`,
		`{"tags":["a","b"],"sizes":[1,2]}`,
	},
	{
		"nested struct",
		reflect.TypeFor[nestedIn](),
		`{"type":"object","properties":{"origin":{"type":"object","properties":{"x":{"type":"integer"},"y":{"type":"integer"}},"required":["x","y"],"additionalProperties":false},"path":{"type":"array","items":{"type":"object","properties":{"x":{"type":"integer"},"y":{"type":"integer"}},"required":["x","y"],"additionalProperties":false}}},"required":["origin","path"],"additionalProperties":false}`,
		`{"origin":{"x":1,"y":2},"path":[{"x":3,"y":4}]}`,
	},
	{
		"optional fields",
		reflect.TypeFor[optionalIn](),
		`{"type":"object","properties":{"zero":{"type":"string"},"empty":{"type":"string"},"ptr":{"type":"integer"},"req":{"type":"string"}},"required":["req"],"additionalProperties":false}`,
		`{"req":"x"}`,
	},
	{
		"embedded struct flattens in place",
		reflect.TypeFor[embedIn](),
		`{"type":"object","properties":{"lead":{"type":"string"},"id":{"type":"string"},"note":{"type":"string"}},"required":["lead","id","note"],"additionalProperties":false}`,
		`{"lead":"a","id":"b","note":"c"}`,
	},
	{
		"an outer field shadows an embedded one",
		reflect.TypeFor[embedShadowIn](),
		`{"type":"object","properties":{"id":{"type":"string","description":"the outer one wins"}},"required":["id"],"additionalProperties":false}`,
		`{"id":"x"}`,
	},
	{
		"a named embedded struct stays nested",
		reflect.TypeFor[embedNamedIn](),
		`{"type":"object","properties":{"ident":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}},"required":["ident"],"additionalProperties":false}`,
		`{"ident":{"id":"x"}}`,
	},
	{
		"jsontext.Value is unconstrained",
		reflect.TypeFor[rawIn](),
		`{"type":"object","properties":{"payload":{"description":"any JSON"}},"required":["payload"],"additionalProperties":false}`,
		`{"payload":{"anything":[1,2,3]}}`,
	},
	{
		"time.Time is a date-time string",
		reflect.TypeFor[timeIn](),
		`{"type":"object","properties":{"at":{"type":"string","format":"date-time"},"until":{"type":"string","format":"date-time"}},"required":["at"],"additionalProperties":false}`,
		`{"at":"2026-09-14T12:00:00Z"}`,
	},
	{
		"byte slices and byte arrays are base64 strings",
		reflect.TypeFor[bytesIn](),
		`{"type":"object","properties":{"data":{"type":"string"},"digest":{"type":"string"},"codes":{"type":"array","items":{"type":"integer"}}},"required":["data","digest","codes"],"additionalProperties":false}`,
		`{"data":"aGk=","digest":"AQIDBA==","codes":[1,2]}`,
	},
	{
		"untagged, skipped and unexported fields",
		reflect.TypeFor[tagIn](),
		`{"type":"object","properties":{"Name":{"type":"string","description":"no json tag, so the Go field name is the member name"},"kept":{"type":"string"}},"required":["Name","kept"],"additionalProperties":false}`,
		`{"Name":"a","kept":"b"}`,
	},
	{
		"the string tag option makes a number a JSON string",
		reflect.TypeFor[stringTagIn](),
		`{"type":"object","properties":{"big":{"type":"string"}},"required":["big"],"additionalProperties":false}`,
		`{"big":"9007199254740993"}`,
	},
	{
		"a TextMarshaler is a string",
		reflect.TypeFor[textIn](),
		`{"type":"object","properties":{"addr":{"type":"string"}},"required":["addr"],"additionalProperties":false}`,
		`{"addr":"1.2.3.4"}`,
	},
	{
		"a struct with no fields still spells out properties",
		reflect.TypeFor[emptyIn](),
		`{"type":"object","properties":{},"additionalProperties":false}`,
		`{}`,
	},
}

func TestInferSchema(t *testing.T) {
	for _, c := range inferCases {
		t.Run(c.name, func(t *testing.T) {
			s, err := InferSchema(c.typ)
			if err != nil {
				t.Fatalf("InferSchema(%s): %v", c.typ, err)
			}
			got, err := json.Marshal(s)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("schema =\n%s\nwant\n%s", got, c.want)
			}
			if err := s.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil", err)
			}
			// The names in the schema are the names json/v2 matches, so a
			// document built from them must decode with no leftovers.
			into := reflect.New(c.typ).Interface()
			if err := json.Unmarshal([]byte(c.sample), into, json.RejectUnknownMembers(true)); err != nil {
				t.Errorf("Unmarshal(%s) into %s: %v", c.sample, c.typ, err)
			}
		})
	}
}

func TestSchemaFor(t *testing.T) {
	want := `{"type":"object","properties":{"name":{"type":"string","description":"who to greet"}},"required":["name"],"additionalProperties":false}`
	for _, s := range []func() (*Schema, error){SchemaFor[greetIn], SchemaFor[*greetIn]} {
		got, err := s()
		if err != nil {
			t.Fatalf("SchemaFor: %v", err)
		}
		b, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(b) != want {
			t.Errorf("SchemaFor =\n%s\nwant\n%s", b, want)
		}
	}
}

func TestSchemaForRejectsNonStructs(t *testing.T) {
	cases := map[string]func() (*Schema, error){
		"string":    SchemaFor[string],
		"int":       SchemaFor[int],
		"map":       SchemaFor[map[string]int],
		"slice":     SchemaFor[[]greetIn],
		"any":       SchemaFor[any],
		"time.Time": SchemaFor[time.Time],
		"raw JSON":  SchemaFor[jsontext.Value],
	}
	for name, fn := range cases {
		if s, err := fn(); err == nil {
			t.Errorf("%s: SchemaFor() = %+v, want an error", name, s)
		}
	}
}

type mapIn struct {
	M map[string]int `json:"m"`
}

type anyIn struct {
	A any `json:"a"`
}

type ifaceIn struct {
	S fmt.Stringer `json:"s"`
}

type funcIn struct {
	F func() `json:"f"`
}

type chanIn struct {
	C chan int `json:"c"`
}

type complexIn struct {
	Z complex128 `json:"z"`
}

type durationIn struct {
	D time.Duration `json:"d"`
}

type options struct {
	Mode map[string]string `json:"mode"`
}

type deepMapIn struct {
	Options options `json:"options"`
}

type sliceOfMapIn struct {
	L []map[string]int `json:"l"`
}

type kidsIn struct {
	Kids []*kidsIn `json:"kids"`
}

type selfIn struct {
	Next *selfIn `json:"next"`
}

type mutualAIn struct {
	B mutualBIn `json:"b"`
}

type mutualBIn struct {
	A *mutualAIn `json:"a"`
}

type enumOnIntIn struct {
	N int `json:"n" enum:"1,2"`
}

type stringOnBoolIn struct {
	B bool `json:"b,string"`
}

type label string

type embedNonStructIn struct {
	label
	C string `json:"c"`
}

// conflictType is two fields claiming one JSON name, which json/v2 refuses
// to encode. It is built by reflection because go vet refuses to compile the
// equivalent source — and an anonymous struct type has no name, so the field
// paths inference reports are rooted at "input".
func conflictType() reflect.Type {
	str := reflect.TypeFor[string]()
	return reflect.StructOf([]reflect.StructField{
		{Name: "A", Type: str, Tag: `json:"x"`},
		{Name: "B", Type: str, Tag: `json:"x"`},
	})
}

func TestInferSchemaRejects(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
		want string // the field path the message must name
	}{
		{"map", reflect.TypeFor[mapIn](), "mapIn.M"},
		{"any", reflect.TypeFor[anyIn](), "anyIn.A"},
		{"interface", reflect.TypeFor[ifaceIn](), "ifaceIn.S"},
		{"func", reflect.TypeFor[funcIn](), "funcIn.F"},
		{"chan", reflect.TypeFor[chanIn](), "chanIn.C"},
		{"complex", reflect.TypeFor[complexIn](), "complexIn.Z"},
		{"time.Duration", reflect.TypeFor[durationIn](), "durationIn.D"},
		{"map two levels down", reflect.TypeFor[deepMapIn](), "deepMapIn.Options.Mode"},
		{"map inside a slice", reflect.TypeFor[sliceOfMapIn](), "sliceOfMapIn.L[]"},
		{"recursion through a slice", reflect.TypeFor[kidsIn](), "kidsIn.Kids[]"},
		{"recursion through a pointer", reflect.TypeFor[selfIn](), "selfIn.Next"},
		{"mutual recursion", reflect.TypeFor[mutualAIn](), "mutualAIn.B.A"},
		{"enum on a non-string", reflect.TypeFor[enumOnIntIn](), "enumOnIntIn.N"},
		{"the string option on a bool", reflect.TypeFor[stringOnBoolIn](), "stringOnBoolIn.B"},
		{"embedded non-struct", reflect.TypeFor[embedNonStructIn](), "embedNonStructIn.label"},
		{"two fields claiming one name", conflictType(), "input.B"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := InferSchema(c.typ)
			if err == nil {
				t.Fatalf("InferSchema(%s) = %+v, want an error", c.typ, s)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("InferSchema(%s) = %q, want it to name %q", c.typ, err, c.want)
			}
		})
	}
}

// TestInferSchemaErrorIsTyped keeps the field path machine-readable, so a
// caller can report it without scraping the message.
func TestInferSchemaErrorIsTyped(t *testing.T) {
	_, err := InferSchema(reflect.TypeFor[mapIn]())
	if err == nil {
		t.Fatal("InferSchema = nil error, want an error")
	}
	se, ok := errors.AsType[*SchemaError](err)
	if !ok {
		t.Fatalf("error is %T, want *SchemaError", err)
	}
	if se.Path != "mapIn.M" {
		t.Errorf("Path = %q, want %q", se.Path, "mapIn.M")
	}
}
