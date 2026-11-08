// Package state folds the sources' messages into a Snapshot. It owns the
// discovery (which engines and hosts exist), the rate trackers and the age of
// every source, and does no I/O.
package state

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/rates"
	"github.com/ewtodd/llmtop/internal/source"
)

// Config is what the flags decide.
type Config struct {
	Maitred  string
	Interval time.Duration
	NodePort string
	NoHosts  bool
	Extra    []source.ExtraEngine
	GPUNames map[string]string // "host/pci" -> label
}

type engineState struct {
	tracker *rates.Tracker
	err     string
	lastOK  time.Time
}

type nodeState struct {
	tracker *rates.NodeTracker
	last    model.Node
	rates   rates.NodeRates
	have    bool
	err     string
	lastOK  time.Time
}

// State is the model behind the screen.
type State struct {
	cfg Config

	status     *model.Status
	statusRecv time.Time // local time the status arrived
	statusErr  string
	statusOK   time.Time
	tried      bool
	noStatus   bool
	models     []model.ModelStatus

	engines []source.Engine
	hosts   []source.Host
	eng     map[string]*engineState
	node    map[string]*nodeState
}

// New makes an empty state.
func New(cfg Config) *State {
	s := &State{cfg: cfg, eng: map[string]*engineState{}, node: map[string]*nodeState{}}
	s.rediscover()
	return s
}

// Target is one thing to poll.
type Target struct {
	Name string
	URL  string
}

// Targets are the engines to scrape and the nodes to read, from what is known now.
func (s *State) Targets() (engines, nodes []Target) {
	for _, e := range s.engines {
		if e.Serving {
			engines = append(engines, Target{e.Name, e.Root})
		}
	}
	if !s.cfg.NoHosts {
		for _, h := range s.hosts {
			if h.NodeURL != "" {
				nodes = append(nodes, Target{h.Name, h.NodeURL})
			}
		}
	}
	return
}

func (s *State) rediscover() {
	s.engines, s.hosts = source.Discover(s.status, s.cfg.Extra, s.cfg.NodePort)
	serving := map[string]bool{}
	for _, e := range s.engines {
		if e.Serving {
			serving[e.Name] = true
		}
	}
	for name := range s.eng { // a switched-away engine's counters are stale
		if !serving[name] {
			delete(s.eng, name)
		}
	}
	hostSet := map[string]bool{}
	for _, h := range s.hosts {
		hostSet[h.Name] = true
	}
	for name := range s.node {
		if !hostSet[name] {
			delete(s.node, name)
		}
	}
}

// Apply folds in a message from a source.
func (s *State) Apply(msg any) {
	switch m := msg.(type) {
	case source.StatusMsg:
		s.tried = true
		if m.Err != nil {
			s.statusErr = source.ShortErr(m.Err)
			return
		}
		s.statusErr, s.statusOK = "", m.At
		s.noStatus = m.NoStatus
		s.models = m.Models
		if m.Status != nil {
			s.status, s.statusRecv = m.Status, m.At
		} else {
			s.status = nil
		}
		s.rediscover()
	case source.EngineMsg:
		es := s.eng[m.Name]
		if es == nil {
			es = &engineState{tracker: rates.NewTracker(s.cfg.Interval)}
			s.eng[m.Name] = es
		}
		if m.Err != nil {
			es.err = source.ShortErr(m.Err)
			return
		}
		es.err, es.lastOK = "", m.At
		es.tracker.Add(m.Reading)
	case source.NodeMsg:
		ns := s.node[m.Host]
		if ns == nil {
			ns = &nodeState{tracker: rates.NewNodeTracker(s.cfg.Interval)}
			s.node[m.Host] = ns
		}
		if m.Err != nil {
			ns.err = source.ShortErr(m.Err)
			return
		}
		ns.err, ns.lastOK = "", m.At
		ns.last, ns.have = m.Node, true
		ns.rates = ns.tracker.Add(m.Node)
	}
}

func hostPort(root string) string {
	u, err := url.Parse(root)
	if err != nil || u.Host == "" {
		return root
	}
	return u.Host
}

func age(now, t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return max(now.Sub(t).Seconds(), 0)
}

// Snapshot is the screen's data as of now.
func (s *State) Snapshot(now time.Time) Snapshot {
	snap := Snapshot{Time: now}
	snap.Maitred = s.maitredView(now)
	snap.Engines, snap.Standby = s.engineViews(now)
	snap.Hosts = s.hostViews(now)
	snap.Callers, snap.Feed = s.callerViews(now)
	if snap.Hosts == nil {
		snap.Hosts = []HostView{}
	}
	if snap.Engines == nil {
		snap.Engines = []EngineView{}
	}
	if snap.Standby == nil {
		snap.Standby = []StandbyView{}
	}
	if snap.Callers == nil {
		snap.Callers = []CallerView{}
	}
	if snap.Feed == nil {
		snap.Feed = []FeedView{}
	}
	return snap
}

func (s *State) maitredView(now time.Time) MaitredView {
	v := MaitredView{URL: s.cfg.Maitred, State: "connecting"}
	switch {
	case s.statusErr != "":
		v.State, v.Err = "unreachable", s.statusErr
		v.Stale = !s.statusOK.IsZero()
		v.AgeSec = age(now, s.statusOK)
	case s.tried:
		v.State = "ok"
	}
	if s.status != nil {
		v.UpSec = max(s.status.Now-s.status.Started+now.Sub(s.statusRecv).Seconds(), 0)
	}
	if s.noStatus && s.statusErr == "" {
		v.Note = "maitred has no /status"
		v.Models = len(s.models)
		for _, m := range s.models {
			if m.Status == "serving" {
				v.Serving++
			}
		}
	}
	return v
}

func (s *State) engineViews(now time.Time) ([]EngineView, []StandbyView) {
	var views []EngineView
	var standby []StandbyView
	for _, e := range s.engines {
		if !e.Serving {
			standby = append(standby, StandbyView{Name: e.Name, Mode: e.Mode, Reason: e.Reason})
			continue
		}
		v := EngineView{
			Name: e.Name, Display: e.Display, Host: e.Host, Addr: hostPort(e.Root), Mode: e.Mode,
			Live: e.InFlight,
		}
		if e.Slots > 0 {
			v.Slots = model.Some(float64(e.Slots))
		}
		es := s.eng[e.Name]
		if es == nil {
			v.Err = "connecting"
			v.Tiny = true
			views = append(views, v)
			continue
		}
		last, have := es.tracker.Last()
		if es.err != "" {
			v.Err, v.Stale, v.AgeSec = es.err, have, age(now, es.lastOK)
		}
		if !have {
			v.Tiny = true
			views = append(views, v)
			continue
		}
		v.Kind, v.Up = last.Kind, last.Up && es.err == ""
		v.Served = last.Model
		if v.Display == "" {
			v.Display = last.Model
		}
		if last.Slots.OK {
			v.Slots = last.Slots
		}
		v.Running, v.Waiting, v.KV, v.Context = last.Running, last.Waiting, last.KV, last.Context
		v.DeviceLost = last.DeviceLost
		v.Tiny = last.Kind == model.KindHealth || last.DecodeTokens == 0
		r := es.tracker.Rates()
		v.Ready, v.Active = r.Ready, r.Active
		v.Prefill = StreamView{Value: model.Opt{V: r.Prefill.Value, OK: r.Prefill.OK}, Approx: r.Prefill.Approx, Held: r.Prefill.Held}
		v.Decode = StreamView{Value: model.Opt{V: r.Decode.Value, OK: r.Decode.OK}, Approx: r.Decode.Approx, Held: r.Decode.Held}
		v.PrefillAgg, v.DecodeAgg = r.PrefillAgg, r.DecodeAgg
		v.PeakPrefill, v.PeakDecode = r.PeakPrefill, r.PeakDecode
		if !r.Active && !r.IdleSince.IsZero() {
			v.IdleSec = model.Some(max(now.Sub(r.IdleSince).Seconds(), 0))
		}
		v.Busy, v.TTFT, v.Cache, v.Spec = r.Busy, r.TTFT, r.Cache, r.Spec
		views = append(views, v)
	}
	return views, standby
}

func shortModel(id string) string {
	if i := strings.LastIndex(id, "/"); i >= 0 {
		return id[i+1:]
	}
	return id
}

func gpuLabel(cfg Config, host string, g model.GPU) string {
	short := strings.TrimPrefix(g.PCI, "0000:")
	for _, k := range []string{host + "/" + g.PCI, host + "/" + short} {
		if l, ok := cfg.GPUNames[k]; ok {
			return l
		}
	}
	if g.PCI == "" {
		return g.Driver
	}
	return g.Driver + " " + short
}

const iGPUVRAM = 4 << 30

func (s *State) hostViews(now time.Time) []HostView {
	var views []HostView
	for _, h := range s.hosts {
		v := HostView{Name: h.Name, NodeURL: h.NodeURL, NoNode: s.cfg.NoHosts}
		if s.status != nil {
			var hasMode bool
			for _, m := range s.status.Modes {
				if m.Host != h.Name {
					continue
				}
				hasMode = true
				if m.Serving {
					v.Modes = append(v.Modes, strings.TrimSpace(m.Name+" "+m.Label))
				}
			}
			v.Switching = hasMode && len(v.Modes) == 0
		}
		if ns := s.node[h.Name]; ns != nil && !s.cfg.NoHosts {
			if ns.err != "" {
				v.Err, v.Stale, v.AgeSec = ns.err, ns.have, age(now, ns.lastOK)
			}
			if ns.have {
				s.fillHost(&v, h.Name, ns)
			}
		}
		views = append(views, v)
	}
	return views
}

func (s *State) fillHost(v *HostView, host string, ns *nodeState) {
	n := ns.last
	v.CPU, v.Load1, v.MemTotal = ns.rates.CPU, n.Load1, n.MemTotal
	if n.MemTotal.OK && n.MemAvail.OK {
		v.MemUsed = model.Some(n.MemTotal.V - n.MemAvail.V)
	}
	gpus := slices.Clone(n.GPUs)
	slices.SortFunc(gpus, func(a, b model.GPU) int { return cmp.Compare(a.PCI, b.PCI) })
	for _, g := range gpus {
		gv := GPUView{Label: gpuLabel(s.cfg, host, g), Driver: g.Driver, PCI: g.PCI, Temps: g.Temps,
			Power: g.Power, Cap: g.Cap, Fan: g.Fan, Clock: g.Clock}
		if !gv.Power.OK {
			if w, ok := ns.rates.Power[g.Chip]; ok {
				gv.Power = model.Some(w)
			}
		}
		for _, c := range n.Cards {
			if g.Card == "" || c.Name != g.Card {
				continue
			}
			gv.Busy = c.Busy
			if c.VRAMSize.OK && c.VRAMSize.V >= iGPUVRAM {
				gv.MemKind, gv.MemUsed, gv.MemSize = "vram", c.VRAMUsed, c.VRAMSize
			} else if c.GTTSize.OK {
				gv.MemKind, gv.MemUsed, gv.MemSize = "gtt", c.GTTUsed, c.GTTSize
			} else if c.VRAMSize.OK {
				gv.MemKind, gv.MemUsed, gv.MemSize = "vram", c.VRAMUsed, c.VRAMSize
			}
		}
		v.GPUs = append(v.GPUs, gv)
	}
}

func (s *State) callerViews(now time.Time) ([]CallerView, []FeedView) {
	st := s.status
	if st == nil {
		return nil, nil
	}
	nowM := st.Now + now.Sub(s.statusRecv).Seconds() // maitred's clock
	var callers []CallerView
	for _, k := range st.Keys {
		if k.Requests == 0 {
			continue
		}
		c := CallerView{Name: k.Name, Live: k.InFlight, Requests: k.Requests,
			PromptTokens: k.PromptTokens, OutTokens: k.CompletionTokens, CachedTokens: k.CachedTokens}
		if k.PromptTokens > 0 {
			c.CachePct = model.Some(100 * float64(k.CachedTokens) / float64(k.PromptTokens))
		}
		if k.Last != nil {
			c.LastAgoSec = model.Some(max(nowM-*k.Last, 0))
		}
		var ttfb []float64
		for _, r := range st.Recent {
			if r.Key != k.Name || nowM-r.Start > 600 {
				continue
			}
			c.Req10m++
			if r.Status >= 400 || (r.Error != "" && r.Error != "client closed") {
				c.Fail10m++
			}
			if r.FirstChunk != nil {
				ttfb = append(ttfb, float64(*r.FirstChunk))
			}
		}
		if len(ttfb) > 0 {
			slices.Sort(ttfb)
			c.TTFBp50MS = model.Some(ttfb[len(ttfb)/2])
		}
		callers = append(callers, c)
	}
	slices.SortStableFunc(callers, func(a, b CallerView) int {
		if a.Live != b.Live {
			return cmp.Compare(b.Live, a.Live)
		}
		switch {
		case a.LastAgoSec.OK && !b.LastAgoSec.OK:
			return -1
		case !a.LastAgoSec.OK && b.LastAgoSec.OK:
			return 1
		}
		return cmp.Compare(a.LastAgoSec.V, b.LastAgoSec.V) // most recent first
	})

	var feed []FeedView
	for _, r := range st.Recent {
		f := FeedView{
			Start: time.Unix(0, int64(r.Start*1e9)), Key: r.Key, Requested: shortModel(r.Requested),
			Model: shortModel(r.Model), Upstream: r.Upstream, Status: r.Status, Done: r.Done, Error: r.Error,
		}
		if r.FirstChunk != nil {
			f.TTFBms = model.Some(float64(*r.FirstChunk))
		}
		if r.DurationMS != nil {
			f.DurationMS = model.Some(float64(*r.DurationMS))
		} else if !r.Done {
			f.ElapsedMS = model.Some(max(nowM-r.Start, 0) * 1000)
		}
		if r.Usage != nil {
			f.HasUsage, f.In, f.Out = true, r.Usage.PromptTokens, r.Usage.CompletionTokens
			if r.Usage.PromptTokens > 0 {
				f.CachePct = model.Some(100 * float64(r.Usage.CachedTokens) / float64(r.Usage.PromptTokens))
			}
		}
		feed = append(feed, f)
	}
	return callers, feed
}

// MaitredURL is the gateway this state polls.
func (s *State) MaitredURL() string { return s.cfg.Maitred }
