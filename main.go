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

	log.Printf("Service initialized on port %s. Bank is empty.", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
