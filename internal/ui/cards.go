package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/state"
)

// Full card geometry: two big numbers on the left, the meters on the right.
const (
	numW      = 16 // widest big number: four digits
	numGap    = 3
	leftW     = 1 + numW + numGap + numW
	rightGap  = 3
	cardRows  = 10 // top border, 6 content rows, stats, totals, bottom border
	compactEn = 2
	minCardW  = 70
)

// engineStyles are the colours of one engine: all muted when its data is stale.
type engineStyles struct {
	pre, dec, val, lab lipgloss.Style
	dim                bool
}

func (v *view) engineStyles(e state.EngineView) engineStyles {
	if e.Stale || !e.Up && e.Err != "" {
		return engineStyles{v.th.Mut, v.th.Mut, v.th.Mut, v.th.Mut, true}
	}
	return engineStyles{v.th.Pre, v.th.Dec, v.th.Plain, v.th.Mut, false}
}

// kvStyle colours K/V use: green below 70%, yellow below 90%, red above.
func (v *view) kvStyle(frac float64) lipgloss.Style {
	switch {
	case frac >= 0.9:
		return v.th.Err
	case frac >= 0.7:
		return v.th.Warn
	}
	return v.th.OK
}

// streamText is a per-stream figure for a meter: "250.0/s", "~12.0/s" or "-".
func streamText(s state.StreamView) string {
	if !s.Value.OK {
		return "-"
	}
	t := fmt.Sprintf("%.1f/s", s.Value.V)
	if s.Approx {
		t = "~" + t
	}
	return t
}

func (v *view) streamMeter(label string, s state.StreamView, peak float64, st, base lipgloss.Style, width int) string {
	fill := st
	if s.Held {
		fill = v.th.Mut
	}
	scale := max(peak, 1)
	frac := 0.0
	if s.Value.OK {
		scale = max(scale, s.Value.V)
		frac = s.Value.V / scale
	}
	text := streamText(s)
	barW := width - lipgloss.Width(label) - 1 - 1 - max(lipgloss.Width(text), 8)
	if barW < 3 {
		return fit(base.Render(label)+" "+text, width)
	}
	return base.Render(label) + " " + meterBar(frac, barW, fill, v.th.Track) + " " + padLeft(text, max(lipgloss.Width(text), 8))
}

func (v *view) kvMeter(label string, e state.EngineView, base lipgloss.Style, width int) string {
	if !e.KV.OK {
		return base.Render(label) + " " + v.th.Mut.Render("-")
	}
	text := pct(100 * e.KV.V)
	barW := width - lipgloss.Width(label) - 1 - 1 - max(len(text), 8)
	if barW < 3 {
		return fit(base.Render(label)+" "+text, width)
	}
	st := v.kvStyle(e.KV.V)
	if e.Stale {
		st = v.th.Mut
	}
	return base.Render(label) + " " + meterBar(e.KV.V, barW, st, v.th.Track) + " " + padLeft(text, max(len(text), 8))
}

// stats is the stats line: run, wait, live, busy, ttft, spec, cache.
func (v *view) stats(e state.EngineView, s engineStyles) string {
	var parts []string
	add := func(label, value string) { parts = append(parts, s.lab.Render(label)+" "+s.val.Render(value)) }
	switch {
	case e.Running.OK && e.Slots.OK:
		add("run", fmt.Sprintf("%.0f/%.0f", e.Running.V, e.Slots.V))
	case e.Running.OK:
		add("run", fmt.Sprintf("%.0f", e.Running.V))
	}
	if e.Waiting.OK {
		if e.Waiting.V > 0 && !s.dim {
			parts = append(parts, s.lab.Render("wait")+" "+v.th.Warn.Render(fmt.Sprintf("%.0f", e.Waiting.V)))
		} else {
			add("wait", fmt.Sprintf("%.0f", e.Waiting.V))
		}
	}
	add("live", fmt.Sprintf("%d", e.Live))
	if e.Busy.OK {
		add("busy", pct(e.Busy.V))
	}
	if e.TTFT.OK {
		add("ttft", seconds(e.TTFT.V))
	}
	if e.Spec.OK {
		add("spec", pct(e.Spec.V))
	}
	if e.Cache.OK {
		add("cache", pct(e.Cache.V))
	}
	return strings.Join(parts, "  ")
}

// totals is the aggregate line.
func (v *view) totals(e state.EngineView, s engineStyles) string {
	pf, dc := 0.0, 0.0
	if e.PrefillAgg.OK {
		pf = e.PrefillAgg.V
	}
	if e.DecodeAgg.OK {
		dc = e.DecodeAgg.V
	}
	out := s.lab.Render("total") + " " + s.pre.Render("pf") + " " + s.val.Render(rate(pf)) + " " +
		s.dec.Render("dec") + " " + s.val.Render(rate(dc)) + " " + s.lab.Render("tok/s")
	if e.DeviceLost.OK && e.DeviceLost.V > 0 {
		out += "  " + v.th.Err.Render(fmt.Sprintf("device lost %.0f", e.DeviceLost.V))
	}
	return out
}

// idleText is "idle 4m" beside the held numbers, "" while active.
func idleText(e state.EngineView) string {
	if e.Active || !e.Ready {
		return ""
	}
	if e.IdleSec.OK {
		return "idle " + seconds(e.IdleSec.V)
	}
	return "idle"
}

func errText(e state.EngineView) string {
	if e.Err == "" {
		return ""
	}
	t := e.Err
	if e.Stale {
		t += ", last data " + seconds(e.AgeSec) + " ago"
	}
	return t
}

func (v *view) bigValue(s state.StreamView, st lipgloss.Style, dim bool) [bigRows]string {
	text := ""
	style := st
	if s.Value.OK {
		text = headline(s.Value.V)
		if s.Approx {
			text = "~" + text
		}
	}
	if !s.Value.OK || s.Held || dim {
		style = v.th.Mut
	}
	rows := bigNumber(text, numW)
	for i := range rows {
		rows[i] = style.Render(rows[i])
	}
	return rows
}

// card renders a full engine card exactly cw columns wide and cardRows high.
func (v *view) card(e state.EngineView, cw int) []string {
	if e.Placeholder {
		return v.placeholderCard(e, cw)
	}
	s := v.engineStyles(e)
	inner := cw - 2
	b := v.th.Border

	// Top border: title on the left, address and kind on the right.
	title := " " + v.th.Bold.Render(e.Name)
	if e.Display != "" {
		title += " " + s.lab.Render(e.Display)
	}
	title += " "
	right := ""
	if e.Addr != "" {
		right = " " + e.Addr
		if e.Kind != "" && e.Kind != model.KindHealth {
			right += " " + e.Kind
		}
		right += " "
		right = s.lab.Render(right)
	}
	if lipgloss.Width(title)+lipgloss.Width(right)+2 > inner {
		right = ""
	}
	title = fit(title, inner-2)
	fill := max(inner-lipgloss.Width(title)-lipgloss.Width(right)-1, 0)
	top := b.Render("╭─") + title + b.Render(strings.Repeat("─", max(fill-1, 0))) + right + b.Render("─╮")
	top = fit(top, cw)

	row := func(content string) string {
		return b.Render("│") + padRight(content, inner) + b.Render("│")
	}
	rightW := inner - (1 + leftW + rightGap) - 1
	pre := v.bigValue(e.Prefill, s.pre, e.Stale)
	dec := v.bigValue(e.Decode, s.dec, e.Stale)

	panel := [bigRows + 1]string{}
	if rightW >= 24 {
		panel[0] = v.streamMeter("PREFILL", e.Prefill, e.PeakPrefill, s.pre, s.pre, rightW)
		panel[1] = v.streamMeter("DECODE ", e.Decode, e.PeakDecode, s.dec, s.dec, rightW)
		panel[2] = v.kvMeter("K/V    ", e, s.lab, rightW)
		var info []string
		if e.Slots.OK {
			info = append(info, fmt.Sprintf("slots %.0f", e.Slots.V))
		}
		if e.Context.OK {
			info = append(info, "ctx "+tokens(e.Context.V))
		}
		if e.Served != "" && e.Served != e.Display {
			info = append(info, e.Served)
		}
		panel[3] = s.lab.Render(strings.Join(info, "  "))
		if t := errText(e); t != "" {
			panel[4] = v.th.Err.Render("down: " + t)
		}
		if t := idleText(e); t != "" {
			panel[5] = s.lab.Render(t)
		}
	}

	pad := strings.Repeat(" ", numGap)
	lines := []string{top}
	for i := 0; i < bigRows; i++ {
		left := " " + padRight(pre[i], numW) + pad + padRight(dec[i], numW)
		lines = append(lines, row(padRight(left, 1+leftW-1)+strings.Repeat(" ", rightGap)+panel[i]))
	}
	labels := " " + padRight(s.pre.Render("prefill tok/s"), numW) + pad + padRight(s.dec.Render("decode tok/s"), numW)
	if e.Decode.Approx || e.Prefill.Approx {
		labels = " " + padRight(s.pre.Render("prefill tok/s ~"), numW) + pad + padRight(s.dec.Render("decode tok/s ~"), numW)
	}
	lines = append(lines, row(padRight(labels, leftW)+strings.Repeat(" ", rightGap)+panel[bigRows]))
	lines = append(lines, row(" "+v.stats(e, s)))
	lines = append(lines, row(" "+v.totals(e, s)))
	lines = append(lines, b.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return lines
}

// placeholderCard is a pinned host's card while nothing serves there: the size of a full card, so
// the screen does not move when the next engine comes up.
func (v *view) placeholderCard(e state.EngineView, cw int) []string {
	inner := cw - 2
	b := v.th.Border
	title := fit(" "+v.th.Bold.Render(e.Name)+" ", inner-2)
	top := fit(b.Render("╭─")+title+b.Render(strings.Repeat("─", max(inner-lipgloss.Width(title)-1, 0))+"╮"), cw)
	row := func(content string) string {
		return b.Render("│") + padRight(fit(content, inner), inner) + b.Render("│")
	}
	lines := []string{top}
	for i := 0; i < cardRows-2; i++ {
		switch i {
		case 2:
			lines = append(lines, row(" "+v.th.Warn.Render(e.Note)))
		case 3:
			lines = append(lines, row(" "+v.th.Mut.Render(v.hostStandby(e.Name))))
		default:
			lines = append(lines, row(""))
		}
	}
	return append(lines, b.Render("╰"+strings.Repeat("─", inner)+"╯"))
}

// hostStandby lists the engines on host that are not serving, with their modes.
func (v *view) hostStandby(host string) string {
	var parts []string
	for _, s := range v.snap.Standby {
		if s.Host != host {
			continue
		}
		p := s.Name
		if s.Mode != "" {
			p += " (" + s.Mode + ")"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return ""
	}
	return "standby: " + strings.Join(parts, ", ")
}

// compact renders the two-line form of an engine, w columns at most.
func (v *view) compact(e state.EngineView, w int) []string {
	if e.Placeholder {
		return []string{fit(v.th.Bold.Render(e.Name)+"  "+v.th.Warn.Render(e.Note), w), fit("  "+v.th.Mut.Render(v.hostStandby(e.Name)), w)}
	}
	s := v.engineStyles(e)
	head := v.th.Bold.Render(e.Name)
	if e.Display != "" {
		head += " " + s.lab.Render(e.Display)
	}
	num := func(st state.StreamView, style lipgloss.Style) string {
		if !st.Value.OK {
			return s.lab.Render("-")
		}
		t := fmt.Sprintf("%.1f", st.Value.V)
		if st.Approx {
			t = "~" + t
		}
		if st.Held || e.Stale {
			style = v.th.Mut
		}
		return style.Render(t)
	}
	l1 := head + "  " + s.pre.Render("pf") + " " + num(e.Prefill, s.pre) + " " + s.dec.Render("dec") + " " + num(e.Decode, s.dec) + " " + s.lab.Render("tok/s")
	if e.KV.OK {
		st := v.kvStyle(e.KV.V)
		if e.Stale {
			st = v.th.Mut
		}
		l1 += "  " + s.lab.Render("kv") + " " + meterBar(e.KV.V, 8, st, v.th.Track) + " " + pct(100*e.KV.V)
	}
	if t := idleText(e); t != "" {
		l1 += "  " + s.lab.Render(t)
	}
	l2 := "  " + v.stats(e, s) + "  " + v.totals(e, s)
	if t := errText(e); t != "" {
		l2 = "  " + v.th.Err.Render("down: "+t) + "  " + l2[2:]
	}
	return []string{fit(l1, w), fit(l2, w)}
}

// tiny is the one-line form of an engine that is up but has no tokens.
func (v *view) tiny(e state.EngineView, nameW, w int) string {
	state_ := v.th.OK.Render("up")
	switch {
	case e.Err == "connecting":
		state_ = v.th.Mut.Render("connecting")
	case e.Err != "" && !e.Stale:
		state_ = v.th.Err.Render("down") + " " + v.th.Err.Render(e.Err)
	case e.Err != "":
		state_ = v.th.Err.Render("down") + " " + v.th.Mut.Render(errText(e))
	}
	line := v.th.Bold.Render(padRight(e.Name, nameW)) + "  " + state_ + "  " + v.th.Mut.Render("live") + " " + fmt.Sprintf("%d", e.Live)
	if e.Display != "" {
		line += "  " + v.th.Mut.Render("("+e.Display+")")
	}
	if e.Addr != "" {
		line += "  " + v.th.Mut.Render(e.Addr)
	}
	return fit(line, w)
}

func (v *view) standbyLine(sb []state.StandbyView, w int) string {
	parts := make([]string, len(sb))
	for i, s := range sb {
		parts[i] = s.Name
		if s.Mode != "" {
			parts[i] += " (" + s.Mode + ")"
		}
	}
	return fit(v.th.Mut.Render("standby: "+strings.Join(parts, ", ")), w)
}
