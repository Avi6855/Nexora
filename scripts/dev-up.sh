#!/bin/bash
set -e

echo "Starting Nexora development environment..."

echo "Bringing up infrastructure services..."
docker-compose up -d cassandra kafka zookeeper kafka-ui

echo "Waiting for Cassandra to become healthy..."
for i in $(seq 1 30); do
  if docker exec nexora-cassandra cqlsh -e "DESCRIBE KEYSPACES;" >/dev/null 2>&1; then
    echo "Cassandra is ready."
    break
  fi
  echo "  Waiting for Cassandra... ($i/30)"
  sleep 5
done

echo "Waiting for Kafka to become healthy..."
for i in $(seq 1 20); do
  if docker exec nexora-kafka kafka-topics --bootstrap-server localhost:9092 --list >/dev/null 2>&1; then
    echo "Kafka is ready."
    break
  fi
  echo "  Waiting for Kafka... ($i/20)"
  sleep 5
done

echo "Infrastructure is ready. Starting all services..."
docker-compose up -d

echo ""
echo "============================================="
echo "  Nexora Development Environment Started!"
echo "============================================="
echo "  API Gateway:  http://localhost:8000"
echo "  Kafka UI:     http://localhost:8080"
echo "  Envoy Admin:  http://localhost:9901"
echo ""
echo "  Services:"
echo "    Identity:     http://localhost:8081"
echo "    User:         http://localhost:8082"
echo "    Account:      http://localhost:8083"
echo "    Ledger:       http://localhost:8084"
echo "    Payment:      http://localhost:8085"
echo "    Transfer:     http://localhost:8086"
echo "    Card:         http://localhost:8087"
echo "    Pot:          http://localhost:8088"
echo "    Fraud:        http://localhost:8089"
echo "    Notification: http://localhost:8090"
echo "    Reconcil.:    http://localhost:8091"
echo "    Replay:       http://localhost:8092"
echo "    Simulation:   http://localhost:8093"
echo "    Policy:       http://localhost:8094"
echo "    Audit:        http://localhost:8095"
echo "    Control:      http://localhost:8096"
echo "    Incident:     http://localhost:8097"
echo "============================================="
