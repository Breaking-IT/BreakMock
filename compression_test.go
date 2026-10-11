//go:build cgo

// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestWireGzipReferenceBytes(t *testing.T) {
	data := bytes.Repeat([]byte("hello world "), 400)
	// Captured directly from WireMock 3.13.2 / Jetty's GzipHandler, with
	// OpenJDK linking zlib 1.2.12. Header, DEFLATE blocks, CRC and length all count.
	want, err := base64.StdEncoding.DecodeString("H4sIAAAAAAAAAO3GsQkAIAwEwFUynEKKh4CN67uGxV11vZOpOyer2t3d3d3d3d3d/bM/KkpQgcASAAA=")
	if err != nil {
		t.Fatal(err)
	}
	got, err := wireGzip(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("gzip differs using zlib %s:\n got %x\nwant %x", wireZlibVersion(), got, want)
	}
}
func TestWireGzipRoundTrip(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("x"), bytes.Repeat([]byte{0, 255, 1, 2}, 20000)} {
		compressed, e := wireGzip(data)
		if e != nil {
			t.Fatal(e)
		}
		r, e := gzip.NewReader(bytes.NewReader(compressed))
		if e != nil {
			t.Fatal(e)
		}
		decoded, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(data, decoded) {
			t.Fatal("round trip differs")
		}
	}
}
func BenchmarkWireGzip(b *testing.B) {
	data := bytes.Repeat([]byte("hello world "), 400)
	b.ReportAllocs()
	for b.Loop() {
		if _, e := wireGzip(data); e != nil {
			b.Fatal(e)
		}
	}
}

func TestWireGzipReferenceCorpus(t *testing.T) {
	raw, err := os.ReadFile("testdata/gzip/wiremock-3.13.2.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Fixtures []struct {
			Kind   string
			Size   int
			SHA256 string
		}
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range corpus.Fixtures {
		t.Run(fmt.Sprintf("%s-%d", fixture.Kind, fixture.Size), func(t *testing.T) {
			data := make([]byte, fixture.Size)
			x := uint32(0x12345678)
			for i := range data {
				switch fixture.Kind {
				case "repeat":
					data[i] = "hello world "[i%12]
				case "counter":
					data[i] = byte(i)
				case "random":
					x ^= x << 13
					x ^= x >> 17
					x ^= x << 5
					data[i] = byte(x)
				default:
					t.Fatal("unknown corpus generator")
				}
			}
			compressed, err := wireGzip(data)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(compressed)
			if fmt.Sprintf("%x", hash) != fixture.SHA256 {
				t.Fatalf("compressed bytes differ, zlib=%s", wireZlibVersion())
			}
		})
	}
}
