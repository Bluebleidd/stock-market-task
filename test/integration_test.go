// Package integration_test runs a full end-to-end scenario against a real
// PostgreSQL container. The HTTP layer uses httptest so no docker-compose is
// required.
package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Bluebleidd/stock-market-task/internal/db"
	"github.com/Bluebleidd/stock-market-task/internal/handlers"
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

// ---------- router helpers ----------

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

// req fires a request directly against a ServeMux using httptest.NewRecorder.
func req(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func mustStatus(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("%s: want status %d got %d, body: %s", t.Name(), want, w.Code, w.Body.String())
	}
}

// ---------- HA helpers ----------

// instanceWriter wraps http.ResponseWriter to inject X-Instance-ID before the
// first header flush.
type instanceWriter struct {
	http.ResponseWriter
	id       string
	injected bool
}

func (w *instanceWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *instanceWriter) WriteHeader(code int) {
	if !w.injected {
		w.ResponseWriter.Header().Set("X-Instance-ID", w.id)
		w.injected = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *instanceWriter) Write(b []byte) (int, error) {
	if !w.injected {
		w.ResponseWriter.Header().Set("X-Instance-ID", w.id)
		w.injected = true
	}
	return w.ResponseWriter.Write(b)
}

// newInstanceHandler wraps newRouter() with:
//   - X-Instance-ID header injection on every response
//   - a safe /chaos stub that returns 200 without calling os.Exit —
//     the test simulates the crash by calling httptest.Server.Close()
func newInstanceHandler(id string) http.Handler {
	router := newRouter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		iw := &instanceWriter{ResponseWriter: w, id: id}
		// Intercept /chaos: respond 200 and let the test close this server.
		if r.Method == http.MethodPost && r.URL.Path == "/chaos" {
			iw.WriteHeader(http.StatusOK)
			return
		}
		router.ServeHTTP(iw, r)
	})
}

// lbPool is a thread-safe round-robin pool of httptest.Servers.
type lbPool struct {
	mu      sync.Mutex
	servers []*httptest.Server
	idx     int
}

func (p *lbPool) pick() *httptest.Server {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.servers) == 0 {
		return nil
	}
	s := p.servers[p.idx%len(p.servers)]
	p.idx++
	return s
}

func (p *lbPool) remove(target *httptest.Server) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, s := range p.servers {
		if s == target {
			p.servers = append(p.servers[:i], p.servers[i+1:]...)
			p.idx = 0
			return
		}
	}
}

// newLBHandler creates an http.Handler that round-robins over pool.
// It manually forwards each request to the picked backend and copies
// the response (including all headers) back to the caller.
func newLBHandler(pool *lbPool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := pool.pick()
		if target == nil {
			http.Error(w, "no backends available", http.StatusServiceUnavailable)
			return
		}

		// Buffer the request body so it can be forwarded.
		var bodyBuf bytes.Buffer
		if r.Body != nil {
			if _, err := io.Copy(&bodyBuf, r.Body); err != nil {
				http.Error(w, "body read: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		fwdURL := target.URL + r.URL.RequestURI()
		fwdReq, err := http.NewRequestWithContext(r.Context(), r.Method, fwdURL, bytes.NewReader(bodyBuf.Bytes()))
		if err != nil {
			http.Error(w, "build request: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Forward application-level request headers.
		for k, v := range r.Header {
			fwdReq.Header[k] = v
		}

		resp, err := http.DefaultClient.Do(fwdReq)
		if err != nil {
			http.Error(w, "backend: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers, skipping hop-by-hop Transfer-Encoding to avoid
		// conflicts when net/http re-encodes the body for the client.
		for k, v := range resp.Header {
			if strings.EqualFold(k, "Transfer-Encoding") {
				continue
			}
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body) //nolint:errcheck
	})
}

// ---------- tests ----------

// TestEndToEnd exercises the documented happy-path scenario step by step.
func TestEndToEnd(t *testing.T) {
	_, err := db.DB.Exec(`TRUNCATE bank_stocks, wallet_stocks, audit_log RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}

	mux := newRouter()

	// Step 1: initialise bank with 5 AAPL.
	w := req(mux, http.MethodPost, "/stocks", `{"stocks":[{"name":"AAPL","quantity":5}]}`)
	mustStatus(t, w, http.StatusOK)

	// Step 2: alice buys AAPL × 3.
	for i := 0; i < 3; i++ {
		w = req(mux, http.MethodPost, "/wallets/alice/stocks/AAPL", `{"type":"buy"}`)
		mustStatus(t, w, http.StatusOK)
	}

	// Step 3: GET /wallets/alice → quantity 3.
	w = req(mux, http.MethodGet, "/wallets/alice", "")
	mustStatus(t, w, http.StatusOK)
	var wallet models.Wallet
	if err := json.NewDecoder(w.Body).Decode(&wallet); err != nil {
		t.Fatalf("decode wallet: %v", err)
	}
	if len(wallet.Stocks) != 1 || wallet.Stocks[0].Name != "AAPL" || wallet.Stocks[0].Quantity != 3 {
		t.Errorf("wallet after 3 buys: %+v", wallet.Stocks)
	}

	// Step 4: GET /wallets/alice/stocks/AAPL → 3.
	w = req(mux, http.MethodGet, "/wallets/alice/stocks/AAPL", "")
	mustStatus(t, w, http.StatusOK)
	var qty int
	if err := json.NewDecoder(w.Body).Decode(&qty); err != nil {
		t.Fatalf("decode qty: %v", err)
	}
	if qty != 3 {
		t.Errorf("wallet stock qty: want 3 got %d", qty)
	}

	// Step 5: GET /stocks → bank has 2 AAPL left.
	w = req(mux, http.MethodGet, "/stocks", "")
	mustStatus(t, w, http.StatusOK)
	var bankResp struct {
		Stocks []models.Stock `json:"stocks"`
	}
	if err := json.NewDecoder(w.Body).Decode(&bankResp); err != nil {
		t.Fatalf("decode bank: %v", err)
	}
	if len(bankResp.Stocks) != 1 || bankResp.Stocks[0].Quantity != 2 {
		t.Errorf("bank after 3 buys: %+v", bankResp.Stocks)
	}

	// Step 6: alice sells AAPL × 1.
	w = req(mux, http.MethodPost, "/wallets/alice/stocks/AAPL", `{"type":"sell"}`)
	mustStatus(t, w, http.StatusOK)

	// Step 7: GET /log → 4 entries (buy buy buy sell) in order.
	w = req(mux, http.MethodGet, "/log", "")
	mustStatus(t, w, http.StatusOK)
	var logResp struct {
		Logs []models.Log `json:"log"`
	}
	if err := json.NewDecoder(w.Body).Decode(&logResp); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	if len(logResp.Logs) != 4 {
		t.Fatalf("log: want 4 entries got %d", len(logResp.Logs))
	}
	wantLog := []models.Log{
		{Type: "buy", WalletID: "alice", StockName: "AAPL"},
		{Type: "buy", WalletID: "alice", StockName: "AAPL"},
		{Type: "buy", WalletID: "alice", StockName: "AAPL"},
		{Type: "sell", WalletID: "alice", StockName: "AAPL"},
	}
	for i, wl := range wantLog {
		got := logResp.Logs[i]
		if got.Type != wl.Type || got.WalletID != wl.WalletID || got.StockName != wl.StockName {
			t.Errorf("log[%d]: want %+v got %+v", i, wl, got)
		}
	}
}

// TestEndToEndErrorPaths verifies the key error cases in a sequential scenario.
func TestEndToEndErrorPaths(t *testing.T) {
	_, err := db.DB.Exec(`TRUNCATE bank_stocks, wallet_stocks, audit_log RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}

	mux := newRouter()

	cases := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode int
	}{
		{
			name:     "buy unknown stock → 404",
			method:   http.MethodPost, path: "/wallets/alice/stocks/GHOST",
			body: `{"type":"buy"}`, wantCode: http.StatusNotFound,
		},
		{
			name:     "POST /stocks initialises bank",
			method:   http.MethodPost, path: "/stocks",
			body: `{"stocks":[{"name":"AAPL","quantity":1}]}`, wantCode: http.StatusOK,
		},
		{
			name:     "buy drains bank",
			method:   http.MethodPost, path: "/wallets/alice/stocks/AAPL",
			body: `{"type":"buy"}`, wantCode: http.StatusOK,
		},
		{
			name:     "buy when bank empty → 400",
			method:   http.MethodPost, path: "/wallets/alice/stocks/AAPL",
			body: `{"type":"buy"}`, wantCode: http.StatusBadRequest,
		},
		{
			name:     "sell unknown stock → 404",
			method:   http.MethodPost, path: "/wallets/alice/stocks/GHOST",
			body: `{"type":"sell"}`, wantCode: http.StatusNotFound,
		},
		{
			name:     "sell when wallet empty → 400",
			method:   http.MethodPost, path: "/wallets/bob/stocks/AAPL",
			body: `{"type":"sell"}`, wantCode: http.StatusBadRequest,
		},
		{
			name:     "alice sells her AAPL → 200",
			method:   http.MethodPost, path: "/wallets/alice/stocks/AAPL",
			body: `{"type":"sell"}`, wantCode: http.StatusOK,
		},
		{
			name:     "GET /stocks → bank replenished",
			method:   http.MethodGet, path: "/stocks",
			body: "", wantCode: http.StatusOK,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := req(mux, c.method, c.path, c.body)
			if w.Code != c.wantCode {
				t.Errorf("status: want %d got %d, body: %s", c.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

// TestChaosHighAvailability spins up two real httptest.Server instances behind a
// round-robin load balancer, triggers /chaos on one of them, immediately shuts
// that instance down, and verifies that all subsequent requests through the LB
// still succeed (served by the surviving instance).
func TestChaosHighAvailability(t *testing.T) {
	_, err := db.DB.Exec(`TRUNCATE bank_stocks, wallet_stocks, audit_log RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("clean: %v", err)
	}

	// Two "app" instances backed by the same shared db.DB.
	inst1 := httptest.NewServer(newInstanceHandler("app1"))
	defer inst1.Close()
	inst2 := httptest.NewServer(newInstanceHandler("app2"))
	defer inst2.Close()

	pool := &lbPool{servers: []*httptest.Server{inst1, inst2}}
	lbSrv := httptest.NewServer(newLBHandler(pool))
	defer lbSrv.Close()

	client := lbSrv.Client() // plain HTTP client configured for this test server

	// call makes a real HTTP request through the load balancer.
	call := func(method, path, body string) *http.Response {
		t.Helper()
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		r, err := http.NewRequest(method, lbSrv.URL+path, reader)
		if err != nil {
			t.Fatalf("build request %s %s: %v", method, path, err)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}
	drain := func(resp *http.Response) {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}

	// Step 1 — seed the bank through the LB.
	resp := call(http.MethodPost, "/stocks", `{"stocks":[{"name":"TSLA","quantity":10}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed bank: want 200 got %d", resp.StatusCode)
	}
	drain(resp)

	// Step 2 — POST /chaos through the LB; record which instance responded.
	// The instance handler returns 200 immediately (without triggering os.Exit);
	// we simulate the crash by closing that server in step 3.
	resp = call(http.MethodPost, "/chaos", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("chaos: want 200 got %d", resp.StatusCode)
	}
	deadID := resp.Header.Get("X-Instance-ID")
	drain(resp)

	if deadID == "" {
		t.Fatal("X-Instance-ID header missing from /chaos response")
	}
	t.Logf("/chaos handled by %s — simulating crash", deadID)

	// Step 3 — remove the dead instance from the pool BEFORE closing it so no
	// new requests are routed to it, then close to free the port.
	var dead *httptest.Server
	if deadID == "app1" {
		dead = inst1
	} else {
		dead = inst2
	}
	pool.remove(dead)
	dead.Close()

	// Step 4 — send 10 consecutive GET /stocks through the LB; all must return
	// 200 with valid data served by the surviving instance.
	failures := 0
	for i := 0; i < 10; i++ {
		resp := call(http.MethodGet, "/stocks", "")
		if resp.StatusCode != http.StatusOK {
			t.Logf("GET /stocks #%d: got %d", i+1, resp.StatusCode)
			failures++
		}
		// Verify response body is parseable with correct TSLA quantity.
		var bankResp struct {
			Stocks []models.Stock `json:"stocks"`
		}
		if decErr := json.NewDecoder(resp.Body).Decode(&bankResp); decErr != nil {
			t.Logf("GET /stocks #%d: decode error: %v", i+1, decErr)
			failures++
		} else if len(bankResp.Stocks) != 1 || bankResp.Stocks[0].Name != "TSLA" || bankResp.Stocks[0].Quantity != 10 {
			t.Logf("GET /stocks #%d: unexpected body: %+v", i+1, bankResp.Stocks)
			failures++
		}
		resp.Body.Close()
	}

	if failures > 0 {
		t.Errorf("HA: %d/10 GET /stocks requests failed after chaos on %s", failures, deadID)
	}
}
