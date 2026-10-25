package runnerwire

import (
	"strings"
	"testing"
)

func TestLinkRoundTrip(t *testing.T) {
	in := LinkPayload{Hub: "https://hub.example", Code: "abc", AccessClientID: "id.access", AccessClientSecret: "sec"}
	link, err := EncodeLink(in)
	if err != nil || !strings.HasPrefix(link, LinkPrefix) || strings.ContainsAny(link, "+/= \n") {
		t.Fatalf("encode = %q %v", link, err)
	}
	out, err := DecodeLink("  " + link + "\r\n")
	if err != nil || out != in {
		t.Fatalf("decode = %+v %v", out, err)
	}
	bad := []string{
		"", "nvlink1.", "nvlink2." + link[len(LinkPrefix):], "nvr_token", LinkPrefix + "!!!",
		LinkPrefix + strings.Repeat("A", MaxLinkBytes),
	}
	for _, half := range []LinkPayload{{Hub: "h"}, {Code: "c"}, {Hub: "h", Code: "c", AccessClientID: "only-id"}} {
		s, _ := EncodeLink(half)
		bad = append(bad, s)
	}
	for _, s := range bad {
		if _, err := DecodeLink(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
