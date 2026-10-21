package runner

import "strings"

// ProfileSummary is a diagnostic reading of a goroutine dump.
// Stack text changes between Go versions, so this is not a pass/fail check.
type ProfileSummary struct {
	Goroutines int `json:"goroutines"`
	HTTP2Serve int `json:"http2_serve_frames"`
	ReadFrames int `json:"read_frames"`
}

// SummarizeProfile counts goroutine headers and a few HTTP/2 frame names.
// A zero HTTP/2 count means the symbols were not recognized, not that the
// server is idle.
func SummarizeProfile(text string) ProfileSummary {
	var sum ProfileSummary
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "goroutine ") {
			sum.Goroutines++
		}
		if strings.Contains(line, "http2.(*serverConn).serve") {
			sum.HTTP2Serve++
		}
		if strings.Contains(line, "readFrames") {
			sum.ReadFrames++
		}
	}
	return sum
}
