package client_test

import (
	"testing"

	"github.com/kalverra/pronto/internal/profiling"
)

func TestMain(m *testing.M) {
	profiling.LeakCheckMain(m)
}
