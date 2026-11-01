package auth

import (
	"net/http/httptest"
	"testing"
)

func TestSessionTokenReadsOneUnambiguousCookie(t *testing.T) {
	for _, testCase := range []struct {
		name, target, cookie, want string
		ok                         bool
	}{
		{"plain over HTTP", "http://hub/", "nodevas_session=a", "a", true},
		{"two plain over HTTP", "http://hub/", "nodevas_session=a; nodevas_session=b", "", false},
		{"__Host- over TLS", "https://hub/", "__Host-nodevas_session=a", "a", true},
		{"__Host- beside a tossed plain one", "https://hub/", "nodevas_session=evil; __Host-nodevas_session=a", "a", true},
		{"plain only over TLS", "https://hub/", "nodevas_session=evil", "", false},
		{"two __Host-", "https://hub/", "__Host-nodevas_session=a; __Host-nodevas_session=b", "", false},
		// The copilot's in-process requests replay a TLS browser's cookies
		// without TLS of their own.
		{"__Host- replayed in process", "http://hub/", "__Host-nodevas_session=a", "a", true},
	} {
		request := httptest.NewRequest("GET", testCase.target, nil)
		request.Header.Set("Cookie", testCase.cookie)
		got, ok := SessionToken(request)
		if got != testCase.want || ok != testCase.ok {
			t.Errorf("%s: SessionToken = %q %v, want %q %v", testCase.name, got, ok, testCase.want, testCase.ok)
		}
	}
}
