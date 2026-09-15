package goodall

import (
	"encoding/json/v2"
	"testing"
)

func TestCachePolicyZeroValueIsAuto(t *testing.T) {
	var p CachePolicy
	if p != CacheAuto {
		t.Fatalf("zero CachePolicy = %q, want CacheAuto", p)
	}
	if got := p.String(); got != "auto" {
		t.Fatalf("zero CachePolicy.String() = %q, want %q", got, "auto")
	}
	for _, c := range []struct {
		p    CachePolicy
		want string
	}{{CacheAuto, "auto"}, {CacheManual, "manual"}, {CacheOff, "off"}} {
		if got := c.p.String(); got != c.want {
			t.Errorf("%v.String() = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestCacheControlJSON(t *testing.T) {
	cases := []struct {
		name string
		in   CacheControl
		want string
	}{
		{"default ttl", CacheControl{}, `{}`},
		{"one hour", CacheControl{TTL: CacheTTL1h}, `{"ttl":"1h"}`},
		{"five minutes", CacheControl{TTL: CacheTTL5m}, `{"ttl":"5m"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("marshal = %s, want %s", got, c.want)
			}
			var back CacheControl
			if err := json.Unmarshal(got, &back); err != nil {
				t.Fatal(err)
			}
			if back != c.in {
				t.Fatalf("round trip = %+v, want %+v", back, c.in)
			}
		})
	}
}
