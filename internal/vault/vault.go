package vault

import (
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

const serviceName = "tokenman"

// knownProviders is the list of providers checked by List().
var knownProviders = []string{"anthropic", "openai", "google"}

// Vault provides secure API key storage using the OS keychain,
// with fallback to environment variables.
type Vault struct{}

// New creates a new Vault instance.
func New() *Vault {
	return &Vault{}
}

// Set stores an API key for the given provider in the OS keychain.
func (v *Vault) Set(provider, key string) error {
	return keyring.Set(serviceName, provider, key)
}

// Get retrieves the API key for the given provider. It first checks the
// OS keychain, then falls back to the environment variable
// TOKENMAN_KEY_{UPPER(provider)}.
func (v *Vault) Get(provider string) (string, error) {
	secret, err := keyring.Get(serviceName, provider)
	if err == nil && secret != "" {
		return secret, nil
	}

	// Fallback to environment variable.
	envKey := "TOKENMAN_KEY_" + strings.ToUpper(provider)
	if val := os.Getenv(envKey); val != "" {
		return val, nil
	}

	return "", fmt.Errorf("no key found for provider %q: not in keychain and %s not set", provider, envKey)
}

// Delete removes the API key for the given provider from the OS keychain.
func (v *Vault) Delete(provider string) error {
	return keyring.Delete(serviceName, provider)
}

// List returns the names of known providers that currently have keys stored.
// It checks both the keychain and environment variables for each provider.
func (v *Vault) List() ([]string, error) {
	var providers []string

	for _, provider := range knownProviders {
		// Check keychain.
		secret, err := keyring.Get(serviceName, provider)
		if err == nil && secret != "" {
			providers = append(providers, provider)
			continue
		}

		// Check environment variable.
		envKey := "TOKENMAN_KEY_" + strings.ToUpper(provider)
		if val := os.Getenv(envKey); val != "" {
			providers = append(providers, provider)
		}
	}

	return providers, nil
}

// ResolveKeyRef parses a key reference and retrieves the corresponding API key.
// Supported formats:
//   - "keyring://tokenman/<provider>" (preferred)
//   - "keychain:tokenman/<provider>" (legacy)
//   - "env:VARIABLE_NAME" (environment variable)
//   - "file:///path/to/key" (plain-text file)
func (v *Vault) ResolveKeyRef(keyRef string) (string, error) {
	// Format 1: keyring://tokenman/<provider>
	if strings.HasPrefix(keyRef, "keyring://") {
		path := strings.TrimPrefix(keyRef, "keyring://")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] != serviceName || parts[1] == "" {
			return "", fmt.Errorf("invalid key reference format: %q (expected \"keyring://tokenman/<provider>\")", keyRef)
		}
		return v.Get(parts[1])
	}

	// Format 2: keychain:tokenman/<provider> (legacy)
	if strings.HasPrefix(keyRef, "keychain:") {
		path := strings.TrimPrefix(keyRef, "keychain:")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] != serviceName || parts[1] == "" {
			return "", fmt.Errorf("invalid key reference path: %q (expected \"tokenman/<provider>\")", path)
		}
		return v.Get(parts[1])
	}

	// Format 3: env:VARIABLE_NAME
	if strings.HasPrefix(keyRef, "env:") {
		envVar := strings.TrimPrefix(keyRef, "env:")
		if val := os.Getenv(envVar); val != "" {
			return val, nil
		}
		return "", fmt.Errorf("environment variable %q is not set", envVar)
	}

	// Format 4: file:///path/to/key
	if strings.HasPrefix(keyRef, "file://") {
		filePath := strings.TrimPrefix(keyRef, "file://")
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("reading key file %q: %w", filePath, err)
		}
		key := strings.TrimSpace(string(data))
		if key == "" {
			return "", fmt.Errorf("key file %q is empty", filePath)
		}
		return key, nil
	}

	return "", fmt.Errorf("invalid key reference format: %q (expected \"keyring://tokenman/<provider>\", \"keychain:tokenman/<provider>\", \"env:VARIABLE_NAME\", or \"file:///path/to/key\")", keyRef)
}
