package project

import (
	"io/fs"
	"syscall"
	"testing"

	"nodevas/internal/realtime"
)

// Only an error that says the disk could not be read is a lookup fault.
// One that depends on what the name names (a file where a folder belongs,
// a name the file system will not take) is an answer: as a fault it would
// tell an outsider that the name is there.
func TestOnlyADiskFaultIsALookupFault(t *testing.T) {
	pm, err := NewManagerAt(t.TempDir(), realtime.NewHub(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pm.Close)
	if err := pm.Activate("other", true, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"other/graph.yaml", "other/graph.yaml/deeper"} {
		if _, err := pm.StoreFor(name); err == nil || IsLookupFault(err) {
			t.Errorf("StoreFor(%q) = %v (fault %v); want a plain error", name, err, IsLookupFault(err))
		}
	}
	for _, answer := range []error{syscall.ENOTDIR, syscall.ENAMETOOLONG, syscall.EINVAL, syscall.EACCES} {
		if IsLookupFault(lookupFailure("x", &fs.PathError{Op: "lstat", Path: "x", Err: answer})) {
			t.Errorf("%v was taken for a disk fault", answer)
		}
	}
	for _, fault := range []error{syscall.EIO, syscall.EMFILE, syscall.ENFILE, syscall.ENOMEM} {
		if !IsLookupFault(lookupFailure("x", &fs.PathError{Op: "lstat", Path: "x", Err: fault})) {
			t.Errorf("%v was not taken for a disk fault", fault)
		}
	}
}
