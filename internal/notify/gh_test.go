package notify_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/notify"
)

func TestDefaultPRStatusChecker_ContextCancelled(t *testing.T) {
	t.Parallel()

	var checker notify.PRStatusChecker = notify.DefaultPRStatusChecker
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := checker(ctx, "kalverra/pronto", 1)
	assert.Error(t, err)
}

func TestDefaultPRStatusChecker_MergedPR(t *testing.T) {
	t.Parallel()

	state, err := notify.DefaultPRStatusChecker(context.Background(), "kalverra/pronto", 1)
	if err != nil {
		t.Skipf("gh call failed: %v", err)
	}
	assert.Equal(t, notify.PRStateMerged, state)
}
