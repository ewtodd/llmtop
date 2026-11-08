package source

import (
	"net"
	"net/url"
	"strings"

	"github.com/ewtodd/llmtop/internal/model"
)

// Engine is one LLM engine llmtop scrapes: an upstream of maitred that is not a
// hosted API, or one added with --engine.
type Engine struct {
	Name     string
	Root     string // server root, without /v1
	Hostname string // from the URL
	Host     string // the host group it belongs to
	Serving  bool   // maitred's verdict; --engine engines are always serving
	Reason   string
	InFlight int
	Slots    int // 0 when unknown
	Display  string
	Mode     string // first mode that lists the upstream
	Extra    bool   // from --engine, not behind maitred
	Utility  bool   // every model on it is hidden (titles, embeddings): a line, never a card
}

// Host is a machine with engines on it; its node_exporter holds the hardware.
type Host struct {
	Name    string
	NodeURL string
	Engines []string
}

// ExtraEngine is a --engine NAME=URL.
type ExtraEngine struct{ Name, Root string }

func hostnameOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Discover derives the engines and the hosts from maitred's /status and the
// --engine flags. st may be nil (maitred unreachable, or no /status).
//
// Hosts group engines by maitred's host. An engine with no host takes its URL's
// hostname, and joins a named host that has an engine on the same hostname
// (fiona's llama and embed upstreams have no mode but share fiona's address).
func Discover(st *model.Status, extra []ExtraEngine, nodePort string) ([]Engine, []Host) {
	var engines []Engine
	seen := map[string]bool{}
	if st != nil {
		modeOf := map[string]string{}
		for _, m := range st.Modes {
			for _, u := range m.Upstreams {
				if _, ok := modeOf[u]; !ok {
					modeOf[u] = m.Name
				}
			}
		}
		for _, u := range st.Upstreams {
			if u.Hosted {
				continue
			}
			e := Engine{
				Name: u.Name, Root: EngineRoot(u.BaseURL), Serving: u.Serving, Reason: u.Reason,
				InFlight: u.InFlight, Mode: modeOf[u.Name],
			}
			e.Hostname = hostnameOf(u.BaseURL)
			if u.Host != nil {
				e.Host = *u.Host
			}
			if u.Slots != nil {
				e.Slots = *u.Slots
			}
			for _, m := range u.Models {
				if !m.Hidden {
					e.Display = m.DisplayName
					break
				}
			}
			if e.Display == "" && len(u.Models) > 0 {
				e.Display = u.Models[0].DisplayName
				e.Utility = true
			}
			engines = append(engines, e)
			seen[e.Name] = true
		}
	}
	for _, x := range extra {
		if seen[x.Name] {
			continue
		}
		engines = append(engines, Engine{
			Name: x.Name, Root: strings.TrimRight(strings.TrimSuffix(strings.TrimRight(x.Root, "/"), "/v1"), "/"),
			Hostname: hostnameOf(x.Root), Serving: true, Extra: true,
		})
	}

	// Named hosts first, so an unnamed engine can join one by hostname.
	var hosts []Host
	index := map[string]int{}
	hostnames := map[string]map[string]bool{} // host -> hostnames of its engines
	add := func(name, hostname string) int {
		i, ok := index[name]
		if !ok {
			i = len(hosts)
			index[name] = i
			hosts = append(hosts, Host{Name: name})
			hostnames[name] = map[string]bool{}
		}
		if hosts[i].NodeURL == "" && hostname != "" {
			hosts[i].NodeURL = "http://" + net.JoinHostPort(hostname, nodePort) + "/metrics"
		}
		hostnames[name][hostname] = true
		return i
	}
	for i := range engines {
		if engines[i].Host != "" {
			add(engines[i].Host, engines[i].Hostname)
		}
	}
	for i := range engines {
		e := &engines[i]
		if e.Host == "" {
			for _, h := range hosts {
				if hostnames[h.Name][e.Hostname] {
					e.Host = h.Name
					break
				}
			}
		}
		if e.Host == "" {
			e.Host = e.Hostname
			if e.Host == "" {
				e.Host = "?"
			}
			add(e.Host, e.Hostname)
		}
		h := &hosts[index[e.Host]]
		h.Engines = append(h.Engines, e.Name)
	}
	return engines, hosts
}
