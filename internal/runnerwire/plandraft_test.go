package runnerwire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJobDraftIsOmittedWhenFalse(t *testing.T) {
	raw, err := json.Marshal(Job{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"draft"`) {
		t.Fatalf("a plain job carries draft: %s", raw)
	}
	raw, err = json.Marshal(Job{Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"draft":true`) {
		t.Fatalf("a draft job lost its flag: %s", raw)
	}
}
