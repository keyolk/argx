package argocd

import (
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// The schedule is expressed in the window's own zone. Reading a 15:00
// Asia/Seoul window in UTC puts it nine hours off, which is the difference
// between "syncs tonight" and "syncs tomorrow morning".
func TestNextOpeningHonoursTheTimeZone(t *testing.T) {
	w := SyncWindow{
		Kind: "allow", Schedule: "0 15 * * *", Duration: "2h", TimeZone: "Asia/Seoul",
	}
	// 2026-08-31 01:00 UTC is 10:00 KST — the window opens at 15:00 KST.
	from := mustTime(t, "2026-08-31T01:00:00Z")

	o, err := w.NextOpening(from)
	if err != nil {
		t.Fatal(err)
	}
	// 15:00 KST is 06:00 UTC.
	if got := o.Start.UTC().Format(time.RFC3339); got != "2026-08-31T06:00:00Z" {
		t.Errorf("start = %s, want 06:00Z (15:00 KST)", got)
	}
	if got := o.End.UTC().Format(time.RFC3339); got != "2026-08-31T08:00:00Z" {
		t.Errorf("end = %s, want 08:00Z", got)
	}
}

// Asking when a window opens while it is open must not skip to tomorrow.
func TestNextOpeningReturnsTheCurrentOccurrence(t *testing.T) {
	w := SyncWindow{Kind: "allow", Schedule: "0 15 * * *", Duration: "5h", TimeZone: "UTC"}
	// 16:00 is one hour into the window.
	from := mustTime(t, "2026-08-31T16:00:00Z")

	o, err := w.NextOpening(from)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.Start.UTC().Format(time.RFC3339); got != "2026-08-31T15:00:00Z" {
		t.Errorf("start = %s, want today's 15:00 — the window is open now", got)
	}
	active, err := w.Active(from)
	if err != nil {
		t.Fatal(err)
	}
	if !active {
		t.Error("the window should report itself active")
	}
}

// A window that has closed reports tomorrow's occurrence.
func TestNextOpeningSkipsAClosedWindow(t *testing.T) {
	w := SyncWindow{Kind: "allow", Schedule: "0 15 * * *", Duration: "1h", TimeZone: "UTC"}
	from := mustTime(t, "2026-08-31T17:00:00Z") // an hour after it closed

	o, err := w.NextOpening(from)
	if err != nil {
		t.Fatal(err)
	}
	if got := o.Start.UTC().Format(time.RFC3339); got != "2026-09-01T15:00:00Z" {
		t.Errorf("start = %s, want tomorrow's 15:00", got)
	}
	if active, _ := w.Active(from); active {
		t.Error("a closed window should not report itself active")
	}
}

// An empty time zone is UTC, which is what the server assumes.
func TestEmptyTimeZoneIsUTC(t *testing.T) {
	w := SyncWindow{Kind: "allow", Schedule: "0 15 * * *", Duration: "1h"}
	o, err := w.NextOpening(mustTime(t, "2026-08-31T01:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if got := o.Start.UTC().Format(time.RFC3339); got != "2026-08-31T15:00:00Z" {
		t.Errorf("start = %s, want 15:00 UTC", got)
	}
}

func TestMalformedWindowsAreErrors(t *testing.T) {
	for _, w := range []SyncWindow{
		{Schedule: "not a cron", Duration: "1h"},
		{Schedule: "0 15 * * *", Duration: "not a duration"},
		{Schedule: "0 15 * * *", Duration: "1h", TimeZone: "Mars/Olympus"},
	} {
		if _, err := w.NextOpening(time.Now()); err == nil {
			t.Errorf("%+v should not parse", w)
		}
	}
}

// ---- NextSyncableAt ----
//
// These mirror the server's CanSync. A time computed by different rules than
// the server enforces is a time the sync gets refused.

func TestNoWindowsMeansSyncNow(t *testing.T) {
	at, _, err := NextSyncableAt(nil, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !at.IsZero() {
		t.Errorf("with no windows syncing is always allowed, got %v", at)
	}
}

// Allow windows exist but none is open: syncing waits for the first.
func TestWaitsForTheNextAllowWindow(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "allow", Schedule: "0 15 * * *", Duration: "1h", TimeZone: "UTC"},
		{Kind: "allow", Schedule: "0 9 * * *", Duration: "1h", TimeZone: "UTC"},
	}
	from := mustTime(t, "2026-08-31T01:00:00Z")

	at, w, err := NextSyncableAt(windows, from, false)
	if err != nil {
		t.Fatal(err)
	}
	// 09:00 comes first.
	if got := at.UTC().Format(time.RFC3339); got != "2026-08-31T09:00:00Z" {
		t.Errorf("next syncable at %s, want the earlier window at 09:00", got)
	}
	if w.Schedule != "0 9 * * *" {
		t.Errorf("waiting on %q, want the 09:00 window", w.Schedule)
	}
}

func TestOpenAllowWindowMeansSyncNow(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "allow", Schedule: "0 15 * * *", Duration: "5h", TimeZone: "UTC"},
	}
	at, _, err := NextSyncableAt(windows, mustTime(t, "2026-08-31T16:00:00Z"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !at.IsZero() {
		t.Errorf("an open allow window permits syncing now, got %v", at)
	}
}

// A deny window blocks until it closes.
func TestDenyWindowBlocksUntilItCloses(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "deny", Schedule: "0 0 * * *", Duration: "6h", TimeZone: "UTC"},
	}
	from := mustTime(t, "2026-08-31T02:00:00Z")

	at, w, err := NextSyncableAt(windows, from, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := at.UTC().Format(time.RFC3339); got != "2026-08-31T06:00:00Z" {
		t.Errorf("next syncable at %s, want the deny window's end at 06:00", got)
	}
	if !w.Blocks() {
		t.Error("the reported window should be the blocking one")
	}
}

// A deny window that permits manual syncs does not block one.
func TestManualSyncPassesADenyWindowThatPermitsIt(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "deny", Schedule: "0 0 * * *", Duration: "6h", TimeZone: "UTC", ManualSync: true},
	}
	from := mustTime(t, "2026-08-31T02:00:00Z")

	if at, _, _ := NextSyncableAt(windows, from, true); !at.IsZero() {
		t.Errorf("a manual sync should pass, got %v", at)
	}
	// An automated one still waits.
	if at, _, _ := NextSyncableAt(windows, from, false); at.IsZero() {
		t.Error("a non-manual sync should still be blocked")
	}
}

// One deny window without manual permission blocks even a manual sync, which
// is the server's rule: it requires every open deny to allow it.
func TestOneStrictDenyBlocksManualSync(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "deny", Schedule: "0 0 * * *", Duration: "6h", TimeZone: "UTC", ManualSync: true},
		{Kind: "deny", Schedule: "0 1 * * *", Duration: "2h", TimeZone: "UTC"},
	}
	from := mustTime(t, "2026-08-31T01:30:00Z") // inside both

	if at, _, _ := NextSyncableAt(windows, from, true); at.IsZero() {
		t.Error("a manual sync should be blocked while one deny forbids it")
	}
}

// A deny window takes precedence over an open allow, matching the server.
func TestDenyOutranksAnOpenAllow(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "allow", Schedule: "0 0 * * *", Duration: "23h", TimeZone: "UTC"},
		{Kind: "deny", Schedule: "0 1 * * *", Duration: "1h", TimeZone: "UTC"},
	}
	from := mustTime(t, "2026-08-31T01:30:00Z")

	at, _, err := NextSyncableAt(windows, from, false)
	if err != nil {
		t.Fatal(err)
	}
	if at.IsZero() {
		t.Error("an open deny blocks even when an allow is open")
	}
}

// A window argx cannot parse must surface rather than being silently treated
// as absent — that would schedule a sync into a window that does block it.
func TestMalformedWindowSurfaces(t *testing.T) {
	windows := []SyncWindow{{Kind: "allow", Schedule: "nonsense", Duration: "1h"}}
	if _, _, err := NextSyncableAt(windows, time.Now(), false); err == nil {
		t.Fatal("a malformed window should be an error, not an absent one")
	}
}

// Every allow window closed, but all permit manual syncs: the server lets a
// manual sync through immediately (inactiveAllows.manualEnabled()). Waiting for
// the next opening would stall a sync the server would have accepted.
func TestClosedAllowWindowsThatPermitManualDoNotWait(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "allow", Schedule: "0 15 * * *", Duration: "1h", TimeZone: "UTC", ManualSync: true},
		{Kind: "allow", Schedule: "0 9 * * *", Duration: "1h", TimeZone: "UTC", ManualSync: true},
	}
	from := mustTime(t, "2026-08-31T01:00:00Z") // both closed

	if at, _, _ := NextSyncableAt(windows, from, true); !at.IsZero() {
		t.Errorf("a manual sync should pass, got a wait until %v", at)
	}
	// An automated sync still waits for the window.
	at, _, err := NextSyncableAt(windows, from, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := at.UTC().Format(time.RFC3339); got != "2026-08-31T09:00:00Z" {
		t.Errorf("a non-manual sync waits until %s, want 09:00", got)
	}
}

// One closed allow window that forbids manual syncs blocks the whole set, which
// is the server's rule: manualEnabled() requires every window to permit it.
func TestOneClosedAllowWithoutManualBlocks(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "allow", Schedule: "0 15 * * *", Duration: "1h", TimeZone: "UTC", ManualSync: true},
		{Kind: "allow", Schedule: "0 9 * * *", Duration: "1h", TimeZone: "UTC"},
	}
	from := mustTime(t, "2026-08-31T01:00:00Z")

	at, _, err := NextSyncableAt(windows, from, true)
	if err != nil {
		t.Fatal(err)
	}
	if at.IsZero() {
		t.Error("one window forbidding manual syncs blocks them all")
	}
}

// A closed deny window is not a constraint at all — only open ones block.
func TestClosedDenyWindowDoesNotBlock(t *testing.T) {
	windows := []SyncWindow{
		{Kind: "deny", Schedule: "0 0 * * *", Duration: "1h", TimeZone: "UTC"},
	}
	from := mustTime(t, "2026-08-31T05:00:00Z")

	if at, _, _ := NextSyncableAt(windows, from, false); !at.IsZero() {
		t.Errorf("a closed deny window should not block, got %v", at)
	}
}
