# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Running the Application

The app requires a port number argument and a PostgreSQL database. Use Docker Compose for the full stack:

```bash
# Windows
start.bat <PORT>

# Linux/Mac
./start.sh <PORT>
```

This sets `APP_PORT` and runs `docker compose up --build`. Two app instances are defined: `app1` (configurable port) and `app2` (fixed port 8081).

To build the Go binary directly:
```bash
go build -o stock-market .
```

The app reads `DB_URL` from environment (defaults to `postgres://postgres:postgres@localhost:5432/stockmarket?sslmode=disable`) and requires a port argument: `./stock-market <PORT>`.

## Architecture

All Go source is in the root package (`main`). No subdirectories or packages.

**Request flow**: HTTP request → `handlers.go` → `market.go` (business logic) → PostgreSQL

**Key files:**
- `main.go` — registers routes and starts the server
- `handlers.go` — HTTP handlers; parse request, call market/db logic, write response
- `market.go` — buy/sell logic with PostgreSQL transactions; bank state management
- `database.go` — DB connection, table initialization (`bank_stocks`, `wallet_stocks`, `audit_log`)
- `models.go` — `Stock`, `Wallet`, `Log` structs (JSON + db tags)

## API Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/wallets/{wallet_id}/stocks/{stock_name}` | Buy or sell stock (body: `{"quantity": N}`, positive = buy, negative = sell) |
| GET | `/wallets/{wallet_id}` | Get all holdings for a wallet |
| GET | `/wallets/{wallet_id}/stocks/{stock_name}` | Get quantity of one stock in wallet |
| GET | `/stocks` | List bank inventory |
| POST | `/stocks` | Initialize bank stock inventory |

## Database Schema

Three tables created on startup via `database.go`:
- `bank_stocks(name TEXT PRIMARY KEY, quantity INT)` — bank's inventory
- `wallet_stocks(wallet_id TEXT, stock_name TEXT, quantity INT, PRIMARY KEY(wallet_id, stock_name))` — user holdings
- `audit_log(id SERIAL, type TEXT, wallet_id TEXT, stock_name TEXT, timestamp TIMESTAMPTZ)` — transaction log

Buy/sell operations use PostgreSQL transactions for atomicity.

## Error Conventions

Handlers return:
- `400` — invalid input or insufficient inventory
- `404` — stock or wallet not found
- `500` — database errors
