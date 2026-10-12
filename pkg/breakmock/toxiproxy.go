// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/Shopify/toxiproxy/v2"
	"github.com/rs/zerolog"
)

const toxiproxyAPIPrefix = "/__admin/toxiproxy"

// ToxiproxyOptions configures the optional embedded TCP fault-injection service.
// Its management API shares BreakMock's HTTP listener. Proxy listeners are TCP
// endpoints explicitly configured here or through the management API.
type ToxiproxyOptions struct {
	Enabled bool                   `json:"enabled"`
	Proxies []ToxiproxyProxyConfig `json:"proxies,omitempty"`
}

// ToxiproxyProxyConfig describes a startup proxy. Enabled defaults to true.
// Toxics use the native Toxiproxy JSON schema and defaults.
type ToxiproxyProxyConfig struct {
	Name     string            `json:"name"`
	Listen   string            `json:"listen"`
	Upstream string            `json:"upstream"`
	Enabled  *bool             `json:"enabled,omitempty"`
	Toxics   []json.RawMessage `json:"toxics,omitempty"`
}

type embeddedToxiproxy struct {
	api      *toxiproxy.ApiServer
	handler  http.Handler
	mu       sync.Mutex
	closed   bool
	closeErr error
}

func newEmbeddedToxiproxy(options ToxiproxyOptions) (*embeddedToxiproxy, error) {
	// A private registry and logger avoid process-global registration conflicts
	// when applications embed multiple BreakMock instances.
	logger := zerolog.New(os.Stderr).Level(zerolog.WarnLevel).With().Str("component", "toxiproxy").Logger()
	t := &embeddedToxiproxy{api: toxiproxy.NewServer(toxiproxy.NewMetricsContainer(nil), logger)}
	for i, config := range options.Proxies {
		if config.Name == "" || config.Listen == "" || config.Upstream == "" {
			_ = t.close()
			return nil, fmt.Errorf("toxiproxy proxy %d: name, listen and upstream are required", i+1)
		}
		proxy := toxiproxy.NewProxy(t.api, config.Name, config.Listen, config.Upstream)
		for j, toxic := range config.Toxics {
			if _, err := proxy.Toxics.AddToxicJson(bytes.NewReader(toxic)); err != nil {
				_ = t.close()
				return nil, fmt.Errorf("toxiproxy proxy %q toxic %d: %w", config.Name, j+1, err)
			}
		}
		enabled := config.Enabled == nil || *config.Enabled
		if err := t.api.Collection.Add(proxy, enabled); err != nil {
			_ = t.close()
			return nil, fmt.Errorf("toxiproxy proxy %q: %w", config.Name, err)
		}
	}
	routes := t.api.Routes()
	// Native routes have a TimeoutHandler. Install this middleware last, inside
	// that timeout, so Close waits for actual handler completion even when a
	// timed-out HTTP request has already returned to its caller. Serialize API
	// handlers because native JSON serialization reads proxy fields without the
	// locks used by concurrent updates. TCP forwarding never takes this lock.
	routes.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.closed {
				http.Error(w, "Toxiproxy is closed", http.StatusServiceUnavailable)
				return
			}
			if r.Context().Err() != nil {
				return // A queued request must not mutate state after its timeout.
			}
			next.ServeHTTP(w, r)
		})
	})
	t.handler = http.StripPrefix(toxiproxyAPIPrefix, routes)
	return t, nil
}

func (t *embeddedToxiproxy) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		// ApiServer.Shutdown only handles a native API listener, which we never
		// start. Collection.Clear stops all proxy listeners and active links.
		t.closeErr = t.api.Collection.Clear()
	}
	return t.closeErr
}

// Close releases embedded proxy listeners and active proxy connections. It is
// safe to call repeatedly. The owner must shut down its HTTP server separately.
func (s *Server) Close() error {
	if s.toxiproxy != nil {
		return s.toxiproxy.close()
	}
	return nil
}
