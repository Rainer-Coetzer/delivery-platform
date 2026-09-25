package main

import (
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/app"
)

func TestSelectModules(t *testing.T) {
	all := []app.Definition{{Name: "customer"}, {Name: "job"}, {Name: "gateway"}}
	cases := []struct {
		list    string
		want    []string
		wantErr bool
	}{
		{"", []string{"customer", "job", "gateway"}, false},
		{" job , gateway ", []string{"job", "gateway"}, false},
		{"job,paymnet", nil, true}, // typo must fail, not silently drop payments
	}
	for _, c := range cases {
		got, err := selectModules(c.list, all)
		if (err != nil) != c.wantErr {
			t.Fatalf("%q: err = %v", c.list, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%q: got %d modules, want %v", c.list, len(got), c.want)
		}
		for i := range got {
			if got[i].Name != c.want[i] {
				t.Fatalf("%q: got %s at %d, want %s", c.list, got[i].Name, i, c.want[i])
			}
		}
	}
}
