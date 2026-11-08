package state

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/rates"
	"github.com/ewtodd/llmtop/internal/source"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSnapshotFromStatusFixture(t *testing.T) {
	var st model.Status
	if err := json.Unmarshal(fixture(t, "maitred-status.json"), &st); err != nil {
		t.Fatal(err)
	}
	s := New(Config{Maitred: "http://m", Interval: time.Second, NodePort: "9100"})
	recv := time.Unix(1_762_590_000, 0)
	s.Apply(source.StatusMsg{At: recv, Status: &st})
	snap := s.Snapshot(recv.Add(10 * time.Second))

	if snap.Maitred.State != "ok" || int(snap.Maitred.UpSec) != 90010 {
		t.Fatalf("maitred %+v", snap.Maitred)
	}
	if len(snap.Standby) != 1 || snap.Standby[0].Name != "unholy-ds4" || snap.Standby[0].Mode != "ds4" {
		t.Fatalf("standby %+v", snap.Standby)
	}
	if len(snap.Engines) != 4 { // glm, eklow, llama, embed: the hosted API is not an engine
		t.Fatalf("engines %d", len(snap.Engines))
	}
	if e := snap.Engines[0]; e.Live != 3 || e.Display != "GLM-5.3-Flash" || e.Slots.V != 8 || e.Err != "connecting" {
		t.Fatalf("%+v", e)
	}
	hosts := map[string]HostView{}
	for _, h := range snap.Hosts {
		hosts[h.Name] = h
	}
	if len(snap.Hosts) != 2 || strings.Join(hosts["son-of-anton"].Modes, "|") != "glm GLM-5.3-Flash" || hosts["fiona"].Modes[0] != "sage Sage A1.5" {
		t.Fatalf("hosts %+v", snap.Hosts)
	}
	if len(snap.Callers) != 1 { // idle-key has no requests
		t.Fatalf("callers %+v", snap.Callers)
	}
	c := snap.Callers[0]
	if c.Name != "son-of-anton" || c.Live != 2 || c.Req10m != 3 || c.Fail10m != 1 || c.TTFTp50MS.V != 840 {
		t.Fatalf("%+v", c)
	}
	if !c.CachePct.OK || c.CachePct.V < 97 || c.CachePct.V > 98 || int(c.LastAgoSec.V*10) != 196 {
		t.Fatalf("%+v", c)
	}
	if len(snap.Feed) != 3 {
		t.Fatal("feed")
	}
	f := snap.Feed[0]
	if f.Done || !f.ElapsedMS.OK || int(f.ElapsedMS.V/1000) != 19 || f.Requested != "auto" || f.Model != "glm5.3-flash" {
		t.Fatalf("streaming entry: %+v", f)
	}
	if g := snap.Feed[1]; g.In != 12000 || g.Out != 300 || int(g.CachePct.V) != 91 || g.DurationMS.V != 9000 {
		t.Fatalf("%+v", g)
	}
	if h := snap.Feed[2]; h.Error != "model_not_found" || h.Status != 404 || h.TTFTms.OK {
		t.Fatalf("%+v", h)
	}
}

func TestSwitchingHost(t *testing.T) {
	var st model.Status
	_ = json.Unmarshal(fixture(t, "maitred-status.json"), &st)
	st.Modes[0].Serving = false
	s := New(Config{Interval: time.Second, NodePort: "9100"})
	s.Apply(source.StatusMsg{At: time.Now(), Status: &st})
	for _, h := range s.Snapshot(time.Now()).Hosts {
		if h.Name == "son-of-anton" && !h.Switching {
			t.Fatal("a host with no serving mode is switching")
		}
		if h.Name == "fiona" && h.Switching {
			t.Fatal("fiona serves sage")
		}
	}
}

func TestNoStatusFallback(t *testing.T) {
	s := New(Config{Interval: time.Second, NodePort: "9100", Extra: []source.ExtraEngine{{Name: "x", Root: "http://127.0.0.1:9/v1"}}})
	s.Apply(source.StatusMsg{At: time.Now(), NoStatus: true, Models: []model.ModelStatus{{ID: "a", Status: "serving"}, {ID: "b", Status: "unavailable"}}})
	snap := s.Snapshot(time.Now())
	if snap.Maitred.Note != "maitred has no /status" || snap.Maitred.Serving != 1 || snap.Maitred.Models != 2 || snap.Maitred.State != "ok" {
		t.Fatalf("%+v", snap.Maitred)
	}
	if len(snap.Engines) != 1 || snap.Engines[0].Name != "x" {
		t.Fatalf("engines come only from --engine: %+v", snap.Engines)
	}
}

func TestUnreachableMaitredKeepsLastGood(t *testing.T) {
	var st model.Status
	_ = json.Unmarshal(fixture(t, "maitred-status.json"), &st)
	s := New(Config{Interval: time.Second, NodePort: "9100"})
	t0 := time.Now()
	s.Apply(source.StatusMsg{At: t0, Status: &st})
	s.Apply(source.StatusMsg{At: t0.Add(5 * time.Second), Err: fmt.Errorf("dial tcp: connection refused")})
	snap := s.Snapshot(t0.Add(8 * time.Second))
	if snap.Maitred.State != "unreachable" || !snap.Maitred.Stale || int(snap.Maitred.AgeSec) != 8 || len(snap.Engines) != 4 {
		t.Fatalf("%+v", snap.Maitred)
	}
}

// ---- integration

type fakeEngine struct {
	srv    *httptest.Server
	n      atomic.Int64
	mode   atomic.Value // func(n int64) string
	health atomic.Bool
}

func newEngine(body func(n int64) string) *fakeEngine {
	e := &fakeEngine{}
	e.mode.Store(body)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			n := e.n.Add(1)
			fmt.Fprint(w, e.mode.Load().(func(int64) string)(n))
		case "/health":
			w.WriteHeader(200)
		default:
			http.NotFound(w, r)
		}
	}))
	return e
}

func glmBody(n int64) string {
	return fmt.Sprintf(`unholy_running 6
unholy_waiting 0
unholy_slots 8
unholy_kv_usage_ratio 0.5
unholy_prefill_tokens_total %d
unholy_decode_tokens_total %d
unholy_prefill_sequence_seconds_total %d
unholy_decode_sequence_seconds_total %d
unholy_busy_seconds_total %d
unholy_info{model="glm5.3-flash"} 1
`, 5000*n, 1000*n, 10*n, 25*n, n)
}

func TestIntegrationPollAndModeSwitch(t *testing.T) {
	glm := newEngine(glmBody)
	defer glm.srv.Close()
	embed := newEngine(func(n int64) string { return "" }) // no metrics: health only
	defer embed.srv.Close()

	var nodeScrapes atomic.Int64
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := nodeScrapes.Add(1)
		fmt.Fprintf(w, `node_cpu_seconds_total{cpu="0",mode="idle"} %d
node_cpu_seconds_total{cpu="0",mode="user"} %d
node_memory_MemTotal_bytes 1.0e11
node_memory_MemAvailable_bytes 4.0e10
node_load1 2.5
node_hwmon_chip_names{chip="0000:c6:00_0_0000:c7:00_0",chip_name="amdgpu"} 1
node_hwmon_sensor_label{chip="0000:c6:00_0_0000:c7:00_0",label="edge",sensor="temp1"} 1
node_hwmon_temp_celsius{chip="0000:c6:00_0_0000:c7:00_0",sensor="temp1"} 64
node_hwmon_power_average_watt{chip="0000:c6:00_0_0000:c7:00_0",sensor="power1"} 200
drm_card_device_info{card="card0",device="0000:c7:00.0",driver="amdgpu"} 1
node_drm_gpu_busy_percent{card="card0"} 91
node_drm_memory_vram_size_bytes{card="card0"} 3.4e10
node_drm_memory_vram_used_bytes{card="card0"} 1.7e10
`, 10*n, 10*n)
	}))
	defer node.Close()
	_, nodePort, _ := net.SplitHostPort(strings.TrimPrefix(node.URL, "http://"))

	host := "son-of-anton"
	mk := func(srvURL string) string { return srvURL + "/v1" }
	maitred := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := model.Status{
			Version: 1, Now: 1000, Started: 0,
			Upstreams: []model.Upstream{
				{Name: "glm", BaseURL: mk(glm.srv.URL), Host: &host, Serving: true, InFlight: 4},
				{Name: "embed", BaseURL: mk(embed.srv.URL), Serving: true},
			},
			Modes: []model.Mode{{Name: "glm", Label: "GLM", Host: host, Upstreams: []string{"glm"}, Serving: true}},
			Keys:  []model.Key{{Name: "k", Requests: 1}},
		}
		_ = json.NewEncoder(w).Encode(st)
	}))
	defer maitred.Close()

	s := New(Config{Maitred: maitred.URL, Interval: 20 * time.Millisecond, NodePort: nodePort})
	c := source.NewClient()
	s.Round(c)
	time.Sleep(30 * time.Millisecond)
	s.Round(c)
	snap := s.Snapshot(time.Now())

	if snap.Maitred.State != "ok" || len(snap.Engines) != 2 || len(snap.Hosts) != 1 {
		t.Fatalf("hosts/engines: %+v", snap)
	}
	e := snap.Engines[0]
	if e.Kind != "unholy" || e.Live != 4 || !e.Ready || !e.Active || e.Decode.Approx {
		t.Fatalf("%+v", e)
	}
	if e.Decode.Value.V != 40 || e.Prefill.Value.V != 500 || e.KV.V != 0.5 || e.Served != "glm5.3-flash" {
		t.Fatalf("per-stream numbers: %+v", e)
	}
	if e.DecodeAgg.V <= 0 || !e.Busy.OK {
		t.Fatalf("aggregate/busy: %+v", e)
	}
	em := snap.Engines[1]
	if em.Kind != model.KindHealth || !em.Up || !em.Tiny {
		t.Fatalf("health-only engine: %+v", em)
	}
	h := snap.Hosts[0]
	if h.Name != host || h.Err != "" || !h.CPU.OK || h.CPU.V != 50 || h.MemUsed.V != 6e10 || h.Load1.V != 2.5 {
		t.Fatalf("host %+v", h)
	}
	if len(h.GPUs) != 1 || h.GPUs[0].Label != "amdgpu c7:00.0" || h.GPUs[0].Busy.V != 91 || h.GPUs[0].MemKind != "vram" || h.GPUs[0].Temps["edge"] != 64 {
		t.Fatalf("gpu %+v", h.GPUs)
	}

	// llm-mode swaps the server behind the port: another kind, smaller counters.
	glm.mode.Store(func(n int64) string {
		return fmt.Sprintf("llamacpp:prompt_tokens_total 3\nllamacpp:tokens_predicted_total %d\nllamacpp:requests_processing 1\n", n)
	})
	for i := 0; i < 3; i++ {
		time.Sleep(25 * time.Millisecond)
		s.Round(c)
		e = s.Snapshot(time.Now()).Engines[0]
		if e.Kind != "llamacpp" {
			t.Fatalf("kind = %q", e.Kind)
		}
		for _, v := range []float64{e.Decode.Value.V, e.Prefill.Value.V, e.DecodeAgg.V, e.PrefillAgg.V, e.Busy.V} {
			if v < 0 || v > 1e7 {
				t.Fatalf("absurd rate after the switch: %+v", e)
			}
		}
		if i == 0 && (e.Ready || e.Decode.Value.OK) {
			t.Fatalf("the window is discarded at the switch: %+v", e)
		}
		if e.Tiny {
			t.Fatalf("a just-switched engine keeps its card: %+v", e)
		}
	}

	// A dead engine keeps its last good reading, dimmed with its age.
	glm.srv.Close()
	s.Round(c)
	e = s.Snapshot(time.Now().Add(3 * time.Second)).Engines[0]
	if e.Err == "" || !e.Stale || e.AgeSec < 3 || e.Kind != "llamacpp" {
		t.Fatalf("%+v", e)
	}
	// JSON round trip (--dump).
	if _, err := json.MarshalIndent(s.Snapshot(time.Now()), "", "  "); err != nil {
		t.Fatal(err)
	}
}

func TestKeepsCard(t *testing.T) {
	now := time.Unix(1_762_590_000, 0)
	idleFor := func(d time.Duration) rates.Rates { return rates.Rates{IdleSince: now.Add(-d)} }
	for _, c := range []struct {
		name         string
		pins, pinned bool
		r            rates.Rates
		want         bool
	}{
		{"no pins: every engine", false, false, rates.Rates{}, true},
		{"pinned and never active", true, true, rates.Rates{}, true},
		{"unpinned, active", true, false, rates.Rates{Active: true}, true},
		{"unpinned, idle a minute", true, false, idleFor(time.Minute), true},
		{"unpinned, idle past the linger", true, false, idleFor(cardLinger), false},
		{"unpinned, never active", true, false, rates.Rates{}, false},
	} {
		if got := keepsCard(c.pins, c.pinned, c.r, now); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func pinned(t *testing.T, pins []string, edit func(*model.Status)) Snapshot {
	t.Helper()
	var st model.Status
	if err := json.Unmarshal(fixture(t, "maitred-status.json"), &st); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&st)
	}
	s := New(Config{Interval: time.Second, NodePort: "9100", Pins: pins})
	s.Apply(source.StatusMsg{At: time.Now(), Status: &st})
	return s.Snapshot(time.Now())
}

func cards(snap Snapshot) string {
	var out []string
	for _, e := range snap.Engines {
		if !e.Tiny {
			out = append(out, e.Name)
		}
	}
	return strings.Join(out, ",")
}

func TestPinsOrderAndIdleCards(t *testing.T) {
	// Nothing has been scraped yet: every engine is connecting, none active.
	if got := cards(pinned(t, nil, nil)); got != "" {
		t.Fatalf("no pins, connecting engines are lines until read: %s", got)
	}
	snap := pinned(t, []string{"fiona", "son-of-anton"}, nil)
	if got := cards(snap); got != "eklow,unholy-glm" {
		t.Fatalf("pinned hosts keep their cards, in pin order: %s", got)
	}
	if !snap.Engines[0].Pinned || snap.Engines[0].Name != "eklow" {
		t.Fatalf("%+v", snap.Engines[0])
	}
	for _, e := range snap.Engines {
		if (e.Name == "llama" || e.Name == "embed") && (!e.Tiny || e.Pinned) {
			t.Fatalf("utilities stay lines on a pinned host: %+v", e)
		}
	}
	if got := cards(pinned(t, []string{"son-of-anton"}, nil)); got != "unholy-glm" {
		t.Fatalf("an unpinned idle engine is a line: %s", got)
	}
	if got := cards(pinned(t, []string{"eklow"}, nil)); got != "eklow" {
		t.Fatalf("an engine can be pinned by name: %s", got)
	}
}

func TestPinnedHostWithNothingServing(t *testing.T) {
	snap := pinned(t, []string{"son-of-anton", "fiona"}, func(st *model.Status) {
		for i := range st.Upstreams {
			if st.Upstreams[i].Name == "unholy-glm" {
				st.Upstreams[i].Serving = false
			}
		}
		for i := range st.Modes {
			if st.Modes[i].Host == "son-of-anton" {
				st.Modes[i].Serving = false
			}
		}
	})
	e := snap.Engines[0]
	if !e.Placeholder || e.Name != "son-of-anton" || !strings.Contains(e.Note, "switching") || e.Tiny {
		t.Fatalf("placeholder first, in pin order: %+v", e)
	}
	if snap.Engines[1].Name != "eklow" {
		t.Fatalf("then fiona's engine: %+v", snap.Engines[1])
	}
	var hosts []string
	for _, sb := range snap.Standby {
		hosts = append(hosts, sb.Host)
	}
	if !strings.Contains(strings.Join(hosts, ","), "son-of-anton") {
		t.Fatalf("standby entries carry their host: %+v", snap.Standby)
	}
	if got := pinned(t, []string{"nowhere"}, nil); len(got.Engines) == 0 || got.Engines[0].Placeholder {
		t.Fatalf("a pin that names nothing adds nothing: %+v", got.Engines)
	}
}

func TestHostPort(t *testing.T) { // keep net/url in use for hostPort
	if hostPort("http://10.0.0.5:8250") != "10.0.0.5:8250" {
		t.Fatal(hostPort("http://10.0.0.5:8250"))
	}
}
