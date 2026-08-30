package argocd

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// When a sync window next opens.
//
// Argo CD does not answer this — it only says whether a window is open right
// now — so argx computes it, using the same parser and the same interpretation
// the server uses (pkg/apis/application/v1alpha1/types.go): five cron fields, a
// duration, and a time zone the schedule is read in.
//
// The point is to be able to wait for a window rather than sync into a closed
// one. A sync that Argo CD refuses is not merely a no-op: it records a failed
// operation on the application, which is noise in exactly the place someone
// looks when something is wrong.

// cronParser matches Argo CD's: five fields, no seconds, no descriptors.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// WindowOpening is one occurrence of a sync window.
type WindowOpening struct {
	Start time.Time
	End   time.Time
}

// NextOpening returns when this window next opens at or after `from`.
//
// A window that is open at `from` returns that occurrence, with its real start
// in the past — the caller wants to know the window is open now, not when it
// will open again.
func (w SyncWindow) NextOpening(from time.Time) (WindowOpening, error) {
	schedule, err := cronParser.Parse(w.Schedule)
	if err != nil {
		return WindowOpening{}, fmt.Errorf("schedule %q: %w", w.Schedule, err)
	}
	dur, err := time.ParseDuration(w.Duration)
	if err != nil {
		return WindowOpening{}, fmt.Errorf("duration %q: %w", w.Duration, err)
	}

	loc := time.UTC
	if w.TimeZone != "" {
		l, err := time.LoadLocation(w.TimeZone)
		if err != nil {
			return WindowOpening{}, fmt.Errorf("time zone %q: %w", w.TimeZone, err)
		}
		loc = l
	}

	// The schedule is expressed in the window's own zone, so the search runs
	// there. Reading a 15:00 Asia/Seoul window in UTC puts it nine hours off.
	local := from.In(loc)

	// A window that started before `from` and has not yet ended is the answer:
	// asking "when does this open" while it is open should not skip to
	// tomorrow. Stepping back one duration finds it.
	if prev := schedule.Next(local.Add(-dur)); prev.Before(local) || prev.Equal(local) {
		if end := prev.Add(dur); end.After(local) {
			return WindowOpening{Start: prev, End: end}, nil
		}
	}

	start := schedule.Next(local)
	return WindowOpening{Start: start, End: start.Add(dur)}, nil
}

// Active reports whether the window is open at the given time.
func (w SyncWindow) Active(at time.Time) (bool, error) {
	o, err := w.NextOpening(at)
	if err != nil {
		return false, err
	}
	return !o.Start.After(at) && o.End.After(at), nil
}

// NextSyncableAt is when this application's windows will next allow a sync.
//
// The rules mirror the server's CanSync, because a time argx computed by
// different rules than the server enforces is a time the sync gets refused:
//
//   - No windows at all: syncing is always allowed.
//   - An open deny window blocks, unless every open deny permits manual syncs
//     and this is a manual sync.
//   - Otherwise an open allow window permits.
//   - Otherwise, if allow windows exist but all are closed, a manual sync still
//     passes when every one of them permits manual syncs. This is the rule that
//     is easiest to miss, and missing it makes argx wait hours for a sync the
//     server would have accepted immediately.
//   - Otherwise the next opening of the earliest allow window is the answer.
//
// One deliberate difference from the server: it approximates the time zone with
// today's fixed UTC offset, so its answer drifts by an hour across a DST
// transition. argx resolves the zone properly. Where they disagree the server
// decides, which is why a scheduled sync re-asks it before firing.
//
// A zero time means syncing is allowed right now. The returned window is the
// one being waited for, so the caller can say which.
func NextSyncableAt(windows []SyncWindow, from time.Time, manual bool) (time.Time, SyncWindow, error) {
	if len(windows) == 0 {
		return time.Time{}, SyncWindow{}, nil
	}

	var (
		allows      []SyncWindow
		openDeny    bool
		denyAllows  = true // whether every open deny permits manual syncs
		openAllow   bool
		blockingWin SyncWindow
	)

	for _, w := range windows {
		active, err := w.Active(from)
		if err != nil {
			return time.Time{}, SyncWindow{}, err
		}
		switch {
		case w.Blocks():
			if active {
				openDeny = true
				blockingWin = w
				if !w.ManualSync {
					denyAllows = false
				}
			}
		default:
			allows = append(allows, w)
			if active {
				openAllow = true
			}
		}
	}

	if openDeny {
		if manual && denyAllows {
			return time.Time{}, SyncWindow{}, nil
		}
		// A deny window blocks until it closes, and another may open behind it.
		// Rather than model that chain, report the end of this one: the caller
		// re-checks, which is also what handles a window edited in the meantime.
		o, err := blockingWin.NextOpening(from)
		if err != nil {
			return time.Time{}, SyncWindow{}, err
		}
		return o.End, blockingWin, nil
	}

	if openAllow || len(allows) == 0 {
		return time.Time{}, SyncWindow{}, nil
	}

	// Every allow window is closed. A manual sync still passes if all of them
	// permit one — the server's inactiveAllows.manualEnabled() rule.
	if manual {
		all := true
		for _, w := range allows {
			if !w.ManualSync {
				all = false
				break
			}
		}
		if all {
			return time.Time{}, SyncWindow{}, nil
		}
	}

	// Allow windows exist but none is open, so syncing waits for the first.
	var (
		soonest time.Time
		which   SyncWindow
	)
	for _, w := range allows {
		o, err := w.NextOpening(from)
		if err != nil {
			return time.Time{}, SyncWindow{}, err
		}
		if soonest.IsZero() || o.Start.Before(soonest) {
			soonest, which = o.Start, w
		}
	}
	return soonest, which, nil
}
