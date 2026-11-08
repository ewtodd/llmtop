package ui

import (
	"math"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/state"
)

// DemoSnapshot is synthetic, animated data for --demo and the screenshots: a
// pure function of the clock, so the same instant draws the same screen.
func DemoSnapshot(now time.Time) state.Snapshot {
	t := float64(now.UnixNano()) / 1e9
	wave := func(period, phase float64) float64 { return 0.5 + 0.5*math.Sin(2*math.Pi*t/period+phase) }
	some := model.Some

	glmDecode := 38 + 10*wave(7, 0)
	glmPrefill := 480 + 160*wave(11, 1)
	glm := state.EngineView{
		Name: "unholy-glm", Display: "GLM-5.3-Flash", Served: "glm5.3-flash", Kind: "unholy", Host: "son-of-anton",
		Addr: "10.0.0.5:8250", Mode: "glm", Up: true, Live: 6, Ready: true, Active: true,
		Slots: some(8), Running: some(6), Waiting: some(math.Floor(2 * wave(13, 2))), KV: some(0.45 + 0.4*wave(29, 0)),
		Context: some(786432),
		Prefill: state.StreamView{Value: some(glmPrefill)}, Decode: state.StreamView{Value: some(glmDecode)},
		PrefillAgg: some(glmPrefill * 1.4), DecodeAgg: some(glmDecode * 6),
		PeakPrefill: 700, PeakDecode: 52,
		Busy: some(88 + 8*wave(5, 0)), TTFT: some(1.2), Cache: some(88), Spec: some(84),
	}

	// The second engine works for twenty seconds, then idles for twenty.
	working := math.Mod(t, 40) < 20
	sage := state.EngineView{
		Name: "eklow", Display: "Sage A1.5 35B A3B", Served: "sage-a1.5", Kind: "eklow", Host: "fiona",
		Addr: "10.0.0.6:8260", Mode: "sage", Up: true, Ready: true, Active: working, Live: 0,
		Slots: some(2), Running: some(0), Waiting: some(0), KV: some(0.06), Context: some(262144),
		Prefill: state.StreamView{Value: some(910), Held: !working}, Decode: state.StreamView{Value: some(71.4), Held: !working},
		PrefillAgg: some(0), DecodeAgg: some(0), PeakPrefill: 1000, PeakDecode: 80,
		Busy: some(0), TTFT: some(0.4), Cache: some(61),
	}
	if working {
		sage.Live, sage.Running = 1, some(1)
		sage.Decode.Value = some(68 + 6*wave(3, 0))
		sage.PrefillAgg, sage.DecodeAgg = sage.Prefill.Value, sage.Decode.Value
		sage.Busy = some(95)
	} else {
		sage.IdleSec = some(math.Mod(t, 40) - 20)
	}

	embed := state.EngineView{
		Name: "embed", Display: "EmbeddingGemma 2", Kind: "health", Host: "fiona", Addr: "10.0.0.6:8262", Up: true, Tiny: true,
	}

	gpu := func(label, driver, pci string, busy, used, size float64, kind string, temps map[string]float64, power, capW, fan, clock float64) state.GPUView {
		return state.GPUView{Label: label, Driver: driver, PCI: pci, Busy: some(busy), MemKind: kind, MemUsed: some(used), MemSize: some(size),
			Temps: temps, Power: some(power), Cap: some(capW), Fan: some(fan), Clock: some(clock)}
	}
	const G = 1 << 30
	jitter := func(base, amp, period, phase float64) float64 { return base + amp*(wave(period, phase)-0.5) }
	r0 := gpu("R9700 #0", "amdgpu", "c7:00.0", jitter(97, 6, 3, 0), 30.8*G, 31.9*G, "vram",
		map[string]float64{"edge": jitter(66, 4, 9, 0), "junction": jitter(88, 6, 9, 0), "mem": 82}, jitter(222, 12, 5, 1), 230, 3112, 3395)
	r1 := gpu("R9700 #1", "amdgpu", "cc:00.0", jitter(99, 2, 4, 1), 30.7*G, 31.9*G, "vram",
		map[string]float64{"edge": 75, "junction": jitter(101, 3, 7, 1), "mem": 94}, jitter(228, 8, 4, 0), 230, 3867, 2709)
	igpu := gpu("Raphael", "amdgpu", "d0:00.0", jitter(8, 6, 6, 2), 114.5*G, 124*G, "gtt",
		map[string]float64{"edge": 62}, 14, 0, 0, 800)
	igpu.Cap, igpu.Fan = model.Opt{}, model.Opt{}
	xe := state.GPUView{Label: "Arc B580", Driver: "xe", PCI: "03:00.0", MemKind: "",
		Temps: map[string]float64{"pkg": 72, "vram": 76}, Power: some(jitter(60, 10, 6, 0))}

	snap := state.Snapshot{
		Time:    now,
		Maitred: state.MaitredView{URL: "http://10.0.0.2:4002", State: "ok", UpSec: 2*86400 + 3*3600 + t - math.Floor(t)},
		Engines: []state.EngineView{glm, sage, embed},
		Standby: []state.StandbyView{{Name: "unholy-ds4", Mode: "ds4"}, {Name: "gufo-deepseek", Mode: "dual"}},
		Hosts: []state.HostView{
			{Name: "son-of-anton", Modes: []string{"glm GLM-5.3-Flash"}, CPU: some(jitter(34, 20, 8, 0)), MemUsed: some(98 * G), MemTotal: some(123 * G),
				Load1: some(6.1), GPUs: []state.GPUView{r0, r1, igpu}},
			{Name: "fiona", Modes: []string{"sage"}, CPU: some(jitter(12, 6, 8, 1)), MemUsed: some(18 * G), MemTotal: some(31 * G),
				Load1: some(1.2), GPUs: []state.GPUView{xe}},
		},
	}

	keys := []struct {
		name         string
		live, req    int
		fail         int
		in, out, hit float64
	}{
		{"son-of-anton", 2, 214, 1, 812345, 40321, 97}, {"claude-code", 3, 96, 0, 12_400_000, 310_000, 92},
		{"home-assistant", 0, 12, 0, 48000, 2100, 40}, {"ci-bot", 0, 3, 2, 9000, 150, 0},
	}
	for i, k := range keys {
		snap.Callers = append(snap.Callers, state.CallerView{
			Name: k.name, Live: k.live, Requests: k.req * 3, Req10m: k.req, Fail10m: k.fail,
			PromptTokens: int64(k.in), OutTokens: int64(k.out), CachedTokens: int64(k.in * k.hit / 100),
			CachePct: some(k.hit), TTFTp50MS: some(600 + float64(i)*350), LastAgoSec: some(float64(3 + i*i*90)),
		})
	}
	names := []string{"claude-code", "son-of-anton", "claude-code", "home-assistant", "son-of-anton", "ci-bot", "claude-code", "son-of-anton"}
	for i, n := range names {
		f := state.FeedView{
			Start: now.Add(-time.Duration(3+i*17) * time.Second), Key: n, Requested: "auto", Model: "glm5.3-flash", Upstream: "unholy-glm",
			Status: 200, TTFTms: some(700 + float64(i*90)), DurationMS: some(9000 + float64(i*1300)), Done: true,
			HasUsage: true, In: int64(12000 + i*3100), Out: int64(300 + i*70), CachePct: some(91),
		}
		switch i {
		case 0:
			f.Done, f.DurationMS, f.HasUsage = false, model.Opt{}, false
			f.ElapsedMS = some(3000 + 1000*math.Mod(t, 60))
		case 3:
			f.Requested, f.Model, f.Upstream = "sage-a1.5", "sage-a1.5", "eklow"
		case 5:
			f.Status, f.TTFTms, f.DurationMS, f.HasUsage, f.Error = 404, model.Opt{}, some(1), false, "model_not_found"
			f.Requested, f.Model, f.Upstream = "nope", "nope", ""
		}
		snap.Feed = append(snap.Feed, f)
	}
	for i := 0; i < 6; i++ { // older entries, so tall windows fill
		f := snap.Feed[1+i%7]
		f.Start = f.Start.Add(-time.Duration(150+i*40) * time.Second)
		snap.Feed = append(snap.Feed, f)
	}
	return snap
}
