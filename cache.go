package goodall

// CachePolicy decides where prompt-cache breakpoints are placed on a request.
// The zero value is CacheAuto, so a consumer who never thinks about caching
// gets the recommended shape for an agent loop.
type CachePolicy string

const (
	// CacheAuto sends the provider's top-level automatic marker plus one
	// explicit breakpoint on the last static system block. Markers set by
	// the caller on individual blocks are ignored.
	CacheAuto CachePolicy = ""
	// CacheManual sends exactly the markers the caller set on blocks and
	// rejects a request carrying more than the provider's limit.
	CacheManual CachePolicy = "manual"
	// CacheOff sends no cache markers at all.
	CacheOff CachePolicy = "off"
)

// String names the policy, calling the zero value "auto".
func (p CachePolicy) String() string {
	if p == CacheAuto {
		return "auto"
	}
	return string(p)
}

// CacheTTL is how long a cache breakpoint's prefix stays warm. The zero
// value leaves the choice to the provider.
type CacheTTL string

const (
	// CacheTTLDefault lets the provider choose its default lifetime.
	CacheTTLDefault CacheTTL = ""
	// CacheTTL5m keeps the prefix for five minutes, refreshed on each hit.
	CacheTTL5m CacheTTL = "5m"
	// CacheTTL1h keeps the prefix for one hour at a higher write price.
	CacheTTL1h CacheTTL = "1h"
)

// CacheControl marks a block as a cache breakpoint under CacheManual. Blocks
// hold it as a pointer: nil is no marker, and a zero CacheControl is a
// marker with the provider's default lifetime.
type CacheControl struct {
	TTL CacheTTL `json:"ttl,omitzero"`
}
