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
echo "the online order now (it lost its stock to the physical sale):"
curl -sS "$BASE_URL/v1/orders/$ORDER_ID"; echo
echo "the online customer tries to pay it anyway:"
post "/v1/orders/$ORDER_ID/pay" ''

step "5. Factory to consumer: print a batch, move it, and see leakage without opening a box"
EXPIRES=$(date -u -v+1y +%F 2>/dev/null || date -u -d '+1 year' +%F)
BATCH_CODE="B$(date +%s)"
echo "print a batch of 2 pallets at the factory:"
curl -sS -o /tmp/demo.batch -w '%{http_code}' -X POST "$BASE_URL/v1/production/batches" -H 'Content-Type: application/json' \
  -d "{\"sku_code\":\"MGN-PROT-500\",\"batch_code\":\"$BATCH_CODE\",\"location_code\":\"FACTORY-01\",\"expires_on\":\"$EXPIRES\",\"pallets\":2}"
echo " pallets: $(grep -oE '"code":"MG[0-9A-Z]{11}"' /tmp/demo.batch | wc -l | tr -d ' ')   $(grep -oE '"cartons":[0-9]+,"units":[0-9]+' /tmp/demo.batch)"
PALLET_A=$(grep -oE '"code":"MG[0-9A-Z]{11}"' /tmp/demo.batch | sed -n 1p | cut -d'"' -f4)
PALLET_B=$(grep -oE '"code":"MG[0-9A-Z]{11}"' /tmp/demo.batch | sed -n 2p | cut -d'"' -f4)
UNITS=($(grep -oE '"serial_code":"MG[0-9A-Z]{11}"' /tmp/demo.batch | cut -d'"' -f4))
echo "receive both pallets at the central warehouse:"
post /v1/shipments/receive "{\"container_code\":\"$PALLET_A\",\"location_code\":\"WH-JKT\"}"
post /v1/shipments/receive "{\"container_code\":\"$PALLET_B\",\"location_code\":\"WH-JKT\"}"
echo "ship pallet A to Distributor Medan, who receives it:"
post /v1/shipments/ship "{\"container_code\":\"$PALLET_A\",\"to_location_code\":\"DIST-MDN\"}"
post /v1/shipments/receive "{\"container_code\":\"$PALLET_A\",\"location_code\":\"DIST-MDN\"}"
echo "three units of that pallet are scanned by consumers in Jakarta, outside Medan's region:"
for i in 0 1 2; do post /v1/verify "{\"code\":\"${UNITS[$i]}\",\"device_id\":\"jkt-phone-$i\",$JAKARTA}" | cut -c1-60; done
echo "the leakage report names the distributor and the pallet:"
curl -sS "$BASE_URL/v1/reports/leakage"; echo
echo "open anomalies of that kind:"
curl -sS "$BASE_URL/v1/anomalies?type=OUT_OF_REGION" | cut -c1-300; echo
echo "stock everywhere (the pallet shipped to Medan is on hand there, the rest in the warehouse):"
curl -sS "$BASE_URL/v1/stock?sku=MGN-PROT-500"; echo

step "6. Online order to consumer, and loyalty points for a unit bought at a partner"
echo "order a unit from the warehouse, pay, and let the picker scan it (a unit of pallet B):"
post /v1/orders '{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-PROT-500","qty":1}]}' -H "Idempotency-Key: $(uuid)"
FULFILL_ID=$(sed -E 's/.*"id":([0-9]+).*/\1/' /tmp/demo.body)
post "/v1/orders/$FULFILL_ID/pay" ''
post "/v1/orders/$FULFILL_ID/fulfill" "{\"serial_codes\":[\"${UNITS[${#UNITS[@]}-1]}\"]}"
echo "a buyer at Distributor Medan opens the pack and claims points with the code under the seal:"
post /v1/points/claims '{"member_id":7,"serial_code":"MG00000000402","inner_code":"DEMO00000402"}'
echo "someone else tries the same unit, and someone guesses a code:"
post /v1/points/claims '{"member_id":8,"serial_code":"MG00000000402","inner_code":"DEMO00000402"}'
post /v1/points/claims '{"member_id":8,"serial_code":"MG00000000401","inner_code":"GUESS1234567"}'
echo "the member's points (they wait out a review window before they can be redeemed):"
curl -sS "$BASE_URL/v1/points/members/7"; echo
