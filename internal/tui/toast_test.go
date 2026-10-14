package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/keyolk/tuikit/toast"
)

// newestToast is the toast the status line shows last.
func newestToast(t *testing.T, m *Model) toast.Toast {
	t.Helper()
	items := m.toasts.Items()
	if len(items) == 0 {
		t.Fatal("no toast queued")
	}
	return items[len(items)-1]
}

// A toast clears on its own timer, so it disappears while argx is idle instead
// of waiting for the next unrelated frame.
func TestToastExpiresOnItsOwnTimer(t *testing.T) {
	m := newTestModel(t, "alpha")
	_, cmd := m.Update(key("F")) // sets "auto-refresh on"
	if m.toastText() == "" {
		t.Fatal("setup: F should toast")
	}
	if cmd == nil {
		t.Fatal("setting a toast must arm its expiry")
	}

	exp, ok := m.toasts.ExpiredMsg(newestToast(t, m).ID())
	if !ok {
		t.Fatal("an info toast must have a live expiry")
	}
	m.Update(exp)
	if m.toastText() != "" {
		t.Errorf("toast = %q after its expiry, want it cleared", m.toastText())
	}
	if strings.Contains(m.renderStatus(), "auto-refresh on") {
		t.Error("an expired toast is still drawn")
	}
}

// An older toast's timer must not clear a newer toast.
func TestOlderToastTimerLeavesNewerToast(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.Update(key("F"))
	old, _ := m.toasts.ExpiredMsg(newestToast(t, m).ID())
	m.Update(key("F")) // "auto-refresh off"

	m.Update(old)
	if m.toastText() != "auto-refresh off" {
		t.Errorf("toast = %q, want the newer one to survive", m.toastText())
	}
}

// A failed scheduled sync is reported at the error level, not as success, and
// it stays until the reader presses a key — it does not time out unseen.
func TestScheduledSyncFailureToastsAsError(t *testing.T) {
	m := newTestModel(t)
	m.schedules = []scheduled{sched(1, "web-frontend", -time.Minute)}

	m.Update(scheduleRunMsg{id: 1, state: scheduleFailed, reason: "boom"})

	if !strings.Contains(m.toastText(), "scheduled sync failed") {
		t.Fatalf("toast = %q, want the failure reported", m.toastText())
	}
	tt := newestToast(t, m)
	if tt.Level != toast.Error {
		t.Errorf("toast level = %v, want error", tt.Level)
	}
	if _, ok := m.toasts.ExpiredMsg(tt.ID()); ok {
		t.Error("an error toast must not have an expiry timer")
	}
	m.toasts = toast.ExpireAll(m.toasts)
	if m.toastText() == "" {
		t.Error("an error must survive the timers")
	}
}

// A later status toast does not replace an unseen error: they have separate
// seats, and the status line shows both.
func TestStatusToastDoesNotWipeAnError(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.setToastErr("sync failed")
	m.setToast("refreshed")
	out := stripANSI(m.renderStatus())
	if !strings.Contains(out, "sync failed") || !strings.Contains(out, "refreshed") {
		t.Errorf("status = %q, want the error and the newer status", out)
	}
}

// The severity is in the text, not only in the color.
func TestToastSeverityIsReadableWithoutColor(t *testing.T) {
	m := newTestModel(t, "alpha")
	m.setToastErr("boom")
	if out := stripANSI(m.renderStatus()); !strings.Contains(out, "✗ boom") {
		t.Errorf("status = %q, want an error marker before the text", out)
	}
}
