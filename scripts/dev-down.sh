#!/bin/bash
set -e

echo "Stopping Nexora development environment..."
docker-compose down -v
echo "All services and volumes removed."
