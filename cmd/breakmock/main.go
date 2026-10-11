// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package main

import (
	"flag"
	"fmt"
	bm "github.com/Breaking-IT/BreakMock"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
)

func main() {
	port := flag.Int("port", 8080, "HTTP listen port")
	host := flag.String("bind-address", "0.0.0.0", "Listen address")
	root := flag.String("root-dir", ".", "Directory containing mappings and __files")
	global := flag.Bool("global-response-templating", false, "Apply response templates to every stub")
	local := flag.Bool("local-response-templating", true, "Enable opt-in response-template transformer")
	disableLogging := flag.Bool("disable-request-logging", false, "Disable request logging and associated JSON response validation")
	disableTemplates := flag.Bool("disable-response-templating", false, "Disable response templating")
	noJournal := flag.Bool("no-request-journal", false, "Disable request journal")
	maxJournal := flag.Int("max-request-journal-entries", 0, "Maximum request journal entries (0 means unlimited)")
	version := flag.Bool("version", false, "Print version")
	flag.Parse()
	if *version {
		fmt.Println("BreakMock 0.1.0-dev (WireMock 3.13.2 compatibility target)")
		return
	}
	s, e := bm.NewServer(bm.Options{RootDir: *root, GlobalTemplating: *global, DisableTemplating: *disableTemplates || (!*local && !*global), DisableRequestLogging: *disableLogging, DisableJournal: *noJournal, MaxJournalEntries: *maxJournal})
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
	if !*noJournal {
		listener = bm.WrapHeaderOrderListener(listener)
		httpServer.ConnContext = bm.HeaderOrderConnContext
	}
	log.Fatal(httpServer.Serve(listener))
}
