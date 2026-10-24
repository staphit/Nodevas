package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkConfigRoundTripAndMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodevas", "agent-network.json")
	cfg, err := LoadNetworkConfig(path)
	if err != nil || len(cfg.ExcludeFolders) != 0 {
		t.Fatalf("missing file = %+v, %v; want empty config", cfg, err)
	}
	private := filepath.Join(t.TempDir(), "private")
	if _, added, err := cfg.AddExclude(private); err != nil || !added {
		t.Fatalf("AddExclude = %v, %v", added, err)
	}
	if _, added, _ := cfg.AddExclude(private + string(filepath.Separator)); added {
		t.Fatal("the same folder was added twice")
	}
	if err := SaveNetworkConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"excludeFolders"`) {
		t.Fatalf("file = %s", raw)
	}
	back, err := LoadNetworkConfig(path)
	if err != nil || len(back.ExcludeFolders) != 1 || back.ExcludeFolders[0] != filepath.Clean(private) {
		t.Fatalf("reloaded = %+v, %v", back, err)
	}
	if _, removed, _ := back.RemoveExclude(private); !removed || len(back.ExcludeFolders) != 0 {
		t.Fatalf("RemoveExclude left %+v", back.ExcludeFolders)
	}
	if _, removed, _ := back.RemoveExclude(private); removed {
		t.Fatal("removing an absent folder reported a removal")
	}
}

func TestExcludedMatchesTheFolderAndBelowOnly(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	cfg := NetworkConfig{ExcludeFolders: []string{private, "relative/ignored"}}
	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{private, true},
		{filepath.Join(private, "repo", "sub"), true},
		{filepath.Join(root, "private-other"), false},
		{root, false},
		{filepath.Join(root, "relative", "ignored"), false},
	} {
		if got := cfg.Excluded(tc.dir) != ""; got != tc.want {
			t.Errorf("Excluded(%s) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}

func TestExcludedFoldsCaseOnlyWhereTheFileSystemDoes(t *testing.T) {
	saved := foldCase
	t.Cleanup(func() { foldCase = saved })
	root := t.TempDir()
	cfg := NetworkConfig{ExcludeFolders: []string{filepath.Join(root, "Private")}}
	upper := filepath.Join(root, "PRIVATE", "repo")

	foldCase = true
	if cfg.Excluded(upper) == "" {
		t.Fatal("case-insensitive match missed")
	}
	foldCase = false
	if cfg.Excluded(upper) != "" {
		t.Fatal("case-sensitive file system matched a differently cased folder")
	}
}

func TestExcludedFolderReportsABrokenConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-network.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder, err := ExcludedFolder(path, t.TempDir())
	if err == nil || folder != "" {
		t.Fatalf("broken config = %q, %v; want on the network with an error", folder, err)
	}
}
