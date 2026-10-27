// Package embeddedpg runs the PostgreSQL server a standalone or desktop
// Nodevas keeps its data in when no NODEVAS_DATABASE_URL is configured.
//
// The binaries are a pinned theseus-rs/postgresql-binaries release whose
// SHA-256 per target is compiled in (manifest.go): the desktop apps ship them,
// the plain CLI downloads them once into NODEVAS_PG_HOME and refuses anything
// whose hash differs. A cluster is one data directory per workspace, listening
// on 127.0.0.1 only, on a free port, behind SCRAM with a random password kept
// in owner-only files; its postmaster is started before the hub and stopped
// with it, and a crashed predecessor's postmaster.pid is recovered.
//
// RED PHASE: every exported function is a compile-only stub returning
// ErrNotImplemented; the tests in this package describe the behaviour.
package embeddedpg

import "errors"

// PinnedVersion is the PostgreSQL release every target is pinned to.
const PinnedVersion = "17.11.1"

var (
	// ErrNotImplemented is what every stub returns until the package exists.
	ErrNotImplemented = errors.New("embeddedpg: not implemented")
	// ErrChecksumMismatch is a downloaded or supplied archive whose SHA-256
	// differs from the manifest.
	ErrChecksumMismatch = errors.New("embeddedpg: archive checksum does not match the pinned SHA-256")
	// ErrUnsafeArchive is an archive entry that would land outside the
	// install directory (absolute path, "..", or a symlink leaving the root).
	ErrUnsafeArchive = errors.New("embeddedpg: unsafe archive entry")
	// ErrStampMismatch is a preinstalled binaries directory whose stamp does
	// not name the pinned version and archive hash.
	ErrStampMismatch = errors.New("embeddedpg: binaries stamp does not match the pinned manifest")
	// ErrOffline is a needed download that could not happen.
	ErrOffline = errors.New("embeddedpg: PostgreSQL binaries are not installed and could not be downloaded")
	// ErrClusterBusy is a cluster another live process owns.
	ErrClusterBusy = errors.New("embeddedpg: the database cluster is in use by another process")
)
