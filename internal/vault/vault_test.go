package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveKeyRef_EnvFormat(t *testing.T) {
	v := New()

	const envVar = "TEST_TOKENMAN_VAULT_KEY"
	const expected = "sk-test-1234"

	t.Setenv(envVar, expected)

	got, err := v.ResolveKeyRef("env:" + envVar)
	if err != nil {
		t.Fatalf("ResolveKeyRef(env:): %v", err)
	}
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestResolveKeyRef_EnvFormat_Unset(t *testing.T) {
	v := New()

	os.Unsetenv("NONEXISTENT_KEY_VAR")

	_, err := v.ResolveKeyRef("env:NONEXISTENT_KEY_VAR")
	if err == nil {
		t.Fatal("expected error for unset env var")
	}
}

func TestResolveKeyRef_InvalidFormat(t *testing.T) {
	v := New()

	_, err := v.ResolveKeyRef("plaintext:secret")
	if err == nil {
		t.Fatal("expected error for invalid key ref format")
	}
}

func TestResolveKeyRef_KeyringBadFormat(t *testing.T) {
	v := New()

	// Missing service/provider structure.
	_, err := v.ResolveKeyRef("keyring://badformat")
	if err == nil {
		t.Fatal("expected error for malformed keyring ref")
	}
}

func TestResolveKeyRef_KeyringWrongService(t *testing.T) {
	v := New()

	_, err := v.ResolveKeyRef("keyring://other-service/anthropic")
	if err == nil {
		t.Fatal("expected error for wrong service name")
	}
}

func TestResolveKeyRef_KeychainBadFormat(t *testing.T) {
	v := New()

	_, err := v.ResolveKeyRef("keychain:badformat")
	if err == nil {
		t.Fatal("expected error for malformed keychain ref")
	}
}

func TestResolveKeyRef_KeychainWrongService(t *testing.T) {
	v := New()

	_, err := v.ResolveKeyRef("keychain:other/anthropic")
	if err == nil {
		t.Fatal("expected error for wrong service name in keychain ref")
	}
}

func TestResolveKeyRef_EmptyProvider(t *testing.T) {
	v := New()

	_, err := v.ResolveKeyRef("keyring://tokenman/")
	if err == nil {
		t.Fatal("expected error for empty provider in keyring ref")
	}
}

func TestGet_EnvFallback(t *testing.T) {
	v := New()

	const envVar = "TOKENMAN_KEY_TESTPROVIDER"
	const expected = "env-key-value"

	t.Setenv(envVar, expected)

	got, err := v.Get("testprovider")
	if err != nil {
		t.Fatalf("Get with env fallback: %v", err)
	}
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestResolveKeyRef_FileFormat(t *testing.T) {
	v := New()

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "api-key.txt")
	if err := os.WriteFile(keyFile, []byte("sk-file-secret-key\n"), 0o600); err != nil {
		t.Fatalf("writing key file: %v", err)
	}

	got, err := v.ResolveKeyRef("file://" + keyFile)
	if err != nil {
		t.Fatalf("ResolveKeyRef(file://): %v", err)
	}
	if got != "sk-file-secret-key" {
		t.Errorf("got %q, want %q", got, "sk-file-secret-key")
	}
}

func TestResolveKeyRef_FileFormat_NotFound(t *testing.T) {
	v := New()

	_, err := v.ResolveKeyRef("file:///nonexistent/path/key.txt")
	if err == nil {
		t.Fatal("expected error for missing key file")
	}
}

func TestResolveKeyRef_FileFormat_Empty(t *testing.T) {
	v := New()

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "empty-key.txt")
	if err := os.WriteFile(keyFile, []byte("  \n"), 0o600); err != nil {
		t.Fatalf("writing key file: %v", err)
	}

	_, err := v.ResolveKeyRef("file://" + keyFile)
	if err == nil {
		t.Fatal("expected error for empty key file")
	}
}

func TestGet_NoKeyFound(t *testing.T) {
	v := New()

	os.Unsetenv("TOKENMAN_KEY_NOPROVIDER")

	_, err := v.Get("noprovider")
	if err == nil {
		t.Fatal("expected error when no key found")
	}
}
