#!/bin/bash
set -euo pipefail

# Celestial Orrey Update Script
# This script rebuilds and redeploys the Docker container with zero downtime for the database

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_DIR"

echo "=== Celestial Orrey Update ==="
echo "Project directory: $PROJECT_DIR"
echo ""

# Check if docker-compose or docker compose is available
if command -v docker-compose &> /dev/null; then
    COMPOSE_CMD="docker-compose"
elif docker compose version &> /dev/null 2>&1; then
    COMPOSE_CMD="docker compose"
else
    echo "Error: docker-compose or docker compose not found"
    exit 1
fi

# Pull latest changes (optional, uncomment if using git)
# echo "Pulling latest changes..."
# git pull

# Ensure data directory exists with correct permissions
echo "Ensuring data directory exists..."
mkdir -p data
chmod 755 data

# Build new image
echo "Building new Docker image..."
$COMPOSE_CMD build --no-cache

# Stop existing container gracefully (allows db flush via SIGTERM)
echo "Stopping existing container (graceful shutdown)..."
$COMPOSE_CMD stop --timeout 35 || true

# Remove old container
echo "Removing old container..."
$COMPOSE_CMD rm -f || true

# Start new container
echo "Starting new container..."
$COMPOSE_CMD up -d

# Wait for container to be healthy
echo "Waiting for container to start..."
sleep 3

# Check container status
if $COMPOSE_CMD ps | grep -q "Up"; then
    echo ""
    echo "=== Update Complete ==="
    echo "Container is running."
    echo ""
    echo "View logs with: $COMPOSE_CMD logs -f"
else
    echo ""
    echo "=== Update Failed ==="
    echo "Container may not have started correctly."
    echo "Check logs with: $COMPOSE_CMD logs"
    exit 1
fi
