package config

import (
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}

	tests := []string{
		"hello world",
		"my-secure-password-123!",
		"",
		"a",
		"very long string with spaces and special characters: @#$%^&*()_+{}|:<>?",
	}

	for _, original := range tests {
		ciphertext, err := Encrypt(original, key)
		if err != nil {
			t.Errorf("Encrypt(%q) failed: %v", original, err)
			continue
		}

		// Decrypt the ciphertext and check if it matches the original plaintext
		decrypted, err := Decrypt(ciphertext, key)
		if err != nil {
			t.Errorf("Decrypt(%q) failed: %v", ciphertext, err)
			continue
		}

		if decrypted != original {
			t.Errorf("Encrypt/Decrypt mismatch: got %q, want %q", decrypted, original)
		}
	}
}

func TestDecryptInvalidCiphertext(t *testing.T) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("failed to generate random key: %v", err)
	}

	invalidCiphertexts := []string{
		"not-base64-encoded-!!!",
		"YQ==", // Base64 for "a", too short to contain GCM nonce
	}

	for _, ct := range invalidCiphertexts {
		_, err := Decrypt(ct, key)
		if err == nil {
			t.Errorf("expected error decrypting invalid ciphertext %q, got nil", ct)
		}
	}
}

func TestDecryptInvalidKeyLength(t *testing.T) {
	invalidKeys := [][]byte{
		nil,
		make([]byte, 16),
		make([]byte, 24),
		make([]byte, 31),
		make([]byte, 33),
	}

	for _, key := range invalidKeys {
		_, err := Encrypt("test", key)
		if err == nil {
			t.Errorf("expected error encrypting with key size %d, got nil", len(key))
		}

		_, err = Decrypt("dGVzdA==", key)
		if err == nil {
			t.Errorf("expected error decrypting with key size %d, got nil", len(key))
		}
	}
}

func TestSaveAndLoadServers(t *testing.T) {
	// Create a temp directory for configuration files
	tempDir, err := os.MkdirTemp("", "sshmanager_config_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Override config directory for testing
	ConfigDirOverride = tempDir
	defer func() {
		ConfigDirOverride = ""
	}()

	originalServers := []Server{
		{
			Alias:          "test-password",
			Host:           "1.1.1.1",
			User:           "root",
			Password:       "p@ssword",
			Port:           22,
			ProjectPath:    "/home/user/project1",
			PrivateKeyPath: "",
			UseSSHAgent:    false,
		},
		{
			Alias:          "test-key",
			Host:           "2.2.2.2",
			User:           "ubuntu",
			Password:       "keypassphrase",
			Port:           2222,
			ProjectPath:    "",
			PrivateKeyPath: "~/.ssh/id_ed25519",
			UseSSHAgent:    false,
		},
		{
			Alias:          "test-agent",
			Host:           "3.3.3.3",
			User:           "admin",
			Password:       "",
			Port:           22,
			ProjectPath:    "/tmp",
			PrivateKeyPath: "",
			UseSSHAgent:    true,
		},
	}

	// Save servers
	err = SaveServers(originalServers)
	if err != nil {
		t.Fatalf("SaveServers failed: %v", err)
	}

	// Verify that the files were created
	serversJsonPath := filepath.Join(tempDir, "servers.json")
	if _, err := os.Stat(serversJsonPath); os.IsNotExist(err) {
		t.Errorf("expected servers.json to exist at %q", serversJsonPath)
	}

	secretKeyPath := filepath.Join(tempDir, "secret.key")
	if _, err := os.Stat(secretKeyPath); os.IsNotExist(err) {
		t.Errorf("expected secret.key to exist at %q", secretKeyPath)
	}

	// Load servers back
	loadedServers, err := LoadServers()
	if err != nil {
		t.Fatalf("LoadServers failed: %v", err)
	}

	if len(loadedServers) != len(originalServers) {
		t.Fatalf("loaded server count mismatch: got %d, want %d", len(loadedServers), len(originalServers))
	}

	for i := range originalServers {
		got := loadedServers[i]
		want := originalServers[i]

		if got.Alias != want.Alias {
			t.Errorf("server[%d].Alias mismatch: got %q, want %q", i, got.Alias, want.Alias)
		}
		if got.Host != want.Host {
			t.Errorf("server[%d].Host mismatch: got %q, want %q", i, got.Host, want.Host)
		}
		if got.User != want.User {
			t.Errorf("server[%d].User mismatch: got %q, want %q", i, got.User, want.User)
		}
		if got.Password != want.Password {
			t.Errorf("server[%d].Password mismatch: got %q, want %q", i, got.Password, want.Password)
		}
		if got.Port != want.Port {
			t.Errorf("server[%d].Port mismatch: got %d, want %d", i, got.Port, want.Port)
		}
		if got.ProjectPath != want.ProjectPath {
			t.Errorf("server[%d].ProjectPath mismatch: got %q, want %q", i, got.ProjectPath, want.ProjectPath)
		}
		if got.PrivateKeyPath != want.PrivateKeyPath {
			t.Errorf("server[%d].PrivateKeyPath mismatch: got %q, want %q", i, got.PrivateKeyPath, want.PrivateKeyPath)
		}
		if got.UseSSHAgent != want.UseSSHAgent {
			t.Errorf("server[%d].UseSSHAgent mismatch: got %t, want %t", i, got.UseSSHAgent, want.UseSSHAgent)
		}
	}
}
