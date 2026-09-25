#!/bin/sh
# End-to-end check: starts the stack with the demo autopilot on, seeds it,
# places one order through the gateway and waits for DELIVERED.
#
#   ./scripts/e2e.sh                     # lite profile: build + start + seed + run
#   E2E_PROFILE=full ./scripts/e2e.sh    # the microservice shape instead
#   E2E_SKIP_UP=1 ./scripts/e2e.sh       # stack already running (with the dev override)
#   E2E_TIMEOUT=5m ./scripts/e2e.sh
set -e
cd "$(dirname "$0")/.."

PROFILE_NAME="${E2E_PROFILE:-lite}"
if [ -z "$E2E_SKIP_UP" ]; then
  echo "-> starting the $PROFILE_NAME profile with docker-compose.dev.yml (DEMO_AUTOPILOT=true)"
  docker compose --profile "$PROFILE_NAME" -f docker-compose.yml -f docker-compose.dev.yml up -d --build --wait
fi

go run ./tools/seed
exec go run ./tools/e2e
