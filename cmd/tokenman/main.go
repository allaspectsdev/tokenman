package main

import (
	"fmt"
	"os"

	"github.com/allaspects/tokenman/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmdStart(os.Args[2:])
	case "stop":
		cmdStop()
	case "status":
		cmdStatus()
	case "setup":
		cmdSetup(os.Args[2:])
	case "keys":
		cmdKeys(os.Args[2:])
	case "init-config":
		cmdInitConfig()
	case "install-service":
		cmdInstallService()
	case "config-export":
		cmdConfigExport(os.Args[2:])
	case "config-import":
		cmdConfigImport(os.Args[2:])
	case "version":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Usage: tokenman <command> [options]

Commands:
  start            Start the tokenman daemon
  stop             Stop the running daemon
  status           Show daemon status and summary stats
  setup            Interactive setup wizard
  keys             Manage API keys (list|set|delete <provider>)
  init-config      Generate default config file
  config-export    Export current config to a TOML file
  config-import    Import config from a TOML file
  install-service  Install as system service (launchd on macOS)
  version          Print version information
  help             Show this help message

Options:
  --foreground     Run in foreground (with 'start')
  --non-interactive  Skip interactive prompts (with 'setup')`)
}
