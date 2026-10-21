package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv("BENCHKIT_PROCESS_HELPER") == "1" {
		runHelper()
		return
	}
	os.Exit(m.Run())
}

func runHelper() {
	mode := "ok"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	switch mode {
	case "ready-exit":
		fmt.Println(`{"event":"ready"}`)
		os.Exit(0)
	case "exit":
		code := 1
		if len(os.Args) > 2 {
			parsed, err := strconv.Atoi(os.Args[2])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			code = parsed
		}
		fmt.Fprintln(os.Stderr, "child failed")
		os.Exit(code)
	case "badjson":
		fmt.Println("this is not json")
		os.Exit(0)
	case "silent":
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "flood":
		chunk := bytes.Repeat([]byte("a"), 1024)
		for range 64 {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(1)
			}
		}
		os.Exit(0)
	case "group":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "group helper needs a marker path")
			os.Exit(2)
		}
		cmd := exec.Command(os.Args[0], "sleep")
		cmd.Env = os.Environ()
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[2], []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(`{"event":"ready"}`)
		_ = cmd.Wait()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown helper %q\n", mode)
		os.Exit(2)
	}
}

func helperSpec(t *testing.T, mode string, args ...string) Spec {
	t.Helper()
	return Spec{
		Path: os.Args[0],
		Args: append([]string{mode}, args...),
		Env:  append(os.Environ(), "BENCHKIT_PROCESS_HELPER=1"),
	}
}

func TestStartReadyAndExit(t *testing.T) {
	ctx := t.Context()
	proc, err := Start(ctx, helperSpec(t, "ready-exit"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proc.ReadEvent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Event != "ready" {
		t.Fatalf("event = %q", msg.Event)
	}
	res, err := proc.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if res.Stdout == "" {
		t.Fatal("stdout was not captured")
	}
}

func TestNonZeroExit(t *testing.T) {
	proc, err := Start(t.Context(), helperSpec(t, "exit", "7"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := proc.Wait()
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit = %d, stderr = %q, err = %v", res.ExitCode, res.Stderr, err)
	}
	if res.Stderr == "" {
		t.Fatal("stderr was dropped")
	}
}

func TestEarlyEOF(t *testing.T) {
	proc, err := Start(t.Context(), helperSpec(t, "silent"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = proc.ReadEvent(t.Context())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadEvent err = %v, want EOF", err)
	}
	if _, err := proc.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedJSON(t *testing.T) {
	proc, err := Start(t.Context(), helperSpec(t, "badjson"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = proc.ReadEvent(t.Context())
	if err == nil {
		t.Fatal("expected a malformed json error")
	}
	res, waitErr := proc.Wait()
	if waitErr != nil {
		t.Fatal(waitErr)
	}
	if res.Stdout == "" {
		t.Fatal("malformed line was not kept in the log")
	}
}

func TestTimeoutKillsChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	proc, err := Start(ctx, helperSpec(t, "sleep"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := proc.Wait()
	if err == nil {
		t.Fatal("expected the timeout to fail the wait")
	}
	if res.ExitCode == 0 {
		t.Fatalf("exit = 0 after timeout, stderr = %q", res.Stderr)
	}
	if running(proc.PID()) {
		t.Fatalf("child %d still running", proc.PID())
	}
}

func TestCancelKillsProcessGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	dir := t.TempDir()
	marker := dir + "/grandchild"
	proc, err := Start(ctx, helperSpec(t, "group", marker))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proc.ReadEvent(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	grandchild, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	started := startTime(grandchild)
	if started == "" {
		t.Fatalf("grandchild %d has no start time (%s)", grandchild, procStat(grandchild))
	}
	// The child is the process-group leader. The grandchild must share that group
	// so the cancel signal reaches it.
	childGroup := processGroup(proc.PID())
	if childGroup != strconv.Itoa(proc.PID()) {
		t.Fatalf("child %d process group = %q, want its own pid", proc.PID(), childGroup)
	}
	if got := processGroup(grandchild); got != childGroup {
		t.Fatalf("grandchild %d process group = %q, want %s", grandchild, got, childGroup)
	}

	cancel()
	res, err := proc.Wait()
	if err == nil {
		t.Fatal("expected cancellation to fail the wait")
	}
	if res.ExitCode == 0 {
		t.Fatalf("exit = 0 after cancel, stderr = %q", res.Stderr)
	}
	// SIGKILL is asynchronous. The grandchild can still be visible in /proc
	// for a short time after the direct child has been waited on.
	require.Eventually(t, func() bool {
		return !running(grandchild) || startTime(grandchild) != started
	}, 2*time.Second, 10*time.Millisecond)
	if running(proc.PID()) {
		t.Fatalf("child %d still running", proc.PID())
	}
}

func TestCancelBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Start(ctx, helperSpec(t, "sleep"))
	if err == nil {
		t.Fatal("expected start to fail on a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestOutputCap(t *testing.T) {
	spec := helperSpec(t, "flood")
	spec.StdoutLimit = 100
	proc, err := Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	res, err := proc.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains([]byte(res.Stdout), []byte(TruncationMarker)) {
		t.Fatalf("stdout missing truncation marker: %q", res.Stdout)
	}
	// The marker is added in String and is not part of the retained bytes.
	if len(res.Stdout) > 100+len(TruncationMarker)+2 {
		t.Fatalf("stdout grew past the cap: %d bytes", len(res.Stdout))
	}
}

func procStat(pid int) string {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func statFields(pid int) []string {
	text := procStat(pid)
	// The command name is wrapped in parentheses and may contain spaces.
	end := strings.LastIndex(text, ")")
	if end < 0 || end+2 >= len(text) {
		return nil
	}
	return strings.Fields(text[end+2:])
}

// running reports whether pid is still executing. A zombie counts as not
// running: the signal was delivered and the process has not been reaped.
func running(pid int) bool {
	fields := statFields(pid)
	if len(fields) == 0 {
		return false
	}
	return fields[0] != "Z" && fields[0] != "X"
}

func startTime(pid int) string {
	fields := statFields(pid)
	// starttime is the 20th field after the command name.
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

func processGroup(pid int) string {
	fields := statFields(pid)
	// pgrp is the 3rd field after the command name.
	if len(fields) < 3 {
		return ""
	}
	return fields[2]
}
