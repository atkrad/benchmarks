// Package protocol defines the line-delimited JSON events exchanged by the
// HTTP/2 idle experiment's server, client, and runner.
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Version is the only protocol version this tree speaks.
const Version = 1

// Event names appear in every encoded message.
const (
	EventReady         = "ready"
	EventProgress      = "progress"
	EventRecheck       = "recheck"
	EventRecheckResult = "recheck_result"
	EventClose         = "close"
	EventClosed        = "closed"
	EventError         = "error"
)

// Message is one protocol event.
type Message interface {
	event() string
}

// Ready is sent by the server when it is listening, and by the client when
// every requested connection is idle.
type Ready struct {
	Version     int    `json:"version"`
	Event       string `json:"event"`
	HTTPSAddr   string `json:"https_addr,omitempty"`
	ControlAddr string `json:"control_addr,omitempty"`
	CertPEM     string `json:"cert_pem,omitempty"`
	GoVersion   string `json:"go_version,omitempty"`
	Connections int    `json:"connections"`
	Retries     int    `json:"retries"`
	Established int    `json:"established"`
}

func (Ready) event() string { return EventReady }

// Progress is how many connections the client has established so far.
type Progress struct {
	Version     int    `json:"version"`
	Event       string `json:"event"`
	Established int    `json:"established"`
	Wanted      int    `json:"wanted"`
}

func (Progress) event() string { return EventProgress }

// Recheck asks the client to send one more request on each held transport.
type Recheck struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
}

func (Recheck) event() string { return EventRecheck }

// RecheckResult reports whether those requests reused the same connections.
type RecheckResult struct {
	Version  int      `json:"version"`
	Event    string   `json:"event"`
	OK       bool     `json:"ok"`
	Reused   int      `json:"reused"`
	Replaced int      `json:"replaced"`
	Errors   int      `json:"errors"`
	Locals   []string `json:"locals,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

func (RecheckResult) event() string { return EventRecheckResult }

// Close asks the client to drop its transports and exit.
type Close struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
}

func (Close) event() string { return EventClose }

// Closed is the client's last message.
type Closed struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
}

func (Closed) event() string { return EventClosed }

// Error reports a failure. The process exits after sending it.
type Error struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
	Message string `json:"message"`
}

func (Error) event() string { return EventError }

// EventOf returns the event name of a decoded message.
func EventOf(msg Message) string { return msg.event() }

// Stats is the server's control-endpoint snapshot. Memory fields are bytes.
// Goroutine and heap figures come from the server process itself.
type Stats struct {
	WallTime      string  `json:"wall_time"`
	Accepted      int64   `json:"accepted"`
	Open          int64   `json:"open"`
	Closed        int64   `json:"closed"`
	H2Requests    int64   `json:"h2_requests"`
	NonH2Requests int64   `json:"non_h2_requests"`
	WriteErrors   int64   `json:"write_errors"`
	ConnRequests  []int64 `json:"conn_requests"`
	Goroutines    int     `json:"goroutines"`
	HeapAlloc     uint64  `json:"heap_alloc"`
	StackInuse    uint64  `json:"stack_inuse"`
	HeapInuse     uint64  `json:"heap_inuse"`
	HeapIdle      uint64  `json:"heap_idle"`
	HeapReleased  uint64  `json:"heap_released"`
	NumGC         uint32  `json:"num_gc"`
	PauseTotalNs  uint64  `json:"pause_total_ns"`
	GoVersion     string  `json:"go_version"`
}

// Encode writes msg as one JSON line.
func Encode(w io.Writer, msg Message) error {
	switch m := msg.(type) {
	case Ready:
		m.Version, m.Event = Version, EventReady
		msg = m
	case Progress:
		m.Version, m.Event = Version, EventProgress
		msg = m
	case Recheck:
		m.Version, m.Event = Version, EventRecheck
		msg = m
	case RecheckResult:
		m.Version, m.Event = Version, EventRecheckResult
		msg = m
	case Close:
		m.Version, m.Event = Version, EventClose
		msg = m
	case Closed:
		m.Version, m.Event = Version, EventClosed
		msg = m
	case Error:
		m.Version, m.Event = Version, EventError
		msg = m
	default:
		return fmt.Errorf("encode protocol: unknown message %T", msg)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("encode protocol: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("encode protocol: %w", err)
	}
	return nil
}

// Decode parses one JSON line. Unknown fields and the wrong version are errors.
func Decode(line []byte) (Message, error) {
	var header struct {
		Version int    `json:"version"`
		Event   string `json:"event"`
	}
	if err := json.Unmarshal(line, &header); err != nil {
		return nil, fmt.Errorf("decode protocol: %w", err)
	}
	if header.Version != Version {
		return nil, fmt.Errorf("decode protocol: version %d, want %d", header.Version, Version)
	}
	var msg Message
	switch header.Event {
	case EventReady:
		msg = &Ready{}
	case EventProgress:
		msg = &Progress{}
	case EventRecheck:
		msg = &Recheck{}
	case EventRecheckResult:
		msg = &RecheckResult{}
	case EventClose:
		msg = &Close{}
	case EventClosed:
		msg = &Closed{}
	case EventError:
		msg = &Error{}
	default:
		return nil, fmt.Errorf("decode protocol: unknown event %q", header.Event)
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(msg); err != nil {
		return nil, fmt.Errorf("decode protocol %s: %w", header.Event, err)
	}
	return msg, nil
}

// AdvanceClient checks the next event the client is allowed to emit.
// The empty state is the start of the exchange.
func AdvanceClient(state, event string) (string, error) {
	allowed := map[string]map[string]struct{}{
		"": {
			EventProgress: {},
			EventReady:    {},
			EventError:    {},
		},
		EventProgress: {
			EventProgress: {},
			EventReady:    {},
			EventError:    {},
		},
		EventReady: {
			EventRecheckResult: {},
			EventError:         {},
		},
		EventRecheckResult: {
			EventClosed: {},
			EventError:  {},
		},
	}
	next, ok := allowed[state]
	if !ok {
		return state, fmt.Errorf("protocol state %q cannot accept %q", state, event)
	}
	if _, ok := next[event]; !ok {
		return state, fmt.Errorf("protocol state %q cannot accept %q", state, event)
	}
	if event == EventError || event == EventClosed {
		return event, nil
	}
	return event, nil
}

// AdvanceCommand checks the next command the runner may send the client.
func AdvanceCommand(state, event string) (string, error) {
	var next string
	switch {
	case state == "" && event == EventRecheck:
		next = EventRecheck
	case state == EventRecheck && event == EventClose:
		next = EventClose
	case event == EventError:
		next = EventError
	default:
		return state, fmt.Errorf("command state %q cannot accept %q", state, event)
	}
	return next, nil
}
