package main

import (
	"fmt"
	"log"
	"os"

	"github.com/mbalazy/pm/internal/storage"
	"github.com/mbalazy/pm/internal/syncserver"
	"github.com/mbalazy/pm/internal/version"
)

func main() {
	token := os.Getenv("PM_SYNC_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "PM_SYNC_TOKEN env var required")
		os.Exit(1)
	}

	addr := os.Getenv("PM_SYNC_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store := storage.NewStore()
	if dataDir := os.Getenv("PM_DATA_DIR"); dataDir != "" {
		store.Root = dataDir
	}
	if err := store.Init(); err != nil {
		log.Fatalf("storage init: %v", err)
	}

	log.Printf("pm-sync %s starting on %s (data: %s)", version.Version, addr, store.Root)
	srv := syncserver.New(store, token)
	if err := srv.ListenAndServe(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
