package notify

import (
	"strings"
	"testing"
)

func TestSMTPRejectsPrivateAddressForRemoteDelivery(t *testing.T) {
	if _, err := resolveSMTPHost("127.0.0.1", false); err == nil {
		t.Fatal("private SMTP address was accepted for remote delivery")
	}
}

func TestSMTPAllowsPrivateAddressOnlyWithLocalPolicy(t *testing.T) {
	ips, err := resolveSMTPHost("127.0.0.1", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].String() != "127.0.0.1" {
		t.Fatalf("resolved addresses = %v", ips)
	}
}

func TestSMTPRejectsHeaderInjection(t *testing.T) {
	if err := validateSMTPField("to", "victim@example.com\r\nBcc: attacker@example.com"); err == nil {
		t.Fatal("SMTP header injection was accepted")
	}
	if !strings.Contains(validateSMTPField("from", "bad\nvalue").Error(), "line break") {
		t.Fatal("SMTP line-break error was not descriptive")
	}
}

func TestSMTPRejectsUnapprovedRemotePort(t *testing.T) {
	settings := NotifySettings{
		SMTPHost: "127.0.0.1",
		SMTPPort: 8080,
		From:     "from@example.com",
	}
	if err := SendMail(settings, "to@example.com", "subject", "body"); err == nil ||
		!strings.Contains(err.Error(), "remote SMTP port") {
		t.Fatalf("unexpected port validation result: %v", err)
	}
}
