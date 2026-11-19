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
)

func serve() error {
	address := flag.String("addr", "127.0.0.1:8088", "Local HTTP address")
	provider := flag.String("provider", "", "Provider name; ask when omitted (demo needs no key)")
	delay := flag.Duration("demo-delay", 60*time.Millisecond, "Delay per demo word")
	flag.Parse()
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("read working directory: %w", err)
	}
	selected, environment, err := prepareProvider(directory, *provider, os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	factory, err := providerModel(selected, *delay, environment)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	embedder, err := scenarioEmbedder(selected)
	if err != nil {
		return err
	}
	defer func() {
		if err := embedder.Close(); err != nil {
			log.Printf("close policy model: %v", err)
		}
	}()
	app := newApplication(ctx, factory, selected, embedder)
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: app.routes(), ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}()
	log.Printf("LISTEN http://%s · provider %s · memory only", listener.Addr(), selected)
	err = server.Serve(listener)
	cancel()
	<-done
	app.wait.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func main() {
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}
