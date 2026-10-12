//go:build !darwin && !linux

// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import "os"

// Without a change time, in-place rewrites cannot be detected; body files are not cached.
func fileChangeTime(os.FileInfo) (int64, bool) { return 0, false }
