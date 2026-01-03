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

var version = "1.1.0"

func main() {
	configPath := flag.String("config", "config.toml", "Path to configuration file")
	showVersion := flag.Bool("version", false, "Show version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("juno-proxy version %s\n", version)
		os.Exit(0)
	}

	config, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	proxy := NewProxy(config)

	// Start ZMQ proxy if enabled
	if proxy.zmqProxy != nil {
		if err := proxy.zmqProxy.Start(); err != nil {
			log.Fatalf("Failed to start ZMQ proxy: %v", err)
		}
	}

	// Create HTTP server
	server := &http.Server{
		Addr:         config.Listen,
		Handler:      proxy,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: config.GetUpstreamTimeout() + 10*time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Println("Shutting down...")

		// Stop ZMQ proxy
		proxy.Stop()

		// Shutdown HTTP server with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Error during server shutdown: %v", err)
		}
	}()

	log.Printf("Starting juno-proxy on %s", config.Listen)
	log.Printf("Upstream RPC: %s", config.Upstream.URL)
	log.Printf("Allowed methods: %v", config.AllowedMethods)
	if config.ProxyAuth.Enabled {
		log.Printf("Proxy authentication: enabled")
	} else {
		log.Printf("Proxy authentication: disabled")
	}
	if config.ZMQ.Enabled {
		log.Printf("ZMQ proxy: %s -> %s", config.ZMQ.UpstreamURL, config.ZMQ.Listen)
	} else {
		log.Printf("ZMQ proxy: disabled")
	}

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server stopped")
}
