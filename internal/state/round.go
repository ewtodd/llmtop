package state

import (
	"net/http"
	"sync"

	"github.com/ewtodd/llmtop/internal/source"
)

// Round polls every source once and applies the results: maitred first (it
// says which engines and hosts exist), then all engines and nodes at once,
// each in its own goroutine. It blocks; the TUI does not use it (it issues the
// same fetches as commands), --dump and the tests do.
func (s *State) Round(c *http.Client) {
	s.Apply(source.FetchStatus(c, s.cfg.Maitred))
	engines, nodes := s.Targets()
	out := make(chan any, len(engines)+len(nodes))
	var wg sync.WaitGroup
	for _, e := range engines {
		wg.Add(1)
		go func() { defer wg.Done(); out <- source.FetchEngine(c, e.Name, e.URL) }()
	}
	for _, n := range nodes {
		wg.Add(1)
		go func() { defer wg.Done(); out <- source.FetchNode(c, n.Name, n.URL) }()
	}
	wg.Wait()
	close(out)
	for m := range out {
		s.Apply(m)
	}
}
