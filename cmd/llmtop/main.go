// Command llmtop is a btop-style terminal monitor for a self-hosted LLM stack.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ewtodd/llmtop/internal/source"
	"github.com/ewtodd/llmtop/internal/state"
	"github.com/ewtodd/llmtop/internal/ui"
)

const defaultMaitred = "http://10.0.0.2:4002"

// listFlag is a repeatable flag.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

type config struct {
	state.Config
	dump, demo bool
}

func parseFlags(args []string, errOut io.Writer) (config, error) {
	fs := flag.NewFlagSet("llmtop", flag.ContinueOnError)
	fs.SetOutput(errOut)
	def := os.Getenv("LLMTOP_MAITRED")
	if def == "" {
		def = defaultMaitred
	}
	var engines, gpuNames listFlag
	c := config{}
	fs.StringVar(&c.Maitred, "maitred", def, "maitred `URL` (default $LLMTOP_MAITRED, else "+defaultMaitred+")")
	fs.DurationVar(&c.Interval, "interval", time.Second, "poll interval")
	port := fs.Int("node-port", 9100, "node_exporter port on each engine host")
	fs.BoolVar(&c.NoHosts, "no-hosts", false, "do not read node_exporter (no host or GPU section)")
	fs.Var(&engines, "engine", "add an engine not behind maitred, `NAME=URL` (server root, no /v1); repeatable")
	fs.Var(&gpuNames, "gpu-name", "display label for a GPU, `HOST/PCI=LABEL`; repeatable")
	fs.BoolVar(&c.dump, "dump", false, "poll every source twice, print the snapshot as JSON and exit")
	fs.BoolVar(&c.demo, "demo", false, "synthetic animated data, no network")
	if err := fs.Parse(args); err != nil {
		return c, err
	}
	if fs.NArg() > 0 {
		return c, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if c.Interval < 100*time.Millisecond {
		return c, fmt.Errorf("--interval %v is too short (at least 100ms)", c.Interval)
	}
	if *port < 1 || *port > 65535 {
		return c, fmt.Errorf("--node-port %d out of range", *port)
	}
	c.NodePort = fmt.Sprint(*port)
	c.Maitred = strings.TrimRight(c.Maitred, "/")
	for _, e := range engines {
		name, url, ok := strings.Cut(e, "=")
		if !ok || name == "" || !strings.HasPrefix(url, "http") {
			return c, fmt.Errorf("--engine %q: want NAME=http://host:port", e)
		}
		c.Extra = append(c.Extra, source.ExtraEngine{Name: name, Root: url})
	}
	c.GPUNames = map[string]string{}
	for _, g := range gpuNames {
		key, label, ok := strings.Cut(g, "=")
		if !ok || !strings.Contains(key, "/") || label == "" {
			return c, fmt.Errorf("--gpu-name %q: want HOST/PCI=LABEL", g)
		}
		c.GPUNames[strings.ToLower(key)] = label
	}
	return c, nil
}

func main() {
	c, err := parseFlags(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "llmtop:", err)
		os.Exit(1)
	}
	if c.dump {
		if err := dump(c, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "llmtop:", err)
			os.Exit(1)
		}
		return
	}
	opt := ui.Options{Demo: c.demo, Interval: c.Interval, Client: source.NewClient()}
	if !c.demo {
		opt.State = state.New(c.Config)
	}
	opt.Theme = ui.StartTheme(os.Stdout)
	if _, err := tea.NewProgram(ui.New(opt), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "llmtop:", err)
		os.Exit(1)
	}
}

// dump polls every source twice, interval apart, and prints the snapshot.
func dump(c config, out io.Writer) error {
	st := state.New(c.Config)
	client := source.NewClient()
	if c.demo {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(ui.DemoSnapshot(time.Now()))
	}
	st.Round(client)
	time.Sleep(c.Interval)
	st.Round(client)
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(st.Snapshot(time.Now()))
}
