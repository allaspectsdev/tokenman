package main

import (
	"fmt"
	"os"

	"github.com/allaspectsdev/tokenman/internal/config"
	"github.com/allaspectsdev/tokenman/internal/daemon"
)

func cmdStart(args []string) {
	foreground := false
	for _, a := range args {
		if a == "--foreground" || a == "-f" {
			foreground = true
		}
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	if err := daemon.Run(cfg, foreground); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdStop() {
	if err := daemon.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "error stopping daemon: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("tokenman stopped")
}

func cmdStatus() {
	if err := daemon.Status(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func cmdSetup(args []string) {
	nonInteractive := false
	for _, a := range args {
		if a == "--non-interactive" {
			nonInteractive = true
		}
	}

	if nonInteractive {
		// Generate default config and start
		cmdInitConfig()
		fmt.Println("Setup complete. Run 'tokenman start' to begin.")
		return
	}

	fmt.Println("TokenMan Setup Wizard")
	fmt.Println("=====================")
	fmt.Println()

	// Step 1: Generate config
	cmdInitConfig()

	// Step 2: Prompt for API keys
	fmt.Println("\nTo add API keys, run: tokenman keys set <provider>")
	fmt.Println("Supported providers: anthropic, openai")
	fmt.Println()
	fmt.Println("Setup complete. Run 'tokenman start' to begin.")
}

func cmdInitConfig() {
	if err := config.InitConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "error generating config: %v\n", err)
		os.Exit(1)
	}
}

func cmdInstallService() {
	if err := daemon.InstallService(); err != nil {
		fmt.Fprintf(os.Stderr, "error installing service: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Service installed successfully")
}

func cmdConfigExport(args []string) {
	path := "tokenman-export.toml"
	if len(args) > 0 {
		path = args[0]
	}
	// Load current config first.
	config.Load("")
	if err := config.ExportConfig(path); err != nil {
		fmt.Fprintf(os.Stderr, "error exporting config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config exported to %s\n", path)
}

func cmdConfigImport(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: tokenman config-import <file>")
		os.Exit(1)
	}
	if err := config.ImportConfig(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "error importing config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Config imported from %s\n", args[0])
}
