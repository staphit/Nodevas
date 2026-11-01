package copilot

import (
	"context"
	"errors"
	"testing"

	"nodevas/internal/testpg"
)

// The API key is write-only: an administrator can replace it but never read
// it back. Pointing the copilot at another endpoint would hand it to that
// endpoint on the next turn, so an endpoint change needs the key re-entered,
// and the operator's environment key only ever goes to the operator's own
// endpoint or the vendor's.
func TestCopilotKeyDoesNotFollowAnEndpointChange(t *testing.T) {
	ctx := context.Background()
	keys := &memoryKeys{key: []byte("sk-stored")}
	service := New(Options{
		Database: testpg.New(t), Keys: keys,
		Defaults: Defaults{
			Enabled: true, Provider: "openai", BaseURL: "https://llm.operator.example/v1",
			APIKey: "sk-env", VendorKeys: map[string]string{"openai": "sk-openai-env"},
		},
		DefaultModel: func(kind string) string { return kind + "-default" },
	})
	evil := "https://evil.example/v1"

	if _, err := service.UpdateSettings(ctx, SettingsUpdate{BaseURL: &evil}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("baseUrl change without re-entering the stored key: err = %v", err)
	}
	anthropic := "anthropic"
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{Provider: &anthropic, BaseURL: &evil}); !errors.Is(err, ErrInvalidSettings) {
		t.Fatalf("provider and baseUrl change without re-entering the stored key: err = %v", err)
	}
	if _, key, _, _ := service.settings.resolve(ctx); key != "sk-stored" {
		t.Fatalf("a refused update changed the key: %q", key)
	}

	// Switching vendor alone keeps the key: it goes to the vendor's own
	// endpoint, as the settings panel's provider switch expects.
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{Provider: &anthropic}); err != nil {
		t.Fatalf("provider switch: %v", err)
	}
	openai := "openai"
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{Provider: &openai}); err != nil {
		t.Fatalf("back to the operator's provider: %v", err)
	}

	fresh := "sk-new"
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{BaseURL: &evil, APIKey: &fresh}); err != nil {
		t.Fatalf("baseUrl change with a new key: %v", err)
	}
	if settings, key, _, _ := service.settings.resolve(ctx); key != "sk-new" || settings.BaseURL != evil {
		t.Fatalf("resolved %q at %q", key, settings.BaseURL)
	}

	// Without a stored key, the environment's keys are the operator's: they
	// never go to an endpoint an administrator typed.
	empty := ""
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{APIKey: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, key, _, _ := service.settings.resolve(ctx); key != "" {
		t.Fatalf("environment key %q resolved for an administrator's baseUrl", key)
	}
	view, err := service.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.HasAPIKey {
		t.Fatalf("view = %+v, want no key for the administrator's endpoint", view)
	}

	// Back on the operator's endpoint, the operator's key applies again.
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{BaseURL: &empty}); err != nil {
		t.Fatal(err)
	}
	if settings, key, _, _ := service.settings.resolve(ctx); key != "sk-env" || settings.BaseURL != "https://llm.operator.example/v1" {
		t.Fatalf("operator's endpoint: resolved %q at %q", key, settings.BaseURL)
	}
	// And a vendor's own endpoint gets the vendor's key.
	if _, err := service.UpdateSettings(ctx, SettingsUpdate{Provider: &anthropic}); err != nil {
		t.Fatal(err)
	}
	if settings, key, _, _ := service.settings.resolve(ctx); key != "sk-env" || settings.BaseURL != "" {
		t.Fatalf("vendor endpoint: resolved %q at %q", key, settings.BaseURL)
	}
}
