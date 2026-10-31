package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nodevas/internal/runnerwire"
)

// A mail goes out in the language of the request that caused it: the
// passcode in the language of the browser that asked for it, the new-device
// notice in the language of the browser that made the link (the runner that
// redeems it is a program and says nothing of its owner's language).

func TestPasscodeMailSpeaksTheAskersLanguage(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			server, _, inbox := accountServerForTest(t)
			before := inbox.count()
			request := httptest.NewRequest(http.MethodPost, "/api/auth/otp/request",
				strings.NewReader(`{"email":"`+testEmail+`"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept-Language", language)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusAccepted {
				t.Fatalf("passcode request = %d %s", response.Code, response.Body)
			}
			inbox.waitFor(t, before+1)
			mail := inbox.last(t)
			if chinese := strings.HasPrefix(language, "zh"); hasCJK(mail.body) != chinese {
				t.Fatalf("passcode mail for %s reads:\n%s", language, mail.body)
			}
			inbox.code(t)
		})
	}
}

func TestDeviceLinkMailSpeaksItsMakersLanguage(t *testing.T) {
	for _, language := range []string{"en-US,en;q=0.9", "zh-TW,zh;q=0.9"} {
		t.Run(language, func(t *testing.T) {
			s := newRunnerTestServer(t)
			bea := s.signIn(t, "bea@example.test")
			stepUp(t, s, bea)
			response := s.do(t, &bea, http.MethodPost, "/api/runners/links", `{"name":"laptop"}`,
				map[string]string{"Accept-Language": language})
			if response.Code != http.StatusCreated {
				t.Fatalf("create link = %d %s", response.Code, response.Body)
			}
			var created runnerwire.CreateLinkResponse
			if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			payload, err := runnerwire.DecodeLink(created.Link)
			if err != nil {
				t.Fatal(err)
			}
			mails := s.inbox.count()
			if code, _ := s.redeem(t, payload.Code); code != http.StatusCreated {
				t.Fatalf("redeem = %d", code)
			}
			s.inbox.waitFor(t, mails+1)
			mail := s.inbox.last(t)
			if chinese := strings.HasPrefix(language, "zh"); hasCJK(mail.body) != chinese {
				t.Fatalf("notice for %s reads:\n%s", language, mail.body)
			}
		})
	}
}
