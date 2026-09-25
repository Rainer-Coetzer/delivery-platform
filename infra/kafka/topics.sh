#!/bin/sh
# Creates every topic this project uses, 3 partitions each, keyed by jobId
# (or agentId for dispatch.location) — see pkg/events/events.go. Run once against
# a broker that is already up; safe to re-run since --if-not-exists no-ops
# on topics that already exist. Also creates the .dlq topic for each one.
set -e

BROKER="${KAFKA_BROKER:-kafka:9092}"
KAFKA_BIN="${KAFKA_BIN:-/opt/kafka/bin}"
PARTITIONS="${TOPIC_PARTITIONS:-3}"
REPLICATION="${TOPIC_REPLICATION:-1}"

TOPICS="
jobs.created
jobs.status-changed
jobs.cancelled
merchant.job-accepted
merchant.job-rejected
merchant.job-ready
payments.completed
payments.failed
payments.refunded
dispatch.assigned
dispatch.stop-completed
dispatch.location
"

for topic in $TOPICS; do
  echo "creating topic: $topic"
  "$KAFKA_BIN/kafka-topics.sh" --bootstrap-server "$BROKER" --create --if-not-exists \
    --topic "$topic" --partitions "$PARTITIONS" --replication-factor "$REPLICATION"
  "$KAFKA_BIN/kafka-topics.sh" --bootstrap-server "$BROKER" --create --if-not-exists \
    --topic "${topic}.dlq" --partitions 1 --replication-factor "$REPLICATION"
done

echo "all topics created"
"$KAFKA_BIN/kafka-topics.sh" --bootstrap-server "$BROKER" --list
