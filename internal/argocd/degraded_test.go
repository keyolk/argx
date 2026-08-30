package argocd

import "testing"

// A failed sync the application has since recovered from is history, not a
// fault. Argo CD keeps the failed operation until the next one replaces it, so
// flagging it would leave an app red for as long as nothing synced it again —
// two applications in a 2,976 fleet were red this way, one from ten days back.
func TestRecoveredSyncFailureIsNotDegraded(t *testing.T) {
	var a Application
	a.Status.Sync.Status = "Synced"
	a.Status.Health.Status = "Healthy"
	a.Status.OperationState = &OperationState{Phase: "Failed"}

	if a.Degraded() {
		t.Error("Synced and Healthy is not a state to flag, whatever the last sync did")
	}
}

// The failure still counts while the application has not recovered.
func TestUnrecoveredSyncFailureIsDegraded(t *testing.T) {
	cases := []struct {
		name, sync, health string
	}{
		{"still out of sync", "OutOfSync", "Healthy"},
		{"still unhealthy", "Synced", "Degraded"},
		{"neither", "OutOfSync", "Progressing"},
	}
	for _, c := range cases {
		var a Application
		a.Status.Sync.Status = c.sync
		a.Status.Health.Status = c.health
		a.Status.OperationState = &OperationState{Phase: "Failed"}
		if !a.Degraded() {
			t.Errorf("%s: a standing sync failure should be flagged", c.name)
		}
	}
}

// Health alone still decides, with or without an operation.
func TestUnhealthyIsAlwaysDegraded(t *testing.T) {
	for _, h := range []string{"Degraded", "Missing", "Unknown"} {
		var a Application
		a.Status.Sync.Status = "Synced"
		a.Status.Health.Status = h
		if !a.Degraded() {
			t.Errorf("health %q should be flagged", h)
		}
	}
}
