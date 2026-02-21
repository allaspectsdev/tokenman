package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/allaspects/tokenman/internal/vault"
	"golang.org/x/term"
)

func cmdKeys(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tokenman keys <list|set|delete> [provider]")
		os.Exit(1)
	}

	v := vault.New()

	switch args[0] {
	case "list":
		providers, err := v.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error listing keys: %v\n", err)
			os.Exit(1)
		}
		if len(providers) == 0 {
			fmt.Println("No API keys stored")
			return
		}
		for _, p := range providers {
			fmt.Printf("  %s: ****\n", p)
		}

	case "set":
		if len(args) < 2 {
			fmt.Println("Usage: tokenman keys set <provider>")
			os.Exit(1)
		}
		provider := strings.ToLower(args[1])
		fmt.Printf("Enter API key for %s: ", provider)
		key, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading key: %v\n", err)
			os.Exit(1)
		}
		if err := v.Set(provider, string(key)); err != nil {
			fmt.Fprintf(os.Stderr, "error storing key: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Key for %s stored successfully\n", provider)

	case "delete":
		if len(args) < 2 {
			fmt.Println("Usage: tokenman keys delete <provider>")
			os.Exit(1)
		}
		provider := strings.ToLower(args[1])
		if err := v.Delete(provider); err != nil {
			fmt.Fprintf(os.Stderr, "error deleting key: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Key for %s deleted\n", provider)

	default:
		fmt.Fprintf(os.Stderr, "unknown keys command: %s\n", args[0])
		os.Exit(1)
	}
}
