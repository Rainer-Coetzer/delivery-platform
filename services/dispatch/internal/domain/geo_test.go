package domain

import "testing"

func TestStepToward_ArrivesWhenCloseEnough(t *testing.T) {
	_, _, arrived := StepToward(0, 0, 0.00001, 0.00001, 1000)
	if !arrived {
		t.Fatal("expected to arrive when the remaining distance is smaller than the step")
	}
}

func TestStepToward_MovesPartway(t *testing.T) {
	lat, lng, arrived := StepToward(0, 0, 1, 1, 1000)
	if arrived {
		t.Fatal("should not arrive in one small step over a large distance")
	}
	if lat <= 0 || lng <= 0 {
		t.Fatalf("expected movement toward target, got lat=%v lng=%v", lat, lng)
	}
}
