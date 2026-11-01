package main

import (
	"bytes"
	"regexp"
	"testing"

	"nodevas/internal/runner"
)

// Every key `nodevas runner set` takes is named in its usage text, so a
// setting the owner may turn off (repoBrowse) is not one only the source
// mentions.
func TestRunnerUsageNamesEverySetting(t *testing.T) {
	var out bytes.Buffer
	runnerUsage(&out)
	for _, key := range runner.SettingKeys {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(key) + `\b`).Match(out.Bytes()) {
			t.Errorf("runner usage does not name the setting %q", key)
		}
	}
}
