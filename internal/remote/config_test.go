package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nodevas/internal/project"
)

func TestFolderRemoteRejectedAtRuntimeOnRemoteServer(t *testing.T) {
	workspace := t.TempDir()
	manager, err := project.NewManagerAt(workspace, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remoteManager := NewManager(manager)
	cfg := Config{Kind: "folder", Folder: filepath.Join(t.TempDir(), "backups")}

	manager.SetRemote(true)
	if _, err := remoteManager.BuildRemote(cfg); !errors.Is(err, ErrFolderRemote) {
		t.Fatalf("BuildRemote returned %v", err)
	}
	if err := remoteManager.Save(cfg); !errors.Is(err, ErrFolderRemote) {
		t.Fatalf("Save returned %v", err)
	}

	manager.SetRemote(false)
	store, err := remoteManager.BuildRemote(cfg)
	if err != nil {
		t.Fatalf("local folder backend: %v", err)
	}
	if store == nil || store.Kind() != "folder" {
		t.Fatalf("local folder backend = %#v", store)
	}
}

// A config saved by a build that still had the Google Drive backend must not
// break this one: it reads as "no remote configured", so backups stay off
// instead of every call failing on an unknown backend.
func TestLegacyDriveConfigLoadsAsNoRemote(t *testing.T) {
	manager, err := project.NewManagerAt(t.TempDir(), nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	remoteManager := NewManager(manager)
	path := remoteManager.ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"kind":"drive","driveFolderId":"abc","autoBackup":true,"intervalHours":6}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	if cfg := remoteManager.Load(); cfg != (Config{}) {
		t.Fatalf("Load() = %+v, want empty config", cfg)
	}
	store, err := remoteManager.Store()
	if err != nil || store != nil {
		t.Fatalf("Store() = %v, %v; want nil, nil", store, err)
	}
	if DueForBackup(remoteManager.Load(), time.Time{}, time.Now()) {
		t.Fatal("a retired backend scheduled an automatic backup")
	}
}
