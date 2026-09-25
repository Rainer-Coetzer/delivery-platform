// Package notify is the notification channel kind: where a notification
// goes besides the in-app inbox (which the notification module always
// keeps). Built-in: console, which logs it. Email, SMS and push providers
// are Phase E; they register the same way.
package notify

import (
	"context"
	"log/slog"

	"github.com/rainercoetzer/delivery-platform/pkg/strategy"
)

// Message is one notification.
type Message struct {
	Recipient string // "user:<id>", "merchant:<id>", "agent:<id>", "job:<id>"
	JobID     string
	Title     string
	Body      string
}

// Channel delivers a message. An error means it wasn't delivered and the
// notification module should retry the event.
type Channel interface {
	Send(ctx context.Context, m Message) error
}

var registry = strategy.NewRegistry[Channel]("notify")

func Register(name string, f strategy.Factory[Channel]) { registry.Register(name, f) }
func Build(name string, p strategy.Params) (Channel, error) {
	return registry.Build(name, p)
}
func Names() []string { return registry.Names() }

func init() { Register("console", newConsole) }

// console writes each message to the log: enough for a demo, and a record
// of what a real channel would have sent.
type console struct{}

func newConsole(p strategy.Params) (Channel, error) {
	if err := p.Decode(&struct{}{}); err != nil {
		return nil, err
	}
	return console{}, nil
}

func (console) Send(ctx context.Context, m Message) error {
	slog.InfoContext(ctx, "notification", "channel", "console", "recipient", m.Recipient, "jobId", m.JobID, "title", m.Title, "body", m.Body)
	return nil
}
