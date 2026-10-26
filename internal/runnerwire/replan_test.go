package runnerwire

import (
	"strings"
	"testing"
)

func TestParseReconcile(t *testing.T) {
	text := "I looked at the code.\n\nAn example someone wrote earlier:\nRECONCILE:\n- skip t1: old\nEND RECONCILE\n\n" +
		"**RECONCILE:**\n" +
		"- keep t1\n" +
		"* change `t2`: also export the totals row\n" +
		"- SKIP t3: the PDF export   was dropped\n" +
		"- flag t4: in progress; must cover the header\n" +
		"- add t9: Export the totals row\n" +
		"- delete t5: not an action\n" +
		"just prose\n" +
		"END RECONCILE\n" +
		"- skip t6: after the end\n"
	items, ok := ParseReconcile(text)
	if !ok {
		t.Fatal("no block found")
	}
	want := []ReconcileItem{
		{Action: ReconcileKeep, NodeID: "t1"},
		{Action: ReconcileChange, NodeID: "t2", Note: "also export the totals row"},
		{Action: ReconcileSkip, NodeID: "t3", Note: "the PDF export was dropped"},
		{Action: ReconcileFlag, NodeID: "t4", Note: "in progress; must cover the header"},
		{Action: ReconcileAdd, NodeID: "t9", Note: "Export the totals row"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %+v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d = %+v, want %+v", i, items[i], want[i])
		}
	}
	if _, ok := ParseReconcile("no block here"); ok {
		t.Fatal("found a block in plain text")
	}
	long, _ := ParseReconcile("RECONCILE:\n- skip t1: " + strings.Repeat("é", 400) + "\nEND RECONCILE")
	if len(long) != 1 || len(long[0].Note) > MaxReconcileNote+len("…") {
		t.Fatalf("note not capped: %d bytes", len(long[0].Note))
	}
}

func TestFormatReconcile(t *testing.T) {
	items := []ReconcileItem{{Action: ReconcileKeep, NodeID: "t1"}, {Action: ReconcileSkip, NodeID: "t2", Note: "gone"}}
	block := FormatReconcile(items, 1000)
	if block != "RECONCILE:\n- keep t1\n- skip t2: gone\nEND RECONCILE" {
		t.Fatalf("block = %q", block)
	}
	if again, ok := ParseReconcile(block); !ok || len(again) != 2 || again[1] != items[1] {
		t.Fatalf("round trip = %+v", again)
	}
	// Too long: keep lines go first.
	short := FormatReconcile(items, len("RECONCILE:\n- skip t2: gone\nEND RECONCILE"))
	if short != "RECONCILE:\n- skip t2: gone\nEND RECONCILE" {
		t.Fatalf("short = %q", short)
	}
}
