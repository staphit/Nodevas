package remote

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type cancelingReader struct {
	cancel context.CancelFunc
	sent   bool
}

func (r *cancelingReader) Read(buf []byte) (int, error) {
	if r.sent {
		return 0, nil
	}
	r.sent = true
	for i := range buf {
		buf[i] = 'x'
	}
	r.cancel()
	return len(buf), nil
}

func TestFolderPushCancellationRemovesPartialFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	remote, err := NewFolderRemote(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err = remote.Push(ctx, "cancelled", &cancelingReader{cancel: cancel}, -1)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Push returned %v", err)
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled push left files: %v", entries)
	}
}

func TestFolderPullReaderObservesCancellation(t *testing.T) {
	dir := t.TempDir()
	remote, err := NewFolderRemote(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := remote.Push(context.Background(), "bundle", bytes.NewReader([]byte("payload")), 7)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := remote.Pull(ctx, ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	cancel()
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read returned %v", err)
	}
}
