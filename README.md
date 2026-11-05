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

## Test

```bash
make test               # unit + integration (needs Docker)
make test-unit          # unit only, no Docker
make test-integration   # integration only
make lint
```

## Not built on purpose

Authentication, rate limiting, payment gateway, POS app, frontend. See ARCHITECTURE.md §2.3 and §20.
