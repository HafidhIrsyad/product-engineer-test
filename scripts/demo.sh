#!/usr/bin/env bash
# Presentation scenarios (ARCHITECTURE.md §18.7). Run `make up` and `make seed` first;
# `make seed` also resets the data, so the demo can be run again. Needs only bash and curl.
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
STORE=STORE-BDG-01
JAKARTA='"lat":-6.2088,"lng":106.8456'
MEDAN='"lat":3.5952,"lng":98.6722'

step() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }
post() { # post <path> <json> [extra curl args] → prints "<status> <body>"
  local path=$1 body=$2; shift 2
  curl -sS -o /tmp/demo.body -w '%{http_code}' -X POST "$BASE_URL$path" -H 'Content-Type: application/json' "$@" -d "$body"
  printf ' %s\n' "$(cat /tmp/demo.body)"
}
uuid() { uuidgen | tr 'A-Z' 'a-z'; }
sale() { # sale <txn-id> <local_seq> <serial> <sku> → one POS transaction
  printf '{"id":"%s","local_seq":%s,"occurred_at":"%s","payment_method":"CASH","total_amount":450000,"items":[{"serial_code":"%s","sku_code":"%s","price":450000}]}' \
    "$1" "$2" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$3" "$4"
}
sync() { printf '{"store_code":"%s","device_id":"pos-bdg-01-a","transactions":[%s]}' "$STORE" "$1"; }

step "1. Verification"
echo "real code, first scan after the sale:"; post /v1/verify "{\"code\":\"MG00000000001\",\"device_id\":\"phone-1\",$JAKARTA}"
echo "made-up code:";                         post /v1/verify '{"code":"MG99999999999","device_id":"phone-1"}'
echo "a clone: the same code seconds later in Medan:"
post /v1/verify "{\"code\":\"MG00000000002\",\"device_id\":\"phone-1\",$JAKARTA}" > /dev/null
post /v1/verify "{\"code\":\"MG00000000002\",\"device_id\":\"phone-2\",$MEDAN}"
echo "a unit allocated to Medan, scanned in Jakarta (gray market signal):"
post /v1/verify "{\"code\":\"MG00000000401\",\"device_id\":\"phone-3\",$JAKARTA}"

step "2. Order surge: 50 parallel orders for 10 units"
ORDER='{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-COLL-200","qty":1}]}'
rm -f /tmp/demo.surge
for _ in $(seq 50); do
  curl -sS -o /dev/null -w '%{http_code}\n' -X POST "$BASE_URL/v1/orders" -H 'Content-Type: application/json' \
    -H "Idempotency-Key: $(uuid)" -d "$ORDER" >> /tmp/demo.surge &
done
wait
echo "created (201): $(grep -c '^201$' /tmp/demo.surge)   sold out (409): $(grep -c '^409$' /tmp/demo.surge)   other: $(grep -vc -E '^(201|409)$' /tmp/demo.surge || true)"

step "3. Offline POS: the same batch sent twice"
TXN=$(uuid); BATCH=$(sync "$(sale "$TXN" 1001 MG00000000101 MGN-PROT-500)")
echo "first send:";  post /v1/pos/sync "$BATCH"
echo "second send:"; post /v1/pos/sync "$BATCH"

step "4. Split-brain: the last unit is sold at the cashier while an online order holds it"
echo "heartbeat:"; post /v1/pos/heartbeat "{\"store_code\":\"$STORE\",\"device_id\":\"pos-bdg-01-a\"}"
echo "online order for the last unit:"
post /v1/orders '{"channel":"D2C","location_code":"'$STORE'","items":[{"sku_code":"MGN-OMEGA-100","qty":1}]}' -H "Idempotency-Key: $(uuid)"
ORDER_ID=$(sed -E 's/.*"id":([0-9]+).*/\1/' /tmp/demo.body)
echo "cashier sale synced afterwards (the unit had already been sold in the shop):"
post /v1/pos/sync "$(sync "$(sale "$(uuid)" 1002 MG00000000301 MGN-OMEGA-100)")"
echo "the online customer tries to pay: the order lost its stock to the physical sale:"
post "/v1/orders/$ORDER_ID/pay" ''
