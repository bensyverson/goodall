package goodall

import (
	json "encoding/json/v2"
	"reflect"
	"testing"
)

// TestSourceConstructors checks each constructor sets its discriminator and
// leaves the other members unset.
func TestSourceConstructors(t *testing.T) {
	cases := []struct {
		name string
		got  Source
		want Source
	}{
		{"bytes", BytesSource("image/png", []byte("PNGDATA")), Source{Type: SourceBytes, MediaType: "image/png", Data: []byte("PNGDATA")}},
		{"url", URLSource("https://example.com/a.png"), Source{Type: SourceURL, URL: "https://example.com/a.png"}},
		{"file", FileSource("file_1"), Source{Type: SourceFile, FileID: "file_1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !reflect.DeepEqual(c.got, c.want) {
				t.Errorf("= %#v, want %#v", c.got, c.want)
			}
		})
	}
}

// TestSourceJSON asserts the wire shape of each source kind, including that
// json/v2 writes Data as base64, and that each round-trips.
func TestSourceJSON(t *testing.T) {
	cases := []struct {
		name string
		src  Source
		want string
	}{
		{"bytes", BytesSource("image/png", []byte("PNGDATA")), `{"type":"base64","media_type":"image/png","data":"UE5HREFUQQ=="}`},
		{"url", URLSource("https://example.com/a.png"), `{"type":"url","url":"https://example.com/a.png"}`},
		{"file", FileSource("file_1"), `{"type":"file","file_id":"file_1"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.src)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("Marshal = %s, want %s", got, c.want)
			}
			var back Source
			if err := json.Unmarshal([]byte(c.want), &back); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(back, c.src) {
				t.Errorf("round trip = %#v, want %#v", back, c.src)
			}
		})
	}
}
