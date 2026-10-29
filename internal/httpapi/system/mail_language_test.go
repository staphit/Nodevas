package system

import (
	"strings"
	"testing"
	"time"

	"nodevas/internal/auth"
	"nodevas/internal/identity"
	"nodevas/internal/locale"
)

// hasChinese reports whether text holds a Chinese character or full-width
// punctuation.
func hasChinese(text string) bool {
	for _, r := range text {
		if (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3000 && r <= 0x303f) || (r >= 0xff00 && r <= 0xffef) {
			return true
		}
	}
	return false
}

// Every mail the server sends is written in its recipient's language, all of
// it: an English reader gets no Chinese line and a Chinese reader no English
// one beyond the names, addresses and codes it carries.
func TestEveryAccountMailSpeaksItsRecipientsLanguage(t *testing.T) {
	challenge := auth.Challenge{Code: "ABCD2345", Email: "bea@example.test", Expires: time.Date(2026, 10, 29, 9, 30, 0, 0, time.Local)}
	account := accountView{Name: "bea", Email: "bea@example.test", Role: identity.RoleAdmin}
	inviter := identity.Actor{Name: "ann"}
	for _, language := range []locale.Language{locale.English, locale.Chinese} {
		chinese := language == locale.Chinese
		mails := []struct {
			name, subject, body string
			keep                []string
		}{
			{"sign-in code", otpSubject(language), otpBody(challenge, language), []string{"ABCD2345", "09:30"}},
			{"step-up code", stepUpSubject(language), stepUpBody(challenge, language), []string{"ABCD2345", "09:30"}},
			{"invitation", inviteSubject(language), inviteText(account, inviter, "https://hub.example", language),
				[]string{"ann", "bea", "bea@example.test", "https://hub.example"}},
		}
		for _, mail := range mails {
			if hasChinese(mail.subject) != chinese {
				t.Errorf("%s subject for %s reads %q", mail.name, language, mail.subject)
			}
			for _, line := range strings.Split(mail.body, "\n") {
				if strings.TrimSpace(line) == "" || strings.Trim(line, "—") == "" {
					continue
				}
				if hasChinese(line) != chinese {
					t.Errorf("%s for %s has the line %q", mail.name, language, line)
				}
			}
			for _, name := range mail.keep {
				if !strings.Contains(mail.body, name) {
					t.Errorf("%s for %s lost %q:\n%s", mail.name, language, name, mail.body)
				}
			}
		}
	}
}
