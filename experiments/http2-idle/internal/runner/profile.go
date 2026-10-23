package runner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ProfileSummary reports goroutine counts from a grouped pprof profile.
// HTTP2Serve and ReadFrames count goroutines whose stacks contain matching
// HTTP/2 functions.
type ProfileSummary struct {
	Goroutines int `json:"goroutines"`
	HTTP2Serve int `json:"http2_serve_frames"`
	ReadFrames int `json:"read_frames"`
}

// profileGroup is one shared stack. count is how many goroutines use it.
type profileGroup struct {
	count    int
	hasServe bool
	hasRead  bool
}

var (
	profileTotalHeader = regexp.MustCompile(`^goroutine profile: total ([0-9]+)\s*$`)
	profileGroupStart  = regexp.MustCompile(`^[0-9]+ @`)
	profileGroupHeader = regexp.MustCompile(`^([1-9][0-9]*) @ 0x[0-9a-fA-F]+(?: 0x[0-9a-fA-F]+)*\s*$`)
	profileFrameSymbol = regexp.MustCompile(`^#\s+\S+\s+(\S+)`)
)

// SummarizeProfile parses a debug=1 goroutine profile.
// debug=1 prints each distinct stack once, with a count of the goroutines
// that share it. An empty profile, any other format, a malformed group
// header, or a count that does not add up to the profile total is an error.
// A profile with no matching serve frames is valid and reports HTTP2Serve 0.
func SummarizeProfile(text string) (ProfileSummary, error) {
	if strings.TrimSpace(text) == "" {
		return ProfileSummary{}, fmt.Errorf("summarize profile: empty profile")
	}

	var sum ProfileSummary
	var current *profileGroup
	declared := -1

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
		count, isGroup, err := profileGroupCount(line)
		if err != nil {
			return ProfileSummary{}, err
		}
		if isGroup {
			sum.addGroup(current)
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
	sum.addGroup(current)
	if sum.Goroutines != declared {
		return ProfileSummary{}, fmt.Errorf("summarize profile: groups sum to %d, profile total is %d", sum.Goroutines, declared)
	}
	return sum, nil
}

func (s *ProfileSummary) addGroup(g *profileGroup) {
	if g == nil {
		return
	}
	s.Goroutines += g.count
	if g.hasServe {
		s.HTTP2Serve += g.count
	}
	if g.hasRead {
		s.ReadFrames += g.count
	}
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

func profileGroupCount(line string) (int, bool, error) {
	if !profileGroupStart.MatchString(line) {
		return 0, false, nil
	}
	match := profileGroupHeader.FindStringSubmatch(line)
	if match == nil {
		return 0, false, fmt.Errorf("summarize profile: malformed group header %q", clip(line, 80))
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false, fmt.Errorf("summarize profile: group count overflows int")
	}
	return n, true, nil
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
