// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
)

// Body files are re-read for every request in WireMock, and concurrent opens
// contend in the kernel. A cached body is reused only while a stat of its path
// still reports the same file, size, modification and change time; the change
// time cannot be preserved by copying tools, so in-place rewrites are detected.
// Every read still goes through os.Root, so paths cannot escape __files.
const (
	bodyFileCacheMaxFile = 16 << 20
	bodyFileCacheBytes   = 64 << 20
)

type bodyFileVersion struct {
	info   os.FileInfo
	change int64
}

type bodyFileEntry struct {
	data    []byte
	version bodyFileVersion
}

var bodyFileCache = struct {
	sync.RWMutex
	entries map[string]*bodyFileEntry
	bytes   int
}{entries: map[string]*bodyFileEntry{}}

func bodyFileVersionOf(info os.FileInfo) (bodyFileVersion, bool) {
	change, ok := fileChangeTime(info)
	return bodyFileVersion{info, change}, ok && info.Mode().IsRegular()
}

func (v bodyFileVersion) matches(info os.FileInfo) bool {
	change, ok := fileChangeTime(info)
	return ok && change == v.change && os.SameFile(v.info, info) && info.Size() == v.info.Size() && info.ModTime().Equal(v.info.ModTime())
}

// readBodyFile returns the contents of name within dir; callers must not modify them.
func readBodyFile(dir, name string) ([]byte, error) {
	key := dir + "\x00" + name
	bodyFileCache.RLock()
	entry := bodyFileCache.entries[key]
	bodyFileCache.RUnlock()
	if entry != nil {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && entry.version.matches(info) {
			return entry.data, nil
		}
	}
	files, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	file, err := files.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	if info.Size() > 0 && info.Size() <= bodyFileCacheMaxFile {
		buffer.Grow(int(info.Size()) + 1)
	}
	if _, err = buffer.ReadFrom(file); err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	// The version is taken before reading, so a concurrent write changes it and
	// the next request reads the file again.
	if version, ok := bodyFileVersionOf(info); ok && len(data) <= bodyFileCacheMaxFile {
		bodyFileCache.Lock()
		if old := bodyFileCache.entries[key]; old != nil {
			bodyFileCache.bytes -= len(old.data)
		}
		if bodyFileCache.bytes+len(data) > bodyFileCacheBytes {
			clear(bodyFileCache.entries)
			bodyFileCache.bytes = 0
		}
		bodyFileCache.entries[key] = &bodyFileEntry{data: data, version: version}
		bodyFileCache.bytes += len(data)
		bodyFileCache.Unlock()
	}
	return data, nil
}
