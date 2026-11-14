package runnerwire

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Runner settings messages round-trip with the names both ends use.
func TestSettingsContract(t *testing.T) {
	if TypeSettingsRequest != "settings_request" || TypeSettingsDone != "settings_done" || CapRemoteSettings != "remote-settings" ||
		SettingsErrRefused != "refused" || SettingsErrOff != "settings_off" || SettingsErrRange != "out_of_range" ||
		DefaultMaxConcurrentCeiling != 8 || SettingsTimeoutSeconds != 10 {
		t.Fatal("settings names or limits changed")
	}
	n := 3
	cases := []struct {
		value any
		keys  []string
	}{
		{Envelope{Type: TypeSettingsRequest, Settings: &SettingsRequest{ID: "s", AccountID: "u", MaxConcurrent: &n}},
			[]string{`"type":"settings_request"`, `"settings":{"id":"s","accountId":"u","maxConcurrent":3}`}},
		{Envelope{Type: TypeSettingsDone, SettingsDone: &SettingsDone{ID: "s", Code: SettingsErrRange, Error: "x", MaxConcurrent: 2, Persisted: true}},
			[]string{`"settingsDone":{"id":"s","code":"out_of_range","error":"x","maxConcurrent":2,"persisted":true}`}},
		{Info{MaxConcurrent: 2, MaxConcurrentCeiling: 8}, []string{`"maxConcurrent":2,"maxConcurrentCeiling":8`}},
		{RunnerSettingsBody{MaxConcurrent: &n}, []string{`{"maxConcurrent":3}`}},
		{RunnerSettingsResponse{Runner: Runner{ID: "rn"}, Persisted: true}, []string{`"runner":{"id":"rn"`, `"persisted":true`}},
	}
	for _, c := range cases {
		raw, err := json.Marshal(c.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range c.keys {
			if !strings.Contains(string(raw), key) {
				t.Errorf("JSON lacks %s:\n%s", key, raw)
			}
		}
		back := reflect.New(reflect.TypeOf(c.value))
		if err := json.Unmarshal(raw, back.Interface()); err != nil || !reflect.DeepEqual(back.Elem().Interface(), c.value) {
			t.Errorf("round trip of %T = %+v, %v", c.value, back.Elem().Interface(), err)
		}
	}
	// A request that sets nothing says so; an older runner's hello has no ceiling.
	raw, _ := json.Marshal(SettingsRequest{ID: "s"})
	if strings.Contains(string(raw), "maxConcurrent") {
		t.Fatalf("empty request names maxConcurrent: %s", raw)
	}
	raw, _ = json.Marshal(Info{})
	if strings.Contains(string(raw), "Ceiling") {
		t.Fatalf("hello without a ceiling names it: %s", raw)
	}
}
