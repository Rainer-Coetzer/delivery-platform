// Command delivery-service runs the delivery module on its own, as a microservice (the
// compose `full` profile): its own database, Kafka, real HTTP to the other
// services. cmd/platform runs the same module alongside the others.
package main

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/services/delivery"
)

func main() {
	app.Run(app.Options{Name: "delivery-service", Definitions: []app.Definition{delivery.Definition}, DefaultPort: "8085"})
}
