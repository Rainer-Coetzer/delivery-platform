package domain

import (
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{events.StatusCreated, events.StatusConfirmed, true},
		{events.StatusCreated, events.StatusPreparing, false}, // must go through CONFIRMED
		{events.StatusCreated, events.StatusCancelled, true},
		{events.StatusDelivered, events.StatusCancelled, false}, // terminal
		{events.StatusCancelled, events.StatusCreated, false},   // terminal
		{events.StatusReady, events.StatusOutForDelivery, true},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestJob_Transition_RejectsDuplicate(t *testing.T) {
	o := &Job{Status: events.StatusCreated}
	if _, err := o.Transition(events.StatusConfirmed, ""); err != nil {
		t.Fatalf("first transition should succeed: %v", err)
	}
	if _, err := o.Transition(events.StatusConfirmed, ""); err == nil {
		t.Fatal("duplicate transition to the same status should fail")
	}
}

func TestJob_MaybeConfirm_WaitsForBoth(t *testing.T) {
	o := &Job{Status: events.StatusCreated}

	transitioned, _, err := o.MaybeConfirm()
	if err != nil || transitioned {
		t.Fatalf("should not confirm with neither paid nor accepted, got transitioned=%v err=%v", transitioned, err)
	}

	o.Paid = true
	transitioned, _, err = o.MaybeConfirm()
	if err != nil || transitioned {
		t.Fatalf("should not confirm with only paid, got transitioned=%v err=%v", transitioned, err)
	}

	o.MerchantAccepted = true
	transitioned, from, err := o.MaybeConfirm()
	if err != nil {
		t.Fatalf("should confirm once both are true: %v", err)
	}
	if !transitioned {
		t.Fatal("expected a transition")
	}
	if from != events.StatusCreated {
		t.Fatalf("from = %s, want CREATED", from)
	}
	if o.Status != events.StatusPreparing {
		t.Fatalf("status = %s, want PREPARING (confirm falls straight through to preparing)", o.Status)
	}
}

func TestSurgeMultiplierFor(t *testing.T) {
	cases := []struct {
		openJobs, freeAgents int
		want                 float64
	}{
		{0, 5, 1.0},
		{5, 5, 1.0},
		{8, 5, 1.2},
		{15, 5, 1.5},
		{30, 5, 2.0},
		{10, 0, 2.0}, // no free agents at all is worst case
	}
	for _, c := range cases {
		if got := SurgeMultiplierFor(c.openJobs, c.freeAgents); got != c.want {
			t.Errorf("SurgeMultiplierFor(%d, %d) = %v, want %v", c.openJobs, c.freeAgents, got, c.want)
		}
	}
}
