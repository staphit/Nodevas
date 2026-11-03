package audit

import (
	"fmt"
	"testing"
	"time"
)

func TestThrottledNotesKeepOneRowPerKeyAndWindow(t *testing.T) {
	var notes throttledNotes
	now := time.Now()
	if due, held := notes.admit("a", now); !due || held != 0 {
		t.Fatalf("first = %v %d", due, held)
	}
	for i := 0; i < 3; i++ {
		if due, _ := notes.admit("a", now.Add(time.Second)); due {
			t.Fatal("a repeat inside the window was due a row")
		}
	}
	if due, _ := notes.admit("b", now.Add(time.Second)); !due {
		t.Fatal("another key waited on the first")
	}
	if due, held := notes.admit("a", now.Add(throttledEvery)); !due || held != 3 {
		t.Fatalf("after the window = %v %d, want a row counting 3 held back", due, held)
	}
	for i := 0; i < 2*maxThrottledKeys; i++ {
		notes.admit(fmt.Sprintf("k%d", i), now.Add(2*throttledEvery))
	}
	if len(notes.seen) > maxThrottledKeys {
		t.Fatalf("remembered %d keys, max %d", len(notes.seen), maxThrottledKeys)
	}
}
