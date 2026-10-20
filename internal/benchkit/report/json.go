package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteJSONFile encodes v as indented JSON and replaces path only after
// the temporary file is synced. A failure leaves an existing path unchanged.
func WriteJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	data = append(data, '\n')
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", path, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	committed = true

	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = dirFile.Close() }()
	if err := dirFile.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", dir, err)
	}
	return nil
}

// JSONLines appends one JSON object per line and syncs after each append.
type JSONLines struct {
	file *os.File
}

// CreateJSONLines opens path for appending. The file is created if needed.
func CreateJSONLines(path string) (*JSONLines, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &JSONLines{file: file}, nil
}

// Append writes one record. The file is left unchanged when encoding fails.
func (j *JSONLines) Append(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode jsonl: %w", err)
	}
	data = append(data, '\n')
	if _, err := j.file.Write(data); err != nil {
		return fmt.Errorf("write jsonl: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("sync jsonl: %w", err)
	}
	return nil
}

// Close closes the file.
func (j *JSONLines) Close() error {
	if err := j.file.Close(); err != nil {
		return fmt.Errorf("close jsonl: %w", err)
	}
	return nil
}
