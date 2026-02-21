package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const launchdLabel = "com.allaspects.tokenman"

// launchdPlistTemplate is the macOS launchd property list for running TokenMan
// as a persistent user agent.
const launchdPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.ProgramPath}}</string>
        <string>start</string>
        <string>--foreground</string>
    </array>

    <key>WorkingDirectory</key>
    <string>{{.WorkingDir}}</string>

    <key>KeepAlive</key>
    <true/>

    <key>RunAtLoad</key>
    <true/>

    <key>StandardOutPath</key>
    <string>{{.LogDir}}/tokenman.out.log</string>

    <key>StandardErrorPath</key>
    <string>{{.LogDir}}/tokenman.err.log</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin</string>
    </dict>

    <key>ProcessType</key>
    <string>Background</string>

    <key>ThrottleInterval</key>
    <integer>5</integer>
</dict>
</plist>
`

type plistData struct {
	Label       string
	ProgramPath string
	WorkingDir  string
	LogDir      string
}

// InstallService generates a launchd plist and installs it as a user agent
// on macOS. The plist is written to ~/Library/LaunchAgents/ and then loaded
// via launchctl.
func InstallService() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determining home directory: %w", err)
	}

	// Resolve the tokenman binary path.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("determining executable path: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolving executable symlinks: %w", err)
	}

	launchAgentsDir := filepath.Join(homeDir, "Library", "LaunchAgents")
	if err := os.MkdirAll(launchAgentsDir, 0o755); err != nil {
		return fmt.Errorf("creating LaunchAgents directory: %w", err)
	}

	dataDir := filepath.Join(homeDir, ".tokenman")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	plistPath := filepath.Join(launchAgentsDir, launchdLabel+".plist")

	data := plistData{
		Label:       launchdLabel,
		ProgramPath: execPath,
		WorkingDir:  dataDir,
		LogDir:      dataDir,
	}

	tmpl, err := template.New("plist").Parse(launchdPlistTemplate)
	if err != nil {
		return fmt.Errorf("parsing plist template: %w", err)
	}

	f, err := os.Create(plistPath)
	if err != nil {
		return fmt.Errorf("creating plist file %s: %w", plistPath, err)
	}
	defer f.Close()

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}

	// Ensure the file is closed before loading.
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing plist file: %w", err)
	}

	fmt.Printf("Plist written to %s\n", plistPath)

	// Try to unload first (ignore errors if not loaded).
	unload := exec.Command("launchctl", "unload", plistPath)
	_ = unload.Run()

	// Load the plist.
	load := exec.Command("launchctl", "load", plistPath)
	load.Stdout = os.Stdout
	load.Stderr = os.Stderr
	if err := load.Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Printf("Service %s loaded via launchctl\n", launchdLabel)
	return nil
}

// UninstallService unloads and removes the launchd plist.
func UninstallService() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determining home directory: %w", err)
	}

	plistPath := filepath.Join(homeDir, "Library", "LaunchAgents", launchdLabel+".plist")

	// Unload (ignore errors if not currently loaded).
	unload := exec.Command("launchctl", "unload", plistPath)
	_ = unload.Run()

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing plist: %w", err)
	}

	fmt.Printf("Service %s uninstalled\n", launchdLabel)
	return nil
}
