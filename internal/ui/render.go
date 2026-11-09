package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// bigRows is the height of the big digits.
const bigRows = 5

// glyphs are the 5-row big digits (after juliantsu/llmtop). Each glyph is as
// wide as its rows; digits are three columns, followed by one separator.
var glyphs = map[rune][bigRows]string{
	'0': {"███", "█ █", "█ █", "█ █", "███"},
	'1': {" █ ", "██ ", " █ ", " █ ", "███"},
	'2': {"███", "  █", "███", "█  ", "███"},
	'3': {"███", "  █", "███", "  █", "███"},
	'4': {"█ █", "█ █", "███", "  █", "  █"},
	'5': {"███", "█  ", "███", "  █", "███"},
	'6': {"███", "█  ", "███", "█ █", "███"},
	'7': {"███", "  █", "  █", "  █", "  █"},
	'8': {"███", "█ █", "███", "█ █", "███"},
	'9': {"███", "█ █", "███", "  █", "███"},
	'.': {"  ", "  ", "  ", "  ", "█ "},
	'-': {"   ", "   ", "███", "   ", "   "},
	' ': {"   ", "   ", "   ", "   ", "   "},
}

// headline formats a tok/s figure for the big digits: one decimal below 100,
// capped so the digits cannot outgrow their block.
func headline(v float64) string {
	v = clampf(v, 0, 9999)
	if v >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// bigNumber renders s in the big glyphs as bigRows lines, none wider than w
// (it is cut, never wrapped). "" renders as a dash.
func bigNumber(s string, w int) [bigRows]string {
	if s == "" {
		s = "-"
	}
	var rows [bigRows]string
	for _, c := range s {
		g, ok := glyphs[c]
		if !ok {
			continue
		}
		for i := range rows {
			rows[i] += g[i] + " "
		}
	}
	for i := range rows {
		rows[i] = strings.TrimRight(ansi.Truncate(rows[i], w, ""), " ")
	}
	return rows
}

func clampf(v, lo, hi float64) float64 {
	if math.IsNaN(v) {
		return lo
	}
	return math.Min(math.Max(v, lo), hi)
}

var eighths = []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// meterBar is a bar of width cells, frac of it filled in eighths of a cell. The
// filled part takes style fill, the rest track.
func meterBar(frac float64, width int, fill, track lipgloss.Style) string {
	if width <= 0 {
		return ""
	}
	frac = clampf(frac, 0, 1)
	total := int(math.Round(frac * float64(width) * 8))
	full, part := total/8, total%8
	var b strings.Builder
	if full > 0 {
		b.WriteString(fill.Render(strings.Repeat("█", full)))
	}
	used := full
	if part > 0 && full < width {
		b.WriteString(fill.Render(eighths[part]))
		used++
	}
	if used < width {
		b.WriteString(track.Render(strings.Repeat("░", width-used)))
	}
	return b.String()
}

// fit truncates s to w columns (ANSI aware); lines are never wrapped.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "")
}

// padRight pads or truncates s to exactly w columns.
func padRight(s string, w int) string {
	s = fit(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// padLeft right-aligns s in w columns.
func padLeft(s string, w int) string {
	s = fit(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s = strings.Repeat(" ", gap) + s
	}
	return s
}

// tokens formats a token count: 812, 40.3k, 1.2M.
func tokens(n float64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%.0f", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", n/1000)
	case n < 1_000_000:
		return fmt.Sprintf("%.0fk", n/1000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", n/1e6)
	default:
		return fmt.Sprintf("%.0fM", n/1e6)
	}
}

// rate formats a tok/s total: 214, 2.1k.
func rate(v float64) string {
	if v < 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1fk", v/1000)
}

// seconds formats a duration: 840ms, 1.2s, 38s, 4m, 2h5m, 2d3h.
func seconds(s float64) string {
	switch {
	case s < 0:
		return "-"
	case s < 1:
		return fmt.Sprintf("%.0fms", s*1000)
	case s < 10:
		return fmt.Sprintf("%.1fs", s)
	case s < 60:
		return fmt.Sprintf("%.0fs", s)
	case s < 3600:
		return fmt.Sprintf("%.0fm", math.Floor(s/60))
	case s < 86400:
		h, m := int(s/3600), int(s/60)%60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		d, h := int(s/86400), int(s/3600)%24
		if h == 0 {
			return fmt.Sprintf("%dd", d)
		}
		return fmt.Sprintf("%dd%dh", d, h)
	}
}

// ms formats milliseconds as a duration.
func ms(v float64) string { return seconds(v / 1000) }

// gib formats bytes in GiB with one decimal below 100.
func gib(b float64) string {
	g := b / (1 << 30)
	if g >= 100 {
		return fmt.Sprintf("%.0fG", g)
	}
	return fmt.Sprintf("%.1fG", g)
}

func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }

// ago formats an age: 3s, 4m, 2h5m.
func ago(s float64) string {
	if s < 10 {
		return fmt.Sprintf("%.0fs", s)
	}
	return seconds(s)
}

// gibPair formats used/total memory: 98/123G.
func gibPair(used, total float64) string {
	return fmt.Sprintf("%.0f/%.0fG", used/(1<<30), total/(1<<30))
}
