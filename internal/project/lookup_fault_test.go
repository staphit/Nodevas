package project

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"testing"

	"nodevas/internal/realtime"
)

// A disk that cannot be read is not an answer about the project: StoreFor
// says so (IsLookupFault), so callers can tell "try again" from "no".
func TestStoreForTellsADiskFaultFromAMissingProject(t *testing.T) {
	pm, err := NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	if err := pm.Activate("other", true, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.StoreFor("nosuch"); err == nil || IsLookupFault(err) {
		t.Fatalf("a missing project: %v (fault %v); want a plain error", err, IsLookupFault(err))
	}
	if _, err := pm.StoreFor("bad\x00name"); err == nil || IsLookupFault(err) {
		t.Fatalf("an invalid name: %v; want a plain error", err)
	}

	real := statProjectPath
	t.Cleanup(func() { statProjectPath = real })
	for i, where := range []string{"folder", "graph.yaml"} {
		name := []string{"fresh-a", "fresh-b"}[i]
		statProjectPath = func(root, path string) (fs.FileInfo, error) {
			if (where == "graph.yaml") == strings.HasSuffix(path, string(os.PathSeparator)+"graph.yaml") {
				return nil, &fs.PathError{Op: "lstat", Path: path, Err: syscall.EIO}
			}
			return real(root, path)
		}
		_, err := pm.StoreFor(name)
		if !IsLookupFault(err) {
			t.Errorf("stat of the %s failing: %v; want a lookup fault", where, err)
		}
		statProjectPath = func(string, string) (fs.FileInfo, error) { return nil, os.ErrNotExist }
		if _, err := pm.StoreFor(name); err == nil || IsLookupFault(err) {
			t.Errorf("a %s that is not there: %v; want a plain error", where, err)
		}
		statProjectPath = real
	}
	if !errors.Is(&LookupError{Name: "x", Err: syscall.EIO}, syscall.EIO) {
		t.Error("LookupError does not unwrap")
	}
}
