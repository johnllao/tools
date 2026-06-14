// Command httpreverseproxy implements an HTTP reverse proxy that forwards
// incoming requests to upstream services based on routing rules defined in a
// JSON configuration file.
//
// Routing decisions are made by matching the x-service-name and x-environment
// request headers against a list of ordered rules. The first matching rule
// determines the upstream server. Wildcard ("*") entries match any value.
//
// Usage:
//
//	httpreverseproxy -config /path/to/config.json
//
// The proxy starts immediately on launch, listens on the address configured in
// the JSON file, and runs until the process receives SIGINT (Ctrl+C) or
// SIGTERM, at which point it gracefully shuts down with a 5-second timeout.
//
// Example proxy-config.json:
//
//	{
//	  "listen": ":9090",
//	  "routes": [
//	    {
//	      "service": "api",
//	      "environment": "production",
//	      "upstream": "http://api-prod.internal:8080"
//	    },
//	    {
//	      "service": "*",
//	      "environment": "*",
//	      "upstream": "http://default.internal:8080"
//	    }
//	  ]
//	}
//
// Examples:
//
//	httpreverseproxy -config proxy-config.json
//	curl -H "x-service-name: api" -H "x-environment: production" http://localhost:9090/users
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to the JSON configuration file")
	flag.Parse()

	// Load and validate the configuration. Any errors here are fatal and
	// cause immediate exit — we fail fast rather than starting with a
	// potentially broken configuration.
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}

	// Build the routing engine from the configuration rules.
	router := NewRouter(cfg.Routes)

	// Build the HTTP handler that performs reverse proxying.
	handler := NewHandler(router, cfg.MaxIdleConns)

	// Create the HTTP server with the configured listen address and timeouts.
	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	}

	// Start the server in a background goroutine so we can listen for
	// termination signals in the main goroutine.
	go func() {
		log.Printf("httpreverseproxy: listening on %s (%d routes)",
			cfg.Listen, len(cfg.Routes))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("httpreverseproxy: server error: %s", err)
		}
	}()

	// Block until SIGINT (Ctrl+C) or SIGTERM, then gracefully shut down.
	quitCh := make(chan os.Signal, 1)
	signal.Notify(quitCh, syscall.SIGINT, syscall.SIGTERM)
	<-quitCh

	log.Printf("httpreverseproxy: shutting down...")
	shutdown(srv)
}

// shutdown gracefully shuts down the HTTP server, giving in-flight requests
// up to 5 seconds to complete before forcibly closing connections.
func shutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("httpreverseproxy: shutdown error: %s", err)
	}
}
