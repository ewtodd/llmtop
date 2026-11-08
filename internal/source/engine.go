package source

import (
	"strings"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/prom"
)

// DetectKind names the engine behind a scrape by the metric names present. It
// is run on every scrape: llm-mode swaps servers behind one port. Radiance
// exports vllm: names too, so it is checked before vllm; gufo exports gufo_
// names beside llamacpp:. "" when nothing is recognised.
func DetectKind(s *prom.Set) string {
	switch {
	case s.HasPrefix("unholy_"):
		return model.KindUnholy
	case s.HasPrefix("eklow_"):
		return model.KindEklow
	case s.HasPrefix("radiance:"):
		return model.KindRadiance
	case s.HasPrefix("vllm:"):
		return model.KindVLLM
	case s.HasPrefix("llamacpp:"):
		return model.KindLlamaCpp
	}
	return ""
}

// Normalize turns a scrape into a Reading. ok is false when the metrics are of
// no kind llmtop knows.
func Normalize(s *prom.Set, at time.Time) (model.Reading, bool) {
	kind := DetectKind(s)
	r := model.Reading{At: at, Kind: kind, Up: true}
	switch kind {
	case model.KindUnholy:
		normalizeOwn(s, &r, "unholy_")
	case model.KindEklow:
		normalizeOwn(s, &r, "eklow_")
	case model.KindRadiance:
		normalizeRadiance(s, &r)
	case model.KindVLLM:
		normalizeVLLM(s, &r)
	case model.KindLlamaCpp:
		normalizeLlamaCpp(s, &r)
	default:
		return r, false
	}
	return r, true
}

func opt(s *prom.Set, name string) model.Opt {
	v, ok := s.Sum(name)
	return model.Opt{V: v, OK: ok}
}

func val(s *prom.Set, name string) float64 {
	v, _ := s.Sum(name)
	return v
}

// normalizeOwn reads unholy and eklow, which share the contract's section 1.
// Servers built before the new counters (sequence-seconds, first-token, info,
// slots, context, kv_usage_ratio) lack them; each is then simply absent, and
// the K/V share falls back to the page counts unholy has always exported.
func normalizeOwn(s *prom.Set, r *model.Reading, p string) {
	r.PrefillTokens = val(s, p+"prefill_tokens_total")
	r.DecodeTokens = val(s, p+"decode_tokens_total")
	r.PrefillSeqSeconds = opt(s, p+"prefill_sequence_seconds_total")
	r.DecodeSeqSeconds = opt(s, p+"decode_sequence_seconds_total")
	r.FirstTokenSeconds = opt(s, p+"first_token_seconds_total")
	r.FirstTokenCount = opt(s, p+"first_tokens_total")
	r.Running = opt(s, p+"running")
	r.Waiting = opt(s, p+"waiting")
	r.Slots = opt(s, p+"slots")
	r.Context = opt(s, p+"context_tokens")
	r.Busy = opt(s, p+"busy_seconds_total")
	r.PromptTokens = opt(s, p+"prompt_tokens_total")
	r.CachedTokens = opt(s, p+"cached_prompt_tokens_total")
	r.SpecDrafted = opt(s, p+"spec_drafted_total")
	r.SpecAccepted = opt(s, p+"spec_accepted_total")
	r.Model = s.Label(p+"info", "model")
	if kv, ok := s.Value(p + "kv_usage_ratio"); ok {
		r.KV = model.Some(kv)
	} else if free, ok := s.Value(p + "free_pages"); ok {
		if total, ok := s.Value(p + "total_pages"); ok && total > 0 {
			r.KV = model.Some(1 - free/total)
		}
	}
}

// normalizeRadiance reads radiance: names plus the vllm: gauges it borrows.
//
// There is no per-sequence time on radiance, so per-stream falls back to
// aggregate over mean running. The decode_step_seconds_{best,recent} gauges
// are not used: they are the mean of 32 consecutive single-stream steps, they
// hold their last value while no such window completes, and with speculation a
// step emits 1+accepted tokens, so they are not a live per-stream rate under
// concurrent load.
func normalizeRadiance(s *prom.Set, r *model.Reading) {
	r.PrefillTokens = val(s, "radiance:prefill_tokens_total")
	r.DecodeTokens = val(s, "radiance:decode_tokens_total")
	r.Running = opt(s, "vllm:num_requests_running")
	r.Waiting = opt(s, "vllm:num_requests_waiting")
	r.KV = opt(s, "vllm:kv_cache_usage_perc") // 0..1: "1 means 100 percent usage"
	if !r.KV.OK {
		r.KV = opt(s, "vllm:gpu_cache_usage_perc")
	}
	r.FirstTokenSeconds = opt(s, "vllm:time_to_first_token_seconds_sum")
	r.FirstTokenCount = opt(s, "vllm:time_to_first_token_seconds_count")
	r.SpecDrafted = opt(s, "radiance:draft_tokens_total")
	r.SpecAccepted = opt(s, "radiance:draft_accepted_total")
	r.Busy = opt(s, "radiance:engine_step_seconds_total")
	if cached := opt(s, "radiance:attn_cached_tokens_total"); cached.OK {
		r.CachedTokens = cached
		r.PromptTokens = model.Some(r.PrefillTokens + cached.V)
	}
	r.Model = s.Label("vllm:num_requests_running", "model_name")
}

// normalizeVLLM reads a stock vLLM. Its prompt counter includes prefix-cache
// hits; the cached counter is subtracted to get the tokens computed. The
// per-request phase times advance when a request completes, so per-stream is
// bursty there and falls back to the running mean when the times stand still.
func normalizeVLLM(s *prom.Set, r *model.Reading) {
	prompt := val(s, "vllm:prompt_tokens_total")
	cached := opt(s, "vllm:prompt_tokens_cached_total")
	r.PrefillTokens = prompt - cached.V
	r.DecodeTokens = val(s, "vllm:generation_tokens_total")
	r.PromptTokens = model.Some(prompt)
	r.CachedTokens = cached
	r.PrefillSeqSeconds = opt(s, "vllm:request_prefill_time_seconds_sum")
	r.DecodeSeqSeconds = opt(s, "vllm:request_decode_time_seconds_sum")
	r.FirstTokenSeconds = opt(s, "vllm:time_to_first_token_seconds_sum")
	r.FirstTokenCount = opt(s, "vllm:time_to_first_token_seconds_count")
	r.Running = opt(s, "vllm:num_requests_running")
	r.Waiting = opt(s, "vllm:num_requests_waiting")
	r.KV = opt(s, "vllm:kv_cache_usage_perc")
	if !r.KV.OK {
		r.KV = opt(s, "vllm:gpu_cache_usage_perc")
	}
	r.SpecDrafted = opt(s, "vllm:spec_decode_num_draft_tokens_total")
	r.SpecAccepted = opt(s, "vllm:spec_decode_num_accepted_tokens_total")
	r.Model = s.Label("vllm:num_requests_running", "model_name")
}

// normalizeLlamaCpp reads gufo's llama.cpp --metrics. prompt_tokens_total
// excludes cache hits (gufo's help text says so); the cached counter is the
// other part of the prompt. The *_seconds_total counters are per-request
// processing time summed over requests, i.e. sequence-seconds.
func normalizeLlamaCpp(s *prom.Set, r *model.Reading) {
	r.PrefillTokens = val(s, "llamacpp:prompt_tokens_total")
	r.DecodeTokens = val(s, "llamacpp:tokens_predicted_total")
	r.PrefillSeqSeconds = opt(s, "llamacpp:prompt_seconds_total")
	r.DecodeSeqSeconds = opt(s, "llamacpp:tokens_predicted_seconds_total")
	r.Running = opt(s, "llamacpp:requests_processing")
	r.Waiting = opt(s, "llamacpp:requests_deferred")
	r.KV = opt(s, "llamacpp:kv_cache_usage_ratio")
	r.SpecDrafted = opt(s, "llamacpp:spec_decode_num_draft_tokens_total")
	r.SpecAccepted = opt(s, "llamacpp:spec_decode_num_accepted_tokens_total")
	r.DeviceLost = opt(s, "gufo_device_lost_total")
	if cached := opt(s, "llamacpp:prompt_tokens_cached_total"); cached.OK {
		r.CachedTokens = cached
		r.PromptTokens = model.Some(r.PrefillTokens + cached.V)
	}
}

// EngineRoot is an upstream's base_url without the trailing /v1.
func EngineRoot(baseURL string) string {
	u := strings.TrimRight(baseURL, "/")
	u = strings.TrimSuffix(u, "/v1")
	return strings.TrimRight(u, "/")
}
