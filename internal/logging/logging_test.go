package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// decodeOne reads the single JSON record written to buf.
func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1: %q", len(lines), buf.String())
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("record is not JSON: %v (%q)", err, lines[0])
	}
	return record
}

func TestRecordsAreJSONWithElasticCommonSchemaFieldNames(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{Service: "nodevas"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("signed in", EventAction("sign-in"), ClientIP("203.0.113.7"), User("u-1", "ann"))

	record := decodeOne(t, &buf)
	want := map[string]any{
		"log.level":    "info",
		"message":      "signed in",
		"event.action": "sign-in",
		"client.ip":    "203.0.113.7",
		"user.id":      "u-1",
		"user.name":    "ann",
		"service.name": "nodevas",
	}
	for key, value := range want {
		if record[key] != value {
			t.Fatalf("%s = %v, want %v", key, record[key], value)
		}
	}
	// Kibana keys its time filter off "@timestamp"; slog's "time" would be
	// ingested as an unrecognised string field.
	if _, ok := record["time"]; ok {
		t.Fatal("the record still carries slog's \"time\" key")
	}
	stamp, ok := record["@timestamp"].(string)
	if !ok {
		t.Fatalf("@timestamp = %v, want a string", record["@timestamp"])
	}
	if _, err := time.Parse(ecsTimeFormat, stamp); err != nil {
		t.Fatalf("@timestamp %q is not RFC3339 with milliseconds: %v", stamp, err)
	}
}

func TestSourceLocationUsesTheECSOriginFields(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{Source: true})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("here")

	record := decodeOne(t, &buf)
	name, ok := record["log.origin.file.name"].(string)
	if !ok || !strings.HasSuffix(name, "logging_test.go") {
		t.Fatalf("log.origin.file.name = %v, want this test file", record["log.origin.file.name"])
	}
	if _, ok := record["log.origin.file.line"].(float64); !ok {
		t.Fatalf("log.origin.file.line = %v, want a number", record["log.origin.file.line"])
	}
}

func TestLevelFilterDropsRecordsBelowTheConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{Level: "WARN"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug("debug")
	logger.Info("info")
	if buf.Len() != 0 {
		t.Fatalf("records below warn were written: %q", buf.String())
	}
	logger.Warn("warn")
	if record := decodeOne(t, &buf); record["log.level"] != "warn" {
		t.Fatalf("log.level = %v, want warn", record["log.level"])
	}
}

func TestUnknownLevelAndFormatAreRejected(t *testing.T) {
	// Failing at startup is preferable to silently logging at a level the
	// operator did not ask for.
	if _, err := Setup(&bytes.Buffer{}, Config{Level: "chatty"}); err == nil {
		t.Fatal("an unknown level was accepted")
	}
	if _, err := Setup(&bytes.Buffer{}, Config{Format: "xml"}); err == nil {
		t.Fatal("an unknown format was accepted")
	}
	if _, err := Setup(&bytes.Buffer{}, Config{Format: "text"}); err != nil {
		t.Fatalf("the text format was rejected: %v", err)
	}
}

func TestCredentialShapedKeysAreRedacted(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{})
	if err != nil {
		t.Fatal(err)
	}
	const leaked = "hunter2-do-not-log"
	logger.Info("sign-in attempt",
		slog.String("pin", leaked),
		slog.String("OTP", leaked),
		slog.String("Passcode", leaked),
		slog.String("password", leaked),
		slog.String("secret", leaked),
		slog.String("token", leaked),
		slog.String("Cookie", leaked),
		slog.String("Authorization", leaked),
		slog.String("api_key", leaked),
		slog.String("apikey", leaked),
		// A key nobody enumerated, caught by the suffix rules.
		slog.String("foo_token", leaked),
		slog.String("hmac_secret", leaked),
		// Grouping does not launder a credential.
		slog.Group("request", slog.String("passcode", leaked), slog.String("path", "/api/pair")),
	)

	if strings.Contains(buf.String(), leaked) {
		t.Fatalf("a credential reached the log stream: %q", buf.String())
	}
	record := decodeOne(t, &buf)
	for _, key := range []string{"pin", "OTP", "Passcode", "password", "secret", "token", "Cookie", "Authorization", "api_key", "apikey", "foo_token", "hmac_secret"} {
		if record[key] != Redacted {
			t.Fatalf("%s = %v, want %q", key, record[key], Redacted)
		}
	}
	group, ok := record["request"].(map[string]any)
	if !ok {
		t.Fatalf("request = %v, want an object", record["request"])
	}
	if group["passcode"] != Redacted {
		t.Fatalf("request.passcode = %v, want %q", group["passcode"], Redacted)
	}
	// Redaction must not be so eager that it blanks ordinary fields.
	if group["path"] != "/api/pair" {
		t.Fatalf("request.path = %v, want /api/pair", group["path"])
	}
}

type pairingError struct{}

func (pairingError) Error() string { return "pairing refused" }

func TestErrProducesTheErrorMessageAndType(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{})
	if err != nil {
		t.Fatal(err)
	}
	logger.Error("pairing failed", Err(pairingError{}))

	record := decodeOne(t, &buf)
	if record["error.message"] != "pairing refused" {
		t.Fatalf("error.message = %v, want \"pairing refused\"", record["error.message"])
	}
	// The type is what a dashboard can group by; messages get reworded.
	if record["error.type"] != "logging.pairingError" {
		t.Fatalf("error.type = %v, want logging.pairingError", record["error.type"])
	}
	if record["log.level"] != "error" {
		t.Fatalf("log.level = %v, want error", record["log.level"])
	}
}

func TestErrOfNilContributesNoFields(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("fine", Err(nil))

	record := decodeOne(t, &buf)
	if _, ok := record["error.message"]; ok {
		t.Fatalf("a nil error produced error fields: %v", record)
	}
}

func TestStandardLogPackageCallsBecomeStructuredRecords(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Setup(&buf, Config{Service: "nodevas"}); err != nil {
		t.Fatal(err)
	}
	// The codebase still has log.Printf calls; they must not bypass the
	// pipeline as raw prose.
	log.Printf("watcher restarted after %v", errors.New("closed"))

	record := decodeOne(t, &buf)
	if record["message"] != "watcher restarted after closed" {
		t.Fatalf("message = %v", record["message"])
	}
	if record["log.level"] != "info" {
		t.Fatalf("log.level = %v, want info", record["log.level"])
	}
	if record["service.name"] != "nodevas" {
		t.Fatalf("service.name = %v, want nodevas", record["service.name"])
	}
	if _, ok := record["@timestamp"].(string); !ok {
		t.Fatal("the bridged record has no @timestamp")
	}
}

func TestSetupInstallsTheLoggerAsTheSlogDefault(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Setup(&buf, Config{}); err != nil {
		t.Fatal(err)
	}
	// Packages that never receive a logger still have to reach the same stream.
	slog.Info("from the default logger")
	if record := decodeOne(t, &buf); record["message"] != "from the default logger" {
		t.Fatalf("message = %v", record["message"])
	}
}

func TestTextFormatKeepsTheECSNamesAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	logger, err := Setup(&buf, Config{Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("sign-in attempt", slog.String("pin", "1234"), ClientIP("203.0.113.7"))

	line := buf.String()
	for _, want := range []string{"@timestamp=", "log.level=info", "client.ip=203.0.113.7", fmt.Sprintf("pin=%s", Redacted)} {
		if !strings.Contains(line, want) {
			t.Fatalf("text output %q does not contain %q", line, want)
		}
	}
	if strings.Contains(line, "1234") {
		t.Fatalf("the text format leaked a credential: %q", line)
	}
}
