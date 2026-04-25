package main

import "time"

type Stock struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

type Wallet struct {
	ID     string  `json:"id"`
	Stocks []Stock `json:"stocks"`
}

type Log struct {
	Type      string    `json:"type"`
	WalletID  string    `json:"wallet_id"`
	StockName string    `json:"stock_name"`
	CreatedAt time.Time `json:"-"`
}
