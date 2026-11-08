package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/state"
)

// view is one rendering of a snapshot at a size.
type view struct {
	th     *Theme
	snap   state.Snapshot
	w, h   int
	paused bool
}

// Render draws the snapshot in w x h: at most h lines, none wider than w.
func Render(th *Theme, snap state.Snapshot, w, h int, paused bool) string {
	if w < 1 || h < 1 {
		return ""
	}
	v := &view{th: th, snap: snap, w: w, h: h, paused: paused}
	lines := v.layout()
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = fit(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

// layout stacks the sections, top to bottom, spending rows on the upper ones
// first: header, engines (full cards when they fit, else two lines each),
// hosts, callers, feed.
func (v *view) layout() []string {
	lines := []string{v.header()}

	var big, tiny []state.EngineView
	for _, e := range v.snap.Engines {
		if e.Tiny {
			tiny = append(tiny, e)
		} else {
			big = append(big, e)
		}
	}
	hosts := v.hostLines()
	callersWant := 0
	if len(v.snap.Callers) > 0 {
		callersWant = 1 + len(v.snap.Callers)
	}
	feedMin := 0
	if len(v.snap.Feed) > 0 {
		feedMin = min(4, 1+len(v.snap.Feed))
	}
	callersMin := min(callersWant, 3)

	// The engines' base form is the compact one; cards upgrade while the lower
	// sections keep their minimum.
	tinyRows := len(tiny)
	if len(v.snap.Standby) > 0 {
		tinyRows++
	}
	cols := 1
	if v.w >= 2*100 {
		cols = 2
	}
	full := make([]bool, len(big))
	used := 1 + compactEn*len(big) + tinyRows + len(hosts)
	if v.w >= minCardW {
		for i := range big {
			cost := cardRows - compactEn
			if cols == 2 && i%2 == 1 {
				if !full[i-1] {
					break
				}
				cost = -compactEn // shares the rows of its left neighbour
			}
			if used+cost+callersMin+feedMin > v.h {
				break
			}
			full[i] = true
			used += cost
		}
	}
	lines = append(lines, v.engineLines(big, full, cols)...)
	nameW := 0
	for _, e := range tiny {
		nameW = max(nameW, lipgloss.Width(e.Name))
	}
	for _, e := range tiny {
		lines = append(lines, v.tiny(e, min(nameW, 18), v.w))
	}
	if len(v.snap.Standby) > 0 {
		lines = append(lines, v.standbyLine(v.snap.Standby, v.w))
	}
	lines = append(lines, hosts...)

	rest := v.h - len(lines)
	if callersWant > 0 && rest > 0 {
		give := min(callersWant, max(callersMin, rest-feedMin))
		callers := v.callerLines()
		if give < len(callers) {
			callers = callers[:give]
		}
		lines = append(lines, callers...)
		rest = v.h - len(lines)
	}
	if feedMin > 0 && rest > 1 {
		lines = append(lines, v.feedLines(rest)...)
	}
	return lines
}

// engineLines lays out the engines: full cards (side by side when the width
// allows two of at least 100 columns) and compact pairs of lines.
func (v *view) engineLines(big []state.EngineView, full []bool, cols int) []string {
	var out []string
	for i := 0; i < len(big); {
		if !full[i] {
			out = append(out, v.compact(big[i], v.w)...)
			i++
			continue
		}
		if cols == 2 && i+1 < len(big) && full[i+1] {
			lw := v.w / 2
			l, r := v.card(big[i], lw), v.card(big[i+1], v.w-lw)
			for k := range l {
				out = append(out, l[k]+r[k])
			}
			i += 2
			continue
		}
		out = append(out, v.card(big[i], v.w)...)
		i++
	}
	return out
}

func (v *view) header() string {
	th := v.th
	m := v.snap.Maitred
	parts := []string{th.Bold.Render("llmtop")}
	switch m.State {
	case "ok":
		if m.Note != "" {
			s := th.Warn.Render(m.Note)
			if m.Models > 0 {
				s += th.Mut.Render(fmt.Sprintf(" (%d/%d models serving)", m.Serving, m.Models))
			}
			parts = append(parts, s)
		} else {
			up := ""
			if m.UpSec > 0 {
				up = ", up " + seconds(m.UpSec)
			}
			parts = append(parts, "maitred "+th.OK.Render("ok")+up)
		}
	case "unreachable":
		s := th.Err.Render("maitred unreachable") + th.Mut.Render(" ("+m.Err+")")
		if m.Stale {
			s += th.Mut.Render(", data " + seconds(m.AgeSec) + " old")
		}
		parts = append(parts, s)
	default:
		parts = append(parts, th.Mut.Render("maitred connecting"))
	}
	for _, h := range v.snap.Hosts {
		switch {
		case h.Switching:
			parts = append(parts, h.Name+": "+th.Warn.Render("switching"))
		case len(h.Modes) > 0:
			parts = append(parts, h.Name+": "+strings.Join(h.Modes, "+"))
		}
	}
	left := strings.Join(parts, "  ")
	if v.paused {
		left += "  " + th.Warn.Render("paused")
	}
	clock := th.Mut.Render(v.snap.Time.Format("15:04:05"))
	if lipgloss.Width(left)+2+lipgloss.Width(clock) <= v.w {
		return left + strings.Repeat(" ", v.w-lipgloss.Width(left)-lipgloss.Width(clock)) + clock
	}
	return fit(left, v.w)
}

// ---- hosts and GPUs

// tempStyle colours a temperature by its sensor's usual limits: edge, junction
// and package warn at 90 and alarm at 100; memory and VRAM at 95 and 105.
func (v *view) tempStyle(sensor string, c float64) lipgloss.Style {
	warn, bad := 90.0, 100.0
	if sensor == "mem" || sensor == "vram" {
		warn, bad = 95, 105
	}
	switch {
	case c >= bad:
		return v.th.Err
	case c >= warn:
		return v.th.Warn
	}
	return v.th.Plain
}

var tempOrder = []struct{ key, short string }{
	{"edge", "edge"}, {"junction", "junc"}, {"mem", "mem"}, {"pkg", "pkg"}, {"vram", "vram"},
}

func (v *view) temps(g state.GPUView) string {
	var parts []string
	for _, t := range tempOrder {
		if c, ok := g.Temps[t.key]; ok {
			parts = append(parts, v.th.Mut.Render(t.short)+" "+v.tempStyle(t.key, c).Render(fmt.Sprintf("%.0f", c)))
		}
	}
	if len(parts) == 0 {
		return v.th.Mut.Render("-")
	}
	return strings.Join(parts, " ")
}

func (v *view) hostLines() []string {
	var out []string
	labelW, tempW, memW := 0, 0, 0
	for _, h := range v.snap.Hosts {
		for _, g := range h.GPUs {
			labelW = max(labelW, lipgloss.Width(g.Label))
			tempW = max(tempW, lipgloss.Width(v.temps(g)))
			memW = max(memW, lipgloss.Width(v.gpuMem(g)))
		}
	}
	labelW = min(labelW, 16)
	barW := 10
	if v.w < 110 {
		barW = 6
	}
	for _, h := range v.snap.Hosts {
		if h.NoNode {
			continue
		}
		out = append(out, v.hostLine(h))
		for _, g := range h.GPUs {
			out = append(out, v.gpuLine(g, h.Stale, labelW, barW, memW, tempW))
		}
	}
	return out
}

func (v *view) hostLine(h state.HostView) string {
	th := v.th
	line := th.Bold.Render(h.Name)
	dim := h.Stale
	val := func(s string) string {
		if dim {
			return th.Mut.Render(s)
		}
		return s
	}
	switch {
	case h.Err == "connecting", h.CPU.OK == false && h.MemTotal.OK == false && h.Err == "":
		line += "  " + th.Mut.Render("waiting for node_exporter")
	default:
		if h.CPU.OK {
			line += "  " + th.Mut.Render("cpu") + " " + val(pct(h.CPU.V))
		}
		if h.MemUsed.OK {
			line += "  " + th.Mut.Render("mem") + " " + val(gibPair(h.MemUsed.V, h.MemTotal.V))
		}
		if h.Load1.OK {
			line += "  " + th.Mut.Render("load") + " " + val(fmt.Sprintf("%.1f", h.Load1.V))
		}
	}
	if h.Err != "" {
		msg := "node: " + h.Err
		if h.Stale {
			msg += ", data " + seconds(h.AgeSec) + " old"
		}
		line += "  " + th.Err.Render(msg)
	}
	return line
}

func (v *view) gpuMem(g state.GPUView) string {
	if !g.MemSize.OK || !g.MemUsed.OK {
		return v.th.Mut.Render("-")
	}
	return v.th.Mut.Render(g.MemKind) + " " + fmt.Sprintf("%s/%s", strings.TrimSuffix(gib(g.MemUsed.V), "G"), gib(g.MemSize.V))
}

func (v *view) gpuLine(g state.GPUView, stale bool, labelW, barW, memW, tempW int) string {
	th := v.th
	dim := func(s string) string {
		if stale {
			return th.Mut.Render(s)
		}
		return s
	}
	line := "  " + th.Bold.Render(padRight(g.Label, labelW)) + "  "
	if g.Busy.OK {
		line += th.Mut.Render("busy") + " " + meterBar(g.Busy.V/100, barW, th.Dec, th.Track) + " " + padLeft(pct(g.Busy.V), 4)
	} else {
		line += th.Mut.Render("busy") + " " + padRight(th.Mut.Render("-"), barW+5)
	}
	line += "  " + padRight(v.gpuMem(g), memW) + "  " + padRight(v.temps(g), tempW)
	switch {
	case g.Power.OK && g.Cap.OK:
		line += "  " + padLeft(dim(fmt.Sprintf("%.0f/%.0fW", g.Power.V, g.Cap.V)), 8)
	case g.Power.OK:
		line += "  " + padLeft(dim(fmt.Sprintf("%.0fW", g.Power.V)), 8)
	default:
		line += "  " + padLeft(th.Mut.Render("-"), 8)
	}
	if g.Fan.OK {
		line += "  " + th.Mut.Render("fan") + " " + padLeft(dim(fmt.Sprintf("%.0f", g.Fan.V)), 4)
	}
	if g.Clock.OK {
		line += "  " + dim(fmt.Sprintf("%.0fMHz", g.Clock.V))
	}
	return line
}

// ---- callers

func (v *view) callerLines() []string {
	th := v.th
	gap := "  "
	if v.w < 100 {
		gap = " "
	}
	nameW := 6
	for _, c := range v.snap.Callers {
		nameW = max(nameW, lipgloss.Width(c.Name))
	}
	nameW = min(nameW, 18)
	cols := []struct {
		title string
		w     int
	}{{"live", 4}, {"req/10m", 7}, {"fail/10m", 8}, {"in-tok", 7}, {"out-tok", 7}, {"cache%", 6}, {"ttft p50", 8}, {"last", 6}}
	head := padRight("key", nameW)
	for _, c := range cols {
		head += gap + padLeft(c.title, c.w)
	}
	out := []string{th.Mut.Render(head)}
	for _, c := range v.snap.Callers {
		cell := func(i int, s string) string { return gap + padLeft(s, cols[i].w) }
		line := th.Bold.Render(padRight(c.Name, nameW))
		live := fmt.Sprintf("%d", c.Live)
		if c.Live > 0 {
			line += gap + padLeft(th.Dec.Render(live), cols[0].w)
		} else {
			line += gap + padLeft(th.Mut.Render(live), cols[0].w)
		}
		line += cell(1, fmt.Sprintf("%d", c.Req10m))
		if c.Fail10m > 0 {
			line += gap + padLeft(th.Err.Render(fmt.Sprintf("%d", c.Fail10m)), cols[2].w)
		} else {
			line += gap + padLeft(th.Mut.Render("0"), cols[2].w)
		}
		line += cell(3, tokens(float64(c.PromptTokens)))
		line += cell(4, tokens(float64(c.OutTokens)))
		line += cell(5, optText(c.CachePct, func(x float64) string { return pct(x) }))
		line += cell(6, optText(c.TTFTp50MS, ms))
		line += cell(7, optText(c.LastAgoSec, ago))
		out = append(out, line)
	}
	return out
}

func optText(o model.Opt, f func(float64) string) string {
	if !o.OK {
		return "-"
	}
	return f(o.V)
}

// ---- feed

// feedLines draws the feed header and as many requests as fit in rows lines.
func (v *view) feedLines(rows int) []string {
	th := v.th
	keyW, routeW := 4, 8
	for _, f := range v.snap.Feed {
		keyW = max(keyW, lipgloss.Width(f.Key))
		routeW = max(routeW, lipgloss.Width(route(f)))
	}
	keyW, routeW = min(keyW, 14), min(routeW, 34)
	head := fmt.Sprintf("%-8s  %s  %s  %s  %s  %s  %s", "time", padRight("key", keyW), padRight("requested -> served", routeW),
		"st ", padLeft("ttft", 6), padLeft("dur", 6), "tokens in -> out")
	out := []string{th.Mut.Render(head)}
	for _, f := range v.snap.Feed {
		if len(out) >= rows {
			break
		}
		out = append(out, v.feedLine(f, keyW, routeW))
	}
	return out
}

func route(f state.FeedView) string {
	if f.Upstream == "" && f.Model != "" && f.Requested == f.Model {
		return f.Requested
	}
	if f.Requested == f.Model || f.Model == "" {
		return f.Requested
	}
	return f.Requested + " -> " + f.Model
}

func (v *view) statusText(f state.FeedView) string {
	st := fmt.Sprintf("%3d", f.Status)
	switch {
	case f.Status == 0:
		return v.th.Mut.Render(" - ")
	case f.Status >= 500:
		return v.th.Err.Render(st)
	case f.Status >= 400:
		return v.th.Warn.Render(st)
	}
	return v.th.OK.Render(st)
}

func (v *view) feedLine(f state.FeedView, keyW, routeW int) string {
	th := v.th
	dur := th.Mut.Render(padLeft("-", 6))
	switch {
	case f.DurationMS.OK:
		dur = padLeft(ms(f.DurationMS.V), 6)
	case f.ElapsedMS.OK:
		dur = padLeft(th.Dec.Render("▸"+ms(f.ElapsedMS.V)), 6) // still streaming
	}
	ttft := th.Mut.Render(padLeft("-", 6))
	if f.TTFTms.OK {
		ttft = padLeft(ms(f.TTFTms.V), 6)
	}
	tok := th.Mut.Render("-")
	if f.HasUsage {
		tok = fmt.Sprintf("%s -> %s", tokens(float64(f.In)), tokens(float64(f.Out)))
		if f.CachePct.OK {
			tok += th.Mut.Render(fmt.Sprintf(" (%s)", pct(f.CachePct.V)))
		}
	}
	line := th.Mut.Render(f.Start.Format("15:04:05")) + "  " + padRight(f.Key, keyW) + "  " + padRight(route(f), routeW) + "  " +
		v.statusText(f) + "  " + ttft + "  " + dur + "  " + tok
	if f.Error != "" {
		line += "  " + th.Err.Render(f.Error)
	}
	return line
}
