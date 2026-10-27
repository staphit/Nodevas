package buildinfo

import "testing"

func TestCommitFromLinker(t *testing.T) {
	saved := commit
	t.Cleanup(func() { commit = saved })

	commit = "0123456789abcdef0123456789abcdef01234567"
	if got := Commit(); got != commit {
		t.Fatalf("Commit() = %q, want the linker-set %q", got, commit)
	}
	if got := Version(); got != "0123456" {
		t.Fatalf("Version() = %q, want the short commit", got)
	}
}

func TestCommitIgnoresMalformedLinkerValue(t *testing.T) {
	saved := commit
	t.Cleanup(func() { commit = saved })

	for _, bad := range []string{
		"main",
		"0123456",
		"../../../etc/passwd",
		"0123456789ABCDEF0123456789ABCDEF01234567",
		"0123456789abcdef0123456789abcdef01234567\n",
	} {
		commit = bad
		got := Commit()
		if got == bad {
			t.Fatalf("Commit() passed on the malformed linker value %q", bad)
		}
		if got != "" && !IsCommit(got) {
			t.Fatalf("Commit() = %q, not a full commit", got)
		}
	}
}
