package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreEncryptsAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secret.enc")
	keyPath := filepath.Join(root, "master.key")
	st := New(path, keyPath)
	plain := []byte(`{"password":"smtp-password"}`)

	if err := st.Save(plain); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("smtp-password")) {
		t.Fatal("secret was written in plaintext")
	}
	loaded, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded, plain) {
		t.Fatalf("loaded %q, want %q", loaded, plain)
	}
	if err := st.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret still exists: %v", err)
	}
}

func TestStoreUsesEnvironmentKeyWithoutWritingKeyFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("NODEVAS_SECRET_KEY", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	st := New(filepath.Join(root, "secret.enc"), filepath.Join(root, "master.key"))
	if err := st.Save([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.KeyPath); !os.IsNotExist(err) {
		t.Fatalf("environment key spilled to disk: %v", err)
	}
}
