package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

// ErrBadSchedule means a booked time window can't be honoured.
var ErrBadSchedule = errors.New("schedule rejected")

// ApplyWindows checks the time windows a customer booked (the scheduling
// module) and puts them on the stops. windows maps a stop seq to its
// window; stops without one are "as soon as possible". Times are UTC
// instants, so a window means the same moment wherever it's read.
func ApplyWindows(stops []Stop, windows map[int]events.TimeWindow, limits *packs.SchedulingSettings, now time.Time) error {
	if len(windows) == 0 {
		return nil
	}
	if limits == nil {
		return fmt.Errorf("%w: this platform doesn't take booked times", ErrBadSchedule)
	}
	latest := now.Add(time.Duration(limits.MaxDaysAhead) * 24 * time.Hour)
	minWidth := time.Duration(limits.MinWindowMinutes) * time.Minute
	var problems []string
	var prev *events.TimeWindow
	for i := range stops {
		w, ok := windows[stops[i].Seq]
		if !ok {
			continue
		}
		n := stops[i].Seq
		switch {
		case !w.End.After(w.Start):
			problems = append(problems, fmt.Sprintf("stop %d: the window must end after it starts", n))
		case w.End.Sub(w.Start) < minWidth:
			problems = append(problems, fmt.Sprintf("stop %d: the window must be at least %d minutes", n, limits.MinWindowMinutes))
		}
		if !w.Start.After(now) {
			problems = append(problems, fmt.Sprintf("stop %d: the window must start in the future", n))
		}
		if w.Start.After(latest) {
			problems = append(problems, fmt.Sprintf("stop %d: bookings go at most %d days ahead", n, limits.MaxDaysAhead))
		}
		// Stops are visited in order: a later stop can't have to be done
		// before an earlier one can even start.
		if prev != nil && w.End.Before(prev.Start) {
			problems = append(problems, fmt.Sprintf("stop %d: the window closes before the previous stop's opens", n))
		}
		w.Start, w.End = w.Start.UTC(), w.End.UTC()
		stops[i].Window = &w
		prev = &w
	}
	for seq := range windows {
		if !hasSeq(stops, seq) {
			problems = append(problems, fmt.Sprintf("there is no stop %d to book", seq))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrBadSchedule, strings.Join(problems, "; "))
	}
	return nil
}

func hasSeq(stops []Stop, seq int) bool {
	for _, s := range stops {
		if s.Seq == seq {
			return true
		}
	}
	return false
}
