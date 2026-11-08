// Package source fetches and decodes the three kinds of endpoint llmtop reads.
// Every fetch is one function call with its own timeout that returns a message;
// callers run them in goroutines (the Bubble Tea loop never does I/O).
package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ewtodd/llmtop/internal/model"
	"github.com/ewtodd/llmtop/internal/prom"
)

// Timeout bounds every source.
const Timeout = 800 * time.Millisecond

const maxBody = 16 << 20

// NewClient is the HTTP client of all sources.
func NewClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
	}}
}

// StatusMsg is the outcome of polling maitred.
type StatusMsg struct {
	At       time.Time
	Status   *model.Status
	NoStatus bool                // 404: an older maitred; Models holds the /v1/models fallback
	Models   []model.ModelStatus // fallback only
	Err      error
}

// EngineMsg is the outcome of scraping one engine.
type EngineMsg struct {
	Name    string
	At      time.Time
	Reading model.Reading
	Err     error
}

// NodeMsg is the outcome of scraping one host's node_exporter.
type NodeMsg struct {
	Host string
	At   time.Time
	Node model.Node
	Err  error
}

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("HTTP %d", int(e)) }

func get(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return body, statusError(resp.StatusCode)
	}
	return body, nil
}

func isStatus(err error, code int) bool {
	var se statusError
	return errors.As(err, &se) && int(se) == code
}

// ShortErr is an error as one short phrase for the screen.
func ShortErr(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case strings.Contains(err.Error(), "connection refused"):
		return "connection refused"
	}
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i >= 0 && strings.Contains(s, "dial tcp") {
		s = s[i+2:]
	}
	return s
}

// FetchStatus polls maitred. base is its root URL.
func FetchStatus(c *http.Client, base string) StatusMsg {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	base = strings.TrimRight(base, "/")
	msg := StatusMsg{At: time.Now()}
	body, err := get(ctx, c, base+"/status")
	switch {
	case err == nil:
		var st model.Status
		if jerr := json.Unmarshal(body, &st); jerr != nil {
			msg.Err = fmt.Errorf("bad /status: %w", jerr)
		} else {
			msg.Status = &st
		}
	case isStatus(err, http.StatusNotFound):
		msg.NoStatus = true
		body, err = get(ctx, c, base+"/v1/models")
		if err != nil {
			msg.Err = err
			break
		}
		var list struct {
			Data []model.ModelStatus `json:"data"`
		}
		if jerr := json.Unmarshal(body, &list); jerr != nil {
			msg.Err = fmt.Errorf("bad /v1/models: %w", jerr)
			break
		}
		msg.Models = list.Data
	default:
		msg.Err = err
	}
	return msg
}

// FetchEngine scrapes root/metrics and falls back to root/health. The kind is
// detected on every call.
func FetchEngine(c *http.Client, name, root string) EngineMsg {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	msg := EngineMsg{Name: name, At: time.Now()}
	body, err := get(ctx, c, root+"/metrics")
	if err == nil {
		if r, ok := Normalize(prom.Parse(body), msg.At); ok {
			msg.Reading = r
			return msg
		}
	}
	// A non-200, an empty body or names we do not know: health only.
	if _, herr := get(ctx, c, root+"/health"); herr != nil {
		msg.Err = herr
		return msg
	}
	msg.Reading = model.Reading{At: msg.At, Kind: model.KindHealth, Up: true}
	return msg
}

// FetchNode scrapes a node_exporter.
func FetchNode(c *http.Client, host, url string) NodeMsg {
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	msg := NodeMsg{Host: host, At: time.Now()}
	body, err := get(ctx, c, url)
	if err != nil {
		msg.Err = err
		return msg
	}
	msg.Node = ParseNode(prom.Parse(body), msg.At)
	return msg
}
