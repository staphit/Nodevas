package secrets

import (
	"fmt"
	"os"
	"path/filepath"
)

// MachineKeyPath is where this machine's key lives when NODEVAS_SECRET_KEY is
// not set: <user config dir>/nodevas/secrets/master.key, beside the encrypted
// files every other Store on the server keeps. The server and the CLI both
// derive it this way, so a value one of them sealed the other can open.
func MachineKeyPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config directory: %w", err)
	}
	return filepath.Join(root, "nodevas", "secrets", "master.key"), nil
}

// Seal encrypts plain with AES-256-GCM under the machine key at keyPath (or
// NODEVAS_SECRET_KEY), creating the key file on first use exactly as a Store
// does. aad is authenticated but not stored: Open must be given the same
// bytes, which is how a caller binds a ciphertext to the row it belongs to so
// that copying it onto another row does not make it decrypt there.
//
// It is for small values kept somewhere other than a file of their own — a
// database column — that still must not be readable from that copy alone.
func Seal(keyPath string, plain, aad []byte) ([]byte, error) {
	key, err := loadKey(keyPath)
	if err != nil {
		return nil, err
	}
	return encrypt(key, plain, aad)
}

// Open reverses Seal. A wrong key, a tampered blob and a different aad are
// all the same error.
func Open(keyPath string, blob, aad []byte) ([]byte, error) {
	key, err := loadKey(keyPath)
	if err != nil {
		return nil, err
	}
	return decrypt(key, blob, aad)
}
