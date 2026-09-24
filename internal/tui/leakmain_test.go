package tui_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kalverra/pronto/internal/profiling"
)

func TestMain(m *testing.M) {
	// Models built without WithNotifier use the real notifier factory. Point
	// it at a helper that doesn't exist so tests never read the machine's
	// installed helper (its state drives the notification hint banner) or
	// post real banners.
	_ = os.Setenv("PRONTO_NOTIFY_HELPER", filepath.Join(os.TempDir(), "pronto-tui-test-no-helper"))
	profiling.LeakCheckMain(m)
}
