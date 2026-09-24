#!/bin/sh
# End-to-end check: starts the stack with the demo autopilot on, seeds it,
# places one order through the gateway and waits for DELIVERED.
#
#   ./scripts/e2e.sh                 # build + start + seed + run
#   E2E_SKIP_UP=1 ./scripts/e2e.sh   # stack already running (with the dev override)
#   E2E_TIMEOUT=5m ./scripts/e2e.sh
set -e
cd "$(dirname "$0")/.."

if [ -z "$E2E_SKIP_UP" ]; then
  echo "-> starting stack with docker-compose.dev.yml (DEMO_AUTOPILOT=true)"
  docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build --wait
fi

go run ./tools/seed
exec go run ./tools/e2e
