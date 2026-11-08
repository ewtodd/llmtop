// Package rates turns engine counters into the numbers on the screen.
//
// Per-stream speed is the headline: what one request experiences, delta tokens
// over delta sequence-seconds when the engine exports them. The aggregate over
// concurrent requests is secondary.
package rates

import (
	"math"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
)

const (
	// LongWindow is the window of the slow figures: TTFT, cache hits, speculation.
	LongWindow = 30 * time.Second
	// minSeq is the least sequence-seconds a window must have advanced for a
	// per-stream rate from them; below it the quotient is noise.
	minSeq = 1e-3
	// peakDecay is the per-second decay of the meter peaks (about a 6 minute half-life).
	peakDecay = 0.998
)

// Stream is one per-stream rate in tokens/s.
type Stream struct {
	Value  float64
	OK     bool
	Approx bool // aggregate over mean running, shown with a leading ~
	Held   bool // idle: the last active value, dimmed
}

// Rates are the figures derived from an engine's recent readings.
type Rates struct {
	Prefill, Decode         Stream
	PrefillAgg, DecodeAgg   model.Opt // tokens/s over the window; 0 when idle
	PeakPrefill, PeakDecode float64   // recent per-stream peaks, for the meters

	Ready     bool      // two readings exist: rates are meaningful
	Active    bool      // tokens moved in the window
	IdleSince time.Time // when tokens last moved; zero when never seen

	TTFT  model.Opt // seconds
	Busy  model.Opt // percent
	Cache model.Opt // percent, prefix-cache hits over prompt tokens
	Spec  model.Opt // percent, drafts accepted
}

// Tracker keeps an engine's recent readings and derives its Rates.
type Tracker struct {
	interval time.Duration
	hist     []model.Reading

	rates      Rates
	lastActive time.Time
	heldPre    float64
	heldDec    float64
	havePre    bool
	haveDec    bool
	ttft       model.Opt
	cache      model.Opt
	spec       model.Opt
	peakAt     time.Time
}

// NewTracker makes a tracker for an engine polled every interval.
func NewTracker(interval time.Duration) *Tracker { return &Tracker{interval: interval} }

// Rates are the figures as of the latest reading.
func (t *Tracker) Rates() Rates { return t.rates }

// Last is the latest reading.
func (t *Tracker) Last() (model.Reading, bool) {
	if len(t.hist) == 0 {
		return model.Reading{}, false
	}
	return t.hist[len(t.hist)-1], true
}

func (t *Tracker) window() time.Duration {
	return max(2*t.interval, 2*time.Second)
}

// reset forgets everything: a restart or a mode switch makes the old
// counters, held speeds and peaks belong to another engine.
func (t *Tracker) reset() {
	*t = Tracker{interval: t.interval}
}

// restarted reports whether any counter went backwards, or the kind changed.
func restarted(prev, next model.Reading) bool {
	if prev.Kind != next.Kind {
		return true
	}
	a, b := prev.Counters(), next.Counters()
	for i := range a {
		if a[i].OK && b[i].OK && b[i].V < a[i].V {
			return true
		}
	}
	return false
}

// Add records a reading and recomputes the rates.
func (t *Tracker) Add(r model.Reading) {
	if n := len(t.hist); n > 0 && restarted(t.hist[n-1], r) {
		t.reset()
	}
	if n := len(t.hist); n > 0 && (r.PrefillTokens > t.hist[n-1].PrefillTokens || r.DecodeTokens > t.hist[n-1].DecodeTokens) {
		t.lastActive = r.At // the scrape in which tokens last moved
	}
	t.hist = append(t.hist, r)
	cut := r.At.Add(-LongWindow - t.interval)
	for len(t.hist) > 2 && t.hist[1].At.Before(cut) {
		t.hist = t.hist[1:]
	}
	t.compute()
}

// base finds the oldest reading within w of the latest, or, when only the
// latest is that recent, the one before it.
func (t *Tracker) base(w time.Duration) (model.Reading, bool) {
	n := len(t.hist)
	if n < 2 {
		return model.Reading{}, false
	}
	last := t.hist[n-1]
	from := last.At.Add(-w - t.interval/4)
	for i := 0; i < n-1; i++ {
		if !t.hist[i].At.Before(from) {
			return t.hist[i], true
		}
	}
	return t.hist[n-2], true
}

func delta(a, b model.Opt) (float64, bool) {
	if !a.OK || !b.OK {
		return 0, false
	}
	return b.V - a.V, true
}

func (t *Tracker) meanRunning(from time.Time) (float64, bool) {
	var sum float64
	var n int
	for _, h := range t.hist {
		if !h.At.Before(from) && h.Running.OK {
			sum += h.Running.V
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

func (t *Tracker) compute() {
	last := t.hist[len(t.hist)-1]
	prevRates := t.rates
	out := Rates{TTFT: t.ttft, Cache: t.cache, Spec: t.spec, PeakPrefill: prevRates.PeakPrefill, PeakDecode: prevRates.PeakDecode}

	// The slow figures start from the process totals so they show before the
	// long window has filled.
	if !out.TTFT.OK && last.FirstTokenCount.OK && last.FirstTokenCount.V > 0 {
		out.TTFT = model.Some(last.FirstTokenSeconds.V / last.FirstTokenCount.V)
	}
	if !out.Cache.OK && last.PromptTokens.OK && last.PromptTokens.V > 0 && last.CachedTokens.OK {
		out.Cache = model.Some(100 * last.CachedTokens.V / last.PromptTokens.V)
	}
	if !out.Spec.OK && last.SpecDrafted.OK && last.SpecDrafted.V > 0 && last.SpecAccepted.OK {
		out.Spec = model.Some(100 * last.SpecAccepted.V / last.SpecDrafted.V)
	}

	base, ok := t.base(t.window())
	if !ok || !last.At.After(base.At) {
		out.IdleSince = t.lastActive
		t.rates = out
		return
	}
	out.Ready = true
	dt := last.At.Sub(base.At).Seconds()
	dPre := last.PrefillTokens - base.PrefillTokens
	dDec := last.DecodeTokens - base.DecodeTokens
	run, haveRun := t.meanRunning(base.At)

	phase := func(dTok float64, seqA, seqB model.Opt, held *float64, have *bool) (Stream, model.Opt) {
		agg := model.Some(0)
		var s Stream
		if dTok > 0 {
			agg = model.Some(dTok / dt)
			if dSeq, ok := delta(seqA, seqB); ok && dSeq > minSeq {
				s = Stream{Value: dTok / dSeq, OK: true}
			} else if haveRun && run > 0 {
				s = Stream{Value: dTok / dt / run, OK: true, Approx: true}
			}
			if s.OK {
				*held, *have = s.Value, true
				return s, agg
			}
		}
		if *have {
			s = Stream{Value: *held, OK: true, Held: true}
		}
		return s, agg
	}
	out.Prefill, out.PrefillAgg = phase(dPre, base.PrefillSeqSeconds, last.PrefillSeqSeconds, &t.heldPre, &t.havePre)
	out.Decode, out.DecodeAgg = phase(dDec, base.DecodeSeqSeconds, last.DecodeSeqSeconds, &t.heldDec, &t.haveDec)

	out.Active = dPre > 0 || dDec > 0
	out.IdleSince = t.lastActive

	// Peaks decay with wall time, and rise with live (not held) values.
	if !t.peakAt.IsZero() {
		f := math.Pow(peakDecay, last.At.Sub(t.peakAt).Seconds())
		out.PeakPrefill *= f
		out.PeakDecode *= f
	}
	t.peakAt = last.At
	if out.Prefill.OK && !out.Prefill.Held {
		out.PeakPrefill = max(out.PeakPrefill, out.Prefill.Value)
	}
	if out.Decode.OK && !out.Decode.Held {
		out.PeakDecode = max(out.PeakDecode, out.Decode.Value)
	}

	if d, ok := delta(base.Busy, last.Busy); ok {
		out.Busy = model.Some(clamp(100*d/dt, 0, 100))
	}

	if lb, ok := t.base(LongWindow); ok && last.At.After(lb.At) {
		if dc, ok := delta(lb.FirstTokenCount, last.FirstTokenCount); ok && dc > 0 {
			if ds, ok := delta(lb.FirstTokenSeconds, last.FirstTokenSeconds); ok {
				t.ttft = model.Some(ds / dc)
				out.TTFT = t.ttft
			}
		}
		if dp, ok := delta(lb.PromptTokens, last.PromptTokens); ok && dp > 0 {
			if dc, ok := delta(lb.CachedTokens, last.CachedTokens); ok {
				t.cache = model.Some(clamp(100*dc/dp, 0, 100))
				out.Cache = t.cache
			}
		}
		if dd, ok := delta(lb.SpecDrafted, last.SpecDrafted); ok && dd > 0 {
			if da, ok := delta(lb.SpecAccepted, last.SpecAccepted); ok {
				t.spec = model.Some(clamp(100*da/dd, 0, 100))
				out.Spec = t.spec
			}
		}
	}
	t.rates = out
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }
