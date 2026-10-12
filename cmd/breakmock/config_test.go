// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	bm "github.com/Breaking-IT/BreakMock"
)

func configFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "breakmock.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigDefaultsAndExplicitOverrides(t *testing.T) {
	c, version, err := parseConfig(nil, io.Discard)
	if err != nil || version {
		t.Fatalf("parse: %v %v", version, err)
	}
	if !reflect.DeepEqual(c, defaults()) {
		t.Fatalf("defaults changed: %+v", c)
	}
}

func TestConfigPrecedence(t *testing.T) {
	path := configFile(t, `{"server":{"port":9000,"rootDir":"fixtures","noRequestJournal":true,"maxRequestJournalEntries":1000,"localResponseTemplating":false},"toxiproxy":{"enabled":true,"proxies":[{"name":"database","listen":"127.0.0.1:0","upstream":"127.0.0.1:5432","enabled":false}]}}`)
	c, _, err := parseConfig([]string{"--config", path}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.RootDir != filepath.Join(filepath.Dir(path), "fixtures") || c.Server.Port != 9000 || !c.Toxiproxy.Enabled || len(c.Toxiproxy.Proxies) != 1 {
		t.Fatalf("file not applied: %+v", c)
	}
	c, _, err = parseConfig([]string{"--port=0", "--config", path, "--no-request-journal=false", "--max-request-journal-entries=0", "--enable-toxiproxy=false", "--root-dir=relative", "--local-response-templating=true"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.Port != 0 || c.Server.NoJournal || c.Server.MaxJournal != 0 || c.Toxiproxy.Enabled || c.Server.RootDir != "relative" || !c.Server.LocalTemplating {
		t.Fatalf("explicit zero/false override failed: %+v", c)
	}
	if len(c.Toxiproxy.Proxies) != 1 {
		t.Fatal("override lost proxy configuration")
	}
}

func TestConfigRejectsInvalidInput(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{`, `{} {}`, `{} garbage`, `{"unexpected":1}`, `{"server":{"porrt":3}}`, `{"toxiproxy":{"unknown":true}}`, `{"toxiproxy":{"proxies":[{"name":"x","unknown":1}]}}`, `{"server":{"port":-1}}`, `{"server":{"port":65536}}`, `{"server":{"maxRequestJournalEntries":-1}}`} {
		t.Run(body, func(t *testing.T) {
			if _, _, err := parseConfig([]string{"--config", configFile(t, body)}, io.Discard); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	for _, args := range [][]string{{"--port=-1"}, {"--max-request-journal-entries=-1"}, {"unexpected"}} {
		if _, _, err := parseConfig(args, io.Discard); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
}

func TestInformationalFlagsDoNotLoadConfig(t *testing.T) {
	c, version, err := parseConfig([]string{"--config=/missing/config", "--version"}, io.Discard)
	if err != nil || !version {
		t.Fatalf("version: %v", err)
	}
	c, _, err = parseConfig([]string{"--config=/missing/config", "--licenses"}, io.Discard)
	if err != nil || !c.Licenses {
		t.Fatalf("licenses: %v", err)
	}
	notices := bm.LicenseNotices()
	if !strings.Contains(notices, "Shopify") || !strings.Contains(notices, "Permission is hereby granted, free of charge") {
		t.Fatal("missing Toxiproxy MIT notice")
	}
}

func TestServeShutdown(t *testing.T) {
	for _, admin := range []bool{false, true} {
		name := "context"
		if admin {
			name = "admin"
		}
		t.Run(name, func(t *testing.T) {
			s, err := bm.NewServer(bm.Options{RootDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- serve(ctx, s, l, true) }()
			client := &http.Client{Timeout: time.Second}
			// A real request proves the server is serving before either shutdown trigger.
			response, err := client.Get("http://" + l.Addr().String() + "/__admin/mappings")
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if admin {
				response, err = client.Post("http://"+l.Addr().String()+"/__admin/shutdown", "application/json", nil)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatalf("shutdown status %d", response.StatusCode)
				}
			} else {
				cancel()
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(7 * time.Second):
				t.Fatal("shutdown did not finish")
			}
			conn, err := net.DialTimeout("tcp", l.Addr().String(), 100*time.Millisecond)
			if err == nil {
				conn.Close()
				t.Fatal("HTTP listener remains open")
			}
		})
	}
}

func TestRunClosesProxyWhenHTTPBindFails(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	proxySlot, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddress := proxySlot.Addr().String()
	proxySlot.Close()
	c := defaults()
	c.Server.RootDir = t.TempDir()
	c.Server.BindAddress = "127.0.0.1"
	c.Server.Port = occupied.Addr().(*net.TCPAddr).Port
	c.Toxiproxy = bm.ToxiproxyOptions{Enabled: true, Proxies: []bm.ToxiproxyProxyConfig{{Name: "startup-cleanup", Listen: proxyAddress, Upstream: occupied.Addr().String()}}}
	if err := run(context.Background(), c); err == nil {
		t.Fatal("expected HTTP bind failure")
	}
	reclaimed, err := net.Listen("tcp", proxyAddress)
	if err != nil {
		t.Fatalf("proxy listener leaked on HTTP startup failure: %v", err)
	}
	reclaimed.Close()
}

func TestRunCancellationClosesProxy(t *testing.T) {
	slot, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := slot.Addr().String()
	slot.Close()
	c := defaults()
	c.Server.RootDir = t.TempDir()
	c.Server.BindAddress = "127.0.0.1"
	c.Server.Port = 0
	c.Toxiproxy = bm.ToxiproxyOptions{Enabled: true, Proxies: []bm.ToxiproxyProxyConfig{{Name: "shutdown-cleanup", Listen: address, Upstream: "127.0.0.1:1"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, c); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("proxy listener leaked on shutdown: %v", err)
	}
	reclaimed.Close()
}
