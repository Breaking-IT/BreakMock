// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"github.com/Breaking-IT/BreakMock/internal/adminassets"
	"net/http"
	"path"
	"strings"
)

func serveAdminAsset(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "GET" && r.Method != "HEAD" {
		return false
	}
	p := strings.TrimPrefix(r.URL.Path, "/__admin/")
	name := ""
	switch p {
	case "docs":
		name = "doc-index.html"
	case "docs/swagger":
		name = "swagger/wiremock-admin-api.json"
	case "recorder", "recorder/":
		name = "assets/recorder/index.html"
	case "swagger-ui", "swagger-ui/":
		name = "assets/swagger-ui/index.html"
	default:
		if strings.HasPrefix(p, "recorder/") || strings.HasPrefix(p, "swagger-ui/") {
			name = "assets/" + p
		} else {
			return false
		}
	}
	data, err := adminassets.Files.ReadFile(name)
	if err != nil {
		w.WriteHeader(404)
		return true
	}
	mime := "application/octet-stream"
	switch path.Ext(name) {
	case ".html":
		mime = "text/html"
	case ".json":
		mime = "application/json"
	case ".js":
		mime = "application/javascript"
	case ".css":
		mime = "text/css"
	case ".png":
		mime = "image/png"
	}
	w.Header().Set("Content-Type", mime)
	w.WriteHeader(200)
	if r.Method != "HEAD" {
		_, _ = w.Write(data)
	}
	return true
}
