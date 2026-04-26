package main

import (
	"log"
	"net/http"
	"os"

	"github.com/Bluebleidd/stock-market-task/internal/db"
	"github.com/Bluebleidd/stock-market-task/internal/handlers"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Error: Please provide a port number")
	}
	port := os.Args[1]
	dbURL := os.Getenv("DB_URL")

	if dbURL == "" {
		dbURL = "postgres://user:password@localhost:5432/stock_market?sslmode=disable"
	}

	db.InitDB(dbURL)

	http.HandleFunc("POST /wallets/{wallet_id}/stocks/{stock_name}", handlers.TradeHandler)
	http.HandleFunc("GET /wallets/{wallet_id}", handlers.GetWalletHandler)
	http.HandleFunc("GET /wallets/{wallet_id}/stocks/{stock_name}", handlers.GetWalletStockHandler)

	http.HandleFunc("GET /stocks", handlers.GetStocksHandler)
	http.HandleFunc("POST /stocks", handlers.SetStocksHandler)

	log.Printf("Service initialized on port %s. Bank is empty.", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
