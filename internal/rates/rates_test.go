package rates

import (
	"math"
	"testing"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
)

var t0 = time.Unix(1_700_000_000, 0)

func at(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }

func near(a, b, eps float64) bool { return math.Abs(a-b) <= eps }

// seq is a unholy-shaped reading with sequence-seconds.
func seq(sec, pre, dec, preSeq, decSeq, running float64) model.Reading {
	return model.Reading{
		At: at(sec), Kind: model.KindUnholy, Up: true,
		PrefillTokens: pre, DecodeTokens: dec,
		PrefillSeqSeconds: model.Some(preSeq), DecodeSeqSeconds: model.Some(decSeq),
		Running: model.Some(running),
	}
}

func TestPerStreamFromSequenceSeconds(t *testing.T) {
	tr := NewTracker(time.Second)
	// 6 streams, each decoding 40 tok/s: 240 tok/s aggregate, 6 sequence-seconds per second.
	for i := 0; i <= 3; i++ {
		s := float64(i)
		tr.Add(seq(s, 1000+500*s, 240*s, 2*s, 6*s, 6))
	}
	r := tr.Rates()
	if !r.Ready || !r.Active {
		t.Fatalf("%+v", r)
	}
	if !r.Decode.OK || r.Decode.Approx || r.Decode.Held || !near(r.Decode.Value, 40, 1e-9) {
		t.Fatalf("decode per-stream %+v", r.Decode)
	}
	if !near(r.DecodeAgg.V, 240, 1e-9) {
		t.Fatalf("aggregate %+v", r.DecodeAgg)
	}
	if !near(r.Prefill.Value, 250, 1e-9) || !near(r.PrefillAgg.V, 500, 1e-9) {
		t.Fatalf("prefill %+v agg %+v", r.Prefill, r.PrefillAgg)
	}
	if r.PeakDecode < 40 {
		t.Fatalf("peak %v", r.PeakDecode)
	}
}

func TestWindowIsTwoIntervals(t *testing.T) {
	tr := NewTracker(time.Second)
	// Speed changes: 100 tok/s for the first seconds, then 10. The 2 s window
	// must reflect the last two seconds only.
	d, s := 0.0, 0.0
	for i := 0; i <= 10; i++ {
		tr.Add(seq(float64(i), 0, d, 0, s, 1))
		rate := 100.0
		if i >= 5 {
			rate = 10
		}
		d += rate
		s += 1
	}
	if v := tr.Rates().Decode.Value; !near(v, 10, 1e-9) {
		t.Fatalf("decode = %v", v)
	}
}

func TestApproxFallbackWithoutSequenceSeconds(t *testing.T) {
	tr := NewTracker(time.Second)
	for i := 0; i <= 3; i++ {
		s := float64(i)
		tr.Add(model.Reading{At: at(s), Kind: model.KindRadiance, DecodeTokens: 300 * s, Running: model.Some(3)})
	}
	r := tr.Rates()
	if !r.Decode.OK || !r.Decode.Approx || !near(r.Decode.Value, 100, 1e-9) || !near(r.DecodeAgg.V, 300, 1e-9) {
		t.Fatalf("%+v agg %+v", r.Decode, r.DecodeAgg)
	}
	// Tokens but nothing running at all: no per-stream number can be made up.
	tr = NewTracker(time.Second)
	tr.Add(model.Reading{At: at(0), Kind: model.KindRadiance, DecodeTokens: 0, Running: model.Some(0)})
	tr.Add(model.Reading{At: at(1), Kind: model.KindRadiance, DecodeTokens: 50, Running: model.Some(0)})
	if r := tr.Rates(); r.Decode.OK || !near(r.DecodeAgg.V, 50, 1e-9) {
		t.Fatalf("%+v", r)
	}
}

func TestStalledSequenceSecondsFallBack(t *testing.T) {
	// vllm: the per-request times advance at completion; a window in which they
	// stand still falls back to the running mean.
	tr := NewTracker(time.Second)
	for i := 0; i <= 3; i++ {
		s := float64(i)
		tr.Add(model.Reading{At: at(s), Kind: model.KindVLLM, DecodeTokens: 80 * s,
			DecodeSeqSeconds: model.Some(5), Running: model.Some(2)})
	}
	if r := tr.Rates(); !r.Decode.Approx || !near(r.Decode.Value, 40, 1e-9) {
		t.Fatalf("%+v", r.Decode)
	}
}

func TestCounterResetDiscardsWindow(t *testing.T) {
	tr := NewTracker(time.Second)
	for i := 0; i <= 3; i++ {
		s := float64(i)
		tr.Add(seq(s, 0, 1000+240*s, 0, 6*s, 6))
	}
	// Restart: the counters are small again.
	tr.Add(seq(4, 10, 20, 0.5, 0.5, 1))
	r := tr.Rates()
	if r.Ready || r.PeakDecode != 0 || !r.IdleSince.IsZero() {
		t.Fatalf("no rate until two readings exist again: %+v", r)
	}
	// Held speeds may only come from the new process: its own totals, 20 / 0.5 and 10 / 0.5.
	if !r.Decode.Held || r.Decode.Value != 40 || !r.Prefill.Held || r.Prefill.Value != 20 {
		t.Fatalf("held speeds from the old process: %+v", r)
	}
	tr.Add(seq(5, 10, 60, 0.5, 1.5, 1))
	r = tr.Rates()
	if !r.Ready || !near(r.Decode.Value, 40, 1e-9) || r.Decode.Value > 1e6 {
		t.Fatalf("%+v", r)
	}
}

func TestKindChangeResets(t *testing.T) {
	tr := NewTracker(time.Second)
	tr.Add(seq(0, 0, 0, 0, 0, 1))
	tr.Add(seq(1, 0, 40, 0, 1, 1))
	other := model.Reading{At: at(2), Kind: model.KindLlamaCpp, DecodeTokens: 5000000}
	tr.Add(other)
	if tr.Rates().Ready {
		t.Fatal("a mode switch starts a new window even when the counters happen to grow")
	}
}

func TestIdleHoldsLastActiveValues(t *testing.T) {
	tr := NewTracker(time.Second)
	for i := 0; i <= 3; i++ {
		s := float64(i)
		tr.Add(seq(s, 0, 240*s, 0, 6*s, 6))
	}
	// Quiet from here on.
	for i := 4; i <= 8; i++ {
		tr.Add(seq(float64(i), 0, 720, 0, 18, 0))
	}
	r := tr.Rates()
	if r.Active || !r.Decode.OK || !r.Decode.Held || !near(r.Decode.Value, 40, 1e-9) {
		t.Fatalf("held: %+v active=%v", r.Decode, r.Active)
	}
	if r.DecodeAgg.V != 0 {
		t.Fatalf("aggregate is live, not held: %+v", r.DecodeAgg)
	}
	if got := at(8).Sub(r.IdleSince); got != 5*time.Second {
		t.Fatalf("idle for %v", got)
	}
	// Active again: live values.
	tr.Add(seq(9, 0, 720+20, 0, 18+1, 1))
	tr.Add(seq(10, 0, 720+40, 0, 18+2, 1))
	r = tr.Rates()
	if !r.Active || r.Decode.Held || !near(r.Decode.Value, 20, 1e-9) {
		t.Fatalf("%+v", r)
	}
}

func TestNeverActiveHoldsTheProcessAverage(t *testing.T) {
	tr := NewTracker(time.Second)
	// Idle since llmtop started: 300 prefill tokens over 2 sequence-seconds, 80 decoded over 4.
	tr.Add(seq(0, 300, 80, 2, 4, 0))
	r := tr.Rates()
	if r.Ready || !r.Prefill.Held || r.Prefill.Value != 150 || !r.Decode.Held || r.Decode.Value != 20 {
		t.Fatalf("first reading: %+v", r)
	}
	tr.Add(seq(1, 300, 80, 2, 4, 0))
	r = tr.Rates()
	if r.Active || !r.IdleSince.IsZero() || !r.Decode.Held || r.Decode.Value != 20 || r.PeakDecode != 0 {
		t.Fatalf("idle: %+v", r)
	}
	// Live values replace the averages and feed the peaks.
	tr.Add(seq(2, 300, 120, 2, 5, 1))
	r = tr.Rates()
	if !r.Active || r.Decode.Held || r.Decode.Value != 40 || r.PeakDecode != 40 {
		t.Fatalf("active: %+v", r)
	}
}

func TestFreshProcessHasNoNumbers(t *testing.T) {
	tr := NewTracker(time.Second)
	tr.Add(seq(0, 0, 0, 0, 0, 0))
	tr.Add(seq(1, 0, 0, 0, 0, 0))
	if r := tr.Rates(); r.Decode.OK || r.Prefill.OK {
		t.Fatalf("%+v", r)
	}
}

func TestTTFTCacheSpecBusy(t *testing.T) {
	tr := NewTracker(time.Second)
	for i := 0; i <= 40; i++ {
		s := float64(i)
		r := seq(s, 100*s, 10*s, s, s, 1)
		r.FirstTokenSeconds = model.Some(2 * s)
		r.FirstTokenCount = model.Some(s)
		r.PromptTokens = model.Some(1000 * s)
		r.CachedTokens = model.Some(900 * s)
		r.SpecDrafted = model.Some(100 * s)
		r.SpecAccepted = model.Some(80 * s)
		r.Busy = model.Some(0.5 * s)
		tr.Add(r)
	}
	r := tr.Rates()
	if !near(r.TTFT.V, 2, 1e-9) || !near(r.Cache.V, 90, 1e-9) || !near(r.Spec.V, 80, 1e-9) || !near(r.Busy.V, 50, 1e-9) {
		t.Fatalf("ttft %+v cache %+v spec %+v busy %+v", r.TTFT, r.Cache, r.Spec, r.Busy)
	}
	// No first tokens in the window: the last value stays.
	last, _ := tr.Last()
	for i := 1; i <= 5; i++ {
		q := last
		q.At = at(40 + float64(i))
		tr.Add(q)
	}
	if !near(tr.Rates().TTFT.V, 2, 1e-9) {
		t.Fatalf("ttft must hold: %+v", tr.Rates().TTFT)
	}
}

func TestSlowFiguresStartFromTotals(t *testing.T) {
	tr := NewTracker(time.Second)
	r := seq(0, 0, 0, 0, 0, 0)
	r.FirstTokenSeconds, r.FirstTokenCount = model.Some(30), model.Some(10)
	r.PromptTokens, r.CachedTokens = model.Some(100), model.Some(75)
	tr.Add(r)
	if got := tr.Rates(); !near(got.TTFT.V, 3, 1e-9) || !near(got.Cache.V, 75, 1e-9) {
		t.Fatalf("%+v", got)
	}
}

func TestNodeCPUAndXePower(t *testing.T) {
	tr := NewNodeTracker(time.Second)
	mk := func(s, idle, total, joules float64) model.Node {
		return model.Node{At: at(s), CPUIdle: idle, CPUTotal: total,
			GPUs: []model.GPU{{Chip: "x", Energy: model.Some(joules)}}}
	}
	tr.Add(mk(0, 100, 200, 1000))
	r := tr.Add(mk(1, 130, 300, 1050))
	if !r.CPU.OK || !near(r.CPU.V, 70, 1e-9) {
		t.Fatalf("cpu %+v", r.CPU)
	}
	if !near(r.Power["x"], 50, 1e-9) {
		t.Fatalf("power %v", r.Power)
	}
	// An energy counter that restarts gives no power, never a negative one.
	r = tr.Add(mk(2, 160, 400, 5))
	if _, ok := r.Power["x"]; ok && r.Power["x"] < 0 {
		t.Fatalf("negative power %v", r.Power)
	}
	if first := NewNodeTracker(time.Second).Add(mk(0, 1, 2, 3)); first.CPU.OK || len(first.Power) != 0 {
		t.Fatal("no rates from one scrape")
	}
}
