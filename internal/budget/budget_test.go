package budget

import (
	"testing"
	"time"
)

func TestExhaustedRecordsFirstReason(t *testing.T) {
	tr := New(Limits{MaxModelCalls: 1, MaxFilesRead: 1})
	if _, ok := tr.Exhausted(); ok {
		t.Fatal("a fresh tracker should not report exhaustion")
	}
	if err := tr.ChargeModelCall(); err != nil {
		t.Fatalf("first model call should succeed: %v", err)
	}
	if err := tr.ChargeModelCall(); err == nil {
		t.Fatal("second model call should exhaust the budget")
	}
	reason, ok := tr.Exhausted()
	if !ok || reason == "" {
		t.Fatalf("expected an exhaustion reason, got %q ok=%v", reason, ok)
	}
	// A later exhaustion in another dimension must not overwrite the first reason.
	_ = tr.ChargeFileRead(1)
	if err := tr.ChargeFileRead(1); err == nil {
		t.Fatal("second file read should exhaust the file budget")
	}
	if r2, _ := tr.Exhausted(); r2 != reason {
		t.Errorf("first exhaustion reason should stick: got %q want %q", r2, reason)
	}
}

func TestTimedOutIsSeparateFromExhausted(t *testing.T) {
	tr := New(Limits{Timeout: time.Nanosecond})
	time.Sleep(time.Millisecond)
	if !tr.TimedOut() {
		t.Error("expected the wall-time budget to be exhausted")
	}
	if _, ok := tr.Exhausted(); ok {
		t.Error("wall-time exhaustion must not set the non-time Exhausted reason")
	}
}
