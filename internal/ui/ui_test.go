package ui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/prom"
	"github.com/ewtodd/llmtop/internal/source"
	"github.com/ewtodd/llmtop/internal/state"
)

var sizes = [][2]int{{115, 55}, {80, 24}, {200, 50}, {60, 20}, {40, 10}, {20, 5}, {1, 1}}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixtureSnapshot is a snapshot built from the recorded endpoint bodies.
func fixtureSnapshot(t *testing.T) state.Snapshot {
	t.Helper()
	var st model.Status
	if err := json.Unmarshal(read(t, "maitred-status.json"), &st); err != nil {
		t.Fatal(err)
	}
	s := state.New(state.Config{
		Maitred: "http://m", Interval: time.Second, NodePort: "9100",
		GPUNames: map[string]string{"son-of-anton/0000:c7:00.0": "R9700 #0", "son-of-anton/0000:cc:00.0": "R9700 #1"},
	})
	t0 := time.Unix(1_762_590_000, 0)
	s.Apply(source.StatusMsg{At: t0, Status: &st})
	for i := 0; i < 3; i++ {
		at := t0.Add(time.Duration(i) * time.Second)
		body := strings.Replace(string(read(t, "unholy-new.metrics")), "unholy_decode_tokens_total 15601", "unholy_decode_tokens_total "+string(rune('0'+i))+"5601", 1)
		r, _ := source.Normalize(prom.Parse([]byte(body)), at)
		r.DecodeTokens += float64(240 * i)
		r.DecodeSeqSeconds = model.Some(r.DecodeSeqSeconds.V + float64(6*i))
		s.Apply(source.EngineMsg{Name: "unholy-glm", At: at, Reading: r})
		s.Apply(source.EngineMsg{Name: "embed", At: at, Reading: model.Reading{At: at, Kind: model.KindHealth, Up: true}})
		for host, f := range map[string]string{"son-of-anton": "node-son-of-anton.prom", "fiona": "node-fiona.prom"} {
			s.Apply(source.NodeMsg{Host: host, At: at, Node: source.ParseNode(prom.Parse(read(t, f)), at)})
		}
	}
	return s.Snapshot(t0.Add(3 * time.Second))
}

func checkView(t *testing.T, name string, th *Theme, snap state.Snapshot, w, h int, want ...string) string {
	t.Helper()
	out := Render(th, snap, w, h, false)
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Errorf("%s %dx%d: %d lines", name, w, h, len(lines))
	}
	for i, l := range lines {
		if lw := lipgloss.Width(l); lw > w {
			t.Errorf("%s %dx%d: line %d is %d wide: %q", name, w, h, i, lw, l)
		}
	}
	plain := stripANSI(out)
	for _, s := range want {
		if !strings.Contains(plain, s) {
			t.Errorf("%s %dx%d: missing %q\n%s", name, w, h, s, plain)
		}
	}
	return plain
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			for i < len(s) && !(s[i] >= '@' && s[i] <= '~' && i > 0 && s[i] != '[') {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestRenderSizes(t *testing.T) {
	now := time.Unix(1_700_000_005, 0)
	cases := map[string]state.Snapshot{"demo": DemoSnapshot(now), "fixture": fixtureSnapshot(t)}
	keys := map[string][]string{
		"demo":    {"unholy-glm", "R9700 #0", "son-of-anton"},
		"fixture": {"unholy-glm", "R9700 #0", "son-of-anton"},
	}
	for name, snap := range cases {
		for _, th := range []*Theme{PlainTheme(), ColorTheme()} {
			for _, sz := range sizes {
				var want []string
				if sz[0] >= 60 && sz[1] >= 20 { // below that only the bounds are promised
					want = keys[name]
				}
				checkView(t, name, th, snap, sz[0], sz[1], want...)
			}
		}
	}
}

func TestFullCardsAtPortraitSize(t *testing.T) {
	out := checkView(t, "demo", PlainTheme(), DemoSnapshot(time.Unix(1_700_000_005, 0)), 115, 55,
		"PREFILL", "DECODE", "K/V", "prefill tok/s", "decode tok/s", "total pf", "╭─ unholy-glm GLM-5.3-Flash", "10.0.0.5:8250 unholy",
		"standby: unholy-ds4 (ds4), gufo-deepseek (dual)", "embed  up  live 0  (EmbeddingGemma 2)",
		"son-of-anton  cpu", "busy", "vram 30.8/31.9G", "gtt", "°C junc ", "/230W", "fan 3112", "3395MHz",
		"req/10m", "fail/10m", "ttft p50", "model_not_found", "auto -> glm5.3-flash")
	if n := strings.Count(out, "╭─"); n != 2 {
		t.Errorf("cards = %d", n)
	}
	if !strings.Contains(out, "▸") {
		t.Error("a streaming request shows a live elapsed counter")
	}
}

func TestSideBySideCardsWhenWide(t *testing.T) {
	out := checkView(t, "demo", PlainTheme(), DemoSnapshot(time.Unix(1_700_000_005, 0)), 200, 50)
	first := strings.Split(out, "\n")[1]
	if strings.Count(first, "╭") != 2 {
		t.Errorf("two cards on one row: %q", first)
	}
	narrow := checkView(t, "demo", PlainTheme(), DemoSnapshot(time.Unix(1_700_000_005, 0)), 199, 55)
	if strings.Count(strings.Split(narrow, "\n")[1], "╭") != 1 {
		t.Error("cards stack below two times 100 columns")
	}
}

func TestShortWindowsUseCompactCards(t *testing.T) {
	out := checkView(t, "demo", PlainTheme(), DemoSnapshot(time.Unix(1_700_000_005, 0)), 115, 24, "unholy-glm", "R9700 #0", "son-of-anton")
	if strings.Contains(out, "╭") {
		t.Error("24 rows cannot hold bordered cards plus the hardware")
	}
}

func TestHostileData(t *testing.T) {
	snap := DemoSnapshot(time.Unix(1_700_000_005, 0))
	long := strings.Repeat("界x", 80)
	snap.Engines[0].Name, snap.Engines[0].Display, snap.Engines[0].Addr = long, long, long
	snap.Engines[0].Decode.Value = model.Some(1e12)
	snap.Engines[0].Prefill.Value = model.Some(-5)
	snap.Engines[0].KV = model.Some(7)
	snap.Engines[1].Err, snap.Engines[1].Stale, snap.Engines[1].AgeSec = long, true, 1e7
	snap.Hosts[0].GPUs[0].Label = long
	snap.Hosts[0].Err = long
	snap.Callers[0].Name = long
	snap.Feed[1].Key, snap.Feed[1].Error = long, long
	snap.Maitred.State, snap.Maitred.Err, snap.Maitred.Stale = "unreachable", long, true
	for _, sz := range sizes {
		checkView(t, "hostile", ColorTheme(), snap, sz[0], sz[1])
	}
}

func TestEmptyAndConnectingSnapshots(t *testing.T) {
	for _, snap := range []state.Snapshot{
		{Time: time.Now()},
		state.New(state.Config{Interval: time.Second}).Snapshot(time.Now()),
	} {
		for _, sz := range sizes {
			var want []string
			if sz[0] >= 20 {
				want = []string{"llmtop"}
			}
			checkView(t, "empty", PlainTheme(), snap, sz[0], sz[1], want...)
		}
	}
}

func TestStaleAndIdleHints(t *testing.T) {
	snap := DemoSnapshot(time.Unix(1_700_000_030, 0)) // eklow idles from second 20 of 40
	e := &snap.Engines[1]
	e.Active, e.Stale, e.Err, e.AgeSec = false, true, "timeout", 12
	e.IdleSec = model.Some(240)
	out := checkView(t, "stale", PlainTheme(), snap, 115, 55, "idle 4m", "timeout, last data 12s ago")
	_ = out
	snap.Maitred = state.MaitredView{State: "unreachable", Err: "connection refused", Stale: true, AgeSec: 12}
	checkView(t, "unreachable", PlainTheme(), snap, 115, 55, "maitred unreachable (connection refused), data 12s old")
	snap.Maitred = state.MaitredView{State: "ok", Note: "maitred has no /status", Models: 10, Serving: 6}
	checkView(t, "nostatus", PlainTheme(), snap, 115, 55, "maitred has no /status")
	snap.Hosts[0].Modes, snap.Hosts[0].Switching = nil, true
	checkView(t, "switching", PlainTheme(), snap, 115, 55, "son-of-anton: switching")
	e.Prefill.Approx, e.Decode.Approx = true, true
	if out := checkView(t, "approx", PlainTheme(), snap, 115, 55); strings.Contains(out, "~") {
		t.Errorf("approx figures render without ~:\n%s", out)
	}
}

func TestBigNumberAndFormats(t *testing.T) {
	if got := bigNumber("40.8", numW); len(got[0]) == 0 || lipgloss.Width(got[4]) > numW {
		t.Fatalf("%v", got)
	}
	if headline(12345) != "9999" || headline(40.84) != "40.8" || headline(-3) != "0.0" {
		t.Fatalf("%s %s %s", headline(12345), headline(40.84), headline(-3))
	}
	for in, want := range map[float64]string{0.84: "840ms", 1.2: "1.2s", 38: "38s", 240: "4m", 3 * 3600: "3h", 2*86400 + 3*3600: "2d3h"} {
		if got := seconds(in); got != want {
			t.Errorf("seconds(%v) = %s want %s", in, got, want)
		}
	}
	if tokens(812345) != "812k" || tokens(1_200_000) != "1.2M" || tokens(40321) != "40k" || tokens(150) != "150" {
		t.Error("tokens")
	}
	if got := meterBar(0.5, 10, lipgloss.NewStyle(), lipgloss.NewStyle()); lipgloss.Width(got) != 10 {
		t.Errorf("meter width %d", lipgloss.Width(got))
	}
}

func TestParseOSCColorAndPalette(t *testing.T) {
	if parseOSCColor("\x1b]11;rgb:1e1e/2a2a/3636\x1b\\") != "#1e2a36" || parseOSCColor("junk") != "" || parseOSCColor("#ABCDEF") != "#abcdef" {
		t.Fatal("parseOSCColor")
	}
	if Palette("nope") != nil || Palette("#101820")["text-muted"] == "" {
		t.Fatal("palette")
	}
}

func TestModelKeysAndPolling(t *testing.T) {
	m := New(Options{Demo: true, Interval: time.Second, Theme: PlainTheme()})
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}}); cmd != nil || !m.paused {
		t.Fatal("p pauses")
	}
	if !strings.Contains(m.View(), "paused") {
		t.Fatal("paused shows in the header")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Fatal("q quits")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if lines := strings.Count(m.View(), "\n") + 1; lines > 24 {
		t.Fatal(lines)
	}

	// Real mode: a poll is a command, one per source, and a source already in
	// flight is not polled twice.
	st := state.New(state.Config{Maitred: "http://127.0.0.1:1", Interval: time.Second, NodePort: "9100"})
	m = New(Options{State: st, Interval: time.Second, Theme: PlainTheme(), Client: source.NewClient()})
	cmd := m.poll(true)
	if cmd == nil || !m.inflight["status"] {
		t.Fatal("status poll is a command")
	}
	if again := m.poll(true); again != nil {
		t.Fatal("a source in flight is not polled again")
	}
	m.Update(doneMsg{key: "status", msg: source.StatusMsg{At: time.Now(), Err: os.ErrDeadlineExceeded}})
}

func TestPlaceholderCard(t *testing.T) {
	snap := state.Snapshot{
		Time: time.Now(),
		Engines: []state.EngineView{
			{Name: "son-of-anton", Host: "son-of-anton", Pinned: true, Placeholder: true, Note: "switching: no mode serving"},
			{Name: "eklow", Host: "fiona", Display: "Sage A1.5", Pinned: true, Up: true, Kind: "eklow"},
		},
		Standby: []state.StandbyView{{Name: "unholy", Host: "son-of-anton", Mode: "ds4"}, {Name: "radiance", Host: "son-of-anton", Mode: "qwen"}},
	}
	for _, sz := range sizes {
		var want []string
		if sz[0] >= 60 && sz[1] >= 20 {
			want = []string{"son-of-anton", "switching: no mode serving", "standby: unholy (ds4), radiance (qwen)", "eklow"}
		}
		checkView(t, "placeholder", PlainTheme(), snap, sz[0], sz[1], want...)
	}
	full := Render(PlainTheme(), snap, 115, 55, false)
	if n := strings.Count(stripANSI(full), "╭─"); n != 2 {
		t.Fatalf("both pinned views are full cards at 115x55, got %d:\n%s", n, stripANSI(full))
	}
	lines := strings.Split(stripANSI(full), "\n")
	if !strings.Contains(lines[1], "son-of-anton") || !strings.Contains(lines[1+cardRows], "eklow") {
		t.Fatalf("the placeholder is as tall as a card:\n%s", stripANSI(full))
	}
}

// TestTempStyle checks the temperature colours: the card's own critical limit
// sets warn 20° under and bad 10° under it; a sensor without one keeps the
// usual 90/100 (95/105 for memory).
func TestTempStyle(t *testing.T) {
	v := &view{th: ColorTheme()}
	g := state.GPUView{Crit: map[string]float64{"edge": 110, "junction": 110, "mem": 108}}
	cases := []struct {
		g      state.GPUView
		sensor string
		c      float64
		band   string // ok, warn or err
	}{
		{g, "edge", 79, "ok"},
		{g, "edge", 90, "warn"}, // 110 - 20
		{g, "junction", 99, "warn"},
		{g, "junction", 101, "err"}, // 110 - 10
		{g, "mem", 87, "ok"},
		{g, "mem", 88, "warn"}, // 108 - 20
		{g, "mem", 99, "err"},  // 108 - 10
		{state.GPUView{}, "pkg", 71, "ok"},
		{state.GPUView{}, "pkg", 90, "warn"},
		{state.GPUView{}, "pkg", 101, "err"},
		{state.GPUView{}, "vram", 94, "ok"},
		{state.GPUView{}, "vram", 95, "warn"},
		{state.GPUView{}, "vram", 105, "err"},
		{state.GPUView{}, "edge", 89, "ok"}, // the usual band without a limit
	}
	bands := map[string]lipgloss.Style{"ok": v.th.OK, "warn": v.th.Warn, "err": v.th.Err}
	for _, c := range cases {
		if got := v.tempStyle(c.g, c.sensor, c.c).Render("42"); got != bands[c.band].Render("42") {
			t.Errorf("%s %.0f: band is not %s: %q", c.sensor, c.c, c.band, got)
		}
	}
}
