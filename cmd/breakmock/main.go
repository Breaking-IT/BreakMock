// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
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
	"strconv"
	"sync"
	"syscall"
	"time"

	bm "github.com/Breaking-IT/BreakMock"
)

func main() {
	c, version, err := parseConfig(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
	if c.Licenses {
		fmt.Print(bm.LicenseNotices())
		return
	}
	if version {
		fmt.Println("BreakMock 0.1.0-dev (WireMock 4.0.0-beta.40 compatibility target)")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, c configuration) (err error) {
	s, err := bm.NewServer(c.options())
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	addr := net.JoinHostPort(c.Server.BindAddress, strconv.Itoa(c.Server.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	log.Printf("BreakMock listening on %s", listener.Addr())
	return serve(ctx, s, listener, !c.Server.NoJournal)
}

// serve waits for graceful HTTP shutdown to finish before returning, so run can
// close embedded proxies only after in-flight management requests have finished.
func serve(ctx context.Context, s *bm.Server, listener net.Listener, journal bool) error {
	defer listener.Close()
	httpServer := &http.Server{Handler: s, ConnContext: bm.HeaderOrderConnContext}
	requested := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	requestShutdown := func() { once.Do(func() { close(requested) }) }
	s.Store.SetShutdownHandler(requestShutdown)
	defer s.Store.SetShutdownHandler(nil)
	var shutdownErr error
	go func() {
		defer close(finished)
		select {
		case <-ctx.Done():
		case <-requested:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr = httpServer.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			shutdownErr = errors.Join(shutdownErr, httpServer.Close())
		}
	}()
	serveErr := httpServer.Serve(bm.WrapWireListener(listener, journal))
	requestShutdown()
	<-finished
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}
