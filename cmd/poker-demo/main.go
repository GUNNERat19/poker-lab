package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cappedpoker/internal/demo"
	"cappedpoker/internal/live"
)

func main() {
	port := flag.Int("port", 8080, "local HTTP port")
	turnTime := flag.Duration("turn-timeout", 60*time.Second, "turn time for multiplayer rooms")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		log.Fatal("port must be between 1 and 65535")
	}
	if *turnTime <= 0 {
		log.Fatal("turn-timeout must be positive")
	}
	app, err := demo.New()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var multiplayer *live.Server
	if databaseURL := os.Getenv("POKER_DATABASE_URL"); databaseURL != "" {
		multiplayer, err = live.OpenPersistent(ctx, *turnTime, databaseURL, os.Getenv("POKER_STORAGE_KEY"))
		if err != nil {
			log.Fatal(err)
		}
		log.Print("PostgreSQL multiplayer storage enabled")
	} else {
		multiplayer = live.New(ctx, *turnTime)
	}
	defer multiplayer.Close()
	if rpc, address := os.Getenv("POKER_ESCROW_RPC_URL"), os.Getenv("POKER_ESCROW_ADDRESS"); rpc != "" || address != "" {
		if err := multiplayer.ConfigureEscrow(ctx, rpc, address); err != nil {
			log.Fatal(err)
		}
	}
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/live/", multiplayer)
	mux.Handle("/", app)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(deadline)
	}()
	log.Printf("Poker Lab is ready at http://%s", addr)
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
