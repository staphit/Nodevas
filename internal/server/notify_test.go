package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"nodevas/internal/notify"
	"nodevas/internal/realtime"
	"strings"
	"testing"
)

// The API must not hand the stored password back out.
func TestNotifySettingsEndpointHidesThePassword(t *testing.T) {
	pm := projectManagerForTest(t)
	server := serverForTest(t, pm, realtime.NewHub(), nil)
	settings := notify.DefaultNotifySettings()
	settings.SMTPHost = "smtp.example.com"
	settings.From = "vised@example.com"
	settings.SMTPPass = "do-not-leak-this"
	if err := server.notifier.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/notify/settings", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "do-not-leak-this") {
		t.Fatalf("the endpoint returned the password: %s", response.Body)
	}
	var decoded struct {
		HasPassword bool `json:"hasPassword"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.HasPassword {
		t.Fatal("hasPassword = false, want true")
	}

	// Saving without a password keeps the stored one.
	body := `{"enabled":false,"leadMinutes":60,"smtpHost":"smtp.example.com",` +
		`"smtpPort":587,"smtpUser":"","smtpPass":"","from":"vised@example.com","defaultTo":""}`
	save := httptest.NewRequest(http.MethodPut, "/api/notify/settings", strings.NewReader(body))
	save.Header.Set("Content-Type", "application/json")
	saveResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(saveResponse, save)
	if saveResponse.Code != http.StatusOK {
		t.Fatalf("save status = %d, body = %s", saveResponse.Code, saveResponse.Body)
	}
	if kept := server.notifier.Settings().SMTPPass; kept != "do-not-leak-this" {
		t.Fatalf("password after save = %q, want it kept", kept)
	}
}
