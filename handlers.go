package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
)

type TradeRequest struct {
	Type string `json:"type"`
}

func TradeHandler(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")
	stockName := r.PathValue("stock_name")

	var req TradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Rule: if the stock doesnt exist this should return 404
	var bankQty int
	err := DB.QueryRow("SELECT quantity FROM bank_stocks WHERE name = $1", stockName).Scan(&bankQty)
	if err == sql.ErrNoRows {
		http.Error(w, "Stock does not exist", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}

	switch req.Type {
	case "buy":
		// Rule: If there is no stock in the bank buy should return 400
		if bankQty <= 0 {
			http.Error(w, "No stock in the bank", http.StatusBadRequest)
			return
		}
		err = BuyStock(walletID, stockName)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case "sell":
		err = SellStock(walletID, stockName)
		if err != nil {
			// Rule: if there is no stock in the wallet sell should return 400
			if err.Error() == "stock not available in wallet" {
				http.Error(w, "No stock in wallet", http.StatusBadRequest)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "Invalid type, must be buy or sell", http.StatusBadRequest)
		return
	}

	// Rule: If the operation succeeds it should return 200
	w.WriteHeader(http.StatusOK)
}

// GET /wallets/{wallet_id}
func GetWalletHandler(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")

	rows, err := DB.Query("SELECT stock_name, quantity FROM wallet_stocks WHERE wallet_id = $1", walletID)
	if err != nil {
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var stocks []Stock
	for rows.Next() {
		var s Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			http.Error(w, "Error scanning data", http.StatusInternalServerError)
			return
		}
		stocks = append(stocks, s)
	}

	if stocks == nil {
		stocks = []Stock{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Wallet{
		ID:     walletID,
		Stocks: stocks,
	})
}

// GET /wallets/{wallet_id}/stocks/{stock_name}
func GetWalletStockHandler(w http.ResponseWriter, r *http.Request) {
	walletID := r.PathValue("wallet_id")
	stockName := r.PathValue("stock_name")

	var quantity int
	err := DB.QueryRow("SELECT quantity FROM wallet_stocks WHERE wallet_id = $1 AND stock_name = $2",
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
