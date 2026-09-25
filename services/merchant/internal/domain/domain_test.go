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
	day := Merchant{OpensAt: hm(9, 0), ClosesAt: hm(22, 0), IsOpen: true}
	night := Merchant{OpensAt: hm(18, 0), ClosesAt: hm(2, 0), IsOpen: true}
	closed := Merchant{OpensAt: hm(0, 0), ClosesAt: hm(23, 59), IsOpen: false}

	cases := []struct {
		name string
		r    Merchant
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

func TestOpenAtTime_UsesMerchantTimezone(t *testing.T) {
	hm := func(h, m int) time.Duration { return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute }
	windhoek := Merchant{ID: "w", OpensAt: hm(9, 0), ClosesAt: hm(22, 0), Timezone: "Africa/Windhoek", IsOpen: true}
	berlin := Merchant{ID: "b", OpensAt: hm(9, 0), ClosesAt: hm(22, 0), Timezone: "Europe/Berlin", IsOpen: true}
	utc := func(y int, mo time.Month, d, h, m int) time.Time { return time.Date(y, mo, d, h, m, 0, 0, time.UTC) }

	cases := []struct {
		name string
		r    Merchant
		at   time.Time
		want bool
	}{
		// Windhoek is UTC+2 all year.
		{"windhoek 07:30Z is 09:30 local, open", windhoek, utc(2026, 9, 24, 7, 30), true},
		{"windhoek 06:30Z is 08:30 local, closed", windhoek, utc(2026, 9, 24, 6, 30), false},
		{"windhoek 20:30Z is 22:30 local, closed (UTC would say open)", windhoek, utc(2026, 9, 24, 20, 30), false},
		// Berlin is UTC+1 in winter and UTC+2 in summer: the same UTC
		// instant lands either side of opening.
		{"berlin winter 07:30Z is 08:30 local, closed", berlin, utc(2026, 1, 15, 7, 30), false},
		{"berlin summer 07:30Z is 09:30 local, open", berlin, utc(2026, 7, 15, 7, 30), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.r.OpenAtTime(c.at)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("OpenAtTime = %v, want %v", got, c.want)
			}
		})
	}

	if _, err := (Merchant{ID: "x", Timezone: "Mars/Olympus"}).OpenAtTime(time.Now()); err == nil {
		t.Fatal("an invalid timezone must be an error, not silently UTC")
	}
}

func TestCheckItems(t *testing.T) {
	catalogue := map[string]CatalogueItem{
		"a": {ID: "a", MerchantID: "r1", Name: "Burger", PriceCents: 8500, StockQty: 5, Available: true},
		"b": {ID: "b", MerchantID: "r1", Name: "Chips", PriceCents: 2000, StockQty: 0, Available: true},
		"c": {ID: "c", MerchantID: "r1", Name: "Soup", PriceCents: 3000, StockQty: 9, Available: false},
		"x": {ID: "x", MerchantID: "r2", Name: "Elsewhere", PriceCents: 100, StockQty: 9, Available: true},
	}
	cases := []struct {
		name   string
		req    []RequestedItem
		wantOK bool
	}{
		{"in stock, right price", []RequestedItem{{"a", 5, 8500}}, true},
		{"more than stock", []RequestedItem{{"a", 6, 8500}}, false},
		{"out of stock", []RequestedItem{{"b", 1, 2000}}, false},
		{"unavailable", []RequestedItem{{"c", 1, 3000}}, false},
		{"unknown item", []RequestedItem{{"zzz", 1, 1}}, false},
		{"another merchant's item", []RequestedItem{{"x", 1, 100}}, false},
		{"stale or forged price", []RequestedItem{{"a", 1, 1}}, false},
		{"zero quantity", []RequestedItem{{"a", 0, 8500}}, false},
		{"empty job", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, reason := CheckItems("r1", catalogue, c.req)
			if ok != c.wantOK {
				t.Fatalf("ok = %v (%q), want %v", ok, reason, c.wantOK)
			}
			if !ok && reason == "" {
				t.Fatal("a rejection must carry a reason")
			}
		})
	}
}
