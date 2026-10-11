// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func templateDateTokens(format string, emit func(string, bool) string) string {
	var out strings.Builder
	for i := 0; i < len(format); {
		if format[i] == '\'' {
			if i+1 < len(format) && format[i+1] == '\'' {
				out.WriteByte('\'')
				i += 2
				continue
			}
			i++
			var literal strings.Builder
			for i < len(format) {
				if format[i] == '\'' {
					if i+1 < len(format) && format[i+1] == '\'' {
						literal.WriteByte('\'')
						i += 2
						continue
					}
					i++
					break
				}
				literal.WriteByte(format[i])
				i++
			}
			out.WriteString(emit(literal.String(), false))
			continue
		}
		start := i
		i++
		if (format[start] >= 'A' && format[start] <= 'Z') || (format[start] >= 'a' && format[start] <= 'z') {
			for i < len(format) && format[i] == format[start] {
				i++
			}
			out.WriteString(emit(format[start:i], true))
		} else {
			out.WriteString(emit(format[start:i], false))
		}
	}
	return out.String()
}
func templateDateLayout(format string) string {
	if format == "" {
		return "2006-01-02T15:04:05Z0700"
	}
	return templateDateTokens(format, func(t string, token bool) string {
		if !token {
			return t
		}
		switch t {
		case "yyyy", "yyy", "y":
			return "2006"
		case "yy":
			return "06"
		case "MMMM":
			return "January"
		case "MMM":
			return "Jan"
		case "MM":
			return "01"
		case "M":
			return "1"
		case "dd":
			return "02"
		case "d":
			return "2"
		case "HH", "H":
			return "15"
		case "hh":
			return "03"
		case "h":
			return "3"
		case "mm", "m":
			return "04"
		case "ss", "s":
			return "05"
		case "SSS":
			return "000"
		case "EEEE":
			return "Monday"
		case "EEE", "EE", "E":
			return "Mon"
		case "a":
			return "pm"
		case "Z", "ZZ":
			return "-0700"
		case "X":
			return "Z07"
		case "XX":
			return "Z0700"
		case "XXX":
			return "Z07:00"
		case "z", "zz", "zzz":
			return "MST"
		}
		return t
	})
}
func templateFormatDate(date time.Time, format string) string {
	if format == "" {
		return date.Format("2006-01-02T15:04:05Z07:00")
	}
	return templateDateTokens(format, func(token string, isToken bool) string {
		if !isToken {
			return token
		}
		var n int
		switch token[0] {
		case 'y':
			n = date.Year()
			if len(token) == 2 {
				n %= 100
			}
		case 'M':
			if len(token) >= 3 {
				if len(token) == 3 {
					return date.Format("Jan")
				}
				return date.Format("January")
			}
			n = int(date.Month())
		case 'd':
			n = date.Day()
		case 'H':
			n = date.Hour()
		case 'h':
			n = date.Hour() % 12
			if n == 0 {
				n = 12
			}
		case 'm':
			n = date.Minute()
		case 's':
			n = date.Second()
		case 'S':
			n = date.Nanosecond() / 1000000
		case 'E':
			if len(token) >= 4 {
				return date.Format("Monday")
			}
			return date.Format("Mon")
		case 'a':
			if date.Hour() < 12 {
				return "am"
			}
			return "pm"
		case 'Z':
			return date.Format("-0700")
		case 'X':
			if len(token) == 1 {
				return date.Format("Z07")
			}
			if len(token) == 2 {
				return date.Format("Z0700")
			}
			return date.Format("Z07:00")
		case 'z':
			return date.Format("MST")
		default:
			return token
		}
		if len(token) == 1 {
			return strconv.Itoa(n)
		}
		return fmt.Sprintf("%0*d", len(token), n)
	})
}
