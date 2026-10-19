package protocol

import (
	"bytes"
	"strings"
	"testing"
)

func TestRoundTripAndUnknownField(t *testing.T) {
	var buf bytes.Buffer
	msg := Ready{HTTPSAddr: "127.0.0.1:1", GoVersion: "go1.27.2", Connections: 0}
	if err := Encode(&buf, msg); err != nil {
		t.Fatal(err)
	}
	got, err := Decode(bytes.TrimSpace(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	ready, ok := got.(*Ready)
	if !ok {
		t.Fatalf("type %T", got)
	}
	if ready.Version != Version || ready.Event != EventReady || ready.HTTPSAddr != msg.HTTPSAddr {
		t.Fatalf("ready = %+v", ready)
	}

	_, err = Decode([]byte(`{"version":1,"event":"ready","nope":true}`))
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
	_, err = Decode([]byte(`{"version":9,"event":"ready"}`))
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("err = %v", err)
	}
	_, err = Decode([]byte(`{"version":1,"event":"nope"}`))
	if err == nil {
		t.Fatal("expected unknown event to fail")
	}
	_, err = Decode([]byte(`{`))
	if err == nil {
		t.Fatal("expected malformed json to fail")
	}
}

func TestClientTransitions(t *testing.T) {
	state := ""
	for _, event := range []string{EventProgress, EventProgress, EventReady, EventRecheckResult, EventClosed} {
		next, err := AdvanceClient(state, event)
		if err != nil {
			t.Fatalf("from %q event %q: %v", state, event, err)
		}
		state = next
	}
	if _, err := AdvanceClient(EventReady, EventProgress); err == nil {
		t.Fatal("progress after ready should fail")
	}
	if _, err := AdvanceClient(EventClosed, EventReady); err == nil {
		t.Fatal("ready after closed should fail")
	}
	next, err := AdvanceClient(EventReady, EventError)
	if err != nil || next != EventError {
		t.Fatalf("error transition = %q, %v", next, err)
	}
}

func TestCommandTransitions(t *testing.T) {
	state, err := AdvanceCommand("", EventRecheck)
	if err != nil {
		t.Fatal(err)
	}
	state, err = AdvanceCommand(state, EventClose)
	if err != nil || state != EventClose {
		t.Fatalf("state = %q, %v", state, err)
	}
	if _, err := AdvanceCommand("", EventClose); err == nil {
		t.Fatal("close before recheck should fail")
	}
}

func TestEncodeRejectsUnknown(t *testing.T) {
	if err := Encode(ioDiscard{}, unknown{}); err == nil {
		t.Fatal("expected an unknown message to fail")
	}
}

type unknown struct{}

func (unknown) event() string { return "nope" }

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
