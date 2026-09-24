package notify_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/notify"
)

func TestSystemSounds_IncludesKnownBuiltins(t *testing.T) {
	t.Parallel()

	sounds := notify.SystemSounds()
	assert.Contains(t, sounds, "Glass")
	assert.Contains(t, sounds, "Basso")
}

func TestValidSoundName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want bool
	}{
		{"", true},
		{"default", true},
		{"Glass", true},
		{"Basso", true},
		{"/System/Library/Sounds/Glass.aiff", false},
		{"Glass.aiff", false},
		{"not-a-real-sound", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, notify.ValidSoundName(tc.name), "name=%q", tc.name)
		})
	}
}
