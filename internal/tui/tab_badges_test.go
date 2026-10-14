package tui

import (
	"strings"
	"testing"
)

// The tabs say what they hold before the reader switches to them.
func TestTabBadges(t *testing.T) {
	m := appModel(t, nil)
	m.app.Status.Resources = append(m.app.Status.Resources,
		struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}{Kind: "Deployment", Name: "web", Status: "OutOfSync"},
		struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Status string `json:"status"`
		}{Kind: "Service", Name: "web", Status: "Synced"})
	m.app.Status.Health.Status = "Degraded"

	bar := stripANSI(m.renderTabBar())
	if !strings.Contains(bar, "RESOURCES "+m.gl.outOfSync+"1") {
		t.Errorf("RESOURCES should count the out-of-sync resource: %q", bar)
	}
	if !strings.Contains(bar, "DETAILS "+m.gl.no) {
		t.Errorf("DETAILS should mark a degraded application: %q", bar)
	}

	m.app.Status.Resources = nil
	m.app.Status.Health.Status = "Healthy"
	bar = stripANSI(m.renderTabBar())
	if strings.Contains(bar, "RESOURCES "+m.gl.outOfSync) || strings.Contains(bar, m.gl.no) {
		t.Errorf("nothing to say, no badge: %q", bar)
	}
}
