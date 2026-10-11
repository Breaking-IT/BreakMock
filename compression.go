//go:build cgo

// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

/*
#cgo LDFLAGS: -lz
#include <zlib.h>
#include <string.h>

// Use the same zlib default strategy/level and raw-deflate mode as Java's
// Deflater(DEFAULT_COMPRESSION, true). All state lives within this call.
static int breakmock_deflate(const unsigned char *src, unsigned int src_len,
                            unsigned char *dst, unsigned int dst_len,
                            unsigned long *written) {
    z_stream stream;
    memset(&stream, 0, sizeof(stream));
    int status = deflateInit2(&stream, Z_DEFAULT_COMPRESSION, Z_DEFLATED,
                              -MAX_WBITS, 8, Z_DEFAULT_STRATEGY);
    if (status != Z_OK) return status;
    stream.next_in = (Bytef *)src;
    stream.avail_in = src_len;
    stream.next_out = dst;
    stream.avail_out = dst_len;
    status = deflate(&stream, Z_FINISH);
    *written = stream.total_out;
    int cleanup = deflateEnd(&stream);
    if (status != Z_STREAM_END) return status == Z_OK ? Z_BUF_ERROR : status;
    return cleanup;
}
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"unsafe"
)

// wireGzip produces Jetty-compatible gzip bytes using the platform's zlib.
// Java's Deflater also uses zlib. Compressed byte equivalence is checked against
// the reference runtime; zlib versions are part of that compatibility contract.
func wireGzip(data []byte) ([]byte, error) {
	if uint64(len(data)) > uint64(math.MaxUint32)-1024*1024 {
		return nil, fmt.Errorf("gzip input too large")
	}
	bound := uint64(C.compressBound(C.uLong(len(data))))
	if bound > math.MaxUint32 {
		return nil, fmt.Errorf("gzip output bound too large")
	}
	result := make([]byte, 10+int(bound)+8)
	copy(result, []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0})
	var input *C.uchar
	if len(data) > 0 {
		input = (*C.uchar)(unsafe.Pointer(&data[0]))
	}
	var written C.ulong
	status := C.breakmock_deflate(input, C.uint(len(data)), (*C.uchar)(unsafe.Pointer(&result[10])), C.uint(bound), &written)
	if status != C.Z_OK {
		return nil, fmt.Errorf("zlib deflate failed: %d", int(status))
	}
	end := 10 + int(written)
	binary.LittleEndian.PutUint32(result[end:end+4], crc32.ChecksumIEEE(data))
	binary.LittleEndian.PutUint32(result[end+4:end+8], uint32(len(data)))
	return result[:end+8], nil
}

func wireZlibVersion() string { return C.GoString(C.zlibVersion()) }
