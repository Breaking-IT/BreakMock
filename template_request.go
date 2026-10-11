// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"
)

func templateBodyBinary(contentType string) bool {
	contentType = strings.Split(contentType, ";")[0]
	for _, word := range []string{"text", "json", "xml", "html", "yaml", "csv", "x-www-form-urlencoded"} {
		if strings.Contains(contentType, word) {
			return false
		}
	}
	return true
}
func templateRequestExtras(model map[string]any, request Request) {
	model["bodyAsBase64"] = base64.StdEncoding.EncodeToString(request.Body)
	model["binary"] = templateBodyBinary(request.Headers.Get("Content-Type"))
	model["clientIp"] = request.ClientIP
	if len(request.PathParams) > 0 {
		path := model["path"].(templatePath)
		for k, v := range request.PathParams {
			path[k] = v
		}
		model["pathSegments"] = path
	}
	line := map[string]any{}
	for _, key := range []string{"method", "scheme", "host", "port", "query", "url", "clientIp", "path", "pathSegments", "baseUrl"} {
		line[key] = model[key]
	}
	model["requestLine"] = line
	mediaType, params, _ := mime.ParseMediaType(request.Headers.Get("Content-Type"))
	isMultipart := strings.HasPrefix(mediaType, "multipart/") && params["boundary"] != ""
	model["multipart"] = isMultipart
	parts := map[string]any{}
	model["parts"] = parts
	if !isMultipart {
		return
	}
	reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
	index := 0
	for {
		part, e := reader.NextPart()
		if e != nil {
			break
		}
		body, e := io.ReadAll(part)
		part.Close()
		if e != nil {
			break
		}
		name := part.FormName()
		if name == "" && mediaType == "multipart/related" {
			name = "part-" + strconv.Itoa(index)
			index++
		}
		headers := templateHeaders{}
		for k, v := range part.Header {
			headers[strings.ToLower(k)] = templateValues(v)
		}
		parts[name] = map[string]any{"name": name, "body": string(body), "bodyAsBase64": base64.StdEncoding.EncodeToString(body), "binary": templateBodyBinary(part.Header.Get("Content-Type")), "headers": headers}
	}
}
