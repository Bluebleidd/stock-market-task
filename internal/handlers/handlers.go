package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bluebleidd/stock-market-task/internal/db"
	"github.com/Bluebleidd/stock-market-task/internal/market"
	"github.com/Bluebleidd/stock-market-task/internal/models"
)

type TradeRequest struct {
	Type string `json:"type"`
}

// POST /wallets/{wallet_id}/stocks/{stock_name}
func TradeHandler(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")
	stockName := r.PathValue("stock_name")

	var req TradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	switch req.Type {
	case "buy":
		err := market.BuyStock(walletID, stockName)
		if err != nil {
			if errors.Is(err, market.ErrStockNotFound) {
				http.Error(w, "Stock not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, market.ErrNotEnoughInBank) {
				http.Error(w, "Not enough stock in the bank", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	case "sell":
		err := market.SellStock(walletID, stockName)
		if err != nil {
			if errors.Is(err, market.ErrStockNotFound) {
				http.Error(w, "Stock not found", http.StatusNotFound)
				return
			}
			if errors.Is(err, market.ErrNotEnoughInWallet) {
				http.Error(w, "Not enough stock in wallet", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Invalid type, must be buy or sell", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// GET /wallets/{wallet_id}
func GetWalletHandler(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")

	rows, err := db.DB.Query("SELECT stock_name, quantity FROM wallet_stocks WHERE wallet_id = $1", walletID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var stocks []models.Stock
	for rows.Next() {
		var s models.Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			http.Error(w, "Error scanning data", http.StatusInternalServerError)
			return
		}
		stocks = append(stocks, s)
	}

	if stocks == nil {
		stocks = []models.Stock{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(models.Wallet{
		ID:     walletID,
		Stocks: stocks,
	})
}

// GET /wallets/{wallet_id}/stocks/{stock_name}
func GetWalletStockHandler(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")
	stockName := r.PathValue("stock_name")

	var quantity int
	err := db.DB.QueryRow("SELECT quantity FROM wallet_stocks WHERE wallet_id = $1 AND stock_name = $2",
		walletID, stockName).Scan(&quantity)

	if err == sql.ErrNoRows {
		fmt.Fprint(w, "0")
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "%d", quantity)
}

// GET /stocks
func GetStocksHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.DB.Query("SELECT name, quantity FROM bank_stocks")
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var stocks []models.Stock
	for rows.Next() {
		var s models.Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			http.Error(w, "Error scanning data", http.StatusInternalServerError)
			return
		}
		stocks = append(stocks, s)
	}

	if stocks == nil {
		stocks = []models.Stock{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stocks)
}

// POST /stocks
func SetStocksHandler(w http.ResponseWriter, r *http.Request) {
	var stocks []models.Stock

	if err := json.NewDecoder(r.Body).Decode(&stocks); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	err := market.SetBankState(stocks)
	if err != nil {
		http.Error(w, "Failed to set bank state", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
