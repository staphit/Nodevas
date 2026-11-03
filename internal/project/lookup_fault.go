package project

import (
	"errors"
	"slices"
	"syscall"

	"nodevas/internal/store"
)

// statProjectPath is the stat a lookup makes; tests stand a disk fault in
// for it.
var statProjectPath = store.StatProjectPath

// LookupError is a project lookup that failed for a reason other than the
// project's absence or an unsafe path: the disk could not be read. It is
// not an answer about the project, so callers report it as a passing
// failure (try again) rather than as "no such project" or "not yours".
type LookupError struct {
	Name string
	Err  error
}

func (e *LookupError) Error() string { return "look up project " + e.Name + ": " + e.Err.Error() }

func (e *LookupError) Unwrap() error { return e.Err }

// IsLookupFault reports whether err is a LookupError.
func IsLookupFault(err error) bool {
	var lookup *LookupError
	return errors.As(err, &lookup)
}

// lookupFailure is err from a lookup's stat. Only an error saying the disk
// could not be read (diskFaultErrnos: I/O errors, out of handles or memory,
// a dropped network share) is a LookupError. Anything else is an answer
// about the name and refused as such: an error that depends on what the
// name names (a file where a folder belongs, a link, a name the file
// system will not take), answered as a passing fault, would tell an
// outsider that the name, or a file inside a project, is there.
func lookupFailure(name string, err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) && slices.Contains(diskFaultErrnos, errno) {
		return &LookupError{Name: name, Err: err}
	}
	return err
}

// lookupFault, when set, is what every StoreFor fails with
// (InjectLookupFault).
type lookupFault struct{ err error }

// InjectLookupFault makes every lookup that reaches the disk fail with a
// LookupError wrapping err until restore is called. It stands in for a disk
// fault in tests of the packages that call StoreFor.
func (pm *ProjectManager) InjectLookupFault(err error) (restore func()) {
	pm.fault.Store(&lookupFault{err: err})
	return func() { pm.fault.Store(nil) }
}

func (pm *ProjectManager) injectedFault() error {
	if fault := pm.fault.Load(); fault != nil {
		return fault.err
	}
	return nil
}
