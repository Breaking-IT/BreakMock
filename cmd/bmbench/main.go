// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
// A bounded HTTP workload driver shared by both servers for comparative measurements.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("url", "http://localhost:18082/bench", "Target URL")
	duration := flag.Duration("duration", 5*time.Second, "Measurement duration")
	rate := flag.Int("rate", 0, "Offered requests per second; 0 means unlimited")
	expected := flag.String("expected-body", "", "Expected response body, when supplied")
	workers := flag.Int("concurrency", 32, "Concurrent persistent connections")
	method := flag.String("method", http.MethodGet, "Request method")
	requestBodyFile := flag.String("body-file", "", "File sent as each request body, when supplied")
	expectedFile := flag.String("expected-body-file", "", "File holding the exact expected response body, when supplied")
	var headers headerFlags
	flag.Var(&headers, "header", "Request header as 'Name: value'; repeatable")
	flag.Parse()
	var requestBody, expectedBody []byte
	checkBody := *expected != ""
	if checkBody {
		expectedBody = []byte(*expected)
	}
	for _, file := range []struct {
		path string
		into *[]byte
	}{{*requestBodyFile, &requestBody}, {*expectedFile, &expectedBody}} {
		if file.path == "" {
			continue
		}
		data, err := os.ReadFile(file.path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		*file.into = data
	}
	checkBody = checkBody || *expectedFile != ""
	if *rate < 0 || *workers < 1 || *duration <= 0 {
		fmt.Fprintln(os.Stderr, "rate must be nonnegative; concurrency and duration must be positive")
		os.Exit(2)
	}
	transport := &http.Transport{MaxIdleConns: *workers, MaxIdleConnsPerHost: *workers, MaxConnsPerHost: *workers, DisableCompression: true}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	var requests, failures, dropped, received atomic.Int64
	var mu sync.Mutex
	var samples []int64
	var wg sync.WaitGroup
	start := time.Now()
	deadline := start.Add(*duration)
	var jobs chan time.Time
	if *rate > 0 {
		jobs = make(chan time.Time, *rate)
		go func() {
			defer close(jobs)
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			scheduled := int64(0)
			for now := range ticker.C {
				elapsed := now.Sub(start)
				if elapsed > *duration {
					elapsed = *duration
				}
				due := int64(float64(elapsed) / float64(time.Second) * float64(*rate))
				for scheduled < due {
					select {
					case jobs <- start.Add(time.Duration(float64(scheduled) / float64(*rate) * float64(time.Second))):
					default:
						dropped.Add(1)
					}
					scheduled++
				}
				if !now.Before(deadline) {
					return
				}
			}
		}()
	}
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := make([]int64, 0, 10000)
			var buffer bytes.Buffer
			for {
				t := time.Now()
				if jobs != nil {
					var ok bool
					t, ok = <-jobs
					if !ok {
						break
					}
				} else if !time.Now().Before(deadline) {
					break
				}
				request, e := http.NewRequest(*method, *target, bytes.NewReader(requestBody))
				if e != nil {
					fmt.Fprintln(os.Stderr, e)
					os.Exit(2)
				}
				if requestBody == nil {
					request.Body = http.NoBody
				}
				for _, h := range headers {
					request.Header.Add(h.name, h.value)
				}
				r, e := client.Do(request)
				if e != nil {
					failures.Add(1)
					continue
				}
				var n int64
				if checkBody {
					buffer.Reset()
					n, e = buffer.ReadFrom(r.Body)
				} else {
					n, e = io.Copy(io.Discard, r.Body)
				}
				r.Body.Close()
				received.Add(n)
				if e != nil || r.StatusCode != 200 || (checkBody && !bytes.Equal(buffer.Bytes(), expectedBody)) {
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
	report := map[string]any{"url": *target, "target_rps": *rate, "dropped": dropped.Load(), "concurrency": *workers, "seconds": elapsed, "requests": requests.Load(), "errors": failures.Load(), "rps": float64(requests.Load()) / elapsed, "response_bytes": received.Load(), "p50_ms": percentile(.5), "p95_ms": percentile(.95), "p99_ms": percentile(.99)}
	data, _ := json.Marshal(report)
	fmt.Println(string(data))
	if failures.Load() > 0 || dropped.Load() > 0 {
		os.Exit(1)
	}
}

type header struct{ name, value string }
type headerFlags []header

func (h *headerFlags) String() string { return fmt.Sprint(*h) }
func (h *headerFlags) Set(value string) error {
	name, v, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("header must be 'Name: value'")
	}
	*h = append(*h, header{strings.TrimSpace(name), strings.TrimSpace(v)})
	return nil
}
