package decision

// ToolOption configures a judgment tool at construction, for both [Tool] and
// [AuthoredTool]. [WithModel] is one and so is [WithDescription].
type ToolOption interface {
	applyTool(*toolConfig)
}

// RouteOption configures a [Route]. [WithModel] is one, and so are [WithState]
// and [WithFallthrough].
type RouteOption interface {
	applyRoute(*routeConfig)
}

// ModelOption pins a model. It is both a [ToolOption] and a [RouteOption], which
// is why it is a type of its own: the three adapters share one spelling of the
// one setting they all have. Build one with [WithModel].
type ModelOption string

func (m ModelOption) applyTool(cfg *toolConfig) { cfg.model = string(m) }

func (m ModelOption) applyRoute(cfg *routeConfig) { cfg.model = string(m) }

// WithModel pins the model every call this adapter makes is answered by. It is
// what a caller whose thresholds are tuned against one version reaches for:
// pinned once, at the adapter, rather than at each of the calls a model decides
// to make. Without it the choice is the [Decider]'s, as on a [Request] with no
// model.
func WithModel(model string) ModelOption { return ModelOption(model) }

// toolOptionFunc is a [ToolOption] that applies to tools alone.
type toolOptionFunc func(*toolConfig)

func (f toolOptionFunc) applyTool(cfg *toolConfig) { f(cfg) }

// routeOptionFunc is a [RouteOption] that applies to routes alone.
type routeOptionFunc func(*routeConfig)

func (f routeOptionFunc) applyRoute(cfg *routeConfig) { f(cfg) }
