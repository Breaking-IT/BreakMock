// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var webhookClient = &http.Client{Timeout: 5 * time.Minute, Transport: newProxyTransport()}

// DispatchWebhooks schedules the built-in webhook listeners after serving a response.
func DispatchWebhooks(store *Store, eventID string, req Request, mapping Mapping, rootDir string) {
	if mapping == nil {
		return
	}
	configs := []map[string]any{}
	if actions, ok := mapping["postServeActions"].(map[string]any); ok {
		if p, ok := actions["webhook"].(map[string]any); ok {
			configs = append(configs, p)
		}
	}
	if listeners, ok := mapping["serveEventListeners"].([]any); ok {
		for _, item := range listeners {
			listener, ok := item.(map[string]any)
			if !ok || listener["name"] != "webhook" {
				continue
			}
			phases, has := listener["requestPhases"].([]any)
			if has {
				complete := false
				for _, phase := range phases {
					if phase == "AFTER_COMPLETE" {
						complete = true
					}
				}
				if !complete {
					continue
				}
			}
			if p, ok := listener["parameters"].(map[string]any); ok {
				configs = append(configs, p)
			}
		}
	}
	for _, config := range configs {
		dispatchWebhook(store, eventID, req, config, rootDir)
	}
}
func dispatchWebhook(store *Store, eventID string, original Request, config map[string]any, rootDir string) {
	fail := func(msg string) { store.AppendSubEvent(eventID, "ERROR", map[string]any{"message": msg}) }
	render := func(s string) (string, error) { return RenderWebhookTemplate(s, original, config, rootDir) }
	method := bmString(config["method"])
	if config["method"] == nil {
		fail("Exception thrown while configuring webhook: Cannot invoke \"String.indexOf(String)\" because \"content\" is null")
		return
	}
	method, err := render(method)
	if err != nil {
		fail("Exception thrown while configuring webhook: " + err.Error())
		return
	}
	target, err := render(bmString(config["url"]))
	if err != nil {
		fail("Exception thrown while configuring webhook: " + err.Error())
		return
	}
	body := []byte(nil)
	if text, ok := config["body"].(string); ok {
		value, e := render(text)
		if e != nil {
			fail("Exception thrown while configuring webhook: " + e.Error())
			return
		}
		body = []byte(value)
	} else if encoded, ok := config["base64Body"].(string); ok {
		body, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			fail("Exception thrown while configuring webhook: " + err.Error())
			return
		}
	}
	if method != "POST" && method != "PUT" && method != "PATCH" {
		body = nil
	}
	request, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		fail("Exception thrown while configuring webhook: " + err.Error())
		return
	}
	headers := map[string]any{}
	if h, ok := config["headers"].(map[string]any); ok {
		for name, value := range h {
			values := []any{value}
			if list, ok := value.([]any); ok {
				values = list
			}
			rendered := []string{}
			for _, v := range values {
				text, e := render(bmString(v))
				if e != nil {
					fail("Exception thrown while configuring webhook: " + e.Error())
					return
				}
				request.Header.Add(name, text)
				rendered = append(rendered, text)
			}
			if len(rendered) == 1 {
				headers[name] = rendered[0]
			} else {
				headers[name] = rendered
			}
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	port := 80
	if request.URL.Scheme == "https" {
		port = 443
	}
	if explicit := request.URL.Port(); explicit != "" {
		port, _ = strconv.Atoi(explicit)
	}
	query := map[string]any{}
	for k, v := range request.URL.Query() {
		query[k] = map[string]any{"key": k, "values": v}
	}
	store.AppendSubEvent(eventID, "WEBHOOK_REQUEST", map[string]any{"url": request.URL.RequestURI(), "absoluteUrl": target, "method": method, "headers": headers, "cookies": map[string]any{}, "browserProxyRequest": false, "loggedDate": now.UnixMilli(), "bodyAsBase64": base64.StdEncoding.EncodeToString(body), "body": string(body), "scheme": request.URL.Scheme, "host": request.URL.Hostname(), "port": port, "loggedDateString": now.Format(time.RFC3339Nano), "queryParams": query, "formParams": map[string]any{}})
	if request.Header.Get("Connection") == "" {
		request.Header.Set("Connection", "keep-alive")
	}

	delay := float64(0)
	if d, ok := config["delay"].(map[string]any); ok {
		switch bmString(d["type"]) {
		case "fixed":
			delay = bmNumber(d["milliseconds"], 0)
		case "uniform":
			lo := bmNumber(d["lower"], 0)
			delay = lo + rand.Float64()*(bmNumber(d["upper"], 0)-lo)
		case "lognormal":
			delay = math.Exp(math.Log(bmNumber(d["median"], 1)) + bmNumber(d["sigma"], 1)*rand.NormFloat64())
		}
	}
	go func() {
		if delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
		response, e := webhookClient.Do(request)
		if e != nil {
			fail(fmt.Sprintf("Failed to fire webhook %s %s: %s", method, target, e))
			return
		}
		defer response.Body.Close()
		data, e := io.ReadAll(response.Body)
		if e != nil {
			fail(fmt.Sprintf("Failed to fire webhook %s %s: %s", method, target, e))
			return
		}
		h := map[string]any{}
		for k, v := range response.Header {
			if len(v) == 1 {
				h[k] = v[0]
			} else {
				h[k] = v
			}
		}
		store.appendSubEvent(eventID, "WEBHOOK_RESPONSE", map[string]any{"status": response.StatusCode, "fromProxy": false, "headers": h, "bodyAsBase64": base64.StdEncoding.EncodeToString(data), "body": strings.ToValidUTF8(string(data), "�")}, &adminOrder{children: map[string]*adminOrder{"headers": {keys: proxyResponseHeaderOrder(response)}}})
	}()
}
