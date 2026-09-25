package domain

import (
	"errors"
	"regexp"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
)

func TestRequireProof(t *testing.T) {
	stops := []Stop{
		{Stop: events.Stop{Seq: 0, Kind: "pickup"}},
		{Stop: events.Stop{Seq: 1, Kind: "dropoff"}},
		{Stop: events.Stop{Seq: 2, Kind: "service"}},
	}
	err := RequireProof("j1", stops, map[string]string{"dropoff": "pin", "service": "signature"}, func() (string, error) { return "0427", nil })
	if err != nil {
		t.Fatal(err)
	}
	if stops[0].Proof != "" || stops[0].PIN != "" {
		t.Fatalf("a pickup needs nothing here: %+v", stops[0])
	}
	if stops[1].Proof != "pin" || stops[1].PIN != "0427" || stops[1].PinHash != events.PinHash("j1", 1, "0427") {
		t.Fatalf("dropoff PIN: %+v", stops[1])
	}
	if stops[2].Proof != "signature" || stops[2].PIN != "" || stops[2].PinHash != "" {
		t.Fatalf("a signature needs no PIN: %+v", stops[2])
	}
	// The hash is bound to job and stop: the same PIN elsewhere doesn't match.
	if events.PinHash("j1", 1, "0427") == events.PinHash("j2", 1, "0427") || events.PinHash("j1", 1, "0427") == events.PinHash("j1", 2, "0427") {
		t.Fatal("PIN hashes must differ across jobs and stops")
	}

	boom := errors.New("no entropy")
	if err := RequireProof("j1", []Stop{{Stop: events.Stop{Seq: 0, Kind: "dropoff"}}}, map[string]string{"dropoff": "pin"}, func() (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatalf("a PIN that can't be made must stop the job: %v", err)
	}
}

func TestNewPIN(t *testing.T) {
	four := regexp.MustCompile(`^\d{4}$`)
	for i := 0; i < 50; i++ {
		p, err := NewPIN()
		if err != nil || !four.MatchString(p) {
			t.Fatalf("PIN %q %v", p, err)
		}
	}
}
