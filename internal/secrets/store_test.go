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

// A container keeps the workspace and the key's default home in one volume,
// so a backup of it alone would open every stored secret. The key can come
// from a file elsewhere instead (a Docker or Kubernetes secret), exactly as
// if it were in NODEVAS_SECRET_KEY.
func TestStoreUsesKeyFileFromEnvironmentWithoutWritingKeyFile(t *testing.T) {
	root := t.TempDir()
	const encoded = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	keyFile := filepath.Join(t.TempDir(), "secret_key")
	if err := os.WriteFile(keyFile, []byte(encoded+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODEVAS_SECRET_KEY", "")
	t.Setenv("NODEVAS_SECRET_KEY_FILE", keyFile)
	st := New(filepath.Join(root, "secret.enc"), filepath.Join(root, "master.key"))
	if err := st.Save([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.KeyPath); !os.IsNotExist(err) {
		t.Fatalf("a key was written into the volume beside the secrets: %v", err)
	}
	// The file's key is the key: the same value in NODEVAS_SECRET_KEY opens it.
	t.Setenv("NODEVAS_SECRET_KEY_FILE", "")
	t.Setenv("NODEVAS_SECRET_KEY", encoded)
	if got, err := st.Load(); err != nil || string(got) != "secret" {
		t.Fatalf("Load with the same key = %q, %v", got, err)
	}
}

func TestStoreRefusesAmbiguousOrBadKeyFile(t *testing.T) {
	root := t.TempDir()
	st := New(filepath.Join(root, "secret.enc"), filepath.Join(root, "master.key"))
	good := filepath.Join(t.TempDir(), "good")
	if err := os.WriteFile(good, []byte("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="), 0o600); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(t.TempDir(), "short")
	if err := os.WriteFile(short, []byte("AAEC"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string][2]string{
		"both":    {"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", good},
		"missing": {"", filepath.Join(root, "absent")},
		"short":   {"", short},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("NODEVAS_SECRET_KEY", env[0])
			t.Setenv("NODEVAS_SECRET_KEY_FILE", env[1])
			if err := st.Save([]byte("secret")); err == nil {
				t.Fatal("Save succeeded")
			}
			if _, err := os.Stat(st.KeyPath); !os.IsNotExist(err) {
				t.Fatalf("a key was generated instead: %v", err)
			}
		})
	}
}
