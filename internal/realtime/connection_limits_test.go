package realtime

import (
	"fmt"
	"testing"

	"nodevas/internal/identity"
)

func TestDefaultConnectionLimits(t *testing.T) {
	want := ConnectionLimits{Total: 512, PerAccount: 16, PerIP: 32}
	if got := DefaultConnectionLimits(); got != want {
		t.Fatalf("DefaultConnectionLimits() = %+v, want %+v", got, want)
	}
	if got := NewHub().ConnectionLimits(); got != want {
		t.Fatalf("a new hub's limits = %+v, want the defaults %+v", got, want)
	}
}

func TestSetConnectionLimitsOverridesTheCaps(t *testing.T) {
	hub := NewHub()
	hub.SetConnectionLimits(ConnectionLimits{Total: 5, PerAccount: 2, PerIP: 3})

	account := identity.Actor{ID: "account"}
	for i := range 2 {
		if !hub.reserveConnection(account, fmt.Sprintf("192.0.2.%d", i+1)) {
			t.Fatalf("account connection %d refused under its cap", i)
		}
	}
	if hub.reserveConnection(account, "192.0.2.9") {
		t.Fatal("per-account override not enforced")
	}

	for i := range 3 {
		if !hub.reserveConnection(identity.Actor{ID: fmt.Sprintf("ip-%d", i)}, "198.51.100.1") {
			t.Fatalf("IP connection %d refused under its cap", i)
		}
	}
	if hub.reserveConnection(identity.Actor{ID: "ip-over"}, "198.51.100.1") {
		t.Fatal("per-IP override not enforced")
	}

	// 2 + 3 reserved: the total cap of 5 is reached.
	if hub.reserveConnection(identity.Actor{ID: "fresh"}, "203.0.113.7") {
		t.Fatal("total override not enforced")
	}
}

func TestSetConnectionLimitsKeepsDefaultsForUnsetFields(t *testing.T) {
	hub := NewHub()
	hub.SetConnectionLimits(ConnectionLimits{PerIP: 64, Total: -1})
	want := DefaultConnectionLimits()
	want.PerIP = 64
	if got := hub.ConnectionLimits(); got != want {
		t.Fatalf("limits = %+v, want %+v (zero or negative keeps the default)", got, want)
	}
}
