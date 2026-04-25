package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Error: Please provide a port number")
	}
	port := os.Args[1]

	fmt.Println("Service initialized: 0 wallets found, bank account is empty.")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the Stock Market Service!")
	})

	address := ":" + port
	fmt.Printf("Server is starting on http://localhost%s\n", address)

	err := http.ListenAndServe(address, nil)
	if err != nil {
		log.Fatal("Failed to start server: ", err)
	}
}
