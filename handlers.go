package main

import (
	"database/sql"
	"encoding/json"
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

	if req.Type == "buy" {
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
	} else if req.Type == "sell" {
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
	} else {
		http.Error(w, "Invalid type, must be buy or sell", http.StatusBadRequest)
		return
	}

	// Rule: If the operation succeeds it should return 200
	w.WriteHeader(http.StatusOK)
}
