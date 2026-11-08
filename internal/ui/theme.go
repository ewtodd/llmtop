package ui

import (
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
	"golang.org/x/sys/unix"
)

// Theme is the colours of the screen: the terminal's own ANSI palette for the
// accents, and a muted gray and border generated from the terminal's
// background. The base background is never painted, so terminal transparency
// survives.
type Theme struct {
	Pre, Dec, OK, Warn, Err, Mut, Bold, Border, Track, Plain lipgloss.Style
}

// NewTheme builds the theme on renderer r. pal is Palette's answer for the
// terminal's background, or nil when it is not known (faint text then).
func NewTheme(r *lipgloss.Renderer, pal map[string]string) *Theme {
	fg := func(c string) lipgloss.Style { return r.NewStyle().Foreground(lipgloss.Color(c)) }
	th := &Theme{
		Pre:  fg("5"), // prefill: magenta
		Dec:  fg("6"), // decode: cyan
		OK:   fg("2"),
		Warn: fg("3"),
		Err:  fg("1"),
		Bold: r.NewStyle().Bold(true),
	}
	th.Plain = r.NewStyle()
	th.Mut = r.NewStyle().Faint(true)
	th.Border = r.NewStyle().Faint(true)
	th.Track = r.NewStyle().Faint(true)
	if pal != nil {
		th.Mut = fg(pal["text-muted"])
		th.Border = fg(pal["border"])
		th.Track = fg(pal["border-blurred"])
	}
	return th
}

// isTerminal reports whether w is a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd())
}

// newRenderer is the renderer of out: its own colour profile when out is a
// terminal, else true colour, so colours read the same everywhere else.
func newRenderer(out io.Writer, dark bool) *lipgloss.Renderer {
	r := lipgloss.NewRenderer(out)
	if !isTerminal(out) {
		r.SetColorProfile(termenv.TrueColor)
	}
	r.SetHasDarkBackground(dark)
	return r
}

// queryBackground asks the terminal for its background as #rrggbb (OSC 11,
// 120ms): "" when out is not a terminal, the query is turned off, or the
// terminal does not answer with a colour.
func queryBackground(out io.Writer) string {
	if !isTerminal(out) {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LLMTOP_NO_COLOR_QUERY"))) {
	case "1", "true", "yes", "on":
		return ""
	}
	f := out.(*os.File)
	state, err := term.MakeRaw(f.Fd())
	if err != nil {
		return ""
	}
	defer func() { _ = term.Restore(f.Fd(), state) }()
	return parseOSCColor(askTerminal(f, "\x1b]11;?\x1b\\", 120*time.Millisecond))
}

// askTerminal sends query to the terminal f and returns its reply: what
// arrives within timeout, up to the BEL or ST that ends it.
func askTerminal(f *os.File, query string, timeout time.Duration) string {
	if _, err := f.WriteString(query); err != nil {
		return ""
	}
	fd := int(f.Fd())
	deadline := time.Now().Add(timeout)
	var reply []byte
	buf := make([]byte, 64)
	for {
		left := time.Until(deadline)
		if left <= 0 {
			break
		}
		var set unix.FdSet
		set.Set(fd)
		tv := unix.NsecToTimeval(left.Nanoseconds())
		n, err := unix.Select(fd+1, &set, nil, nil, &tv)
		if err == unix.EINTR {
			continue
		}
		if err != nil || n == 0 {
			break
		}
		n, err = unix.Read(fd, buf)
		if err != nil || n <= 0 {
			break
		}
		reply = append(reply, buf[:n]...)
		if s := string(reply); strings.Contains(s, "\x07") || strings.Contains(s, "\x1b\\") {
			break
		}
	}
	return string(reply)
}

var (
	oscRGB = regexp.MustCompile(`rgba?:([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})/([0-9a-fA-F]{1,4})`)
	oscHex = regexp.MustCompile(`#([0-9a-fA-F]{6})`)
)

// parseOSCColor reads an OSC 10/11 reply into #rrggbb: "rgb:RRRR/GGGG/BBBB"
// with 1 to 4 digits a channel, or "#rrggbb". "" when it is neither.
func parseOSCColor(payload string) string {
	if m := oscRGB.FindStringSubmatch(payload); m != nil {
		var ch [3]int
		for i := range ch {
			v, _ := strconv.ParseUint(m[i+1], 16, 32)
			scale := float64(uint64(1)<<(4*len(m[i+1])) - 1)
			ch[i] = int(math.RoundToEven(float64(v) * 255 / scale))
		}
		return hexOf(ch[0], ch[1], ch[2])
	}
	if m := oscHex.FindStringSubmatch(payload); m != nil {
		return strings.ToLower("#" + m[1])
	}
	return ""
}

// StartTheme is the theme the program starts in, on the surfaces of the
// terminal's background (asked before Bubble Tea takes the terminal).
func StartTheme(out io.Writer) *Theme {
	bg := queryBackground(out)
	dark := lipgloss.HasDarkBackground()
	if r, g, b, ok := parseHex(bg); ok {
		dark = luminance(r, g, b) <= 127.5
	}
	return NewTheme(newRenderer(out, dark), Palette(bg))
}

// PlainTheme is a theme without colours, for tests and non-terminals.
func PlainTheme() *Theme {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	return NewTheme(r, nil)
}

// ColorTheme is a true-colour theme on a dark background, for tests.
func ColorTheme() *Theme {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	return NewTheme(r, Palette("#101820"))
}
