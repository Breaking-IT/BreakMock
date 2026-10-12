// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	bm "github.com/Breaking-IT/BreakMock/pkg/breakmock"
)

type serverConfig struct {
	Port             int    `json:"port"`
	BindAddress      string `json:"bindAddress"`
	RootDir          string `json:"rootDir"`
	GlobalTemplating bool   `json:"globalResponseTemplating"`
	LocalTemplating  bool   `json:"localResponseTemplating"`
	DisableLogging   bool   `json:"disableRequestLogging"`
	DisableTemplates bool   `json:"disableResponseTemplating"`
	NoJournal        bool   `json:"noRequestJournal"`
	MaxJournal       int    `json:"maxRequestJournalEntries"`
	WSIdle           int64  `json:"websocketIdleTimeout"`
	WSText           int64  `json:"websocketMaxTextMessageSize"`
	WSBinary         int64  `json:"websocketMaxBinaryMessageSize"`
}

type configuration struct {
	Licenses  bool                `json:"-"`
	Server    serverConfig        `json:"server"`
	Toxiproxy bm.ToxiproxyOptions `json:"toxiproxy"`
}

func defaults() configuration {
	return configuration{Toxiproxy: bm.ToxiproxyOptions{Enabled: true}, Server: serverConfig{Port: 8080, BindAddress: "0.0.0.0", RootDir: ".", LocalTemplating: true, WSIdle: 300000, WSText: 10485760, WSBinary: 10485760}}
}

func configFlags(c *configuration, output io.Writer) (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("breakmock", flag.ContinueOnError)
	fs.SetOutput(output)
	s := &c.Server
	path := fs.String("config", "", "JSON configuration file")
	version := fs.Bool("version", false, "Print version")
	fs.BoolVar(&c.Licenses, "licenses", false, "Print license notices")
	fs.IntVar(&s.Port, "port", s.Port, "HTTP listen port")
	fs.StringVar(&s.BindAddress, "bind-address", s.BindAddress, "Listen address")
	fs.StringVar(&s.RootDir, "root-dir", s.RootDir, "Directory containing mappings and __files")
	fs.BoolVar(&s.GlobalTemplating, "global-response-templating", s.GlobalTemplating, "Apply response templates to every stub")
	fs.BoolVar(&s.LocalTemplating, "local-response-templating", s.LocalTemplating, "Enable opt-in response-template transformer")
	fs.BoolVar(&s.DisableLogging, "disable-request-logging", s.DisableLogging, "Disable request logging")
	fs.BoolVar(&s.DisableTemplates, "disable-response-templating", s.DisableTemplates, "Disable response templating")
	fs.BoolVar(&s.NoJournal, "no-request-journal", s.NoJournal, "Disable request journal")
	fs.IntVar(&s.MaxJournal, "max-request-journal-entries", s.MaxJournal, "Maximum request journal entries (0 means unlimited)")
	fs.Int64Var(&s.WSIdle, "websocket-idle-timeout", s.WSIdle, "WebSocket idle timeout in milliseconds")
	fs.Int64Var(&s.WSText, "websocket-max-text-message-size", s.WSText, "Maximum WebSocket text message bytes")
	fs.Int64Var(&s.WSBinary, "websocket-max-binary-message-size", s.WSBinary, "Maximum WebSocket binary message bytes")
	fs.BoolVar(&c.Toxiproxy.Enabled, "enable-toxiproxy", c.Toxiproxy.Enabled, "Enable embedded Toxiproxy API and configured proxies")
	return fs, path, version
}

func parseConfig(args []string, output io.Writer) (configuration, bool, error) {
	cli := defaults()
	fs, path, version := configFlags(&cli, output)
	if err := fs.Parse(args); err != nil {
		return cli, false, err
	}
	if fs.NArg() != 0 {
		return cli, false, fmt.Errorf("unexpected positional argument %q", fs.Arg(0))
	}
	if *version || cli.Licenses {
		return cli, *version, nil
	}
	c := defaults()
	if *path != "" {
		file, err := os.Open(*path)
		if err != nil {
			return c, false, err
		}
		defer file.Close()
		dec := json.NewDecoder(file)
		dec.DisallowUnknownFields()
		// Decode through a pointer so a JSON null is not silently accepted as a configuration.
		target := &c
		if err := dec.Decode(&target); err != nil {
			return c, false, fmt.Errorf("configuration: %w", err)
		}
		if target == nil {
			return c, false, errors.New("configuration must be a JSON object")
		}
		var extra any
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("multiple JSON values")
			}
			return c, false, fmt.Errorf("configuration: %w", err)
		}
		if !filepath.IsAbs(c.Server.RootDir) {
			absolute, err := filepath.Abs(filepath.Join(filepath.Dir(*path), c.Server.RootDir))
			if err != nil {
				return c, false, err
			}
			c.Server.RootDir = absolute
		}
	}
	overrides, _, _ := configFlags(&c, io.Discard)
	var overrideErr error
	fs.Visit(func(f *flag.Flag) {
		if err := overrides.Set(f.Name, f.Value.String()); err != nil {
			overrideErr = err
		}
	})
	if overrideErr != nil {
		return c, false, overrideErr
	}
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return c, false, errors.New("port must be between 0 and 65535")
	}
	if c.Server.MaxJournal < 0 {
		return c, false, errors.New("max-request-journal-entries must be nonnegative")
	}
	return c, false, nil
}

func (c configuration) options() bm.Options {
	s := c.Server
	return bm.Options{WebSocketOptionsConfigured: true, WebSocketIdleTimeout: s.WSIdle, WebSocketMaxTextMessageSize: s.WSText, WebSocketMaxBinaryMessageSize: s.WSBinary, RootDir: s.RootDir, GlobalTemplating: s.GlobalTemplating, DisableTemplating: s.DisableTemplates || (!s.LocalTemplating && !s.GlobalTemplating), DisableRequestLogging: s.DisableLogging, DisableJournal: s.NoJournal, MaxJournalEntries: s.MaxJournal, Toxiproxy: c.Toxiproxy}
}
