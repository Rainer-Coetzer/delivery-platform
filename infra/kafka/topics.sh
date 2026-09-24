#!/bin/sh
# Creates every topic this project uses, 3 partitions each, keyed by orderId
# (or driverId for delivery.location) — see docs/EVENTS.md. Run once against
# a broker that is already up; safe to re-run since --if-not-exists no-ops
# on topics that already exist. Also creates the .dlq topic for each one.
set -e

BROKER="${KAFKA_BROKER:-kafka:9092}"
PARTITIONS="${TOPIC_PARTITIONS:-3}"
REPLICATION="${TOPIC_REPLICATION:-1}"

TOPICS="
orders.created
orders.status-changed
orders.cancelled
restaurant.order-accepted
restaurant.order-rejected
restaurant.order-preparing
restaurant.order-ready
payments.completed
payments.failed
payments.refunded
delivery.assigned
delivery.picked-up
delivery.completed
delivery.location
users.registered
notifications.created
"

for topic in $TOPICS; do
  echo "creating topic: $topic"
  kafka-topics.sh --bootstrap-server "$BROKER" --create --if-not-exists \
    --topic "$topic" --partitions "$PARTITIONS" --replication-factor "$REPLICATION"
  kafka-topics.sh --bootstrap-server "$BROKER" --create --if-not-exists \
    --topic "${topic}.dlq" --partitions 1 --replication-factor "$REPLICATION"
done

echo "all topics created"
kafka-topics.sh --bootstrap-server "$BROKER" --list
