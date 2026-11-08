// Package prom parses the Prometheus text exposition format, as much of it as
// llmtop reads: samples with labels. Comments, timestamps and exemplars are
// skipped; a line that does not parse is skipped, never an error, because a
// scrape with one odd line should still show the rest.
package prom

import (
	"math"
	"strconv"
	"strings"
)

// Sample is one line of the exposition: name{labels} value.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Set is a parsed scrape, indexed by metric name.
type Set struct {
	byName map[string][]Sample
	n      int
}

// Parse reads an exposition body.
func Parse(body []byte) *Set {
	s := &Set{byName: map[string][]Sample{}}
	for _, line := range strings.Split(string(body), "\n") {
		if smp, ok := parseLine(line); ok {
			s.byName[smp.Name] = append(s.byName[smp.Name], smp)
			s.n++
		}
	}
	return s
}

// Len is the number of samples.
func (s *Set) Len() int { return s.n }

// Samples are all the series of a metric name, in body order.
func (s *Set) Samples(name string) []Sample { return s.byName[name] }

// Names lists the metric names present.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.byName))
	for n := range s.byName {
		out = append(out, n)
	}
	return out
}

// HasPrefix reports whether any metric name starts with prefix.
func (s *Set) HasPrefix(prefix string) bool {
	for n := range s.byName {
		if strings.HasPrefix(n, prefix) {
			return true
		}
	}
	return false
}

// Value is the value of a metric's first series, when present and finite.
func (s *Set) Value(name string) (float64, bool) {
	for _, smp := range s.byName[name] {
		if isFinite(smp.Value) {
			return smp.Value, true
		}
	}
	return 0, false
}

// Sum adds up a metric's finite series (a histogram's _sum over engines, say).
func (s *Set) Sum(name string) (float64, bool) {
	var total float64
	var found bool
	for _, smp := range s.byName[name] {
		if isFinite(smp.Value) {
			total += smp.Value
			found = true
		}
	}
	return total, found
}

// Label is the value of a label on a metric's first series carrying it.
func (s *Set) Label(name, label string) string {
	for _, smp := range s.byName[name] {
		if v, ok := smp.Labels[label]; ok {
			return v
		}
	}
	return ""
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func parseLine(line string) (Sample, bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' {
		return Sample{}, false
	}
	i := 0
	for i < len(line) && line[i] != '{' && line[i] != ' ' && line[i] != '\t' {
		i++
	}
	smp := Sample{Name: line[:i]}
	if smp.Name == "" {
		return Sample{}, false
	}
	rest := line[i:]
	if strings.HasPrefix(rest, "{") {
		labels, after, ok := parseLabels(rest)
		if !ok {
			return Sample{}, false
		}
		smp.Labels, rest = labels, after
	}
	fields := strings.Fields(rest) // value, then an optional timestamp
	if len(fields) == 0 {
		return Sample{}, false
	}
	v, err := strconv.ParseFloat(fields[0], 64) // accepts NaN, +Inf, -Inf
	if err != nil {
		return Sample{}, false
	}
	smp.Value = v
	return smp, true
}

// parseLabels reads {a="b",c="d\"e"} and returns the labels and the text after
// the closing brace.
func parseLabels(s string) (map[string]string, string, bool) {
	labels := map[string]string{}
	i := 1
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			return nil, "", false
		}
		if s[i] == '}' {
			return labels, s[i+1:], true
		}
		start := i
		for i < len(s) && s[i] != '=' {
			i++
		}
		if i >= len(s) {
			return nil, "", false
		}
		key := strings.TrimSpace(s[start:i])
		i++ // '='
		if i >= len(s) || s[i] != '"' {
			return nil, "", false
		}
		i++
		var val strings.Builder
		closed := false
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					val.WriteByte('\n')
				default: // \\ and \" and anything else keep the character
					val.WriteByte(s[i+1])
				}
				i += 2
				continue
			}
			if c == '"' {
				closed = true
				i++
				break
			}
			val.WriteByte(c)
			i++
		}
		if !closed {
			return nil, "", false
		}
		labels[key] = val.String()
	}
}
