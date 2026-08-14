package main

import (
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	address := os.Getenv("UPSTREAM_ADDR")
	if address == "" {
		address = ":8080"
	}

	server := &http.Server{
		Addr:              address,
		Handler:           newRouter(newDocumentStore()),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("upstream document service listening on %s (no authorization code inside)", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("upstream document service: %v", err)
	}
}
