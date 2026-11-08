package rates

import (
	"time"

	"github.com/ewtodd/llmtop/internal/model"
)

// NodeRates are the figures of a host that need two scrapes.
type NodeRates struct {
	CPU   model.Opt          // percent busy, all CPUs
	Power map[string]float64 // watts by hwmon chip, from an energy counter (xe)
}

// NodeTracker derives CPU use and xe power from node_exporter scrapes.
type NodeTracker struct {
	interval time.Duration
	hist     []model.Node
}

// NewNodeTracker makes a tracker for a host polled every interval.
func NewNodeTracker(interval time.Duration) *NodeTracker { return &NodeTracker{interval: interval} }

// Add records a scrape and returns the rates over the window.
func (t *NodeTracker) Add(n model.Node) NodeRates {
	if k := len(t.hist); k > 0 && (n.CPUTotal < t.hist[k-1].CPUTotal) {
		t.hist = nil // the host rebooted
	}
	t.hist = append(t.hist, n)
	w := max(2*t.interval, 2*time.Second)
	for len(t.hist) > 2 && t.hist[1].At.Before(n.At.Add(-w-t.interval/4)) {
		t.hist = t.hist[1:]
	}
	out := NodeRates{Power: map[string]float64{}}
	if len(t.hist) < 2 {
		return out
	}
	base := t.hist[0]
	dt := n.At.Sub(base.At).Seconds()
	if dt <= 0 {
		return out
	}
	if dTotal := n.CPUTotal - base.CPUTotal; dTotal > 0 {
		out.CPU = model.Some(clamp(100*(1-(n.CPUIdle-base.CPUIdle)/dTotal), 0, 100))
	}
	prev := map[string]model.GPU{}
	for _, g := range base.GPUs {
		prev[g.Chip] = g
	}
	for _, g := range n.GPUs {
		if p, ok := prev[g.Chip]; ok && g.Energy.OK && p.Energy.OK && g.Energy.V >= p.Energy.V {
			out.Power[g.Chip] = (g.Energy.V - p.Energy.V) / dt
		}
	}
	return out
}
