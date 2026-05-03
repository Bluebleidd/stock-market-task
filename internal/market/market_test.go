package market_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Bluebleidd/stock-market-task/internal/db"
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

	// Retry until postgres is actually accepting queries (port open != ready for queries).
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

// cleanTables truncates all three tables and resets the audit_log sequence.
func cleanTables(t *testing.T) {
	t.Helper()
	_, err := db.DB.Exec(`TRUNCATE bank_stocks, wallet_stocks, audit_log RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("cleanTables: %v", err)
	}
}

func bankQty(t *testing.T, name string) int {
	t.Helper()
	var qty int
	err := db.DB.QueryRow(`SELECT quantity FROM bank_stocks WHERE name = $1`, name).Scan(&qty)
	if errors.Is(err, sql.ErrNoRows) {
		return -1
	}
	if err != nil {
		t.Fatalf("bankQty: %v", err)
	}
	return qty
}

func walletQty(t *testing.T, walletID, stockName string) int {
	t.Helper()
	var qty int
	err := db.DB.QueryRow(`SELECT quantity FROM wallet_stocks WHERE wallet_id = $1 AND stock_name = $2`, walletID, stockName).Scan(&qty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("walletQty: %v", err)
	}
	return qty
}

// ---------- SetBankState ----------

func TestSetBankState(t *testing.T) {
	tests := []struct {
		name    string
		seed    []models.Stock
		input   []models.Stock
		wantLen int
		check   func(t *testing.T, got []models.Stock)
	}{
		{
			name:    "sets stocks correctly",
			input:   []models.Stock{{Name: "AAPL", Quantity: 10}, {Name: "GOOG", Quantity: 5}},
			wantLen: 2,
			check: func(t *testing.T, got []models.Stock) {
				byName := map[string]int{}
				for _, s := range got {
					byName[s.Name] = s.Quantity
				}
				if byName["AAPL"] != 10 {
					t.Errorf("AAPL qty: want 10 got %d", byName["AAPL"])
				}
				if byName["GOOG"] != 5 {
					t.Errorf("GOOG qty: want 5 got %d", byName["GOOG"])
				}
			},
		},
		{
			name:    "overwrites previous state entirely",
			seed:    []models.Stock{{Name: "OLD", Quantity: 99}},
			input:   []models.Stock{{Name: "NEW", Quantity: 3}},
			wantLen: 1,
			check: func(t *testing.T, got []models.Stock) {
				if got[0].Name != "NEW" || got[0].Quantity != 3 {
					t.Errorf("expected {NEW 3} got %+v", got[0])
				}
			},
		},
		{
			name:    "empty slice clears the bank",
			seed:    []models.Stock{{Name: "MSFT", Quantity: 7}},
			input:   []models.Stock{},
			wantLen: 0,
			check:   func(_ *testing.T, _ []models.Stock) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleanTables(t)
			if len(tt.seed) > 0 {
				if err := market.SetBankState(tt.seed); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			if err := market.SetBankState(tt.input); err != nil {
				t.Fatalf("SetBankState: %v", err)
			}
			got, err := market.GetBankState()
			if err != nil {
				t.Fatalf("GetBankState: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len: want %d got %d (%+v)", tt.wantLen, len(got), got)
			}
			tt.check(t, got)
		})
	}
}

// ---------- BuyStock ----------

func TestBuyStock(t *testing.T) {
	t.Run("returns ErrStockNotFound when stock not in bank", func(t *testing.T) {
		cleanTables(t)
		err := market.BuyStock("alice", "UNKNOWN")
		if !errors.Is(err, market.ErrStockNotFound) {
			t.Fatalf("want ErrStockNotFound got %v", err)
		}
	})

	t.Run("returns ErrNotEnoughInBank when bank qty is 0", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 0})
		err := market.BuyStock("alice", "AAPL")
		if !errors.Is(err, market.ErrNotEnoughInBank) {
			t.Fatalf("want ErrNotEnoughInBank got %v", err)
		}
	})

	t.Run("decrements bank quantity by 1", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("BuyStock: %v", err)
		}
		if got := bankQty(t, "AAPL"); got != 4 {
			t.Errorf("bank qty: want 4 got %d", got)
		}
	})

	t.Run("increments wallet quantity (new wallet)", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("newwallet", "AAPL"); err != nil {
			t.Fatalf("BuyStock: %v", err)
		}
		if got := walletQty(t, "newwallet", "AAPL"); got != 1 {
			t.Errorf("wallet qty: want 1 got %d", got)
		}
	})

	t.Run("increments wallet quantity (existing wallet)", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("first buy: %v", err)
		}
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("second buy: %v", err)
		}
		if got := walletQty(t, "alice", "AAPL"); got != 2 {
			t.Errorf("wallet qty: want 2 got %d", got)
		}
	})

	t.Run("writes buy entry to audit_log", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		if err := market.BuyStock("alice", "AAPL"); err != nil {
			t.Fatalf("BuyStock: %v", err)
		}
		logs, err := market.GetAuditLog()
		if err != nil {
			t.Fatalf("GetAuditLog: %v", err)
		}
		if len(logs) != 1 {
			t.Fatalf("log count: want 1 got %d", len(logs))
		}
		if logs[0].Type != "buy" || logs[0].WalletID != "alice" || logs[0].StockName != "AAPL" {
			t.Errorf("log entry: %+v", logs[0])
		}
	})

	t.Run("atomic concurrent buys never oversell", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "RACE", Quantity: 1})

		const workers = 50
		errs := make([]error, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := range workers {
			go func(i int) {
				defer wg.Done()
				errs[i] = market.BuyStock("racer", "RACE")
			}(i)
		}
		wg.Wait()

		successes := 0
		for _, err := range errs {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Errorf("concurrent buys: want exactly 1 success got %d", successes)
		}
		if got := bankQty(t, "RACE"); got != 0 {
			t.Errorf("bank qty after concurrent buys: want 0 got %d", got)
		}
	})
}

// ---------- SellStock ----------

func TestSellStock(t *testing.T) {
	t.Run("returns ErrStockNotFound when stock unknown to system", func(t *testing.T) {
		cleanTables(t)
		err := market.SellStock("alice", "GHOST")
		if !errors.Is(err, market.ErrStockNotFound) {
			t.Fatalf("want ErrStockNotFound got %v", err)
		}
	})

	t.Run("returns ErrNotEnoughInWallet when wallet has 0 of stock", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		err := market.SellStock("alice", "AAPL")
		if !errors.Is(err, market.ErrNotEnoughInWallet) {
			t.Fatalf("want ErrNotEnoughInWallet got %v", err)
		}
	})

	t.Run("decrements wallet quantity by 1", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		mustBuy(t, "alice", "AAPL")
		mustBuy(t, "alice", "AAPL")
		if err := market.SellStock("alice", "AAPL"); err != nil {
			t.Fatalf("SellStock: %v", err)
		}
		if got := walletQty(t, "alice", "AAPL"); got != 1 {
			t.Errorf("wallet qty: want 1 got %d", got)
		}
	})

	t.Run("increments bank quantity by 1", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		mustBuy(t, "alice", "AAPL")
		if err := market.SellStock("alice", "AAPL"); err != nil {
			t.Fatalf("SellStock: %v", err)
		}
		if got := bankQty(t, "AAPL"); got != 5 {
			t.Errorf("bank qty: want 5 got %d", got)
		}
	})

	t.Run("writes sell entry to audit_log", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		mustBuy(t, "alice", "AAPL")
		if err := market.SellStock("alice", "AAPL"); err != nil {
			t.Fatalf("SellStock: %v", err)
		}
		logs, err := market.GetAuditLog()
		if err != nil {
			t.Fatalf("GetAuditLog: %v", err)
		}
		if len(logs) != 2 {
			t.Fatalf("log count: want 2 got %d", len(logs))
		}
		if logs[1].Type != "sell" || logs[1].WalletID != "alice" || logs[1].StockName != "AAPL" {
			t.Errorf("sell log entry: %+v", logs[1])
		}
	})

	t.Run("atomic concurrent sells never go below 0", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "RACE", Quantity: 1})
		mustBuy(t, "seller", "RACE")

		const workers = 50
		errs := make([]error, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := range workers {
			go func(i int) {
				defer wg.Done()
				errs[i] = market.SellStock("seller", "RACE")
			}(i)
		}
		wg.Wait()

		successes := 0
		for _, err := range errs {
			if err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Errorf("concurrent sells: want exactly 1 success got %d", successes)
		}
		if got := walletQty(t, "seller", "RACE"); got != 0 {
			t.Errorf("wallet qty after concurrent sells: want 0 got %d", got)
		}
	})
}

// ---------- GetAuditLog ----------

func TestGetAuditLog(t *testing.T) {
	t.Run("returns empty slice not nil when no operations", func(t *testing.T) {
		cleanTables(t)
		logs, err := market.GetAuditLog()
		if err != nil {
			t.Fatalf("GetAuditLog: %v", err)
		}
		if logs == nil {
			t.Fatal("want non-nil empty slice got nil")
		}
		if len(logs) != 0 {
			t.Fatalf("want 0 entries got %d", len(logs))
		}
	})

	t.Run("returns entries in insertion order", func(t *testing.T) {
		cleanTables(t)
		mustSetBank(t, models.Stock{Name: "AAPL", Quantity: 5})
		mustBuy(t, "alice", "AAPL")
		mustBuy(t, "bob", "AAPL")
		if err := market.SellStock("alice", "AAPL"); err != nil {
			t.Fatalf("SellStock: %v", err)
		}

		logs, err := market.GetAuditLog()
		if err != nil {
			t.Fatalf("GetAuditLog: %v", err)
		}
		if len(logs) != 3 {
			t.Fatalf("want 3 entries got %d", len(logs))
		}

		want := []models.Log{
			{Type: "buy", WalletID: "alice", StockName: "AAPL"},
			{Type: "buy", WalletID: "bob", StockName: "AAPL"},
			{Type: "sell", WalletID: "alice", StockName: "AAPL"},
		}
		for i, w := range want {
			if logs[i].Type != w.Type || logs[i].WalletID != w.WalletID || logs[i].StockName != w.StockName {
				t.Errorf("log[%d]: want %+v got %+v", i, w, logs[i])
			}
		}
	})

	t.Run("returns at most 10000 entries", func(t *testing.T) {
		cleanTables(t)
		// Insert 10001 rows directly to verify the LIMIT without running 10001 transactions.
		stmt, err := db.DB.Prepare(`INSERT INTO audit_log (type, wallet_id, stock_name) VALUES ('buy', 'u', 'S')`)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		defer stmt.Close()
		for i := 0; i < 10001; i++ {
			if _, err := stmt.Exec(); err != nil {
				t.Fatalf("insert row %d: %v", i, err)
			}
		}
		logs, err := market.GetAuditLog()
		if err != nil {
			t.Fatalf("GetAuditLog: %v", err)
		}
		if len(logs) != 10000 {
			t.Errorf("want 10000 got %d", len(logs))
		}
	})
}

// ---------- helpers ----------

func mustSetBank(t *testing.T, stocks ...models.Stock) {
	t.Helper()
	if err := market.SetBankState(stocks); err != nil {
		t.Fatalf("mustSetBank: %v", err)
	}
}

func mustBuy(t *testing.T, walletID, stockName string) {
	t.Helper()
	if err := market.BuyStock(walletID, stockName); err != nil {
		t.Fatalf("mustBuy(%s, %s): %v", walletID, stockName, err)
	}
}
