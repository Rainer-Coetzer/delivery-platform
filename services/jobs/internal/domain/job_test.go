package domain

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/lifecycle"
)

func foodMachine(t *testing.T) *lifecycle.Machine {
	t.Helper()
	data, err := os.ReadFile("../../../../packs/food/lifecycle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := lifecycle.Load(data, lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func foodJob() *Job {
	return &Job{ID: "j1", Status: "CREATED", Milestone: lifecycle.Created, Stops: []Stop{
		{Stop: events.Stop{Seq: 0, Kind: events.StopPickup}},
		{Stop: events.Stop{Seq: 1, Kind: events.StopDropoff}},
	}}
}

func TestJob_ConfirmsOnlyOnceBothFactsAreIn(t *testing.T) {
	m, j := foodMachine(t), foodJob()
	steps, err := j.Apply(m, lifecycle.Trigger{Event: events.PaymentCompleted}, "")
	if err != nil || len(steps) != 0 || j.Status != "CREATED" || !j.Facts["paid"] {
		t.Fatalf("paid alone sets the fact and waits: steps=%+v status=%s facts=%v err=%v", steps, j.Status, j.Facts, err)
	}
	steps, err = j.Apply(m, lifecycle.Trigger{Event: events.MerchantAccepted}, "")
	if err != nil || len(steps) != 2 {
		t.Fatalf("acceptance should confirm and fall through to PREPARING: %+v %v", steps, err)
	}
	// PREPARING maps no milestone, so the job stays on the last one it reached.
	if j.Status != "PREPARING" || j.Milestone != lifecycle.Confirmed {
		t.Fatalf("status/milestone = %s/%s, want PREPARING/confirmed", j.Status, j.Milestone)
	}
}

func TestJob_StopsDriveTransit(t *testing.T) {
	m, j := foodMachine(t), foodJob()
	j.Status, j.Milestone = "READY", lifecycle.ReadyForDispatch
	now := time.Now()

	info, err := j.CompleteStop(0, now)
	if err != nil || !info.FirstPickup || info.LastStop {
		t.Fatalf("stop 0 is the first pickup and not the last: %+v %v", info, err)
	}
	if _, err := j.Apply(m, lifecycle.Trigger{Event: events.DispatchStopCompleted, Stop: info}, ""); err != nil || j.Milestone != lifecycle.InTransit {
		t.Fatalf("pickup should put the job in transit: %s %v", j.Milestone, err)
	}
	if _, err := j.CompleteStop(0, now); err == nil {
		t.Fatal("completing a stop twice must be refused")
	}
	if _, err := j.CompleteStop(7, now); !errors.Is(err, ErrNoSuchStop) {
		t.Fatalf("unknown stop: %v", err)
	}
	info, _ = j.CompleteStop(1, now)
	if _, err := j.Apply(m, lifecycle.Trigger{Event: events.DispatchStopCompleted, Stop: info}, ""); err != nil || j.Status != "DELIVERED" || !j.Terminal() {
		t.Fatalf("last stop should complete the job: %s %v", j.Status, err)
	}
}

func TestJob_CancelKeepsReason(t *testing.T) {
	m, j := foodMachine(t), foodJob()
	if _, err := j.Apply(m, lifecycle.Trigger{Event: events.PaymentFailed}, "card declined"); err != nil {
		t.Fatal(err)
	}
	if j.Milestone != lifecycle.Cancelled || j.CancelReason != "card declined" || !j.Terminal() {
		t.Fatalf("got %s %q", j.Milestone, j.CancelReason)
	}
	if _, err := j.Apply(m, lifecycle.Trigger{Event: events.MerchantReady}, ""); !errors.Is(err, lifecycle.ErrNoTransition) {
		t.Fatalf("nothing moves a cancelled job: %v", err)
	}
}

func TestJob_SettleOnFoodIsANoop(t *testing.T) {
	m, j := foodMachine(t), foodJob()
	if steps := j.Settle(m); len(steps) != 0 || j.Status != "CREATED" {
		t.Fatalf("a new food job waits: %+v", steps)
	}
}
