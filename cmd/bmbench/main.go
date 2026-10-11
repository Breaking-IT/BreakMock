// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// A bounded HTTP workload driver shared by both servers for comparative measurements.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("url", "http://localhost:18082/bench", "Target URL")
	duration := flag.Duration("duration", 5*time.Second, "Measurement duration")
	workers := flag.Int("concurrency", 32, "Concurrent persistent connections")
	flag.Parse()
	transport := &http.Transport{MaxIdleConns: *workers, MaxIdleConnsPerHost: *workers, MaxConnsPerHost: *workers, DisableCompression: true}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	var requests, failures atomic.Int64
	var mu sync.Mutex
	var samples []int64
	var wg sync.WaitGroup
	start := time.Now()
	deadline := start.Add(*duration)
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]int64, 0, 10000)
			for time.Now().Before(deadline) {
				t := time.Now()
				r, e := client.Get(*target)
				if e != nil {
					failures.Add(1)
					continue
				}
				_, e = io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if e != nil || r.StatusCode != 200 {
					failures.Add(1)
				}
				requests.Add(1)
				local = append(local, time.Since(t).Microseconds())
			}
			mu.Lock()
			samples = append(samples, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	percentile := func(p float64) float64 {
		if len(samples) == 0 {
			return 0
		}
		return float64(samples[int(float64(len(samples)-1)*p)]) / 1000
	}
	report := map[string]any{"url": *target, "concurrency": *workers, "seconds": elapsed, "requests": requests.Load(), "errors": failures.Load(), "rps": float64(requests.Load()) / elapsed, "p50_ms": percentile(.5), "p95_ms": percentile(.95), "p99_ms": percentile(.99)}
	data, _ := json.Marshal(report)
	fmt.Println(string(data))
	if failures.Load() > 0 {
		os.Exit(1)
	}
}
