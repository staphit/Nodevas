// Package secrets provides small, authenticated-encryption-backed files for
// credentials that must stay outside a workspace.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nodevas/internal/store"
)

// Store encrypts one file with AES-256-GCM. The key is either supplied through
// NODEVAS_SECRET_KEY or kept in a 0600 machine-local key file.
type Store struct {
	Path    string
	KeyPath string

	mu sync.Mutex
}

func New(path, keyPath string) *Store {
	return &Store{Path: path, KeyPath: keyPath}
}

// Load returns nil, nil when the secret has not been created yet.
func (s *Store) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	blob, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	key, err := loadKey(s.KeyPath)
	if err != nil {
		return nil, err
	}
	return decrypt(key, blob)
}

func (s *Store) Save(plain []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, err := loadKey(s.KeyPath)
	if err != nil {
		return err
	}
	blob, err := encrypt(key, plain)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	if err := store.WriteMetaFile(s.Path, blob); err != nil {
		return err
	}
	return os.Chmod(s.Path, 0o600)
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func loadKey(keyPath string) ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv("NODEVAS_SECRET_KEY")); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("NODEVAS_SECRET_KEY must be base64: %w", err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("NODEVAS_SECRET_KEY must decode to 32 bytes")
		}
		return key, nil
	}

	data, err := os.ReadFile(keyPath)
	if err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if decodeErr != nil || len(key) != 32 {
			return nil, fmt.Errorf("invalid secret master key %q", keyPath)
		}
		return key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, err
	}
	encoded := []byte(base64.StdEncoding.EncodeToString(key))
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return loadKey(keyPath)
		}
		return nil, err
	}
	if _, writeErr := file.Write(encoded); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(keyPath)
		return nil, writeErr
	}
	if syncErr := file.Sync(); syncErr != nil {
		_ = file.Close()
		_ = os.Remove(keyPath)
		return nil, syncErr
	}
	if closeErr := file.Close(); closeErr != nil {
		_ = os.Remove(keyPath)
		return nil, closeErr
	}
	return key, nil
}

func encrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}

func decrypt(key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
}
