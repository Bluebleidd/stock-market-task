package db

import (
	"database/sql"
	"log"

	_ "github.com/lib/pq"
)

var DB *sql.DB

func InitDB(url string) {
	var err error
	DB, err = sql.Open("postgres", url)
	if err != nil {
		log.Fatal(err)
	}
	if err = DB.Ping(); err != nil {
		log.Fatalf("cannot connect to DB: %v", err)
	}

	queries := []string{
		`CREATE TABLE IF NOT EXISTS bank_stocks (
			name TEXT PRIMARY KEY,
			quantity INT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS wallet_stocks (
			wallet_id TEXT,
			stock_name TEXT,
			quantity INT NOT NULL,
			PRIMARY KEY (wallet_id, stock_name)
		);`,
		`CREATE TABLE IF NOT EXISTS audit_log (
			id SERIAL PRIMARY KEY,
			type TEXT,
			wallet_id TEXT,
			stock_name TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);`,
	}

	for _, q := range queries {
		if _, err := DB.Exec(q); err != nil {
			log.Fatalf("Table creation failed: %v", err)
		}
	}
}
