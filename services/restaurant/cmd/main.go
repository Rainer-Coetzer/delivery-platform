// Command restaurant-service runs the restaurant module on its own, as a microservice (the
// compose `full` profile): its own database, Kafka, real HTTP to the other
// services. cmd/platform runs the same module alongside the others.
package main

import (
	"github.com/rainercoetzer/delivery-platform/pkg/app"
	"github.com/rainercoetzer/delivery-platform/services/restaurant"
)

func main() {
	app.Run(app.Options{Name: "restaurant-service", Definitions: []app.Definition{restaurant.Definition}, DefaultPort: "8082"})
}
