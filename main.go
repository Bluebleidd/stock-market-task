package main

import (
	"log"
	"net/http"
	"os"
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

	InitDB(dbURL)

	http.HandleFunc("POST /wallets/{wallet_id}/stocks/{stock_name}", TradeHandler)
	http.HandleFunc("GET /wallets/{wallet_id}", GetWalletHandler)
	http.HandleFunc("GET /wallets/{wallet_id}/stocks/{stock_name}", GetWalletStockHandler)

	http.HandleFunc("GET /stocks", GetStocksHandler)
	http.HandleFunc("POST /stocks", SetStocksHandler)

	log.Printf("Service initialized on port %s. Bank is empty.", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
