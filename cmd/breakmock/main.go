// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	bm "github.com/Breaking-IT/BreakMock"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	port := flag.Int("port", 8080, "HTTP listen port")
	host := flag.String("bind-address", "0.0.0.0", "Listen address")
	root := flag.String("root-dir", ".", "Directory containing mappings and __files")
	global := flag.Bool("global-response-templating", false, "Apply response templates to every stub")
	local := flag.Bool("local-response-templating", true, "Enable opt-in response-template transformer")
	disableLogging := flag.Bool("disable-request-logging", false, "Disable request logging")
	disableTemplates := flag.Bool("disable-response-templating", false, "Disable response templating")
	noJournal := flag.Bool("no-request-journal", false, "Disable request journal")
	maxJournal := flag.Int("max-request-journal-entries", 0, "Maximum request journal entries (0 means unlimited)")
	wsIdle := flag.Int64("websocket-idle-timeout", 300000, "WebSocket idle timeout in milliseconds")
	wsText := flag.Int64("websocket-max-text-message-size", 10485760, "Maximum WebSocket text message bytes")
	wsBinary := flag.Int64("websocket-max-binary-message-size", 10485760, "Maximum WebSocket binary message bytes")
	version := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *version {
		fmt.Println("BreakMock 0.1.0-dev (WireMock 4.0.0-beta.40 compatibility target)")
		return
	}
	s, e := bm.NewServer(bm.Options{WebSocketOptionsConfigured: true, WebSocketIdleTimeout: *wsIdle, WebSocketMaxTextMessageSize: *wsText, WebSocketMaxBinaryMessageSize: *wsBinary, RootDir: *root, GlobalTemplating: *global, DisableTemplating: *disableTemplates || (!*local && !*global), DisableRequestLogging: *disableLogging, DisableJournal: *noJournal, MaxJournalEntries: *maxJournal})
	if e != nil {
		log.Print(e)
		os.Exit(1)
	}
	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	log.Printf("BreakMock listening on %s", addr)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{Handler: s}
	s.Store.SetShutdownHandler(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	})
	listener = bm.WrapWireListener(listener, !*noJournal)
	httpServer.ConnContext = bm.HeaderOrderConnContext
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
