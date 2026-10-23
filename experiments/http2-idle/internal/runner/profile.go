package runner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProfileSummary counts goroutines in a debug=1 profile.
// That format groups identical stacks and prints the group size first.
// Goroutines is the sum of those sizes. HTTP2Serve and ReadFrames are the
// same kind of count for groups whose stack contains a matching HTTP/2
// function. A group is counted once per category, even when several frames
// match. The JSON names predate that weighting: the values are goroutine
// counts, not a count of text lines. Symbol names are a diagnostic clue and
// can change between Go versions. They are not the benchmark measurement.
type ProfileSummary struct {
	Goroutines int `json:"goroutines"`
	HTTP2Serve int `json:"http2_serve_frames"`
	ReadFrames int `json:"read_frames"`
}

// profileGroup is one debug=1 stack, shared by count goroutines.
type profileGroup struct {
	count    int
	hasServe bool
	hasRead  bool
}

var (
	profileTotalHeader = regexp.MustCompile(`^goroutine profile: total ([0-9]+)\s*$`)
	profileGroupHeader = regexp.MustCompile(`^([1-9][0-9]*) @`)
	profileFrameSymbol = regexp.MustCompile(`^#\s+\S+\s+(\S+)`)
)

// SummarizeProfile parses text from /debug/pprof/goroutine?debug=1.
// It returns an error for an empty profile, a debug=2 trace, a malformed
// line, or a group total that disagrees with the header. A valid profile
// with no HTTP/2 serve frames returns HTTP2Serve 0 and a nil error.
func SummarizeProfile(text string) (ProfileSummary, error) {
	if strings.TrimSpace(text) == "" {
		return ProfileSummary{}, fmt.Errorf("summarize profile: empty profile")
	}

	var sum ProfileSummary
	var current *profileGroup
	declared := -1
	summed := 0

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if declared < 0 {
			total, ok := profileTotal(line)
			if !ok {
				return ProfileSummary{}, fmt.Errorf("summarize profile: unsupported format (want a debug=1 grouped profile), got %q", clip(line, 80))
			}
			declared = total
			continue
		}
		if count, ok := profileGroupCount(line); ok {
			if err := sum.addGroup(current); err != nil {
				return ProfileSummary{}, err
			}
			if current != nil {
				summed += current.count
			}
			current = &profileGroup{count: count}
			continue
		}
		symbol, ok := profileSymbol(line)
		if !ok {
			return ProfileSummary{}, fmt.Errorf("summarize profile: malformed line %q", clip(line, 80))
		}
		if current == nil {
			return ProfileSummary{}, fmt.Errorf("summarize profile: stack frame before a group header")
		}
		switch {
		case isHTTP2Serve(symbol):
			current.hasServe = true
		case isHTTP2Read(symbol):
			current.hasRead = true
		}
	}
	if declared < 0 {
		return ProfileSummary{}, fmt.Errorf("summarize profile: unsupported format (want a debug=1 grouped profile)")
	}
	if err := sum.addGroup(current); err != nil {
		return ProfileSummary{}, err
	}
	if current != nil {
		summed += current.count
	}
	if summed != declared {
		return ProfileSummary{}, fmt.Errorf("summarize profile: groups sum to %d, profile total is %d", summed, declared)
	}
	return sum, nil
}

func (s *ProfileSummary) addGroup(g *profileGroup) error {
	if g == nil {
		return nil
	}
	if g.count < 1 {
		return fmt.Errorf("summarize profile: group count must be positive")
	}
	s.Goroutines += g.count
	if g.hasServe {
		s.HTTP2Serve += g.count
	}
	if g.hasRead {
		s.ReadFrames += g.count
	}
	return nil
}

func profileTotal(line string) (int, bool) {
	match := profileTotalHeader.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func profileGroupCount(line string) (int, bool) {
	match := profileGroupHeader.FindStringSubmatch(line)
	if match == nil {
		return 0, false
	}
	n, err := strconv.Atoi(match[1])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

func profileSymbol(line string) (string, bool) {
	match := profileFrameSymbol.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	symbol := match[1]
	if i := strings.LastIndex(symbol, "+"); i > 0 {
		symbol = symbol[:i]
	}
	return symbol, true
}

func isHTTP2Serve(symbol string) bool {
	switch symbol {
	case "net/http/internal/http2.(*serverConn).serve",
		"net/http/internal/http2.(*serverConn).serveLoop",
		"golang.org/x/net/http2.(*serverConn).serve",
		"golang.org/x/net/http2.(*serverConn).serveLoop":
		return true
	default:
		return false
	}
}

func isHTTP2Read(symbol string) bool {
	switch symbol {
	case "net/http/internal/http2.(*serverConn).readFrames",
		"golang.org/x/net/http2.(*serverConn).readFrames":
		return true
	default:
		return false
	}
}
