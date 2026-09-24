// Command payment-service runs the payment module on its own, as a microservice (the
// compose `full` profile): its own database, Kafka, real HTTP to the other
// services. cmd/platform runs the same module alongside the others.
package main

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/services/payment"
)

func main() {
	app.Run(app.Options{Name: "payment-service", Definitions: []app.Definition{payment.Definition}, DefaultPort: "8084"})
}
