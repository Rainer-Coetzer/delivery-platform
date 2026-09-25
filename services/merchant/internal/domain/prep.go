package domain

import "time"

// DueForReady reports whether a job that started preparing at `since`
// has been in the prep for at least `prep` as of `now`. Used by the demo
// autopilot to mark jobs ready without a person pressing the button.
func DueForReady(now, since time.Time, prep time.Duration) bool {
	return !now.Before(since.Add(prep))
}
