package tui

import (
	"strings"
	"testing"
	"time"
)

// A toast clears on its own timer, so it disappears while argx is idle instead
// of waiting for the next unrelated frame.
func TestToastExpiresOnItsOwnTimer(t *testing.T) {
	m := newTestModel(t, "alpha")
	_, cmd := m.Update(key("F")) // sets "auto-refresh on"
	if m.toast.text == "" {
		t.Fatal("setup: F should toast")
	}
	if cmd == nil {
		t.Fatal("setting a toast must arm its expiry")
	}

	m.Update(toastExpireMsg{seq: m.toast.seq})
	if m.toast.text != "" {
		t.Errorf("toast = %q after its expiry, want it cleared", m.toast.text)
	}
	if strings.Contains(m.renderStatus(), "auto-refresh on") {
		t.Error("an expired toast is still drawn")
	}
}

// An older toast's timer must not clear a newer toast.
func TestOlderToastTimerLeavesNewerToast(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.Update(key("F"))
	old := m.toast.seq
	m.Update(key("F")) // "auto-refresh off"

	m.Update(toastExpireMsg{seq: old})
	if m.toast.text != "auto-refresh off" {
		t.Errorf("toast = %q, want the newer one to survive", m.toast.text)
	}
}

// A failed scheduled sync is reported at the error level, not as success.
func TestScheduledSyncFailureToastsAsError(t *testing.T) {
	m := newTestModel(t)
	m.schedules = []scheduled{sched(1, "web-frontend", -time.Minute)}

	m.Update(scheduleRunMsg{id: 1, state: scheduleFailed, reason: "boom"})

	if !strings.Contains(m.toast.text, "scheduled sync failed") {
		t.Fatalf("toast = %q, want the failure reported", m.toast.text)
	}
	if m.toast.level != toastErr {
		t.Errorf("toast level = %d, want toastErr", m.toast.level)
	}
}
