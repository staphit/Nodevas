package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"nodevas/internal/copilot/provider"
	"nodevas/internal/copilot/toolbridge"
	"nodevas/internal/db/dbtest"
)

func TestStoreConversationsBelongToTheirAccount(t *testing.T) {
	ctx := context.Background()
	store := NewStore(dbtest.Open(t))

	first, err := store.CreateConversation(ctx, "alice", "demo", ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.ID) != 32 || first.CreatedAt == 0 || first.CreatedAt != first.UpdatedAt {
		t.Fatalf("created = %+v", first)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := store.CreateConversation(ctx, "alice", "other", ModePlan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConversation(ctx, "bob", "demo", ModeDefault); err != nil {
		t.Fatal(err)
	}

	all, err := store.ListConversations(ctx, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || all[0].ID != second.ID || all[1].ID != first.ID {
		t.Fatalf("newest first = %+v", all)
	}
	demo, err := store.ListConversations(ctx, "alice", "demo")
	if err != nil || len(demo) != 1 || demo[0].ID != first.ID {
		t.Fatalf("project filter = %+v, %v", demo, err)
	}

	if _, err := store.Conversation(ctx, "bob", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account read = %v", err)
	}
	title := "renamed"
	if _, err := store.UpdateConversation(ctx, "bob", first.ID, ConversationPatch{Title: &title}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account patch = %v", err)
	}
	if err := store.DeleteConversation(ctx, "bob", first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other account delete = %v", err)
	}

	mode := ModeAcceptEdits
	updated, err := store.UpdateConversation(ctx, "alice", first.ID, ConversationPatch{Title: &title, Mode: &mode})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "renamed" || updated.Mode != ModeAcceptEdits || updated.UpdatedAt < first.UpdatedAt {
		t.Fatalf("updated = %+v", updated)
	}
	// An automatic title never overwrites one somebody chose.
	if err := store.setTitleIfEmpty(ctx, first.ID, "auto"); err != nil {
		t.Fatal(err)
	}
	if kept, _ := store.Conversation(ctx, "alice", first.ID); kept.Title != "renamed" {
		t.Fatalf("title = %q", kept.Title)
	}
}

func TestStoreEntriesAreOrderedAndDeletedWithTheirConversation(t *testing.T) {
	ctx := context.Background()
	database := dbtest.Open(t)
	store := NewStore(database)
	conversation, err := store.CreateConversation(ctx, "alice", "demo", ModeDefault)
	if err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{EntryUser, EntryAssistant, EntryTool} {
		entry, err := store.AppendEntry(ctx, Entry{
			ConversationID: conversation.ID, Kind: kind, Text: strings.Repeat("x", i),
			ToolName: "get_node", ToolStatus: ToolOK, Message: `{"role":"user","blocks":[]}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		if entry.Seq != int64(i+1) || entry.ID == "" || entry.CreatedAt == 0 {
			t.Fatalf("entry %d = %+v", i, entry)
		}
	}
	entries, err := store.Entries(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Kind != EntryUser || entries[2].Kind != EntryTool ||
		entries[2].Message == "" || entries[2].ToolStatus != ToolOK {
		t.Fatalf("entries = %+v", entries)
	}
	touched, _ := store.Conversation(ctx, "alice", conversation.ID)
	if touched.UpdatedAt < entries[2].CreatedAt {
		t.Fatalf("conversation not touched: %+v", touched)
	}

	if err := store.DeleteConversation(ctx, "alice", conversation.ID); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM copilot_entries`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d entries survived their conversation", left)
	}
}

type memoryKeys struct{ key []byte }

func (m *memoryKeys) Load() ([]byte, error)   { return m.key, nil }
func (m *memoryKeys) Save(plain []byte) error { m.key = append([]byte(nil), plain...); return nil }
func (m *memoryKeys) Clear() error            { m.key = nil; return nil }

func TestSettingsLayerDefaultsOverridesAndKeys(t *testing.T) {
	ctx := context.Background()
	keys := &memoryKeys{}
	service := New(Options{
		Database: dbtest.Open(t), Keys: keys,
		Defaults: Defaults{
			Enabled: true, Provider: "anthropic", Model: "claude-from-yaml", Effort: "low",
			VendorKeys: map[string]string{"anthropic": "sk-ant-env"},
		},
		DefaultModel: func(kind string) string { return kind + "-default" },
	})

	view, err := service.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Provider != "anthropic" || view.Model != "claude-from-yaml" || !view.HasAPIKey ||
		view.APIKeySource != "environment" || view.AllowBypass {
		t.Fatalf("defaults = %+v", view)
	}

	// Switching vendor drops the configured model and the other vendor's key.
	openai, allow := "openai", true
	view, err = service.UpdateSettings(ctx, SettingsUpdate{Provider: &openai, AllowBypass: &allow})
	if err != nil {
		t.Fatal(err)
	}
	if view.Model != "openai-default" || view.HasAPIKey || !view.AllowBypass {
		t.Fatalf("after switching to openai = %+v", view)
	}

	key := "sk-stored"
	if view, err = service.UpdateSettings(ctx, SettingsUpdate{APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	if !view.HasAPIKey || view.APIKeySource != "stored" || string(keys.key) != "sk-stored" {
		t.Fatalf("after storing a key = %+v", view)
	}
	_, resolved, _, _ := service.settings.resolve(ctx)
	if resolved != "sk-stored" {
		t.Fatalf("resolved key = %q", resolved)
	}

	empty := ""
	if view, err = service.UpdateSettings(ctx, SettingsUpdate{APIKey: &empty, Provider: &empty}); err != nil {
		t.Fatal(err)
	}
	if view.Provider != "anthropic" || view.APIKeySource != "environment" || keys.key != nil {
		t.Fatalf("after clearing = %+v", view)
	}

	for name, update := range map[string]SettingsUpdate{
		"provider": {Provider: ptr("gemini")},
		"effort":   {Effort: ptr("extreme")},
		"baseUrl":  {BaseURL: ptr("file:///etc/passwd")},
		"model":    {Model: ptr("a\nb")},
	} {
		if _, err := service.UpdateSettings(ctx, update); !errors.Is(err, ErrInvalidSettings) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestStatusAllowsAKeylessLocalOpenAIServer(t *testing.T) {
	service := New(Options{
		Database: dbtest.Open(t),
		Defaults: Defaults{Enabled: true, Provider: "openai", Model: "llama3", BaseURL: "http://127.0.0.1:11434/v1"},
		NewProvider: func(provider.Config) (provider.Provider, error) {
			return nil, errors.New("not called")
		},
		OpenBridge: func(context.Context, toolbridge.Options) (toolbridge.Bridge, error) {
			return nil, errors.New("not called")
		},
	})
	status, err := service.Status(context.Background(), alice)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.Provider != "openai" || status.Model != "llama3" || status.AllowBypass ||
		len(status.Modes) != 3 {
		t.Fatalf("status = %+v", status)
	}

	service.opts.Defaults.Enabled = false
	if status, _ = service.Status(context.Background(), alice); status.Enabled || !strings.Contains(status.Reason, "copilot.enabled") {
		t.Fatalf("disabled status = %+v", status)
	}
}

func ptr(value string) *string { return &value }
