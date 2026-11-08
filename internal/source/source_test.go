package source

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/prom"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func read(t *testing.T, name string) model.Reading {
	t.Helper()
	r, ok := Normalize(prom.Parse(fixture(t, name)), time.Unix(100, 0))
	if !ok {
		t.Fatalf("%s: not recognised", name)
	}
	return r
}

func TestUnholyOldShape(t *testing.T) {
	r := read(t, "unholy-glm.metrics")
	if r.Kind != model.KindUnholy {
		t.Fatal(r.Kind)
	}
	if r.PrefillTokens != 230632 || r.DecodeTokens != 15601 {
		t.Fatalf("tokens %v %v", r.PrefillTokens, r.DecodeTokens)
	}
	if r.PrefillSeqSeconds.OK || r.DecodeSeqSeconds.OK || r.FirstTokenCount.OK || r.Slots.OK || r.Context.OK {
		t.Fatal("new counters must be absent in the deployed shape")
	}
	if !r.KV.OK || !near(r.KV.V, 1-3509.0/4096) {
		t.Fatalf("kv from pages = %+v", r.KV)
	}
	if r.Running.V != 6 || !r.Busy.OK || r.CachedTokens.V != 1166592 || r.SpecAccepted.V != 6588 {
		t.Fatalf("reading %+v", r)
	}
}

func TestUnholyNewShape(t *testing.T) {
	r := read(t, "unholy-new.metrics")
	if !near(r.KV.V, 0.42) {
		t.Fatalf("kv_usage_ratio must win over the page counts: %+v", r.KV)
	}
	if r.Model != "glm5.3-flash" || r.Slots.V != 8 || r.Context.V != 786432 {
		t.Fatalf("info/slots/context: %+v", r)
	}
	if r.PrefillSeqSeconds.V != 120.5 || r.DecodeSeqSeconds.V != 1560.1 {
		t.Fatalf("sequence seconds %+v %+v", r.PrefillSeqSeconds, r.DecodeSeqSeconds)
	}
	if r.FirstTokenSeconds.V != 240.5 || r.FirstTokenCount.V != 197 || r.Waiting.V != 1 {
		t.Fatalf("first token %+v", r)
	}
}

func TestEklow(t *testing.T) {
	r := read(t, "eklow.metrics")
	if r.Kind != model.KindEklow || r.Model != "sage-a1.5" || r.Slots.V != 2 || r.DecodeTokens != 800 || !near(r.KV.V, 0.05) {
		t.Fatalf("%+v", r)
	}
	if r.DecodeSeqSeconds.V != 16 || r.CachedTokens.V != 4000 {
		t.Fatalf("%+v", r)
	}
}

func TestRadiance(t *testing.T) {
	r := read(t, "radiance.metrics")
	if r.Kind != model.KindRadiance { // not vllm, though it exports vllm: names
		t.Fatal(r.Kind)
	}
	if r.PrefillTokens != 60000 || r.DecodeTokens != 41000 || r.Running.V != 3 || !near(r.KV.V, 0.31) {
		t.Fatalf("%+v", r)
	}
	if r.PrefillSeqSeconds.OK || r.DecodeSeqSeconds.OK {
		t.Fatal("radiance has no sequence seconds")
	}
	if r.FirstTokenSeconds.V != 33 || r.FirstTokenCount.V != 30 || r.SpecDrafted.V != 5000 || r.SpecAccepted.V != 4000 || r.Busy.V != 800.5 {
		t.Fatalf("%+v", r)
	}
	if r.CachedTokens.V != 30000 || r.PromptTokens.V != 90000 || r.Model != "qwen3.8-27b" {
		t.Fatalf("%+v", r)
	}
}

func TestVLLM(t *testing.T) {
	r := read(t, "vllm.metrics")
	if r.Kind != model.KindVLLM {
		t.Fatal(r.Kind)
	}
	if r.PrefillTokens != 4000 { // prompt minus cached
		t.Fatalf("prefill = %v", r.PrefillTokens)
	}
	if r.DecodeTokens != 2000 || r.PrefillSeqSeconds.V != 8 || r.DecodeSeqSeconds.V != 40 || r.Waiting.V != 2 {
		t.Fatalf("%+v", r)
	}
	if r.CachedTokens.V != 6000 || r.PromptTokens.V != 10000 || r.SpecAccepted.V != 70 {
		t.Fatalf("%+v", r)
	}
}

func TestLlamaCpp(t *testing.T) {
	r := read(t, "llamacpp.metrics")
	if r.Kind != model.KindLlamaCpp || r.PrefillTokens != 8000 || r.DecodeTokens != 3000 {
		t.Fatalf("%+v", r)
	}
	if r.PrefillSeqSeconds.V != 16 || r.DecodeSeqSeconds.V != 60 || r.Running.V != 2 || r.Waiting.V != 1 || !near(r.KV.V, 0.25) {
		t.Fatalf("%+v", r)
	}
	if r.DeviceLost.V != 2 || r.PromptTokens.V != 10000 || r.CachedTokens.V != 2000 {
		t.Fatalf("%+v", r)
	}
}

func TestUnknownKind(t *testing.T) {
	if _, ok := Normalize(prom.Parse([]byte("go_goroutines 12\n")), time.Now()); ok {
		t.Fatal("go metrics are no engine")
	}
}

func TestEngineRoot(t *testing.T) {
	for in, want := range map[string]string{
		"http://10.0.0.5:8250/v1":  "http://10.0.0.5:8250",
		"http://10.0.0.6:8262/v1/": "http://10.0.0.6:8262",
		"http://h:1":               "http://h:1",
	} {
		if got := EngineRoot(in); got != want {
			t.Errorf("%s -> %s", in, got)
		}
	}
}

func TestChipPCI(t *testing.T) {
	for in, want := range map[string]string{
		"0000:cb:00_0_0000:cc:00_0": "0000:cc:00.0",
		"0000:02:01_0_0000:03:00_0": "0000:03:00.0",
		"0000:00:08_1_0000:d0:00_0": "0000:d0:00.0",
		"platform_coretemp_0":       "",
		"pci0000:00_0000:00:18_3":   "0000:00:18.3",
	} {
		if got := ChipPCI(in); got != want {
			t.Errorf("%s -> %q want %q", in, got, want)
		}
	}
}

func TestNodeSonOfAnton(t *testing.T) {
	n := ParseNode(prom.Parse(fixture(t, "node-son-of-anton.prom")), time.Now())
	if len(n.GPUs) != 3 {
		t.Fatalf("gpus = %d (k10temp must not count)", len(n.GPUs))
	}
	if !n.MemTotal.OK || n.Load1.V != 1.18 || n.CPUTotal <= n.CPUIdle || n.CPUIdle == 0 {
		t.Fatalf("host: %+v", n)
	}
	byPCI := map[string]model.GPU{}
	for _, g := range n.GPUs {
		byPCI[g.PCI] = g
	}
	g := byPCI["0000:cc:00.0"]
	if g.Driver != "amdgpu" || g.Temps["junction"] != 101 || g.Temps["edge"] != 75 || g.Temps["mem"] != 78 {
		t.Fatalf("temps %+v", g)
	}
	if g.Power.V != 222 || g.Cap.V != 230 || g.Fan.V != 3867 || g.Clock.V != 2709 {
		t.Fatalf("%+v", g)
	}
	if g.Card != "card1" {
		t.Fatalf("drm_card_device_info join: %q", g.Card)
	}
	if byPCI["0000:c7:00.0"].Card != "card0" || byPCI["0000:d0:00.0"].Card != "card2" {
		t.Fatal("card mapping")
	}
	if byPCI["0000:d0:00.0"].Cap.OK {
		t.Fatal("the iGPU has no power cap")
	}
	var card1 model.DRMCard
	for _, c := range n.Cards {
		if c.Name == "card1" {
			card1 = c
		}
	}
	if card1.Busy.V != 97 || card1.VRAMUsed.V != 3.2934592512e10 || card1.GTTSize.V != 1.33143986176e11 {
		t.Fatalf("%+v", card1)
	}
}

func TestNodeFionaXe(t *testing.T) {
	n := ParseNode(prom.Parse(fixture(t, "node-fiona.prom")), time.Now())
	if len(n.GPUs) != 1 {
		t.Fatalf("gpus = %d", len(n.GPUs))
	}
	g := n.GPUs[0]
	if g.Driver != "xe" || g.PCI != "0000:03:00.0" || g.Temps["pkg"] != 72 || g.Temps["vram"] != 76 {
		t.Fatalf("%+v", g)
	}
	if g.Power.OK || !g.Energy.OK || g.Energy.V != 3.9717440174316e+07 {
		t.Fatalf("xe power comes from the energy counter: %+v", g)
	}
	if g.Card != "card1" {
		t.Fatalf("xe card via info: %q", g.Card)
	}
	if n.Cards[1].Busy.OK {
		t.Fatal("no drm data for xe")
	}
}

func TestNodeOrderFallbackWithoutInfo(t *testing.T) {
	body := strings.ReplaceAll(string(fixture(t, "node-son-of-anton.prom")), "drm_card_device_info", "ignored_metric")
	n := ParseNode(prom.Parse([]byte(body)), time.Now())
	want := map[string]string{"0000:c7:00.0": "card0", "0000:cc:00.0": "card1", "0000:d0:00.0": "card2"}
	for _, g := range n.GPUs {
		if g.Card != want[g.PCI] {
			t.Errorf("%s -> %q", g.PCI, g.Card)
		}
	}
	// Counts that do not match: no pairing at all.
	body = strings.ReplaceAll(body, "node_drm_gpu_busy_percent{card=\"card2\"} 100", "")
	body = strings.ReplaceAll(body, "node_drm_memory_vram_size_bytes{card=\"card2\"}", "node_x{card=\"card2\"}")
	body = strings.ReplaceAll(body, "node_drm_card_info{card=\"card2\"", "node_x{card=\"card2\"")
	n = ParseNode(prom.Parse([]byte(body)), time.Now())
	for _, g := range n.GPUs {
		if g.Card != "" {
			t.Errorf("mismatched counts must pair nothing, %s -> %q", g.PCI, g.Card)
		}
	}
}

func status(t *testing.T) *model.Status {
	t.Helper()
	var st model.Status
	if err := json.Unmarshal(fixture(t, "maitred-status.json"), &st); err != nil {
		t.Fatal(err)
	}
	return &st
}

func TestStatusDecode(t *testing.T) {
	st := status(t)
	if st.Version != 1 || len(st.Upstreams) != 6 || len(st.Modes) != 3 || len(st.Keys) != 2 || len(st.Recent) != 3 {
		t.Fatalf("%+v", st)
	}
	if st.Upstreams[0].Slots == nil || *st.Upstreams[0].Slots != 8 || st.Upstreams[3].Host != nil {
		t.Fatal("nullable fields")
	}
	if st.Keys[1].Last != nil || st.Recent[0].DurationMS != nil || st.Recent[1].Usage.CachedTokens != 11000 {
		t.Fatal("nullable fields in keys/recent")
	}
}

func TestDiscoverHosts(t *testing.T) {
	engines, hosts := Discover(status(t), []ExtraEngine{{Name: "side", Root: "http://10.0.0.9:8000/v1"}}, "9100")
	names := []string{}
	for _, e := range engines {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "unholy-glm,unholy-ds4,eklow,llama,embed,side" {
		t.Fatalf("engines (hosted dropped, extra added): %v", names)
	}
	if engines[0].Root != "http://10.0.0.5:8250" || engines[0].Display != "GLM-5.3-Flash" || engines[0].Slots != 8 || engines[0].Mode != "glm" {
		t.Fatalf("%+v", engines[0])
	}
	if engines[3].Display != "Llama" {
		t.Fatalf("hidden-only models still name the engine: %+v", engines[3])
	}
	if len(hosts) != 3 {
		t.Fatalf("hosts = %+v", hosts)
	}
	h := map[string]Host{}
	for _, x := range hosts {
		h[x.Name] = x
	}
	if got := strings.Join(h["fiona"].Engines, ","); got != "eklow,llama,embed" {
		t.Fatalf("fiona's modeless upstreams must merge into fiona: %s", got)
	}
	if h["fiona"].NodeURL != "http://10.0.0.6:9100/metrics" || h["son-of-anton"].NodeURL != "http://10.0.0.5:9100/metrics" {
		t.Fatalf("%+v", h)
	}
	if h["10.0.0.9"].NodeURL != "http://10.0.0.9:9100/metrics" || !engines[5].Extra {
		t.Fatalf("an --engine on its own address is its own host: %+v", h)
	}
}

func serve(handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	s := httptest.NewServer(handler)
	return s, s.Client()
}

func TestFetchStatus404Fallback(t *testing.T) {
	s, c := serve(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Write(fixture(t, "maitred-models.json"))
		default:
			http.NotFound(w, r)
		}
	})
	defer s.Close()
	msg := FetchStatus(c, s.URL)
	if msg.Err != nil || !msg.NoStatus || msg.Status != nil {
		t.Fatalf("%+v", msg)
	}
	var serving int
	for _, m := range msg.Models {
		if m.Status == "serving" {
			serving++
		}
	}
	if len(msg.Models) != 10 || serving != 6 {
		t.Fatalf("models %d serving %d", len(msg.Models), serving)
	}
}

func TestFetchStatusOKAndErrors(t *testing.T) {
	s, c := serve(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			w.Write(fixture(t, "maitred-status.json"))
		}
	})
	msg := FetchStatus(c, s.URL+"/")
	s.Close()
	if msg.Err != nil || msg.Status == nil || len(msg.Status.Upstreams) != 6 {
		t.Fatalf("%+v", msg)
	}
	msg = FetchStatus(c, "http://127.0.0.1:1")
	if msg.Err == nil || msg.Status != nil || ShortErr(msg.Err) == "" {
		t.Fatalf("unreachable: %+v", msg)
	}
	bad, bc := serve(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>")) })
	defer bad.Close()
	if msg := FetchStatus(bc, bad.URL); msg.Err == nil {
		t.Fatal("garbage /status must be an error")
	}
}

func TestFetchEngineKindsAndHealth(t *testing.T) {
	body := ""
	code := 200
	s, c := serve(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metrics":
			if code != 200 {
				w.WriteHeader(code)
				return
			}
			w.Write([]byte(body))
		case "/health":
			w.Write([]byte("ok"))
		}
	})
	defer s.Close()
	body = string(fixture(t, "radiance.metrics"))
	if m := FetchEngine(c, "e", s.URL); m.Err != nil || m.Reading.Kind != model.KindRadiance {
		t.Fatalf("%+v", m)
	}
	body = string(fixture(t, "llamacpp.metrics")) // same port, another server
	if m := FetchEngine(c, "e", s.URL); m.Reading.Kind != model.KindLlamaCpp {
		t.Fatalf("kind must be re-detected: %+v", m)
	}
	body = ""
	if m := FetchEngine(c, "e", s.URL); m.Err != nil || m.Reading.Kind != model.KindHealth || !m.Reading.Up {
		t.Fatalf("empty body is health-only: %+v", m)
	}
	code = 404
	if m := FetchEngine(c, "e", s.URL); m.Reading.Kind != model.KindHealth {
		t.Fatalf("%+v", m)
	}
	if m := FetchEngine(c, "e", "http://127.0.0.1:1"); m.Err == nil {
		t.Fatal("unreachable engine")
	}
}
