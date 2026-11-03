package locale

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
)

func TestMessageRendersInEitherLanguage(t *testing.T) {
	message := Say("node “%s” is gone", "節點「%s」不見了", "Alpha")
	if got := message.In(English); got != "node “Alpha” is gone" {
		t.Fatalf("English = %q", got)
	}
	if got := message.In(Chinese); got != "節點「Alpha」不見了" {
		t.Fatalf("Chinese = %q", got)
	}
	if message.Error() != message.In(English) {
		t.Fatalf("Error() = %q, want the English", message.Error())
	}
}

// A sentence of the hub's wrapped in someone else's words, or wrapping
// someone else's error, is translated and the rest is left as it was.
func TestRenderTranslatesOnlyTheHubsOwnWords(t *testing.T) {
	inner := Errorf("copy failed: %w", "複製失敗：%w", fs.ErrPermission)
	if !errors.Is(inner, fs.ErrPermission) {
		t.Fatal("the wrapped error is not reachable")
	}
	outer := fmt.Errorf("load project %q: %w", "main", inner)
	if got := Render(outer, Chinese); got != `load project "main": 複製失敗：permission denied` {
		t.Fatalf("Chinese = %q", got)
	}
	if got := Render(outer, English); got != `load project "main": copy failed: permission denied` {
		t.Fatalf("English = %q", got)
	}
	nested := Errorf("open failed: %w", "開啟失敗：%w", inner)
	if got := Render(nested, Chinese); got != "開啟失敗：複製失敗：permission denied" {
		t.Fatalf("nested Chinese = %q", got)
	}
	if got := Render(errors.New("plain"), Chinese); got != "plain" {
		t.Fatalf("plain error = %q", got)
	}
}

func TestFromTag(t *testing.T) {
	for tag, want := range map[string]Language{
		"":                       English,
		"en-US,en;q=0.9":         English,
		"zh-TW":                  Chinese,
		"zh-Hant-TW,zh;q=0.9":    Chinese,
		"ZH":                     Chinese,
		"fr-FR,zh-TW;q=0.9":      English,
		" zh-CN ;q=1, en;q=0.5 ": Chinese,
	} {
		if got := FromTag(tag); got != want {
			t.Errorf("FromTag(%q) = %q, want %q", tag, got, want)
		}
	}
}
