// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	raymond "github.com/Breaking-IT/BreakMock/internal/handlebars"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

var templateLocaleOnce sync.Once
var templateLocale string

func templateDefaultLocale() string {
	templateLocaleOnce.Do(func() {
		if runtime.GOOS == "darwin" {
			if value, e := exec.Command("/usr/bin/defaults", "read", "-g", "AppleLocale").Output(); e == nil {
				templateLocale = strings.TrimSpace(string(value))
			}
		}
		if templateLocale == "" {
			for _, name := range []string{"LC_ALL", "LC_NUMERIC", "LANG"} {
				if value := os.Getenv(name); value != "" {
					templateLocale = strings.SplitN(value, ".", 2)[0]
					break
				}
			}
		}
		if templateLocale == "" || templateLocale == "C" {
			templateLocale = "en_US"
		}
	})
	return templateLocale
}
func templateNumberFormat(o *raymond.Options) string {
	value := templateNumber(o.Param(0), 0)
	pattern := o.ParamStr(1)
	locale := o.ParamStr(2)
	if locale == "" {
		locale = templateDefaultLocale()
	}
	max, min := 3, 0
	style := strings.ToLower(pattern)
	if style == "integer" || style == "percent" {
		max = 0
	} else if strings.ContainsAny(pattern, "#0") {
		max = 0
		if i := strings.LastIndex(pattern, "."); i >= 0 {
			fraction := pattern[i+1:]
			max = len(fraction)
			min = strings.Count(fraction, "0")
		}
	}
	if v := o.HashProp("maximumFractionDigits"); v != nil {
		max = int(templateNumber(v, float64(max)))
	}
	if v := o.HashProp("minimumFractionDigits"); v != nil {
		min = int(templateNumber(v, float64(min)))
	}
	if max < min {
		max = min
	}
	if max > 100 {
		max = 100
	}
	if min > 100 {
		min = 100
	}
	opts := []number.Option{number.MaxFractionDigits(max), number.MinFractionDigits(min)}
	if o.HashProp("groupingUsed") == false || (strings.ContainsAny(pattern, "#0") && !strings.Contains(pattern, ",")) {
		opts = append(opts, number.NoSeparator())
	}
	printer := message.NewPrinter(language.Make(strings.ReplaceAll(locale, "_", "-")))
	if style == "percent" {
		return printer.Sprint(number.Percent(value, opts...))
	}
	return printer.Sprint(number.Decimal(value, opts...))
}
