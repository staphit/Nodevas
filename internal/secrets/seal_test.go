package secrets

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestSealRoundTripsAndBindsTheAssociatedData(t *testing.T) {
	t.Setenv("NODEVAS_SECRET_KEY", "")
	keyPath := filepath.Join(t.TempDir(), "master.key")
	plain := []byte("totp-secret-bytes")

	blob, err := Seal(keyPath, plain, []byte("row-a"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("sealed blob contains the plaintext")
	}
	opened, err := Open(keyPath, blob, []byte("row-a"))
	if err != nil || !bytes.Equal(opened, plain) {
		t.Fatalf("Open = %q, %v", opened, err)
	}
	if _, err := Open(keyPath, blob, []byte("row-b")); err == nil {
		t.Fatal("a blob opened under different associated data")
	}
	other := filepath.Join(t.TempDir(), "master.key")
	if _, err := Open(other, blob, []byte("row-a")); err == nil {
		t.Fatal("a blob opened under a different machine key")
	}
}
