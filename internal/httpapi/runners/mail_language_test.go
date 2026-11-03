package runners

import (
	"strings"
	"testing"
	"time"

	"nodevas/internal/locale"
	"nodevas/internal/runnerwire"
)

func hasChinese(text string) bool {
	for _, r := range text {
		if (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

// The new-device notice is written in its recipient's language, all of it.
func TestNewDeviceMailSpeaksItsRecipientsLanguage(t *testing.T) {
	runner := runnerwire.Runner{ID: "r1", Name: "laptop"}
	at := time.Date(2026, 10, 29, 1, 2, 3, 0, time.UTC)
	for _, language := range []locale.Language{locale.English, locale.Chinese} {
		chinese := language == locale.Chinese
		subject, body := redeemMail(runner, "new-laptop", "linux", "192.0.2.7", at, language)
		if hasChinese(subject) != chinese {
			t.Errorf("subject for %s reads %q", language, subject)
		}
		for _, line := range strings.Split(body, "\n") {
			if strings.TrimSpace(line) != "" && hasChinese(line) != chinese {
				t.Errorf("notice for %s has the line %q", language, line)
			}
		}
		for _, name := range []string{"laptop", "new-laptop", "linux", "192.0.2.7"} {
			if !strings.Contains(body, name) {
				t.Errorf("notice for %s lost %q:\n%s", language, name, body)
			}
		}
	}
}
