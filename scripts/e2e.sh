#!/bin/sh
# Drives one order from CREATED to DELIVERED through the public gateway API,
# the way infra/seed/seed.sh's demo data is meant to be used. Run after
# `docker compose up` reports every service healthy and after seed.sh.
#
# Usage: ./scripts/e2e.sh
set -e

GATEWAY="${GATEWAY_URL:-http://localhost:8080}"
command -v jq >/dev/null 2>&1 || { echo "this script needs jq"; exit 1; }

echo "== logging in as the seeded customer =="
TOKEN=$(curl -sf -X POST "$GATEWAY/auth/login" -H 'Content-Type: application/json' \
  -d '{"email":"nangula@example.com","password":"password123"}' | jq -r .token)
CUSTOMER_ID=$(curl -sf -X POST "$GATEWAY/auth/login" -H 'Content-Type: application/json' \
  -d '{"email":"nangula@example.com","password":"password123"}' | jq -r .user.id)
echo "customer id: $CUSTOMER_ID"

echo "== fetching a restaurant and its menu =="
RESTAURANT=$(curl -sf "$GATEWAY/restaurants" -H "Authorization: Bearer $TOKEN" | jq '.[0]')
RID=$(echo "$RESTAURANT" | jq -r .id)
RNAME=$(echo "$RESTAURANT" | jq -r .name)
RLAT=$(echo "$RESTAURANT" | jq -r .lat)
RLNG=$(echo "$RESTAURANT" | jq -r .lng)
MENU_ITEM=$(curl -sf "$GATEWAY/restaurants/$RID/menu-items" -H "Authorization: Bearer $TOKEN" | jq '.[0]')
MID=$(echo "$MENU_ITEM" | jq -r .id)
MNAME=$(echo "$MENU_ITEM" | jq -r .name)
MPRICE=$(echo "$MENU_ITEM" | jq -r .priceCents)
echo "restaurant: $RNAME ($RID), item: $MNAME"

echo "== placing the order =="
ORDER=$(curl -sf -X POST "$GATEWAY/orders/" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "
{
  \"customerId\": \"$CUSTOMER_ID\",
  \"customerName\": \"Nangula Amutenya\",
  \"restaurantId\": \"$RID\",
  \"restaurantName\": \"$RNAME\",
  \"items\": [{\"menuItemId\": \"$MID\", \"name\": \"$MNAME\", \"quantity\": 1, \"unitPriceCents\": $MPRICE}],
  \"paymentMethod\": \"cash\",
  \"pickup\": {\"lat\": $RLAT, \"lng\": $RLNG},
  \"dropoff\": {\"lat\": -22.5594, \"lng\": 17.0832}
}")
OID=$(echo "$ORDER" | jq -r .id)
echo "order id: $OID"

echo "== waiting for the order to reach DELIVERED (up to 90s) =="
for i in $(seq 1 30); do
  STATUS=$(curl -sf "$GATEWAY/orders/$OID" -H "Authorization: Bearer $TOKEN" | jq -r .status)
  echo "  [$i] status: $STATUS"
  if [ "$STATUS" = "DELIVERED" ]; then
    echo "SUCCESS: order $OID reached DELIVERED"
    exit 0
  fi
  if [ "$STATUS" = "CANCELLED" ]; then
    echo "order was cancelled (payment failed or restaurant rejected) — this is a valid outcome, cash payments always succeed though, so investigate if seen"
    exit 1
  fi
  sleep 3
done

echo "FAILED: order did not reach DELIVERED within the timeout"
exit 1
