// Package measurement reads Linux process memory and checks sample series.
// It has no knowledge of HTTP/2.
package measurement

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prometheus/procfs"
)

// Quantity is a byte count. Supported is false when the kernel did not
// report the field. A supported quantity may still be zero; that zero is
// a real measurement, not a stand-in for a failed read.
type Quantity struct {
	Bytes     uint64
	Supported bool
}

// Value returns the byte count. An unsupported field is an error, never 0.
func (q Quantity) Value() (uint64, error) {
	if !q.Supported {
		return 0, ErrUnsupported
	}
	return q.Bytes, nil
}

// Memory is host-observed memory for one process, in bytes.
// RSS and anonymous RSS are instantaneous. Peak is the high-water mark (VmHWM).
type Memory struct {
	RSS  Quantity
	Anon Quantity
	Peak Quantity
}

// ReadMemory reads RSS, anonymous RSS, and peak RSS for pid from a procfs
// mounted at mount. An empty mount uses the default (/proc).
//
// procfs reports missing status fields as zero, which is also a legal
// measurement. After NewStatus succeeds, this function checks which keys
// are present in the same file and marks absent ones unsupported. The
// numeric conversion still comes from procfs, which documents status
// memory fields as bytes (the kernel's kB values multiplied by 1024).
func ReadMemory(mount string, pid int) (Memory, error) {
	if pid <= 0 {
		return Memory{}, fmt.Errorf("read memory: invalid pid %d", pid)
	}
	if mount == "" {
		mount = procfs.DefaultMountPoint
	}

	fs, err := procfs.NewFS(mount)
	if err != nil {
		return Memory{}, fmt.Errorf("open procfs %s: %w", mount, err)
	}
	proc, err := fs.Proc(pid)
	if err != nil {
		return Memory{}, fmt.Errorf("find pid %d: %w", pid, err)
	}
	status, err := proc.NewStatus()
	if err != nil {
		return Memory{}, fmt.Errorf("read status for %d: %w", pid, err)
	}

	keys, err := statusKeys(filepath.Join(mount, strconv.Itoa(pid), "status"))
	if err != nil {
		return Memory{}, fmt.Errorf("read status for %d: %w", pid, err)
	}
	return Memory{
		RSS:  quantity(keys, "VmRSS", status.VmRSS),
		Anon: quantity(keys, "RssAnon", status.RssAnon),
		Peak: quantity(keys, "VmHWM", status.VmHWM),
	}, nil
}

func quantity(keys map[string]struct{}, name string, bytes uint64) Quantity {
	if _, ok := keys[name]; !ok {
		return Quantity{}
	}
	return Quantity{Bytes: bytes, Supported: true}
}

// statusKeys returns the field names present in a /proc/<pid>/status file.
// It does not interpret values. procfs does that.
func statusKeys(path string) (map[string]struct{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{})
	for line := range strings.SplitSeq(string(data), "\n") {
		name, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		keys[name] = struct{}{}
	}
	return keys, nil
}
