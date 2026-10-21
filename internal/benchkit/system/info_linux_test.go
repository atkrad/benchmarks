package system

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProc(t *testing.T, cpu, mem string) string {
	t.Helper()
	dir := t.TempDir()
	if cpu != "" {
		if err := os.WriteFile(filepath.Join(dir, "cpuinfo"), []byte(cpu), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if mem != "" {
		if err := os.WriteFile(filepath.Join(dir, "meminfo"), []byte(mem), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCollectFixture(t *testing.T) {
	mount := writeProc(t, "processor\t: 0\nmodel name\t: Fixture CPU\n", "MemTotal: 2048 kB\n")
	info, err := Collect(mount, Identity{
		NixpkgsRev:  "abc",
		NixpkgsHash: "sha256-test",
		SourceRev:   "dirty-rev",
		Dirty:       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.CPUModel != "Fixture CPU" {
		t.Fatalf("cpu = %q", info.CPUModel)
	}
	if info.MemoryBytes != 2048*1024 {
		t.Fatalf("memory = %d", info.MemoryBytes)
	}
	if info.OS == "" || info.Kernel == "" || info.GoVersion == "" {
		t.Fatalf("host fields = %+v", info)
	}
	if info.NofileSoft == 0 || !info.ProcReadable || !info.SourceDirty {
		t.Fatalf("limits = %+v", info)
	}
	if info.NixpkgsRev != "abc" || info.SourceRev != "dirty-rev" {
		t.Fatalf("identity = %+v", info)
	}
}

func TestCollectMissingMemory(t *testing.T) {
	mount := writeProc(t, "processor\t: 0\nmodel name\t: Fixture CPU\n", "")
	if _, err := Collect(mount, Identity{}); err == nil {
		t.Fatal("expected missing meminfo to fail")
	}
}

func TestCollectMissingCPU(t *testing.T) {
	mount := writeProc(t, "", "MemTotal: 1024 kB\n")
	if _, err := Collect(mount, Identity{}); err == nil {
		t.Fatal("expected missing cpuinfo to fail")
	}
}

func TestCollectLiveProc(t *testing.T) {
	info, err := Collect("", Identity{})
	if err != nil {
		t.Fatal(err)
	}
	if info.CPUModel == "" || info.MemoryBytes == 0 || info.Kernel == "" || info.GoVersion == "" {
		t.Fatalf("live info = %+v", info)
	}
}
