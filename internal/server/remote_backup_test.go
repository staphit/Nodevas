package server

import (
	"context"
	"nodevas/internal/remote"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDueForBackup(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		cfg  remote.Config
		last time.Time
		want bool
	}{
		{"disabled", remote.Config{Kind: "folder", IntervalHours: 24}, time.Time{}, false},
		{"no backend", remote.Config{AutoBackup: true, IntervalHours: 24}, time.Time{}, false},
		{"never backed up", remote.Config{Kind: "folder", AutoBackup: true, IntervalHours: 24}, time.Time{}, true},
		{"recent", remote.Config{Kind: "folder", AutoBackup: true, IntervalHours: 24}, now.Add(-time.Hour), false},
		{"overdue", remote.Config{Kind: "folder", AutoBackup: true, IntervalHours: 1}, now.Add(-2 * time.Hour), true},
		{"zero interval falls back to default", remote.Config{Kind: "folder", AutoBackup: true}, now.Add(-2 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := remote.DueForBackup(tc.cfg, tc.last, now); got != tc.want {
				t.Fatalf("dueForBackup = %v, want %v", got, tc.want)
			}
		})
	}
}

func countBundles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), remote.RemoteBundleExt) {
			n++
		}
	}
	return n
}

func TestScheduledBackupPushesWhenDueAndRecordsState(t *testing.T) {
	pm := projectManagerForTest(t)
	manager := remote.NewManager(pm)
	backupDir := t.TempDir()
	if err := manager.Save(remote.Config{
		Kind: "folder", Folder: backupDir, AutoBackup: true, IntervalHours: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// Never backed up, so the first check is due.
	manager.MaybeBackup()
	if got := countBundles(t, backupDir); got != 1 {
		t.Fatalf("after first backup: %d bundles, want 1", got)
	}
	if manager.LastBackupAt().IsZero() {
		t.Fatal("last-backup timestamp was not recorded")
	}

	// A second check right away is not due, so nothing new is pushed.
	manager.MaybeBackup()
	if got := countBundles(t, backupDir); got != 1 {
		t.Fatalf("second check pushed again: %d bundles, want 1", got)
	}

	// Backdating the state makes it due again, but the snapshot hash is still
	// identical, so the scheduler must not create duplicate cloud versions.
	if err := manager.SetLastBackupAt(time.Now().Add(-2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	manager.MaybeBackup()
	if got := countBundles(t, backupDir); got != 1 {
		t.Fatalf("unchanged workspace created a duplicate bundle: %d", got)
	}
}

func TestScheduledBackupSkippedWhenDisabled(t *testing.T) {
	pm := projectManagerForTest(t)
	manager := remote.NewManager(pm)
	backupDir := t.TempDir()
	if err := manager.Save(remote.Config{
		Kind: "folder", Folder: backupDir, AutoBackup: false, IntervalHours: 1,
	}); err != nil {
		t.Fatal(err)
	}
	manager.MaybeBackup()
	if _, err := os.Stat(backupDir); err != nil {
		t.Fatal(err)
	}
	if got := countBundles(t, backupDir); got != 0 {
		t.Fatalf("disabled backup still pushed: %d bundles", got)
	}
	if !manager.LastBackupAt().IsZero() {
		t.Fatal("disabled backup recorded a timestamp")
	}
}

func TestFinalBackupFlushesChangedWorkspace(t *testing.T) {
	pm := projectManagerForTest(t)
	manager := remote.NewManager(pm)
	backupDir := t.TempDir()
	if err := manager.Save(remote.Config{
		Kind: "folder", Folder: backupDir, AutoBackup: true, IntervalHours: 24,
	}); err != nil {
		t.Fatal(err)
	}
	manager.MaybeBackup()
	if got := countBundles(t, backupDir); got != 1 {
		t.Fatalf("initial backup count = %d, want 1", got)
	}

	graphPath := filepath.Join(pm.Workspace(), "main", "graph.yaml")
	graph, err := os.ReadFile(graphPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(graphPath, append(graph, []byte("\n# changed before shutdown\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.FinalBackup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := countBundles(t, backupDir); got != 2 {
		t.Fatalf("final backup count = %d, want 2", got)
	}
}

func TestBackupStatePathDistinctFromConfig(t *testing.T) {
	pm := projectManagerForTest(t)
	manager := remote.NewManager(pm)
	if manager.StatePath() == manager.ConfigPath() {
		t.Fatal("state and config must not share a file")
	}
	if filepath.Dir(manager.StatePath()) != filepath.Dir(manager.ConfigPath()) {
		t.Fatal("state and config should sit in the same secrets dir")
	}
}
