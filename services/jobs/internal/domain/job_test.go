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

	info, err := j.CompleteStop(0, now, nil)
	if err != nil || !info.FirstPickup || info.LastStop {
		t.Fatalf("stop 0 is the first pickup and not the last: %+v %v", info, err)
	}
	if _, err := j.Apply(m, lifecycle.Trigger{Event: events.DispatchStopCompleted, Stop: info}, ""); err != nil || j.Milestone != lifecycle.InTransit {
		t.Fatalf("pickup should put the job in transit: %s %v", j.Milestone, err)
	}
	if _, err := j.CompleteStop(0, now, nil); err == nil {
		t.Fatal("completing a stop twice must be refused")
	}
	if _, err := j.CompleteStop(7, now, nil); !errors.Is(err, ErrNoSuchStop) {
		t.Fatalf("unknown stop: %v", err)
	}
	info, _ = j.CompleteStop(1, now, nil)
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

func TestJob_EventShapes(t *testing.T) {
	j := foodJob()
	j.Items = []Item{
		{Kind: LineCatalogue, CatalogueItemID: "i1", Name: "Platter", Quantity: 2, UnitPriceCents: 6500},
		{Kind: LinePackage, Name: "Box", Quantity: 3, WeightGrams: 1200, LengthMm: 100, WidthMm: 100, HeightMm: 100},
	}
	lines := j.EventLines()
	if len(lines) != 2 || lines[0].CatalogueItemID != "i1" || lines[0].WeightGrams != 0 {
		t.Fatalf("catalogue line: %+v", lines[0])
	}
	// Volume is per unit on the event; the job's total is SizeOf's business.
	if lines[1].Kind != LinePackage || lines[1].WeightGrams != 1200 || lines[1].VolumeCm3 != 1000 {
		t.Fatalf("package line: %+v", lines[1])
	}
	if stops := j.EventStops(); len(stops) != 2 || stops[1].Kind != events.StopDropoff {
		t.Fatalf("stops: %+v", stops)
	}
}

func TestJob_SetFact(t *testing.T) {
	m, err := lifecycle.Load([]byte(`schema: 1
initial: NEW
states:
  - { name: NEW, milestone: created }
  - { name: READY, milestone: ready_for_dispatch }
  - { name: DONE, milestone: completed, terminal: true }
  - { name: GONE, milestone: cancelled, terminal: true }
facts:
  approved: { by: [merchant] }
transitions:
  - { from: NEW, to: READY, auto: true, when: [approved] }
  - { from: READY, to: DONE, on: dispatch.stop-completed, stop: last_stop }
  - { from: "*", to: GONE, by: [admin] }
`), lifecycle.Known{Events: events.AllTopics, Roles: []string{"customer", "merchant", "agent", "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	j := &Job{Status: "NEW", Milestone: lifecycle.Created}
	if _, err := j.SetFact(m, "approved", "customer"); !errors.Is(err, lifecycle.ErrNoTransition) {
		t.Fatalf("customer may not approve: %v", err)
	}
	steps, err := j.SetFact(m, "approved", "merchant")
	if err != nil || len(steps) != 1 || j.Status != "READY" || j.Milestone != lifecycle.ReadyForDispatch || !j.Facts["approved"] {
		t.Fatalf("approval unlocks READY: %+v %+v %v", steps, j, err)
	}
}
