package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseFlags(t *testing.T) {
	t.Setenv("LLMTOP_MAITRED", "")
	c, err := parseFlags(nil, io.Discard)
	if err != nil || c.Maitred != defaultMaitred || c.Interval != time.Second || c.NodePort != "9100" || c.NoHosts {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	t.Setenv("LLMTOP_MAITRED", "http://env:1/")
	c, _ = parseFlags(nil, io.Discard)
	if c.Maitred != "http://env:1" {
		t.Fatalf("env default: %s", c.Maitred)
	}
	c, err = parseFlags([]string{
		"--maitred", "http://m:2", "--interval", "500ms", "--node-port", "9200", "--no-hosts",
		"--engine", "a=http://h:1", "--engine", "b=http://h:2/v1",
		"--gpu-name", `son-of-anton/0000:c7:00.0=R9700 #0`, "--dump", "--demo",
	}, io.Discard)
	if err != nil || c.Maitred != "http://m:2" || c.Interval != 500*time.Millisecond || c.NodePort != "9200" || !c.NoHosts || !c.dump || !c.demo {
		t.Fatalf("%+v %v", c, err)
	}
	if len(c.Extra) != 2 || c.Extra[1].Name != "b" || c.GPUNames["son-of-anton/0000:c7:00.0"] != "R9700 #0" {
		t.Fatalf("%+v", c)
	}
	for _, bad := range [][]string{
		{"--nope"}, {"extra"}, {"--engine", "noequals"}, {"--engine", "a=notaurl"}, {"--gpu-name", "x=y"},
		{"--interval", "1ms"}, {"--node-port", "0"},
	} {
		if _, err := parseFlags(bad, io.Discard); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}

func TestDumpUnreachableMaitred(t *testing.T) {
	c, _ := parseFlags([]string{"--maitred", "http://127.0.0.1:1", "--interval", "100ms"}, io.Discard)
	var out bytes.Buffer
	if err := dump(c, &out); err != nil {
		t.Fatal(err)
	}
	var snap map[string]any
	if err := json.Unmarshal(out.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap["maitred"].(map[string]any)["state"] != "unreachable" {
		t.Fatalf("%s", out.String())
	}
}

func TestDumpExtraEngineWithoutMaitred(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			n++
			fmt.Fprintf(w, "llamacpp:tokens_predicted_total %d\nllamacpp:tokens_predicted_seconds_total %d\nllamacpp:requests_processing 1\n", 100*n, 2*n)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	c, _ := parseFlags([]string{"--maitred", "http://127.0.0.1:1", "--interval", "100ms", "--no-hosts", "--engine", "gufo=" + srv.URL}, io.Discard)
	var out bytes.Buffer
	if err := dump(c, &out); err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Engines []struct {
			Name   string
			Kind   string
			Decode struct{ Tok_s *float64 }
		}
	}
	if err := json.Unmarshal(out.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Engines) != 1 || snap.Engines[0].Kind != "llamacpp" || snap.Engines[0].Decode.Tok_s == nil || *snap.Engines[0].Decode.Tok_s != 50 {
		t.Fatalf("%s", out.String())
	}
}
