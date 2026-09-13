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
