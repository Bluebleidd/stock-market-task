package main

import (
	"database/sql"
	"errors"
)

func SetBankState(stocks []Stock) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM bank_stocks")
	if err != nil {
		return err
	}

	for _, s := range stocks {
		_, err = tx.Exec("INSERT INTO bank_stocks (name, quantity) VALUES ($1, $2)", s.Name, s.Quantity)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func GetBankState() ([]Stock, error) {
	rows, err := DB.Query("SELECT name, quantity FROM bank_stocks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stocks []Stock
	for rows.Next() {
		var s Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			return nil, err
		}
		stocks = append(stocks, s)
	}

	if stocks == nil {
		stocks = []Stock{}
	}
	return stocks, nil
}

func BuyStock(walletID, stockName string) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var bankQty int
	err = tx.QueryRow("SELECT quantity FROM bank_stocks WHERE name = $1", stockName).Scan(&bankQty)
	if err == sql.ErrNoRows || bankQty <= 0 {
		return errors.New("stock not available in bank")
	} else if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE bank_stocks SET quantity = quantity - 1 WHERE name = $1", stockName)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO wallet_stocks (wallet_id, stock_name, quantity)
		VALUES ($1, $2, 1)
		ON CONFLICT (wallet_id, stock_name)
		DO UPDATE SET quantity = wallet_stocks.quantity + 1
	`, walletID, stockName)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`INSERT INTO audit_log (type, wallet_id, stock_name) VALUES ('buy', $1, $2)`, walletID, stockName)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func SellStock(walletID, stockName string) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var walletQty int
	err = tx.QueryRow("SELECT quantity FROM wallet_stocks WHERE wallet_id = $1 AND stock_name = $2", walletID, stockName).Scan(&walletQty)
	if err == sql.ErrNoRows || walletQty <= 0 {
		return errors.New("stock not available in wallet")
	} else if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE wallet_stocks SET quantity = quantity - 1 WHERE wallet_id = $1 AND stock_name = $2", walletID, stockName)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT INTO bank_stocks (name, quantity)
		VALUES ($1, 1)
		ON CONFLICT (name)
		DO UPDATE SET quantity = bank_stocks.quantity + 1
	`, stockName)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`INSERT INTO audit_log (type, wallet_id, stock_name) VALUES ('sell', $1, $2)`, walletID, stockName)
	if err != nil {
		return err
	}

	return tx.Commit()
}
