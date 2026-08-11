package remote

import (
	"errors"
	"path/filepath"
	"testing"

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
