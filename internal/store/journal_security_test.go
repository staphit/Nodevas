package store

import (
	"errors"
	"os"
	"testing"
)

func TestJournalRejectsOversizedEventAndTotalFile(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.appendJournal(make([]byte, maxJournalLineBytes+1)); err == nil {
		t.Fatal("oversized journal event was accepted")
	}
	if err := os.MkdirAll(st.Root()+"/run", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(st.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxJournalBytes); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.appendJournal([]byte("{}\n")); err == nil {
		t.Fatal("journal grew beyond its safety limit")
	}
}

func TestLoadStateRejectsOversizedJournalWithoutUnboundedRead(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := os.MkdirAll(st.Root()+"/run", 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(st.JournalPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxJournalBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadState(); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized journal error = %v", err)
	}
}
