package main

import (
	"context"
	"flag"
	"gifstudio/internal/app"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	config := flag.String("config", ".env", "environment file")
	port := flag.String("port", "8096", "listen port on loopback")
	flag.Parse()
	cfg, err := app.LoadEnv(*config)
	if err != nil {
		log.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	server := &http.Server{Addr: "127.0.0.1:" + *port, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	log.Printf("拾光 GIF listening on %s", server.Addr)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
