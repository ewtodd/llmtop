package ui

import (
	"net/http"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ewtodd/llmtop/internal/source"
	"github.com/ewtodd/llmtop/internal/state"
)

// Options are what the program needs from the command line.
type Options struct {
	State    *state.State // nil in demo mode
	Demo     bool
	Interval time.Duration
	Theme    *Theme
	Client   *http.Client
}

type tickMsg struct{}   // poll now
type redrawMsg struct{} // clock and ages

// doneMsg delivers a source's result to the loop and frees its slot.
type doneMsg struct {
	key string
	msg any
}

// Model is the Bubble Tea model. It does no I/O: polls are commands.
type Model struct {
	opt      Options
	w, h     int
	paused   bool
	inflight map[string]bool
	first    bool
	now      func() time.Time
}

// New makes the model.
func New(opt Options) *Model {
	return &Model{opt: opt, inflight: map[string]bool{}, now: time.Now, w: 115, h: 55}
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func redraw() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return redrawMsg{} })
}

// Init starts polling and the clock.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.poll(true), tick(m.opt.Interval), redraw())
}

// fetch is one source's poll as a command, run by Bubble Tea in a goroutine.
func (m *Model) fetch(key string, f func() any) tea.Cmd {
	if m.inflight[key] {
		return nil
	}
	m.inflight[key] = true
	return func() tea.Msg { return doneMsg{key: key, msg: f()} }
}

// poll issues the fetches that are due. Engines and nodes are only known once
// maitred has answered once; withStatus adds maitred itself.
func (m *Model) poll(withStatus bool) tea.Cmd {
	st := m.opt.State
	if st == nil {
		return nil
	}
	c := m.opt.Client
	var cmds []tea.Cmd
	if withStatus {
		url := st.MaitredURL()
		cmds = append(cmds, m.fetch("status", func() any { return source.FetchStatus(c, url) }))
	}
	engines, nodes := st.Targets()
	for _, e := range engines {
		cmds = append(cmds, m.fetch("e:"+e.Name, func() any { return source.FetchEngine(c, e.Name, e.URL) }))
	}
	for _, n := range nodes {
		cmds = append(cmds, m.fetch("n:"+n.Name, func() any { return source.FetchNode(c, n.Name, n.URL) }))
	}
	return tea.Batch(cmds...)
}

// Update handles keys, resizes, ticks and results.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "p":
			m.paused = !m.paused
		case "r":
			return m, m.poll(true)
		}
	case tickMsg:
		if m.paused {
			return m, tick(m.opt.Interval)
		}
		return m, tea.Batch(m.poll(true), tick(m.opt.Interval))
	case redrawMsg:
		return m, redraw()
	case doneMsg:
		delete(m.inflight, msg.key)
		if m.opt.State == nil {
			return m, nil
		}
		m.opt.State.Apply(msg.msg)
		if _, ok := msg.msg.(source.StatusMsg); ok && !m.first {
			// maitred has just said which engines exist: poll them now rather
			// than at the next tick.
			m.first = true
			return m, m.poll(false)
		}
	}
	return m, nil
}

// snapshot is the data of the frame.
func (m *Model) snapshot() state.Snapshot {
	if m.opt.Demo || m.opt.State == nil {
		return DemoSnapshot(m.now())
	}
	return m.opt.State.Snapshot(m.now())
}

// View draws the frame.
func (m *Model) View() string {
	return Render(m.opt.Theme, m.snapshot(), m.w, m.h, m.paused)
}
