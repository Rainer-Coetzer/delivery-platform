package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rainercoetzer/delivery-platform/pkg/events"
	"github.com/rainercoetzer/delivery-platform/pkg/packs"
)

func TestApplyWindows(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	limits := &packs.SchedulingSettings{MaxDaysAhead: 7, MinWindowMinutes: 30, LeadMinutes: 20}
	at := func(h, m int) time.Time { return now.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute) }
	win := func(fromH, toH int) events.TimeWindow { return events.TimeWindow{Start: at(fromH, 0), End: at(toH, 0)} }
	stops := func() []Stop {
		return []Stop{{Stop: events.Stop{Seq: 0, Kind: "pickup"}}, {Stop: events.Stop{Seq: 1, Kind: "dropoff"}}}
	}
	cases := []struct {
		name    string
		windows map[int]events.TimeWindow
		want    string
	}{
		{"none is ASAP", nil, ""},
		{"pickup window", map[int]events.TimeWindow{0: win(2, 3)}, ""},
		{"both", map[int]events.TimeWindow{0: win(2, 3), 1: win(3, 5)}, ""},
		{"backwards", map[int]events.TimeWindow{0: win(3, 2)}, "must end after it starts"},
		{"too narrow", map[int]events.TimeWindow{0: {Start: at(2, 0), End: at(2, 15)}}, "at least 30 minutes"},
		{"in the past", map[int]events.TimeWindow{0: win(-2, 1)}, "must start in the future"},
		{"too far ahead", map[int]events.TimeWindow{0: win(24*8, 24*8+1)}, "at most 7 days ahead"},
		{"drop before pickup can start", map[int]events.TimeWindow{0: win(5, 6), 1: win(2, 3)}, "closes before the previous stop's opens"},
		{"a stop that isn't there", map[int]events.TimeWindow{7: win(2, 3)}, "no stop 7"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := stops()
			err := ApplyWindows(s, c.windows, limits, now)
			if c.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				for seq, w := range c.windows {
					if s[seq].Window == nil || !s[seq].Window.Start.Equal(w.Start) {
						t.Fatalf("stop %d window not applied: %+v", seq, s[seq].Window)
					}
				}
				return
			}
			if !errors.Is(err, ErrBadSchedule) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want ErrBadSchedule mentioning %q, got %v", c.want, err)
			}
		})
	}
	if err := ApplyWindows(stops(), map[int]events.TimeWindow{0: win(2, 3)}, nil, now); !errors.Is(err, ErrBadSchedule) {
		t.Fatalf("without the module: %v", err)
	}
}

// A window given with an offset is stored as the same instant in UTC.
func TestApplyWindowsNormalisesToUTC(t *testing.T) {
	now := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	windhoek := time.FixedZone("CAT", 2*3600)
	s := []Stop{{Stop: events.Stop{Seq: 0, Kind: "pickup"}}}
	start := time.Date(2026, 9, 25, 14, 0, 0, 0, windhoek) // 12:00 UTC
	err := ApplyWindows(s, map[int]events.TimeWindow{0: {Start: start, End: start.Add(time.Hour)}},
		&packs.SchedulingSettings{MaxDaysAhead: 1, MinWindowMinutes: 30}, now)
	if err != nil || s[0].Window.Start.Location() != time.UTC || s[0].Window.Start.Hour() != 12 {
		t.Fatalf("got %+v %v", s[0].Window, err)
	}
}
