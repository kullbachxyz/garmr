package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"garmr/internal/cfg"
	"garmr/internal/importer"
	"garmr/internal/store"
	"garmr/internal/web"
)

func main() {
	configPath := flag.String("config", "", "path to config")
	flag.Parse()

	// Auto-detect config file if not specified
	actualConfigPath := *configPath
	if actualConfigPath == "" {
		actualConfigPath = "./garmr.json"
	}

	c := cfg.Load(actualConfigPath)

	db, err := store.Open(c.DBPath)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	if err := db.EnsureInitialUser(c.AuthUser, c.AuthPass); err != nil {
		log.Fatalf("auth bootstrap: %v", err)
	}

	im := importer.New(c, db)

	// Context that cancels on SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start background polling only if enabled
	if c.PollMs > 0 {
		go im.Run(ctx)
	}

	// Start HTTP server
	srv := web.New(c, db, im)
	go func() {
		log.Printf("http: listening on %s", c.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
			log.Printf("http: %v", err)
		}
	}()

	// Wait for Ctrl-C
	<-ctx.Done()
	log.Printf("shutting down...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	log.Printf("bye")
}
