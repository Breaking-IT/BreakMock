// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/Breaking-IT/BreakMock/internal/localeassets"
	"golang.org/x/text/language"
)

type templateDateLocale struct {
	Patterns map[string]string   `json:"patterns"`
	Symbols  map[string][]string `json:"symbols"`
}

var templateDateLocaleData struct {
	once sync.Once
	sync.Mutex
	raw    map[string]json.RawMessage
	parsed map[string]*templateDateLocale
}

func loadTemplateDateLocale(locale string) *templateDateLocale {
	templateDateLocaleData.once.Do(func() {
		reader, err := gzip.NewReader(bytes.NewReader(localeassets.DateLocales))
		if err != nil {
			return
		}
		defer reader.Close()
		var data struct {
			Locales map[string]json.RawMessage `json:"locales"`
		}
		if json.NewDecoder(reader).Decode(&data) == nil {
			templateDateLocaleData.raw = data.Locales
		}
		templateDateLocaleData.parsed = map[string]*templateDateLocale{}
	})
	tag := language.Make(strings.ReplaceAll(locale, "_", "-")).String()
	templateDateLocaleData.Lock()
	defer templateDateLocaleData.Unlock()
	for {
		if found := templateDateLocaleData.parsed[tag]; found != nil {
			return found
		}
		if raw := templateDateLocaleData.raw[tag]; raw != nil {
			var data templateDateLocale
			if json.Unmarshal(raw, &data) == nil {
				templateDateLocaleData.parsed[tag] = &data
				return &data
			}
		}
		if i := strings.LastIndex(tag, "-"); i >= 0 {
			tag = tag[:i]
		} else if tag != "und" {
			tag = "und"
		} else {
			break
		}
	}
	return &templateDateLocale{}
}
func templateLocalizedDate(t time.Time, format, timeStyle, locale string) string {
	data := loadTemplateDateLocale(locale)
	key := format
	if timeStyle != "" {
		key += "/" + timeStyle
	}
	if localized := data.Patterns[key]; localized != "" {
		format = localized
	}
	return templateDateTokens(format, func(token string, isToken bool) string {
		if !isToken {
			return token
		}
		lookup := token
		index := 0
		switch token[0] {
		case 'M', 'L':
			if len(token) >= 3 {
				index = int(t.Month()) - 1
			} else {
				return templateFormatDate(t, token)
			}
		case 'E', 'c':
			index = (int(t.Weekday()) + 6) % 7
			if len(token) < 3 {
				lookup = "EEE"
			}
			if token[0] == 'c' {
				lookup = "EEEE"
			}
		case 'a', 'B':
			index = t.Hour() * 2
			if t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0 {
				index++
			}
			lookup = string(token[0])
		case 'G':
			lookup = "G"
		default:
			return templateFormatDate(t, token)
		}
		if values := data.Symbols[lookup]; index < len(values) {
			return values[index]
		}
		return templateFormatDate(t, token)
	})
}
