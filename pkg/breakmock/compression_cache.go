// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "sync"

// Compressing a large static body costs far more than looking it up, and wire
// gzip output depends only on the input bytes. Entries are keyed by content, so
// a changed body file or mapping can never be served a stale compressed form.
const (
	gzipCacheMinBytes = 1 << 10
	gzipCacheMaxBytes = 16 << 20
	gzipCacheBytes    = 64 << 20
)

var gzipCache = struct {
	sync.RWMutex
	entries map[string][]byte
	bytes   int
}{entries: map[string][]byte{}}

// cachedWireGzip returns wireGzip(data); callers must not modify the result.
func cachedWireGzip(data []byte) ([]byte, error) {
	if len(data) < gzipCacheMinBytes || len(data) > gzipCacheMaxBytes {
		return wireGzip(data)
	}
	gzipCache.RLock()
	compressed, ok := gzipCache.entries[string(data)]
	gzipCache.RUnlock()
	if ok {
		return compressed, nil
	}
	compressed, err := wireGzip(data)
	if err != nil {
		return nil, err
	}
	size := len(data) + len(compressed)
	gzipCache.Lock()
	if gzipCache.bytes+size > gzipCacheBytes {
		// Start a new generation rather than tracking recency on every hit.
		clear(gzipCache.entries)
		gzipCache.bytes = 0
	}
	if _, exists := gzipCache.entries[string(data)]; !exists {
		gzipCache.entries[string(data)] = compressed
		gzipCache.bytes += size
	}
	gzipCache.Unlock()
	return compressed, nil
}
