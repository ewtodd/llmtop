package prom

import (
	"math"
	"os"
	"testing"
)

func TestParseUnholyFixture(t *testing.T) {
	body, err := os.ReadFile("../../testdata/unholy-glm.metrics")
	if err != nil {
		t.Fatal(err)
	}
	s := Parse(body)
	if v, ok := s.Value("unholy_decode_tokens_total"); !ok || v != 15601 {
		t.Fatalf("decode tokens = %v %v", v, ok)
	}
	if v, _ := s.Value("unholy_busy_seconds_total"); v != 674.027 {
		t.Fatalf("busy = %v", v)
	}
	if !s.HasPrefix("unholy_") || s.HasPrefix("vllm:") {
		t.Fatal("prefix detection")
	}
	if s.Len() != 18 {
		t.Fatalf("len = %d", s.Len())
	}
}

func TestParseNodeFixtureLabels(t *testing.T) {
	body, err := os.ReadFile("../../testdata/node-son-of-anton.prom")
	if err != nil {
		t.Fatal(err)
	}
	s := Parse(body)
	var found bool
	for _, smp := range s.Samples("node_hwmon_chip_names") {
		if smp.Labels["chip"] == "0000:c6:00_0_0000:c7:00_0" && smp.Labels["chip_name"] == "amdgpu" {
			found = true
		}
	}
	if !found {
		t.Fatal("amdgpu chip not found")
	}
	if v, ok := s.Value("node_load1"); !ok || v != 1.18 {
		t.Fatalf("load1 = %v %v", v, ok)
	}
}

func TestParseEdgeCases(t *testing.T) {
	body := "# HELP x help\n# TYPE x gauge\n" +
		"a{k=\"he said \\\"hi\\\" \\\\ ok\\nline\"} 1.5\n" +
		"b NaN\n" +
		"c +Inf\n" +
		"d -Inf\n" +
		"e{} 7 1700000000\n" +
		"f{x=\"1\",y=\"2\",} 3\n" +
		"bad line here\n" +
		"g{unterminated=\"x} 1\n" +
		"h 1e3\n" +
		"last 42" // no trailing newline
	s := Parse([]byte(body))
	if got := s.Samples("a")[0].Labels["k"]; got != "he said \"hi\" \\ ok\nline" {
		t.Fatalf("escaped label = %q", got)
	}
	if _, ok := s.Value("b"); ok {
		t.Fatal("NaN must not be a value")
	}
	if len(s.Samples("b")) != 1 || !math.IsNaN(s.Samples("b")[0].Value) {
		t.Fatal("NaN sample kept")
	}
	if v := s.Samples("c")[0].Value; !math.IsInf(v, 1) {
		t.Fatalf("c = %v", v)
	}
	if _, ok := s.Value("c"); ok {
		t.Fatal("Inf must not be a value")
	}
	if v, _ := s.Value("e"); v != 7 {
		t.Fatalf("e = %v (timestamp must be ignored)", v)
	}
	if s.Samples("f")[0].Labels["y"] != "2" {
		t.Fatal("trailing comma in labels")
	}
	if len(s.Samples("bad")) != 0 || len(s.Samples("g")) != 0 {
		t.Fatal("malformed lines must be skipped")
	}
	if v, _ := s.Value("h"); v != 1000 {
		t.Fatalf("h = %v", v)
	}
	if v, ok := s.Value("last"); !ok || v != 42 {
		t.Fatalf("last line without newline = %v %v", v, ok)
	}
	if got, ok := Parse([]byte("m{a=\"1\"} 2\nm{a=\"2\"} 3\n")).Sum("m"); !ok || got != 5 {
		t.Fatalf("sum = %v", got)
	}
	if Parse(nil).Len() != 0 {
		t.Fatal("empty body")
	}
}
