package prefs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"nodevas/internal/testpg"
)

func TestMain(m *testing.M) { os.Exit(testpg.Main(m)) }

// newStore is a store on a fresh database whose clock moves only when the
// test moves it.
func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	store := NewStore(testpg.New(t))
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.clock = func() time.Time { return now }
	return store, &now
}

func set(pairs ...string) Change {
	change := Change{Set: map[string]json.RawMessage{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		change.Set[pairs[i]] = json.RawMessage(pairs[i+1])
	}
	return change
}

func refused(t *testing.T, err error, full bool) {
	t.Helper()
	var refusal *RefusedError
	if !errors.As(err, &refusal) || refusal.Full != full {
		t.Fatalf("err = %v, want a refusal with Full=%v", err, full)
	}
}

func TestEmptyAccountHasEmptyMaps(t *testing.T) {
	store, _ := newStore(t)
	got, err := store.Get(context.Background(), "local")
	if err != nil || got.Values == nil || got.UpdatedAt == nil || len(got.Values) != 0 {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	encoded, _ := json.Marshal(got)
	if string(encoded) != `{"values":{},"updatedAt":{}}` {
		t.Fatalf("encoded = %s", encoded)
	}
}

func TestSetReadRemove(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	got, err := store.Apply(ctx, "local", set("theme", `"dark"`, "textScale", ` 1.25 `, "timeline.axes", `{ "x": "day",  "y": ["a", "b"] }`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"theme": `"dark"`, "textScale": `1.25`, "timeline.axes": `{"x":"day","y":["a","b"]}`}
	for key, value := range want {
		if string(got.Values[key]) != value || got.UpdatedAt[key] != "2026-11-14T09:00:00.000000Z" {
			t.Errorf("%s = %s at %q, want %s", key, got.Values[key], got.UpdatedAt[key], value)
		}
	}
	read, err := store.Get(ctx, "local")
	if err != nil || len(read.Values) != 3 || string(read.Values["timeline.axes"]) != want["timeline.axes"] {
		t.Fatalf("Get = %+v, %v", read, err)
	}

	// A null removes, as remove does; a key that is not there is no error.
	got, err = store.Apply(ctx, "local", Change{Set: map[string]json.RawMessage{"theme": json.RawMessage("null")}, Remove: []string{"textScale", "neverSet"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Values) != 1 || got.Values["timeline.axes"] == nil || len(got.UpdatedAt) != 1 {
		t.Fatalf("after removing = %+v", got)
	}
}

// Of two writes to one key the later one the hub takes stays, stamped
// with the hub's clock; writing a value it already has changes nothing.
func TestLastWriteWinsByTheHubsClock(t *testing.T) {
	store, now := newStore(t)
	ctx := context.Background()
	if _, err := store.Apply(ctx, "local", set("theme", `"dark"`, "language", `"zh-TW"`)); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(1500 * time.Millisecond)
	got, err := store.Apply(ctx, "local", set("theme", `"light"`, "language", `"zh-TW"`))
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Values["theme"]) != `"light"` || got.UpdatedAt["theme"] != "2026-11-14T09:00:01.500000Z" {
		t.Fatalf("theme = %s at %s", got.Values["theme"], got.UpdatedAt["theme"])
	}
	if got.UpdatedAt["language"] != "2026-11-14T09:00:00.000000Z" {
		t.Fatalf("an unchanged value was restamped: %s", got.UpdatedAt["language"])
	}
}

func TestAccountsAreKeptApart(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	if _, err := store.Apply(ctx, "local", set("theme", `"dark"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, "acct-bea", set("theme", `"light"`, "editorMode", `"source"`)); err != nil {
		t.Fatal(err)
	}
	local, _ := store.Get(ctx, "local")
	bea, _ := store.Get(ctx, "acct-bea")
	if len(local.Values) != 1 || string(local.Values["theme"]) != `"dark"` {
		t.Fatalf("local = %+v", local.Values)
	}
	if len(bea.Values) != 2 || string(bea.Values["theme"]) != `"light"` {
		t.Fatalf("bea = %+v", bea.Values)
	}
	if _, err := store.Apply(ctx, "acct-bea", Change{Remove: []string{"theme"}}); err != nil {
		t.Fatal(err)
	}
	if local, _ = store.Get(ctx, "local"); string(local.Values["theme"]) != `"dark"` {
		t.Fatal("removing bea's theme removed local's")
	}
}

func TestMalformedChangesAreRefused(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	tooMany := Change{Set: map[string]json.RawMessage{}}
	for i := range MaxKeysPerChange + 1 {
		tooMany.Set[fmt.Sprintf("k%d", i)] = json.RawMessage(`1`)
	}
	for name, change := range map[string]Change{
		"digit first":        set("1theme", `1`),
		"space":              set("the me", `1`),
		"slash":              set("a/b", `1`),
		"empty key":          set("", `1`),
		"key too long":       set("k"+strings.Repeat("x", MaxKeyBytes), `1`),
		"not JSON":           set("theme", `dark`),
		"two values":         set("theme", `"a" "b"`),
		"value too long":     set("theme", `"`+strings.Repeat("x", MaxValueBytes)+`"`),
		"invalid UTF-8":      set("theme", "\"\xff\xfe\""),
		"set and remove":     {Set: map[string]json.RawMessage{"theme": json.RawMessage(`1`)}, Remove: []string{"theme"}},
		"removed twice":      {Remove: []string{"theme", "theme"}},
		"bad removal":        {Remove: []string{"-x"}},
		"too many in a call": tooMany,
	} {
		_, err := store.Apply(ctx, "local", change)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		var refusal *RefusedError
		if !errors.As(err, &refusal) || refusal.Full {
			t.Errorf("%s: err = %v, want a malformed-change refusal", name, err)
		}
	}
	// A value at the limit, and a key at its longest, are fine.
	longest := "k" + strings.Repeat("x", MaxKeyBytes-1)
	if _, err := store.Apply(ctx, "local", set(longest, `"`+strings.Repeat("x", MaxValueBytes-2)+`"`)); err != nil {
		t.Fatalf("at the limits: %v", err)
	}
	if _, err := store.Apply(ctx, "", set("theme", `1`)); err == nil {
		t.Fatal("a change for no account accepted")
	}
}

// An account may hold MaxKeysPerAccount keys and MaxBytesPerAccount bytes:
// a change past either is refused whole, and one that shrinks an account
// already past them (after a lower limit, say) still goes through.
func TestAccountLimits(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	for batch := range MaxKeysPerAccount / 50 {
		change := Change{Set: map[string]json.RawMessage{}}
		for i := range 50 {
			change.Set[fmt.Sprintf("key%03d", batch*50+i)] = json.RawMessage(`true`)
		}
		if _, err := store.Apply(ctx, "local", change); err != nil {
			t.Fatalf("batch %d: %v", batch, err)
		}
	}
	_, err := store.Apply(ctx, "local", set("one.more", `1`, "key000", `false`))
	refused(t, err, true)
	got, _ := store.Get(ctx, "local")
	if len(got.Values) != MaxKeysPerAccount || string(got.Values["key000"]) != "true" {
		t.Fatalf("a refused change was partly applied: %d keys, key000 = %s", len(got.Values), got.Values["key000"])
	}
	// Changing a key the account already has is not growth.
	if _, err := store.Apply(ctx, "local", set("key000", `false`)); err != nil {
		t.Fatalf("change at the key limit: %v", err)
	}
	// Another account is not this one's limit.
	if _, err := store.Apply(ctx, "acct-bea", set("one.more", `1`)); err != nil {
		t.Fatalf("another account: %v", err)
	}

	big := `"` + strings.Repeat("x", MaxValueBytes-2) + `"`
	bytesStore, _ := newStore(t)
	for i := 0; ; i++ {
		_, err := bytesStore.Apply(ctx, "local", set(fmt.Sprintf("big%02d", i), big))
		if err != nil {
			refused(t, err, true)
			if i != MaxBytesPerAccount/(MaxValueBytes+5) {
				t.Fatalf("refused at value %d", i)
			}
			break
		}
	}

	// Past the limits already (rows written before a limit came down): a
	// removal goes through, growth does not.
	overStore, _ := newStore(t)
	err = overStore.database.Tx(ctx, func(tx *sql.Tx) error {
		for i := range MaxKeysPerAccount + 5 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO account_preferences (account_id, key, value, updated_at) VALUES ('local', ?, '1', 'then')`,
				fmt.Sprintf("old%03d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := overStore.Apply(ctx, "local", Change{Remove: []string{"old000"}}); err != nil {
		t.Fatalf("shrinking an account past its limit: %v", err)
	}
	_, err = overStore.Apply(ctx, "local", set("fresh", `1`))
	refused(t, err, true)
}

// A value written by something other than this store that is not JSON is
// left out of the answer instead of breaking it.
func TestNonJSONRowIsSkipped(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	if _, err := store.database.ExecContext(ctx, `INSERT INTO account_preferences (account_id, key, value, updated_at) VALUES ('local', 'broken', 'not json', 'then')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, "local", set("theme", `"dark"`)); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "local")
	if err != nil || len(got.Values) != 1 || got.Values["broken"] != nil {
		t.Fatalf("Get = %+v, %v", got.Values, err)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("encode: %v", err)
	}
}

func TestDeleteAccount(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	for _, account := range []string{"acct-gone", "acct-kept"} {
		if _, err := store.Apply(ctx, account, set("theme", `"dark"`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.database.Tx(ctx, func(tx *sql.Tx) error { return DeleteAccount(ctx, tx, "acct-gone") }); err != nil {
		t.Fatal(err)
	}
	gone, _ := store.Get(ctx, "acct-gone")
	kept, _ := store.Get(ctx, "acct-kept")
	if len(gone.Values) != 0 || len(kept.Values) != 1 {
		t.Fatalf("gone = %v, kept = %v", gone.Values, kept.Values)
	}
}
