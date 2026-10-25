package runners

import (
	"errors"
	"testing"
	"time"

	"nodevas/internal/identity"
	"nodevas/internal/runnerwire"
)

func TestLinksAreSingleUseAndExpire(t *testing.T) {
	l := NewLinks()
	now := time.Now()
	l.SetClock(func() time.Time { return now })
	bea := identity.Actor{ID: "u-bea", Name: "bea"}

	code, expires, err := l.Issue(bea, "rev1", "laptop")
	if err != nil || len(code) < 40 || !expires.Equal(now.Add(runnerwire.LinkTTLSeconds*time.Second)) {
		t.Fatalf("issue = %q %v %v", code, expires, err)
	}
	other, _, _ := l.Issue(bea, "rev1", "desk")
	if other == code {
		t.Fatal("two links share a code")
	}
	link, err := l.Redeem(code, "1.2.3.4")
	if err != nil || link.Owner.ID != "u-bea" || link.Name != "laptop" || link.Revision != "rev1" {
		t.Fatalf("redeem = %+v %v", link, err)
	}
	if _, err := l.Redeem(code, "1.2.3.4"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("replay = %v", err)
	}
	now = now.Add(runnerwire.LinkTTLSeconds * time.Second)
	if _, err := l.Redeem(other, "5.6.7.8"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("expired = %v", err)
	}
	if l.Outstanding("u-bea") != 0 {
		t.Fatal("expired links still counted")
	}
}

func TestLinksCapAndThrottle(t *testing.T) {
	l := NewLinks()
	bea := identity.Actor{ID: "u-bea"}
	var last string
	for i := 0; i < runnerwire.MaxLinksPerUser; i++ {
		code, _, err := l.Issue(bea, "", "laptop")
		if err != nil {
			t.Fatal(err)
		}
		last = code
	}
	if _, _, err := l.Issue(bea, "", "laptop"); !errors.Is(err, ErrTooManyLinks) {
		t.Fatalf("over the cap = %v", err)
	}
	if _, _, err := l.Issue(identity.Actor{}, "", "x"); err == nil {
		t.Fatal("a link without an owner")
	}
	for i := 0; i < linkFailureLimit; i++ {
		if _, err := l.Redeem("nope", "9.9.9.9"); !errors.Is(err, ErrLinkNotFound) {
			t.Fatalf("miss %d = %v", i, err)
		}
	}
	if _, err := l.Redeem(last, "9.9.9.9"); !errors.Is(err, ErrLinkThrottled) {
		t.Fatalf("throttled source = %v", err)
	}
	// Another source is unaffected, and the code was not spent by the refusal.
	if _, err := l.Redeem(last, "8.8.8.8"); err != nil {
		t.Fatalf("other source = %v", err)
	}
	if _, err := l.Redeem("", "7.7.7.7"); !errors.Is(err, ErrLinkNotFound) {
		t.Fatalf("empty code = %v", err)
	}
}
