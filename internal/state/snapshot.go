package state

import (
	"time"

	"github.com/ewtodd/llmtop/internal/model"
)

// Snapshot is everything the screen shows, normalized: the UI renders only
// this, --dump prints it, --demo fabricates it.
type Snapshot struct {
	Time    time.Time     `json:"time"`
	Maitred MaitredView   `json:"maitred"`
	Hosts   []HostView    `json:"hosts"`
	Engines []EngineView  `json:"engines"`
	Standby []StandbyView `json:"standby"`
	Callers []CallerView  `json:"callers"`
	Feed    []FeedView    `json:"feed"`
}

// MaitredView is the state of the gateway.
type MaitredView struct {
	URL     string  `json:"url"`
	State   string  `json:"state"` // connecting, ok, unreachable
	Note    string  `json:"note"`  // "maitred has no /status"
	Err     string  `json:"error"` // why unreachable
	UpSec   float64 `json:"up_seconds"`
	AgeSec  float64 `json:"age_seconds"` // of the data shown, when it is stale
	Stale   bool    `json:"stale"`
	Serving int     `json:"models_serving"` // /v1/models fallback only
	Models  int     `json:"models_total"`
}

// StreamView is a per-stream speed.
type StreamView struct {
	Value  model.Opt `json:"tok_s"`
	Approx bool      `json:"approx"`
	Held   bool      `json:"held"`
}

// EngineView is one serving engine.
type EngineView struct {
	Name    string `json:"name"`
	Display string `json:"display"` // the model's display name
	Served  string `json:"served"`  // the name the engine reports
	Kind    string `json:"kind"`
	Host    string `json:"host"`
	Addr    string `json:"addr"` // host:port
	Mode    string `json:"mode"`

	Up     bool    `json:"up"`
	Err    string  `json:"error"`
	AgeSec float64 `json:"age_seconds"` // of the last good reading when Err is set
	Stale  bool    `json:"stale"`
	Tiny   bool    `json:"tiny"` // one line instead of a card
	// Pinned: its card stays while idle (--pin names it or its host). A placeholder is the card of a
	// pinned host that has no engine serving; Note says why.
	Pinned      bool   `json:"pinned"`
	Placeholder bool   `json:"placeholder,omitempty"`
	Note        string `json:"note,omitempty"`

	Live    int       `json:"live"` // maitred's in-flight count
	Slots   model.Opt `json:"slots"`
	Running model.Opt `json:"running"`
	Waiting model.Opt `json:"waiting"`
	KV      model.Opt `json:"kv"`
	Context model.Opt `json:"context"`

	Prefill     StreamView `json:"prefill"`
	Decode      StreamView `json:"decode"`
	PrefillAgg  model.Opt  `json:"prefill_agg_tok_s"`
	DecodeAgg   model.Opt  `json:"decode_agg_tok_s"`
	PeakPrefill float64    `json:"peak_prefill"`
	PeakDecode  float64    `json:"peak_decode"`
	Active      bool       `json:"active"`
	Ready       bool       `json:"ready"`
	IdleSec     model.Opt  `json:"idle_seconds"` // absent when never seen active

	Busy       model.Opt `json:"busy_pct"`
	TTFT       model.Opt `json:"ttft_s"`
	Cache      model.Opt `json:"cache_pct"`
	Spec       model.Opt `json:"spec_pct"`
	DeviceLost model.Opt `json:"device_lost"`
}

// StandbyView is an upstream that is not serving.
type StandbyView struct {
	Name   string `json:"name"`
	Host   string `json:"host,omitempty"`
	Mode   string `json:"mode"`
	Reason string `json:"reason,omitempty"`
}

// HostView is a machine and its GPUs.
type HostView struct {
	Name      string    `json:"name"`
	Modes     []string  `json:"modes"` // serving modes: "glm GLM-5.3-Flash"
	Switching bool      `json:"switching"`
	NodeURL   string    `json:"node_url"`
	CPU       model.Opt `json:"cpu_pct"`
	MemUsed   model.Opt `json:"mem_used_bytes"`
	MemTotal  model.Opt `json:"mem_total_bytes"`
	Load1     model.Opt `json:"load1"`
	GPUs      []GPUView `json:"gpus"`
	Err       string    `json:"error"`
	AgeSec    float64   `json:"age_seconds"`
	Stale     bool      `json:"stale"`
	NoNode    bool      `json:"no_node"` // --no-hosts
}

// GPUView is one GPU.
type GPUView struct {
	Label   string             `json:"label"`
	Driver  string             `json:"driver"`
	PCI     string             `json:"pci"`
	Busy    model.Opt          `json:"busy_pct"`
	MemKind string             `json:"mem_kind"` // vram, or gtt for an iGPU
	MemUsed model.Opt          `json:"mem_used_bytes"`
	MemSize model.Opt          `json:"mem_total_bytes"`
	Temps   map[string]float64 `json:"temps_c"`
	Crit    map[string]float64 `json:"temps_crit_c,omitempty"` // sensors' critical limits, when the cards export them
	Power   model.Opt          `json:"power_w"`
	Cap     model.Opt          `json:"power_cap_w"`
	Fan     model.Opt          `json:"fan_rpm"`
	Clock   model.Opt          `json:"clock_mhz"`
}

// CallerView is a virtual key's activity.
type CallerView struct {
	Name         string    `json:"name"`
	Live         int       `json:"live"`
	Requests     int       `json:"requests"`
	Req10m       int       `json:"req_10m"`
	Fail10m      int       `json:"fail_10m"`
	PromptTokens int64     `json:"prompt_tokens"`
	OutTokens    int64     `json:"completion_tokens"`
	CachedTokens int64     `json:"cached_tokens"`
	CachePct     model.Opt `json:"cache_pct"`
	TTFTp50MS    model.Opt `json:"ttft_p50_ms"`
	LastAgoSec   model.Opt `json:"last_ago_seconds"`
}

// FeedView is one request of the feed.
type FeedView struct {
	Start      time.Time `json:"start"`
	Key        string    `json:"key"`
	Requested  string    `json:"requested"`
	Model      string    `json:"model"`
	Upstream   string    `json:"upstream"`
	Status     int       `json:"status"`
	TTFTms     model.Opt `json:"ttft_ms"`
	DurationMS model.Opt `json:"duration_ms"`
	ElapsedMS  model.Opt `json:"elapsed_ms"` // still streaming: age so far
	Done       bool      `json:"done"`
	HasUsage   bool      `json:"has_usage"`
	In         int64     `json:"in_tokens"`
	Out        int64     `json:"out_tokens"`
	CachePct   model.Opt `json:"cache_pct"`
	Error      string    `json:"error"`
}
