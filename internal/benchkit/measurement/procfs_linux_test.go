package measurement

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadMemoryFixtureUnits(t *testing.T) {
	mem, err := ReadMemory(filepath.Join("testdata", "status", "present"), 4242)
	if err != nil {
		t.Fatal(err)
	}
	// procfs converts the kernel's kB figures to bytes.
	if got, err := mem.RSS.Value(); err != nil || got != 12*1024 {
		t.Fatalf("RSS = %d, %v", got, err)
	}
	if got, err := mem.Anon.Value(); err != nil || got != 4*1024 {
		t.Fatalf("Anon = %d, %v", got, err)
	}
	if got, err := mem.Peak.Value(); err != nil || got != 20*1024 {
		t.Fatalf("Peak = %d, %v", got, err)
	}
}

func TestReadMemoryZeroIsValid(t *testing.T) {
	mem, err := ReadMemory(filepath.Join("testdata", "status", "no-anon"), 4242)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mem.RSS.Value()
	if err != nil || got != 0 {
		t.Fatalf("RSS = %d, %v; a present zero must stay a measurement", got, err)
	}
	if _, err := mem.Anon.Value(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Anon err = %v, want unsupported", err)
	}
	if mem.Anon.Bytes != 0 || mem.Anon.Supported {
		t.Fatalf("unsupported anon = %+v", mem.Anon)
	}
}

func TestReadMemoryMalformedStatus(t *testing.T) {
	_, err := ReadMemory(filepath.Join("testdata", "status", "bad"), 4242)
	if err == nil {
		t.Fatal("expected a status parse error")
	}
}

func TestReadMemoryMissingPID(t *testing.T) {
	_, err := ReadMemory(t.TempDir(), 4242)
	if err == nil {
		t.Fatal("expected a missing pid error")
	}
}

func TestReadMemoryInvalidPID(t *testing.T) {
	_, err := ReadMemory(t.TempDir(), 0)
	if err == nil {
		t.Fatal("expected an invalid pid error")
	}
}

func TestReadMemoryPermission(t *testing.T) {
	mount := t.TempDir()
	dir := filepath.Join(mount, "7")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "status")
	if err := os.WriteFile(path, []byte("Name:\tfixture\nVmRSS:\t1 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := os.ReadFile(path); err == nil {
		t.Skip("process can read a mode 000 file; permission failure is not observable")
	}
	if _, err := ReadMemory(mount, 7); err == nil {
		t.Fatal("expected a permission error, not a zero measurement")
	}
}
