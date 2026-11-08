// Package model holds the plain data the sources produce and the rates consume:
// one normalized engine reading, one node_exporter reading, maitred's /status.
package model

import (
	"encoding/json"
	"time"
)

// Opt is a number that may be absent: engines export different subsets.
type Opt struct {
	V  float64
	OK bool
}

// Some is a present Opt.
func Some(v float64) Opt { return Opt{V: v, OK: true} }

// MarshalJSON writes an absent value as null.
func (o Opt) MarshalJSON() ([]byte, error) {
	if !o.OK {
		return []byte("null"), nil
	}
	return json.Marshal(o.V)
}

// Engine kinds, detected from the metric names of a scrape.
const (
	KindUnholy   = "unholy"
	KindEklow    = "eklow"
	KindRadiance = "radiance"
	KindVLLM     = "vllm"
	KindLlamaCpp = "llamacpp"
	KindHealth   = "health" // no usable /metrics: up or down from /health
)

// Reading is one scrape of an engine, normalized across kinds. Token and
// seconds fields are monotonic counters since the engine process started.
type Reading struct {
	At   time.Time
	Kind string
	Up   bool // health-only: /health answered 2xx (metrics kinds are up by definition)

	PrefillTokens float64 // prompt tokens computed, prefix-cache hits excluded
	DecodeTokens  float64

	PrefillSeqSeconds Opt // summed over sequences, wall seconds in the prefill phase
	DecodeSeqSeconds  Opt

	FirstTokenSeconds Opt // sum of time-to-first-token over requests
	FirstTokenCount   Opt

	Running Opt
	Waiting Opt
	Slots   Opt
	KV      Opt // 0..1
	Busy    Opt // seconds the engine spent stepping

	PromptTokens Opt // prompt tokens of admitted requests, cache hits included
	CachedTokens Opt // of those, served from the prefix cache

	SpecDrafted  Opt
	SpecAccepted Opt

	Model      string
	Context    Opt
	DeviceLost Opt
}

// Counters lists the monotonic counters of a reading, to detect a reset.
func (r Reading) Counters() []Opt {
	return []Opt{
		Some(r.PrefillTokens), Some(r.DecodeTokens),
		r.PrefillSeqSeconds, r.DecodeSeqSeconds,
		r.FirstTokenSeconds, r.FirstTokenCount,
		r.Busy, r.PromptTokens, r.CachedTokens, r.SpecDrafted, r.SpecAccepted,
	}
}

// GPU is one GPU as node_exporter reports it, before rates.
type GPU struct {
	Chip   string // hwmon chip label
	PCI    string // 0000:cc:00.0
	Driver string // amdgpu, xe, i915

	Temps map[string]float64 // by sensor label: edge, junction, mem, pkg, vram
	Power Opt                // watts, when the chip reports power directly
	// Energy is the xe energy counter in joules: power is its rate.
	Energy Opt
	Cap    Opt // watts
	Fan    Opt // rpm
	Clock  Opt // core clock MHz

	Card string // DRM card name joined to this device, "" when unknown
}

// DRMCard is what node_exporter's drm collector reports for one card.
type DRMCard struct {
	Name     string
	Busy     Opt // percent
	VRAMSize Opt
	VRAMUsed Opt
	GTTSize  Opt
	GTTUsed  Opt
	Device   string // PCI device from drm_card_device_info, "" when unknown
}

// Node is one scrape of a host's node_exporter.
type Node struct {
	At       time.Time
	CPUIdle  float64 // seconds, all CPUs
	CPUTotal float64 // seconds, all CPUs, all modes
	MemTotal Opt
	MemAvail Opt
	Load1    Opt
	GPUs     []GPU
	Cards    []DRMCard
}

// Status is maitred's GET /status (contract section 2).
type Status struct {
	Version   int        `json:"version"`
	Now       float64    `json:"now"`
	Started   float64    `json:"started"`
	Upstreams []Upstream `json:"upstreams"`
	Modes     []Mode     `json:"modes"`
	Keys      []Key      `json:"keys"`
	Recent    []Request  `json:"recent"`
}

// Upstream is one configured engine (or hosted API) behind maitred.
type Upstream struct {
	Name     string   `json:"name"`
	BaseURL  string   `json:"base_url"`
	Host     *string  `json:"host"`
	Hosted   bool     `json:"hosted"`
	Serving  bool     `json:"serving"`
	Reason   string   `json:"reason"`
	InFlight int      `json:"in_flight"`
	Slots    *int     `json:"slots"`
	Models   []Served `json:"models"`
}

// Served is a model an upstream is configured to serve.
type Served struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Hidden      bool   `json:"hidden"`
	Tier        int    `json:"tier"`
}

// Mode is a llm-mode: the set of upstreams a host runs together.
type Mode struct {
	Name      string   `json:"name"`
	Label     string   `json:"label"`
	Host      string   `json:"host"`
	Upstreams []string `json:"upstreams"`
	Serving   bool     `json:"serving"`
}

// Key is a client's virtual key, counted since maitred started.
type Key struct {
	Name             string   `json:"name"`
	InFlight         int      `json:"in_flight"`
	Requests         int      `json:"requests"`
	Failures         int      `json:"failures"`
	PromptTokens     int64    `json:"prompt_tokens"`
	CompletionTokens int64    `json:"completion_tokens"`
	CachedTokens     int64    `json:"cached_tokens"`
	Last             *float64 `json:"last"`
}

// Usage is the OpenAI usage object maitred found in an answer.
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CachedTokens     int64 `json:"cached_tokens"`
}

// Request is one entry of maitred's recent-request ring.
type Request struct {
	ID         int64   `json:"id"`
	Start      float64 `json:"start"`
	Key        string  `json:"key"`
	Requested  string  `json:"requested"`
	Model      string  `json:"model"`
	Upstream   string  `json:"upstream"`
	Route      string  `json:"route"`
	Path       string  `json:"path"`
	Status     int     `json:"status"`
	HeadersMS  *int64  `json:"headers_ms"`
	FirstChunk *int64  `json:"first_chunk_ms"`
	DurationMS *int64  `json:"duration_ms"`
	Done       bool    `json:"done"`
	Error      string  `json:"error"`
	Usage      *Usage  `json:"usage"`
}

// ModelStatus is one row of the /v1/models fallback for a maitred without /status.
type ModelStatus struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"status_reason,omitempty"`
}
