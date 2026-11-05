# Product Engineer Test — mGanik Consumer Goods

Prototype backend for serial hologram verification, omnichannel stock and orders, and offline POS sync.

| Document | Content |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | Architecture, data design, concurrency strategy, test plan |
| [PRODUCT_DEFENSE.md](PRODUCT_DEFENSE.md) | The 4 operational cases: solution, trade-offs, business metrics |

## Requirements

- Go 1.27
- Docker (PostgreSQL for `make up` and for integration tests)

## Run

```bash
make up      # build + start postgres and api, wait until /healthz answers
make seed    # demo data
make demo    # presentation scenarios
make down    # stop and delete the database
```

API: `http://localhost:8080`. Logs, including every SQL query (local only): `docker compose logs -f api`.

## Try it while the service runs

After `make up` and `make seed` the API is on `http://localhost:8080`. `make demo` runs the four presentation scenarios; the same calls by hand (seed codes are in `db/seed/seed.sql`):

```bash
# Verify a unit (first scan after the sale), then a made-up code
curl -s localhost:8080/v1/verify -d '{"code":"MG00000000003","device_id":"phone-1","lat":-6.2088,"lng":106.8456}'
curl -s localhost:8080/v1/verify -d '{"code":"MG99999999999","device_id":"phone-1"}'

# Create an order (same Idempotency-Key = same order), pay it, cancel it
curl -s localhost:8080/v1/orders -H 'Idempotency-Key: 11111111-1111-4111-8111-111111111111' \
  -d '{"channel":"D2C","location_code":"WH-JKT","items":[{"sku_code":"MGN-COLL-200","qty":2}]}'
curl -s -X POST localhost:8080/v1/orders/1/pay
curl -s -X POST localhost:8080/v1/orders/1/cancel

# POS: heartbeat, then sync an offline sale (send it twice: the second answer is DUPLICATE)
curl -s -X POST localhost:8080/v1/pos/heartbeat -d '{"store_code":"STORE-BDG-01","device_id":"pos-bdg-01-a"}'
curl -s localhost:8080/v1/pos/sync -d '{"store_code":"STORE-BDG-01","device_id":"pos-bdg-01-a","transactions":[{
  "id":"22222222-2222-4222-8222-222222222222","local_seq":1,"occurred_at":"2026-11-05T09:15:22+07:00",
  "payment_method":"CASH","total_amount":450000,
  "items":[{"serial_code":"MG00000000102","sku_code":"MGN-PROT-500","price":450000}]}]}'
```

Every request and every SQL query is logged: `docker compose logs -f api`. Run `make seed` again to reset the data.

Without Docker for the API itself: `cp config.example.yaml config.yaml`, start only the database with `docker compose up -d postgres`, then `make run`.

## Test

```bash
make test               # unit + integration (needs Docker)
make test-unit          # unit only, no Docker
make test-integration   # integration only
make lint
```

## Not built on purpose

Authentication, rate limiting, payment gateway, POS app, frontend. See ARCHITECTURE.md §2.3 and §20.
