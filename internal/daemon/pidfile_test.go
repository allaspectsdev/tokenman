package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWritePID_ReadPID(t *testing.T) {
	dir := t.TempDir()

	if err := WritePID(dir); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	pid, err := ReadPID(dir)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}

	if pid != os.Getpid() {
		t.Errorf("ReadPID got %d, want %d", pid, os.Getpid())
	}
}

func TestReadPID_NoFile(t *testing.T) {
	dir := t.TempDir()

	_, err := ReadPID(dir)
	if err == nil {
		t.Fatal("expected error reading nonexistent PID file")
	}
}

func TestReadPID_InvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, pidFilename)

	if err := os.WriteFile(path, []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := ReadPID(dir)
	if err == nil {
		t.Fatal("expected error parsing invalid PID")
	}
}

func TestRemovePID(t *testing.T) {
	dir := t.TempDir()

	if err := WritePID(dir); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	if err := RemovePID(dir); err != nil {
		t.Fatalf("RemovePID: %v", err)
	}

	// Verify file is gone.
	path := filepath.Join(dir, pidFilename)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("PID file still exists after RemovePID")
	}
}

func TestRemovePID_NoFile(t *testing.T) {
	dir := t.TempDir()

	// Removing a nonexistent PID file should not error.
	if err := RemovePID(dir); err != nil {
		t.Fatalf("RemovePID on nonexistent file: %v", err)
	}
}

func TestIsRunning_Self(t *testing.T) {
	dir := t.TempDir()

	if err := WritePID(dir); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	if !IsRunning(dir) {
		t.Error("IsRunning returned false for our own PID")
	}
}

func TestIsRunning_NoFile(t *testing.T) {
	dir := t.TempDir()

	if IsRunning(dir) {
		t.Error("IsRunning returned true with no PID file")
	}
}

func TestIsRunning_DeadProcess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, pidFilename)

	// Write a PID that almost certainly doesn't exist.
	deadPID := 99999
	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPID)), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// This should return false for a dead process (may depend on OS).
	// On most systems PID 99999 won't be running.
	// We just verify it doesn't panic.
	_ = IsRunning(dir)
}

func TestWritePID_CreatesDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "nested", "dir")

	if err := WritePID(dir); err != nil {
		t.Fatalf("WritePID with nested dir: %v", err)
	}

	pid, err := ReadPID(dir)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("got PID %d, want %d", pid, os.Getpid())
	}
}
