#!/usr/bin/env bash
set -euo pipefail

BROKER="kafka:29092"
TOPIC_MAIN="orders-placed"
TOPIC_DLQ="orders-placed-dlq"
TOPIC_RETRY="orders-retry"
TOPIC_RETRY_DLQ="orders-retry-dlq"
PARTITIONS=4
REPLICATION=1
DLQ_RETENTION_MS=$((14 * 24 * 60 * 60 * 1000))  # 14 days
RETRY_RETENTION_MS=$((24 * 60 * 60 * 1000))      # 24 hours

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

    echo 'Creating RETRY topic: ${TOPIC_RETRY}'
    kafka-topics \
      --bootstrap-server ${BROKER} \
      --create \
      --if-not-exists \
      --topic ${TOPIC_RETRY} \
      --partitions ${PARTITIONS} \
      --replication-factor ${REPLICATION} \
      --config cleanup.policy=delete \
      --config retention.ms=${RETRY_RETENTION_MS} || true

    echo 'Creating RETRY DLQ topic: ${TOPIC_RETRY_DLQ}'
    kafka-topics \
      --bootstrap-server ${BROKER} \
      --create \
      --if-not-exists \
      --topic ${TOPIC_RETRY_DLQ} \
      --partitions ${PARTITIONS} \
      --replication-factor ${REPLICATION} \
      --config cleanup.policy=delete \
      --config retention.ms=${DLQ_RETENTION_MS} || true

    echo 'Verify topics config:'
    kafka-topics --bootstrap-server ${BROKER} --describe --topic ${TOPIC_RETRY} || true
    kafka-topics --bootstrap-server ${BROKER} --describe --topic ${TOPIC_RETRY_DLQ} || true

    echo 'Listing topics (post):'
    kafka-topics --bootstrap-server ${BROKER} --list || true
  "

echo "Kafka initialization completed."