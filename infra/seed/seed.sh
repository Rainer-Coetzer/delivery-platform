#!/bin/sh
# Seeds a handful of Windhoek restaurants, menu items and drivers straight
# through the public API, so the demo has something to show without
# touching any database directly. Run after `docker compose up` reports
# every service healthy: `./infra/seed/seed.sh`.
set -e

GATEWAY="${GATEWAY_URL:-http://localhost:8080}"

echo "== registering demo accounts =="
curl -sf -X POST "$GATEWAY/auth/register" -H 'Content-Type: application/json' \
  -d '{"name":"Nangula Amutenya","email":"nangula@example.com","password":"password123","role":"customer"}' >/dev/null
curl -sf -X POST "$GATEWAY/auth/register" -H 'Content-Type: application/json' \
  -d '{"name":"Admin","email":"admin@example.com","password":"password123","role":"admin"}' >/dev/null

echo "== creating restaurants (direct to restaurant-service, no auth needed for demo seeding) =="
NANDOS=$(curl -sf -X POST http://localhost:8082/restaurants -H 'Content-Type: application/json' \
  -d '{"name":"Nando'"'"'s Maerua","lat":-22.5608,"lng":17.0658,"opensAt":"09:00","closesAt":"22:00"}' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
echo "Nando's Maerua id: $NANDOS"

OHLTHAVER=$(curl -sf -X POST http://localhost:8082/restaurants -H 'Content-Type: application/json' \
  -d '{"name":"Xwama Cultural Village","lat":-22.5750,"lng":17.0850,"opensAt":"08:00","closesAt":"21:00"}' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
echo "Xwama id: $OHLTHAVER"

for rid in "$NANDOS" "$OHLTHAVER"; do
  curl -sf -X POST "http://localhost:8082/restaurants/$rid/menu-items" -H 'Content-Type: application/json' \
    -d '{"name":"Quarter Chicken & Chips","priceCents":8500,"stockQty":50,"available":true}' >/dev/null
  curl -sf -X POST "http://localhost:8082/restaurants/$rid/menu-items" -H 'Content-Type: application/json' \
    -d '{"name":"Kapana Platter","priceCents":6500,"stockQty":30,"available":true}' >/dev/null
done

echo "== registering drivers =="
for name in "Johannes Shikongo" "Maria Nakale" "Petrus Amupolo"; do
  DID=$(curl -sf -X POST http://localhost:8085/drivers -H 'Content-Type: application/json' \
    -d "{\"name\":\"$name\",\"vehicle\":\"bike\",\"lat\":-22.5700,\"lng\":17.0836}" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  curl -sf -X PATCH "http://localhost:8085/drivers/$DID/status" -H 'Content-Type: application/json' \
    -d '{"status":"AVAILABLE"}' >/dev/null
  echo "driver $name id: $DID (AVAILABLE)"
done

echo "seed complete"
