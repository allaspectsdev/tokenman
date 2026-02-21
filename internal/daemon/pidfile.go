package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const pidFilename = "tokenman.pid"

// WritePID writes the current process ID to dataDir/tokenman.pid.
func WritePID(dataDir string) error {
	path := pidPath(dataDir)

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("creating data directory for PID file: %w", err)
	}

	pid := os.Getpid()
	data := []byte(strconv.Itoa(pid))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing PID file %s: %w", path, err)
	}
	return nil
}

// ReadPID reads the PID from dataDir/tokenman.pid.
func ReadPID(dataDir string) (int, error) {
	path := pidPath(dataDir)

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("reading PID file %s: %w", path, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parsing PID from %s: %w", path, err)
	}
	return pid, nil
}

// RemovePID removes the PID file from dataDir.
func RemovePID(dataDir string) error {
	path := pidPath(dataDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing PID file %s: %w", path, err)
	}
	return nil
}

// IsRunning checks whether the PID file exists and the process is alive.
// A process is considered alive if sending signal 0 succeeds.
func IsRunning(dataDir string) bool {
	pid, err := ReadPID(dataDir)
	if err != nil {
		return false
	}
	return isProcessAlive(pid)
}

// isProcessAlive checks whether the process with the given PID is running
// by sending signal 0. On Unix systems, this verifies the process exists
// without actually sending a signal.
func isProcessAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Signal 0 checks if the process exists without sending an actual signal.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// pidPath returns the full path to the PID file.
func pidPath(dataDir string) string {
	return filepath.Join(dataDir, pidFilename)
}
