package process

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// waitDelay is how long Wait waits for a killed child to exit before giving up.
const waitDelay = 3 * time.Second

// lineLimit is the largest JSON line ReadEvent will accept.
const lineLimit = 1 << 20

// Spec describes one child process. Env nil means the child inherits the
// current environment. A non-nil Env, including an empty slice, is the
// complete environment.
type Spec struct {
	Path        string
	Args        []string
	Env         []string
	Dir         string
	StdoutLimit int
	StderrLimit int
}

// Result is the outcome of a child that has been waited on.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type event struct {
	line []byte
	err  error
}

// Proc is a started child. ReadEvent must not be called concurrently with
// itself. Wait may be called from another goroutine; it unblocks ReadEvent.
type Proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan event
	stdout *Buffer
	stderr *Buffer
	wg     sync.WaitGroup
	stop   chan struct{}
	once   sync.Once
}

// Start launches spec.Path. Cancelling ctx kills the child's process group.
// The child is a group leader, so grandchildren die with it.
func Start(ctx context.Context, spec Spec) (*Proc, error) {
	if spec.Path == "" {
		return nil, errors.New("start process: path is empty")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	if spec.Env != nil {
		cmd.Env = spec.Env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("start process: stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("start process: stderr: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("start process: stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start process: %w", err)
	}

	p := &Proc{
		cmd:    cmd,
		stdin:  stdin,
		events: make(chan event, 64),
		stdout: NewBuffer(spec.StdoutLimit),
		stderr: NewBuffer(spec.StderrLimit),
		stop:   make(chan struct{}),
	}
	p.wg.Add(2)
	go p.readStdout(stdout)
	go p.readStderr(stderr)
	return p, nil
}

func (p *Proc) readStdout(r io.Reader) {
	defer p.wg.Done()
	defer close(p.events)

	sc := bufio.NewScanner(r)
	// Go 1.28 sets the buffer length to its capacity. A capacity larger than
	// the length would look like already-read zeros and drop real output.
	sc.Buffer(make([]byte, 0), lineLimit)
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		_, _ = p.stdout.Write(append(append([]byte(nil), line...), '\n'))
		select {
		case p.events <- event{line: line}:
		case <-p.stop:
			return
		}
	}
	if err := sc.Err(); err != nil {
		select {
		case p.events <- event{err: err}:
		case <-p.stop:
		}
	}
}

func (p *Proc) readStderr(r io.Reader) {
	defer p.wg.Done()
	_, _ = io.Copy(p.stderr, r)
}

// PID returns the child's process id, or 0 if it has not started.
func (p *Proc) PID() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// ReadEvent returns the next stdout line as JSON. A line that is not JSON
// is an error and the bytes are not discarded from the captured log.
// io.EOF means the child closed stdout.
func (p *Proc) ReadEvent(ctx context.Context) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case ev, ok := <-p.events:
		if !ok {
			return nil, io.EOF
		}
		if ev.err != nil {
			return nil, fmt.Errorf("read stdout: %w", ev.err)
		}
		if !json.Valid(ev.line) {
			return nil, fmt.Errorf("malformed json line: %q", clip(ev.line, 120))
		}
		return json.RawMessage(append([]byte(nil), ev.line...)), nil
	}
}

// Kill sends SIGKILL to the child's process group.
func (p *Proc) Kill() error {
	if p.cmd.Process == nil {
		return errors.New("kill process: not started")
	}
	err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kill process group: %w", err)
	}
	return nil
}

// Write writes to the child's stdin.
func (p *Proc) Write(b []byte) (int, error) {
	n, err := p.stdin.Write(b)
	if err != nil {
		return n, fmt.Errorf("write stdin: %w", err)
	}
	return n, nil
}

// CloseStdin closes the child's stdin. Wait also closes it.
func (p *Proc) CloseStdin() error {
	if err := p.stdin.Close(); err != nil {
		return fmt.Errorf("close stdin: %w", err)
	}
	return nil
}

// Wait blocks until the child exits and the captured output is complete.
// A non-zero exit, a signal, or a cancelled context is returned as an error.
// Result is still filled in, including the exit code when the child exited.
func (p *Proc) Wait() (Result, error) {
	_ = p.stdin.Close()
	err := p.cmd.Wait()
	p.once.Do(func() { close(p.stop) })
	p.wg.Wait()

	res := Result{
		ExitCode: -1,
		Stdout:   p.stdout.String(),
		Stderr:   p.stderr.String(),
	}
	if err == nil {
		res.ExitCode = 0
		return res, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
		return res, fmt.Errorf("process exited with status %d: %w", res.ExitCode, err)
	}
	return res, fmt.Errorf("wait for process: %w", err)
}

func clip(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
