// Package system collects host and toolchain facts for a benchmark run.
package system

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/prometheus/procfs"
)

// Identity is build and Nix provenance supplied by the caller.
// The wrapper knows the flake inputs; this package does not parse a lock file.
type Identity struct {
	NixpkgsRev  string
	NixpkgsHash string
	SourceRev   string
	Dirty       bool
}

// Info is the host and toolchain description stored with a run.
type Info struct {
	OS           string `json:"os"`
	Kernel       string `json:"kernel"`
	Arch         string `json:"arch"`
	CPUModel     string `json:"cpu_model"`
	MemoryBytes  uint64 `json:"memory_bytes"`
	NofileSoft   uint64 `json:"nofile_soft"`
	NofileHard   uint64 `json:"nofile_hard"`
	ProcReadable bool   `json:"proc_readable"`
	GoVersion    string `json:"go_version"`
	MainModule   string `json:"main_module,omitempty"`
	VCSRevision  string `json:"vcs_revision,omitempty"`
	NixpkgsRev   string `json:"nixpkgs_rev,omitempty"`
	NixpkgsHash  string `json:"nixpkgs_hash,omitempty"`
	SourceRev    string `json:"source_rev,omitempty"`
	SourceDirty  bool   `json:"source_dirty"`
}

// Collect reads CPU and memory from a procfs at mount (or /proc when mount
// is empty) and adds the current process's limit, kernel, and Go version.
// Missing CPU model or memory is an error. It does not invent zeroes.
func Collect(mount string, id Identity) (Info, error) {
	if mount == "" {
		mount = procfs.DefaultMountPoint
	}
	fs, err := procfs.NewFS(mount)
	if err != nil {
		return Info{}, fmt.Errorf("open procfs %s: %w", mount, err)
	}

	cpus, err := fs.CPUInfo()
	if err != nil {
		return Info{}, fmt.Errorf("read cpuinfo: %w", err)
	}
	if len(cpus) == 0 || cpus[0].ModelName == "" {
		return Info{}, fmt.Errorf("read cpuinfo: model name is missing")
	}

	mem, err := fs.Meminfo()
	if err != nil {
		return Info{}, fmt.Errorf("read meminfo: %w", err)
	}
	if mem.MemTotalBytes == nil {
		return Info{}, fmt.Errorf("read meminfo: MemTotal is missing")
	}

	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err != nil {
		return Info{}, fmt.Errorf("uname: %w", err)
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		return Info{}, fmt.Errorf("getrlimit NOFILE: %w", err)
	}

	info := Info{
		OS:           cstring(uname.Sysname[:]),
		Kernel:       cstring(uname.Release[:]),
		Arch:         runtime.GOARCH,
		CPUModel:     cpus[0].ModelName,
		MemoryBytes:  *mem.MemTotalBytes,
		NofileSoft:   limit.Cur,
		NofileHard:   limit.Max,
		ProcReadable: true,
		GoVersion:    runtime.Version(),
		NixpkgsRev:   id.NixpkgsRev,
		NixpkgsHash:  id.NixpkgsHash,
		SourceRev:    id.SourceRev,
		SourceDirty:  id.Dirty,
	}
	if build, ok := debug.ReadBuildInfo(); ok {
		info.MainModule = build.Main.Path
		for _, setting := range build.Settings {
			if setting.Key == "vcs.revision" {
				info.VCSRevision = setting.Value
			}
		}
	}
	if info.OS == "" || info.Kernel == "" {
		return Info{}, fmt.Errorf("uname: sysname or release is empty")
	}
	if _, err := os.Stat(mount); err != nil {
		return Info{}, fmt.Errorf("stat procfs: %w", err)
	}
	return info, nil
}

func cstring(buf []int8) string {
	out := make([]byte, 0, len(buf))
	for _, c := range buf {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
