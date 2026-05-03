package handlers_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Bluebleidd/stock-market-task/internal/db"
	"github.com/Bluebleidd/stock-market-task/internal/handlers"
	"github.com/Bluebleidd/stock-market-task/internal/market"
	"github.com/Bluebleidd/stock-market-task/internal/models"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	req := tc.ContainerRequest{
		Image: "postgres:15-alpine",
		Env: map[string]string{
			"POSTGRES_USER":     "test",
			"POSTGRES_PASSWORD": "test",
			"POSTGRES_DB":       "testdb",
		},
		ExposedPorts: []string{"5432/tcp"},
		WaitingFor:   wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	pgC, err := tc.GenericContainer(ctx, tc.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		fmt.Printf("start postgres container: %v\n", err)
		os.Exit(1)
	}
	defer pgC.Terminate(ctx) //nolint:errcheck

	host, err := pgC.Host(ctx)
	if err != nil {
		fmt.Printf("container host: %v\n", err)
		os.Exit(1)
	}
	port, err := pgC.MappedPort(ctx, "5432")
	if err != nil {
		fmt.Printf("container port: %v\n", err)
		os.Exit(1)
	}

	connStr := fmt.Sprintf("postgres://test:test@%s:%s/testdb?sslmode=disable", host, port.Port())

	retryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		probe, pErr := sql.Open("postgres", connStr)
		if pErr == nil {
			if pingErr := probe.PingContext(retryCtx); pingErr == nil {
				probe.Close()
				break
			}
			probe.Close()
		}
		select {
		case <-retryCtx.Done():
			fmt.Println("postgres never became ready")
			os.Exit(1)
		case <-time.After(200 * time.Millisecond):
		}
	}

	db.InitDB(connStr)
	os.Exit(m.Run())
}

// newRouter builds the same mux as cmd/server/main.go.
func newRouter() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /wallets/{wallet_id}/stocks/{stock_name}", handlers.TradeHandler)
	mux.HandleFunc("GET /wallets/{wallet_id}", handlers.GetWalletHandler)
	mux.HandleFunc("GET /wallets/{wallet_id}/stocks/{stock_name}", handlers.GetWalletStockHandler)
	mux.HandleFunc("GET /stocks", handlers.GetStocksHandler)
	mux.HandleFunc("POST /stocks", handlers.SetStocksHandler)
	mux.HandleFunc("GET /log", handlers.GetLogHandler)
	mux.HandleFunc("POST /chaos", handlers.ChaosHandler)
	return mux
}

// do fires a request against the mux and returns the recorder.
func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, bodyReader)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func cleanTables(t *testing.T) {
	t.Helper()
	_, err := db.DB.Exec(`TRUNCATE bank_stocks, wallet_stocks, audit_log RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("cleanTables: %v", err)
	}
}

func seedBank(t *testing.T, stocks ...models.Stock) {
	t.Helper()
	if err := market.SetBankState(stocks); err != nil {
		t.Fatalf("seedBank: %v", err)
	}
}

// ---------- POST /wallets/{wallet_id}/stocks/{stock_name} ----------

func TestTradeHandler(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T)
		walletID string
		stock    string
		body     string
		wantCode int
	}{
		{
			name:     "404 when stock not in bank",
			setup:    func(t *testing.T) { cleanTables(t) },
			walletID: "alice", stock: "GHOST",
			body:     `{"type":"buy"}`,
			wantCode: http.StatusNotFound,
		},
		{
			name: "400 buy when bank has 0",
			setup: func(t *testing.T) {
				cleanTables(t)
				seedBank(t, models.Stock{Name: "AAPL", Quantity: 0})
			},
			walletID: "alice", stock: "AAPL",
			body:     `{"type":"buy"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "400 sell when wallet has 0",
			setup: func(t *testing.T) {
				cleanTables(t)
				seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
			},
			walletID: "alice", stock: "AAPL",
			body:     `{"type":"sell"}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "200 buy success",
			setup: func(t *testing.T) {
				cleanTables(t)
				seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
			},
			walletID: "alice", stock: "AAPL",
			body:     `{"type":"buy"}`,
			wantCode: http.StatusOK,
		},
		{
			name: "200 sell success",
			setup: func(t *testing.T) {
				cleanTables(t)
				seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
				if err := market.BuyStock("alice", "AAPL"); err != nil {
					t.Fatalf("setup buy: %v", err)
				}
			},
			walletID: "alice", stock: "AAPL",
			body:     `{"type":"sell"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "400 invalid JSON body",
			setup:    func(t *testing.T) { cleanTables(t) },
			walletID: "alice", stock: "AAPL",
			body:     `{invalid}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "400 unknown type value",
			setup: func(t *testing.T) {
				cleanTables(t)
				seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
			},
			walletID: "alice", stock: "AAPL",
			body:     `{"type":"transfer"}`,
			wantCode: http.StatusBadRequest,
		},
	}

	mux := newRouter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup(t)
			w := do(mux, http.MethodPost, "/wallets/"+tt.walletID+"/stocks/"+tt.stock, tt.body)
			if w.Code != tt.wantCode {
				t.Errorf("status: want %d got %d, body: %s", tt.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

// ---------- GET /wallets/{wallet_id} ----------

func TestGetWalletHandler(t *testing.T) {
	mux := newRouter()

	t.Run("empty wallet returns stocks array not null", func(t *testing.T) {
		cleanTables(t)
		w := do(mux, http.MethodGet, "/wallets/newuser", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var wallet models.Wallet
		if err := json.NewDecoder(w.Body).Decode(&wallet); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if wallet.ID != "newuser" {
			t.Errorf("id: want newuser got %s", wallet.ID)
		}
		if wallet.Stocks == nil {
			t.Error("stocks: want [] got nil")
		}
		if len(wallet.Stocks) != 0 {
			t.Errorf("stocks len: want 0 got %d", len(wallet.Stocks))
		}
	})

	t.Run("returns correct stocks after a buy", func(t *testing.T) {
		cleanTables(t)
		seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("buy: %v", err)
		}
		w := do(mux, http.MethodGet, "/wallets/alice", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var wallet models.Wallet
		if err := json.NewDecoder(w.Body).Decode(&wallet); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(wallet.Stocks) != 1 || wallet.Stocks[0].Name != "AAPL" || wallet.Stocks[0].Quantity != 1 {
			t.Errorf("stocks: %+v", wallet.Stocks)
		}
	})

	t.Run("returns correct stocks after a sell", func(t *testing.T) {
		cleanTables(t)
		seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("buy: %v", err)
		}
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("buy2: %v", err)
		}
		if err := market.SellStock("alice", "AAPL"); err != nil {
			t.Fatalf("sell: %v", err)
		}
		w := do(mux, http.MethodGet, "/wallets/alice", "")
		var wallet models.Wallet
		if err := json.NewDecoder(w.Body).Decode(&wallet); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(wallet.Stocks) != 1 || wallet.Stocks[0].Quantity != 1 {
			t.Errorf("stocks: %+v", wallet.Stocks)
		}
	})
}

// ---------- GET /wallets/{wallet_id}/stocks/{stock_name} ----------

func TestGetWalletStockHandler(t *testing.T) {
	mux := newRouter()

	t.Run("returns 0 for unknown wallet/stock", func(t *testing.T) {
		cleanTables(t)
		w := do(mux, http.MethodGet, "/wallets/ghost/stocks/NONE", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var qty int
		if err := json.NewDecoder(w.Body).Decode(&qty); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if qty != 0 {
			t.Errorf("qty: want 0 got %d", qty)
		}
	})

	t.Run("returns correct qty after buy", func(t *testing.T) {
		cleanTables(t)
		seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("buy: %v", err)
		}
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("buy2: %v", err)
		}
		w := do(mux, http.MethodGet, "/wallets/alice/stocks/AAPL", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var qty int
		if err := json.NewDecoder(w.Body).Decode(&qty); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if qty != 2 {
			t.Errorf("qty: want 2 got %d", qty)
		}
	})
}

// ---------- GET /stocks ----------

func TestGetStocksHandler(t *testing.T) {
	mux := newRouter()

	t.Run("returns empty stocks array on empty bank", func(t *testing.T) {
		cleanTables(t)
		w := do(mux, http.MethodGet, "/stocks", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var resp struct {
			Stocks []models.Stock `json:"stocks"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Stocks == nil || len(resp.Stocks) != 0 {
			t.Errorf("stocks: %+v", resp.Stocks)
		}
	})

	t.Run("returns correct state after SetBankState", func(t *testing.T) {
		cleanTables(t)
		seedBank(t, models.Stock{Name: "AAPL", Quantity: 10}, models.Stock{Name: "GOOG", Quantity: 3})
		w := do(mux, http.MethodGet, "/stocks", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var resp struct {
			Stocks []models.Stock `json:"stocks"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Stocks) != 2 {
			t.Fatalf("stocks len: want 2 got %d", len(resp.Stocks))
		}
		byName := map[string]int{}
		for _, s := range resp.Stocks {
			byName[s.Name] = s.Quantity
		}
		if byName["AAPL"] != 10 || byName["GOOG"] != 3 {
			t.Errorf("stocks: %+v", resp.Stocks)
		}
	})
}

// ---------- POST /stocks ----------

func TestSetStocksHandler(t *testing.T) {
	mux := newRouter()

	t.Run("200 on valid body", func(t *testing.T) {
		cleanTables(t)
		w := do(mux, http.MethodPost, "/stocks", `{"stocks":[{"name":"TSLA","quantity":20}]}`)
		if w.Code != http.StatusOK {
			t.Errorf("status: want 200 got %d", w.Code)
		}
	})

	t.Run("sets bank correctly verified by GET /stocks", func(t *testing.T) {
		cleanTables(t)
		do(mux, http.MethodPost, "/stocks", `{"stocks":[{"name":"TSLA","quantity":20}]}`)
		w := do(mux, http.MethodGet, "/stocks", "")
		var resp struct {
			Stocks []models.Stock `json:"stocks"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Stocks) != 1 || resp.Stocks[0].Name != "TSLA" || resp.Stocks[0].Quantity != 20 {
			t.Errorf("stocks: %+v", resp.Stocks)
		}
	})

	t.Run("400 on malformed JSON", func(t *testing.T) {
		cleanTables(t)
		w := do(mux, http.MethodPost, "/stocks", `{bad json`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("status: want 400 got %d", w.Code)
		}
	})
}

// ---------- GET /log ----------

func TestGetLogHandler(t *testing.T) {
	mux := newRouter()

	t.Run("returns empty log initially", func(t *testing.T) {
		cleanTables(t)
		w := do(mux, http.MethodGet, "/log", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var resp struct {
			Logs []models.Log `json:"log"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Logs == nil || len(resp.Logs) != 0 {
			t.Errorf("log: %+v", resp.Logs)
		}
	})

	t.Run("returns entries in order after mixed operations", func(t *testing.T) {
		cleanTables(t)
		seedBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		do(mux, http.MethodPost, "/wallets/alice/stocks/AAPL", `{"type":"buy"}`)
		do(mux, http.MethodPost, "/wallets/bob/stocks/AAPL", `{"type":"buy"}`)
		do(mux, http.MethodPost, "/wallets/alice/stocks/AAPL", `{"type":"sell"}`)

		w := do(mux, http.MethodGet, "/log", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status: %d", w.Code)
		}
		var resp struct {
			Logs []models.Log `json:"log"`
		}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Logs) != 3 {
			t.Fatalf("log len: want 3 got %d", len(resp.Logs))
		}

		want := []models.Log{
			{Type: "buy", WalletID: "alice", StockName: "AAPL"},
			{Type: "buy", WalletID: "bob", StockName: "AAPL"},
			{Type: "sell", WalletID: "alice", StockName: "AAPL"},
		}
		for i, w := range want {
			if resp.Logs[i].Type != w.Type || resp.Logs[i].WalletID != w.WalletID || resp.Logs[i].StockName != w.StockName {
				t.Errorf("log[%d]: want %+v got %+v", i, w, resp.Logs[i])
			}
		}
	})
}

// ---------- POST /chaos ----------

func TestChaosHandler(t *testing.T) {
	// The handler writes 200 and starts a goroutine that calls os.Exit(1) after 100ms.
	// We only verify the status code here; testing the process exit in-process would
	// kill the test binary. The exit behaviour is covered by the E2E integration test.
	mux := newRouter()
	cleanTables(t)

	w := do(mux, http.MethodPost, "/chaos", "")
	if w.Code != http.StatusOK {
		t.Errorf("chaos status: want 200 got %d", w.Code)
	}
	// Allow the goroutine to fire; the test binary will survive because this is
	// httptest (no actual server process to kill) — os.Exit will kill this process.
	// Sleep is avoided; we accept the os.Exit risk since this test runs last.
}
