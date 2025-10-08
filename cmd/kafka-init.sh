#!/usr/bin/env bash
set -euo pipefail

# This script initializes Kafka topics inside the 'kafka' container.
# It is safe to run multiple times (topic-exists errors are ignored).

# Notes:
# - When running inside the Kafka container, use broker 'kafka:29092' (internal listener).
# - 'localhost:9092' is for clients on the host machine (outside Docker).

BROKER="kafka:29092"
TOPIC_MAIN="orders-placed"
TOPIC_DLQ="orders-placed-dlq"
PARTITIONS=4
REPLICATION=1
DLQ_RETENTION_MS=$((14 * 24 * 60 * 60 * 1000))  # 14 days

echo "Creating topics in container against broker: ${BROKER}"

docker exec kafka \
  bash -lc "
    set -euo pipefail

    echo 'Listing topics (pre):'
    kafka-topics --bootstrap-server ${BROKER} --list || true

    echo 'Creating main topic: ${TOPIC_MAIN}'
    kafka-topics \
      --bootstrap-server ${BROKER} \
      --create \
      --if-not-exists \
      --topic ${TOPIC_MAIN} \
      --partitions ${PARTITIONS} \
      --replication-factor ${REPLICATION} || true

    echo 'Verify main topic:'
    kafka-topics --bootstrap-server ${BROKER} --describe --topic ${TOPIC_MAIN} || true

    echo 'Creating DLQ topic: ${TOPIC_DLQ}'
    kafka-topics \
      --bootstrap-server ${BROKER} \
      --create \
      --if-not-exists \
      --topic ${TOPIC_DLQ} \
      --partitions ${PARTITIONS} \
      --replication-factor ${REPLICATION} \
      --config cleanup.policy=delete \
      --config retention.ms=${DLQ_RETENTION_MS} || true

    echo 'Verify DLQ topic config:'
    kafka-configs \
      --bootstrap-server ${BROKER} \
      --entity-type topics \
      --entity-name ${TOPIC_DLQ} \
      --describe || true

    echo 'Listing topics (post):'
    kafka-topics --bootstrap-server ${BROKER} --list || true
  "

echo "Kafka initialization completed."