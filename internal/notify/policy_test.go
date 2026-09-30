package notify_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
)

func scope(tab notify.Tab, sec model.Section) notify.Scope {
	return notify.Scope{Tab: tab, Section: sec}
}

func TestPolicy_Wants(t *testing.T) {
	t.Parallel()

	prio := scope(notify.TabPriority, "")
	prioAttn := scope(notify.TabPriority, model.SectionAttention)
	focus := scope(notify.TabFocus, "")

	policy := notify.Policy{
		prio:     {notify.TriggerEntered: true},
		prioAttn: {notify.TriggerCIPassed: true},
		focus:    {notify.TriggerCIFailed: true},
	}

	cases := []struct {
		name string
		p    notify.Policy
		n    notify.Notification
		want bool
	}{
		{
			"empty policy",
			nil,
			notify.Notification{Trigger: notify.TriggerCIPassed, Scopes: []notify.Scope{prio}},
			false,
		},
		{
			"tab-level match",
			policy,
			notify.Notification{Trigger: notify.TriggerEntered, Scopes: []notify.Scope{prio}},
			true,
		},
		{
			"section-level match",
			policy,
			notify.Notification{Trigger: notify.TriggerCIPassed, Scopes: []notify.Scope{prio, prioAttn}},
			true,
		},
		{
			"union across scopes",
			policy,
			notify.Notification{Trigger: notify.TriggerCIFailed, Scopes: []notify.Scope{prio, focus}},
			true,
		},
		{
			"trigger absent everywhere",
			policy,
			notify.Notification{Trigger: notify.TriggerPRMerged, Scopes: []notify.Scope{prio, prioAttn, focus}},
			false,
		},
		{"no scopes", policy, notify.Notification{Trigger: notify.TriggerCIPassed}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.p.Wants(tc.n))
		})
	}
}

func TestPolicy_Select_Collapse(t *testing.T) {
	t.Parallel()

	tabSc := scope(notify.TabPriority, "")
	secSc := scope(notify.TabPriority, model.SectionAttention)
	focusSc := scope(notify.TabFocus, "")

	entered := func(sc notify.Scope) notify.Notification {
		return notify.Notification{
			Trigger: notify.TriggerEntered, Repo: "o/r", PRNumber: 1,
			Scopes: []notify.Scope{sc}, Entered: &sc,
		}
	}
	opened := notify.Notification{
		Trigger: notify.TriggerPROpened, Repo: "o/r", PRNumber: 1,
		Scopes: []notify.Scope{tabSc, secSc, focusSc},
	}
	ci := notify.Notification{Trigger: notify.TriggerCIPassed, Repo: "o/r", PRNumber: 1, Scopes: []notify.Scope{tabSc}}

	all := notify.TriggerSet{
		notify.TriggerEntered: true, notify.TriggerPROpened: true, notify.TriggerCIPassed: true,
	}
	policy := notify.Policy{tabSc: all, secSc: all, focusSc: all}

	t.Run("section-level entered beats tab-level and opened", func(t *testing.T) {
		t.Parallel()
		got := policy.Select([]notify.Notification{opened, entered(tabSc), entered(secSc), ci})
		if assert.Len(t, got, 2) {
			assert.Equal(t, notify.TriggerEntered, got[0].Trigger)
			assert.Equal(t, secSc, *got[0].Entered)
			assert.Equal(t, notify.TriggerCIPassed, got[1].Trigger)
		}
	})

	t.Run("tab-level entered beats opened", func(t *testing.T) {
		t.Parallel()
		got := policy.Select([]notify.Notification{opened, entered(tabSc)})
		if assert.Len(t, got, 1) {
			assert.Equal(t, tabSc, *got[0].Entered)
		}
	})

	t.Run("opened survives alone", func(t *testing.T) {
		t.Parallel()
		got := policy.Select([]notify.Notification{opened})
		if assert.Len(t, got, 1) {
			assert.Equal(t, notify.TriggerPROpened, got[0].Trigger)
		}
	})

	t.Run("unwanted entered does not suppress opened", func(t *testing.T) {
		t.Parallel()
		p := notify.Policy{tabSc: {notify.TriggerPROpened: true}}
		got := p.Select([]notify.Notification{opened, entered(secSc)})
		if assert.Len(t, got, 1) {
			assert.Equal(t, notify.TriggerPROpened, got[0].Trigger)
		}
	})

	t.Run("tab tie goes to earlier tab", func(t *testing.T) {
		t.Parallel()
		got := policy.Select([]notify.Notification{entered(tabSc), entered(focusSc)})
		if assert.Len(t, got, 1) {
			assert.Equal(t, focusSc, *got[0].Entered)
		}
	})

	t.Run("collapse is per PR", func(t *testing.T) {
		t.Parallel()
		other := entered(tabSc)
		other.PRNumber = 2
		got := policy.Select([]notify.Notification{entered(tabSc), other})
		assert.Len(t, got, 2)
	})
}
