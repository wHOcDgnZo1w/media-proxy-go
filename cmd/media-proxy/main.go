// Package main is the entry point for MediaProxy.
package main

import (
	"log"
	"os"

	"media-proxy-go/internal/app"
)

func main() {
	// Create and initialize application
	application, err := app.New()
	if err != nil {
		log.Fatalf("failed to initialize application: %v", err)
	}

	// Ensure cleanup on exit
	defer application.Shutdown()

	// Run the server
	if err := application.Run(); err != nil {
		log.Printf("server error: %v", err)
		os.Exit(1)
	}
}
