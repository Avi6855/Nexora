#!/bin/bash
set -e

CQLSH_HOST="${CASSANDRA_HOSTS:-cassandra}"
CQLSH_PORT="${CQLSH_PORT:-9042}"
MAX_RETRIES=30
RETRY_INTERVAL=2

echo "Waiting for Cassandra at ${CQLSH_HOST}:${CQLSH_PORT}..."
for i in $(seq 1 $MAX_RETRIES); do
  if cqlsh "$CQLSH_HOST" "$CQLSH_PORT" -e "DESCRIBE KEYSPACES;" >/dev/null 2>&1; then
    echo "Cassandra is ready."
    break
  fi
  echo "Attempt $i/$MAX_RETRIES - Cassandra not ready yet, retrying in ${RETRY_INTERVAL}s..."
  sleep $RETRY_INTERVAL
done

if ! cqlsh "$CQLSH_HOST" "$CQLSH_PORT" -e "DESCRIBE KEYSPACES;" >/dev/null 2>&1; then
  echo "ERROR: Cassandra did not become ready within the timeout period."
  exit 1
fi

SCHEMA_FILE="/docker-entrypoint-initdb.d/schema.cql"
if [ -f "$SCHEMA_FILE" ]; then
  echo "Executing schema from ${SCHEMA_FILE}..."
  cqlsh "$CQLSH_HOST" "$CQLSH_PORT" -f "$SCHEMA_FILE"
  echo "Schema applied successfully."
else
  echo "No schema.cql found at ${SCHEMA_FILE}. Skipping schema initialization."
fi

echo "Cassandra initialization complete."
