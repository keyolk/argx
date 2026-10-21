package tui

import (
	"errors"
	"os"
	"testing"

	"github.com/keyolk/tuikit/link"
	"github.com/keyolk/tuikit/selection"
)

// TestMain fails closed on the two side effects a mouse or key test can
// reach: the real clipboard and the real browser. A test that means to copy
// or open installs its own recorder; one that forgets gets an error toast
// instead of writing the developer's clipboard or opening a browser tab.
func TestMain(m *testing.M) {
	selection.Clipboard = func(string) error { return errors.New("test: clipboard not stubbed") }
	link.Launcher = func(string) error { return errors.New("test: launcher not stubbed") }
	os.Exit(m.Run())
}
