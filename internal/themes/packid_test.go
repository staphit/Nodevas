package themes

import "testing"

func TestThePackIdComesFromItsEnglishName(t *testing.T) {
	cases := map[string]string{
		"Magic world":      "magic-world",
		"magic world":      "magic-world",
		"Dark":             "dark",
		"  My_Pack v2.0  ": "my-pack-v2-0",
		"Rock & Roll!":     "rock-roll",
		"../magic":         "magic",
	}
	for name, want := range cases {
		got, err := packID(name)
		if err != nil || got != want {
			t.Errorf("packID(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"", "!!!", "魔法世界", "魔法 world", "none", "NONE", "default", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		if got, err := packID(name); err == nil {
			t.Errorf("packID(%q) = %q, want a refusal", name, got)
		}
	}
}
