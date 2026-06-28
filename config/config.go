package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Server struct {
	Alias          string `json:"alias"`
	Host           string `json:"host"`             // Encrypted base64
	User           string `json:"user"`             // Encrypted base64
	Password       string `json:"password"`         // Encrypted base64
	Port           int    `json:"port"`
	ProjectPath    string `json:"project_path"`     // Plaintext local directory
	PrivateKeyPath string `json:"private_key_path"` // Encrypted base64
	UseSSHAgent    bool   `json:"use_ssh_agent"`
}

const (
	configDirName  = "sshmanager"
	configFileName = "servers.json"
	keyFileName    = "secret.key"
)

var ConfigDirOverride string

func getConfigDir() (string, error) {
	if ConfigDirOverride != "" {
		return ConfigDirOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", configDirName)
	return dir, nil
}

// getOrGenerateKey reads the secret key or creates one if it doesn't exist.
func getOrGenerateKey() ([]byte, error) {
	dir, err := getConfigDir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	keyPath := filepath.Join(dir, keyFileName)
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		// Generate 32 bytes for AES-256
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath, key, 0600); err != nil {
			return nil, err
		}
		return key, nil
	}

	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}

	if len(key) != 32 {
		return nil, errors.New("invalid secret key length, expected 32 bytes")
	}

	return key, nil
}

// Encrypt encrypts plaintext using AES-256-GCM (exported for testing).
func Encrypt(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("invalid key size, expected 32 bytes")
	}
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts AES-256-GCM ciphertext (exported for testing).
func Decrypt(ciphertextBase64 string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("invalid key size, expected 32 bytes")
	}
	if ciphertextBase64 == "" {
		return "", nil
	}

	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// LoadServers reads the server config file and decrypts the sensitive fields.
func LoadServers() ([]Server, error) {
	dir, err := getConfigDir()
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(dir, configFileName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return []Server{}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var rawServers []Server
	if err := json.Unmarshal(data, &rawServers); err != nil {
		return nil, err
	}

	key, err := getOrGenerateKey()
	if err != nil {
		return nil, fmt.Errorf("failed to get/generate key: %w", err)
	}

	servers := make([]Server, len(rawServers))
	for i, s := range rawServers {
		decHost, err := Decrypt(s.Host, key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt host for %s: %w", s.Alias, err)
		}
		decUser, err := Decrypt(s.User, key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt user for %s: %w", s.Alias, err)
		}
		decPass, err := Decrypt(s.Password, key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt password for %s: %w", s.Alias, err)
		}
		decKeyPath, err := Decrypt(s.PrivateKeyPath, key)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt private key path for %s: %w", s.Alias, err)
		}

		servers[i] = Server{
			Alias:          s.Alias,
			Host:           decHost,
			User:           decUser,
			Password:       decPass,
			Port:           s.Port,
			ProjectPath:    s.ProjectPath,
			PrivateKeyPath: decKeyPath,
			UseSSHAgent:    s.UseSSHAgent,
		}
	}

	return servers, nil
}

// SaveServers encrypts fields and writes the servers list to servers.json.
func SaveServers(servers []Server) error {
	dir, err := getConfigDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	key, err := getOrGenerateKey()
	if err != nil {
		return fmt.Errorf("failed to get/generate key: %w", err)
	}

	encryptedServers := make([]Server, len(servers))
	for i, s := range servers {
		encHost, err := Encrypt(s.Host, key)
		if err != nil {
			return err
		}
		encUser, err := Encrypt(s.User, key)
		if err != nil {
			return err
		}
		encPass, err := Encrypt(s.Password, key)
		if err != nil {
			return err
		}
		encKeyPath, err := Encrypt(s.PrivateKeyPath, key)
		if err != nil {
			return err
		}

		encryptedServers[i] = Server{
			Alias:          s.Alias,
			Host:           encHost,
			User:           encUser,
			Password:       encPass,
			Port:           s.Port,
			ProjectPath:    s.ProjectPath,
			PrivateKeyPath: encKeyPath,
			UseSSHAgent:    s.UseSSHAgent,
		}
	}

	data, err := json.MarshalIndent(encryptedServers, "", "  ")
	if err != nil {
		return err
	}

	configPath := filepath.Join(dir, configFileName)
	tempPath := configPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tempPath, configPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	return nil
}
