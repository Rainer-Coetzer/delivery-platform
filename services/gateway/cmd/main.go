// Command gateway runs the gateway module on its own, as a microservice (the
// compose `full` profile): its own database, Kafka, real HTTP to the other
// services. cmd/platform runs the same module alongside the others.
package main

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/services/gateway"
)

func main() {
	app.Run(app.Options{Name: "gateway", Definitions: []app.Definition{gateway.Definition}, DefaultPort: "8080"})
}
