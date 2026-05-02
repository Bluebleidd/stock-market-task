package market

import (
	"database/sql"
	"errors"

	"github.com/Bluebleidd/stock-market-task/internal/db"
	"github.com/Bluebleidd/stock-market-task/internal/models"
)

var ErrStockNotFound = errors.New("stock not found")
var ErrNotEnoughInBank = errors.New("not enough stock available")
var ErrNotEnoughInWallet = errors.New("not enough stock in wallet")

func SetBankState(stocks []models.Stock) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`DELETE FROM bank_stocks`)
	if err != nil {
		return err
	}

	for _, s := range stocks {
		_, err = tx.Exec(`INSERT INTO bank_stocks (name, quantity) VALUES ($1, $2)`, s.Name, s.Quantity)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func GetBankState() ([]models.Stock, error) {
	query := `
		SELECT name, quantity
		FROM bank_stocks
	`
	rows, err := db.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stocks []models.Stock
	for rows.Next() {
		var s models.Stock
		if err := rows.Scan(&s.Name, &s.Quantity); err != nil {
			return nil, err
		}
		stocks = append(stocks, s)
	}

	if stocks == nil {
		stocks = []models.Stock{}
	}
	return stocks, nil
}

func BuyStock(walletID, stockName string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var bankQty int
	query := `
		SELECT quantity
		FROM bank_stocks
		WHERE name = $1
		FOR UPDATE
	`
	err = tx.QueryRow(query, stockName).Scan(&bankQty)
	if err == sql.ErrNoRows {
		return ErrStockNotFound
	} else if err != nil {
		return err
	}

	if bankQty <= 0 {
		return ErrNotEnoughInBank
	}

	query = `
		UPDATE bank_stocks
		SET quantity = quantity - 1
		WHERE name = $1
	`
	_, err = tx.Exec(query, stockName)
	if err != nil {
		return err
	}

	query = `
		INSERT INTO wallet_stocks (wallet_id, stock_name, quantity)
		VALUES ($1, $2, 1)
		ON CONFLICT (wallet_id, stock_name)
		DO UPDATE SET quantity = wallet_stocks.quantity + 1
	`
	_, err = tx.Exec(query, walletID, stockName)
	if err != nil {
		return err
	}

	query = `
		INSERT INTO audit_log (type, wallet_id, stock_name)
		VALUES ('buy', $1, $2)
	`
	_, err = tx.Exec(query, walletID, stockName)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func SellStock(walletID, stockName string) error {
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var walletQty int
	query := `
		SELECT quantity
		FROM wallet_stocks
		WHERE wallet_id = $1
			AND stock_name = $2
		FOR UPDATE
	`
	err = tx.QueryRow(query, walletID, stockName).Scan(&walletQty)
	if err == sql.ErrNoRows {
		return ErrStockNotFound
	} else if err != nil {
		return err
	}

	if walletQty <= 0 {
		return ErrNotEnoughInWallet
	}

	query = `
		UPDATE wallet_stocks
		SET quantity = quantity - 1
		WHERE wallet_id = $1
			AND stock_name = $2
	`
	_, err = tx.Exec(query, walletID, stockName)
	if err != nil {
		return err
	}

	query = `
		INSERT INTO bank_stocks (name, quantity)
		VALUES ($1, 1)
		ON CONFLICT (name)
		DO UPDATE SET quantity = bank_stocks.quantity + 1
	`
	_, err = tx.Exec(query, stockName)
	if err != nil {
		return err
	}

	query = `
		INSERT INTO audit_log (type, wallet_id, stock_name)
		VALUES ('sell', $1, $2)
	`
	_, err = tx.Exec(query, walletID, stockName)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func GetAuditLog() ([]models.Log, error) {
	query := `
		SELECT type, wallet_id, stock_name
		FROM audit_log
		ORDER BY created_at ASC
		LIMIT 10000
	`
	rows, err := db.DB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.Log
	for rows.Next() {
		var l models.Log
		if err := rows.Scan(&l.Type, &l.WalletID, &l.StockName); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}

	if logs == nil {
		logs = []models.Log{}
	}

	return logs, nil
}
