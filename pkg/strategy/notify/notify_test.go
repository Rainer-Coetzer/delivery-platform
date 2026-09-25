package notify

import (
	"context"
	"strings"
	"testing"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy/strategytest"
)

func TestConsole(t *testing.T) {
	c, err := Build("console", strategytest.Params(t, "notify", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), Message{Recipient: "user:u1", Title: "t", Body: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Build("console", strategytest.Params(t, "notify", `{colour: true}`)); err == nil || !strings.Contains(err.Error(), "field colour not found") {
		t.Fatalf("console takes no params: %v", err)
	}
	if got := strings.Join(Names(), ","); got != "console" {
		t.Fatalf("names = %s", got)
	}
}
