package domain

import (
	"testing"
	"time"
)

func TestDueForReady(t *testing.T) {
	since := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		now  time.Time
		prep time.Duration
		want bool
	}{
		{"just started", since, 20 * time.Second, false},
		{"one second short", since.Add(19 * time.Second), 20 * time.Second, false},
		{"exactly due", since.Add(20 * time.Second), 20 * time.Second, true},
		{"overdue", since.Add(time.Minute), 20 * time.Second, true},
		{"zero prep is immediately due", since, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DueForReady(c.now, since, c.prep); got != c.want {
				t.Fatalf("DueForReady = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOpenAt(t *testing.T) {
	hm := func(h, m int) time.Duration { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute }
	day := Restaurant{OpensAt: hm(9, 0), ClosesAt: hm(22, 0), IsOpen: true}
	night := Restaurant{OpensAt: hm(18, 0), ClosesAt: hm(2, 0), IsOpen: true}
	closed := Restaurant{OpensAt: hm(0, 0), ClosesAt: hm(23, 59), IsOpen: false}

	cases := []struct {
		name string
		r    Restaurant
		at   time.Duration
		want bool
	}{
		{"day: before opening", day, hm(8, 59), false},
		{"day: at opening", day, hm(9, 0), true},
		{"day: at closing is closed", day, hm(22, 0), false},
		{"overnight: evening", night, hm(23, 0), true},
		{"overnight: after midnight", night, hm(1, 30), true},
		{"overnight: afternoon", night, hm(15, 0), false},
		{"switched off", closed, hm(12, 0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.OpenAt(c.at); got != c.want {
				t.Fatalf("OpenAt = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCheckStock(t *testing.T) {
	catalogue := map[string]MenuItem{
		"a": {ID: "a", Name: "Burger", StockQty: 5, Available: true},
		"b": {ID: "b", Name: "Chips", StockQty: 0, Available: true},
		"c": {ID: "c", Name: "Soup", StockQty: 9, Available: false},
	}
	cases := []struct {
		name   string
		req    []RequestedItem
		wantOK bool
	}{
		{"in stock", []RequestedItem{{"a", 5}}, true},
		{"more than stock", []RequestedItem{{"a", 6}}, false},
		{"out of stock", []RequestedItem{{"b", 1}}, false},
		{"unavailable", []RequestedItem{{"c", 1}}, false},
		{"unknown item", []RequestedItem{{"zzz", 1}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := CheckStock(catalogue, c.req)
			if ok != c.wantOK {
				t.Fatalf("ok = %v (%q), want %v", ok, reason, c.wantOK)
			}
			if !ok && reason == "" {
				t.Fatal("a rejection must carry a reason")
			}
		})
	}
}
