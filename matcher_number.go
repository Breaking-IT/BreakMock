// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var numberOperators = []string{"equalToNumber", "greaterThanNumber", "greaterThanEqualNumber", "lessThanNumber", "lessThanEqualNumber"}

func javaDoubleParse(s string) (float64, error) {
	s = strings.TrimFunc(s, func(r rune) bool { return r <= 0x20 })
	switch s {
	case "NaN", "+NaN", "-NaN":
		return math.NaN(), nil
	case "Infinity", "+Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	}
	if strings.Contains(s, "_") || strings.Contains(strings.ToLower(s), "nan") || strings.Contains(strings.ToLower(s), "inf") {
		return 0, fmt.Errorf("invalid Java double")
	}
	if len(s) > 1 && strings.ContainsRune("dDfF", rune(s[len(s)-1])) {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if math.IsInf(n, 0) {
		return n, nil
	}
	return n, err
}
func numberPatternMatch(op string, expected any, actual string) (bool, float64) {
	e, ee := javaDoubleParse(fmt.Sprint(expected))
	a, ae := javaDoubleParse(actual)
	if ee != nil || ae != nil {
		return false, 1
	}
	matched := false
	switch op {
	case "equalToNumber":
		matched = a == e
	case "greaterThanNumber":
		matched = a > e
	case "greaterThanEqualNumber":
		matched = a >= e
	case "lessThanNumber":
		matched = a < e
	case "lessThanEqualNumber":
		matched = a <= e
	}
	if matched {
		return true, 0
	}
	shift := 0.0
	if e < 0 || a < 0 {
		shift = math.Min(e, a)
	}
	e += shift
	a += shift
	if math.IsInf(e, 1) || math.IsInf(a, 1) || (e == math.MaxFloat64 && a == math.MaxFloat64) {
		return false, 1
	}
	distance := math.Abs(math.Log(e)-math.Log(a)) / 176
	if math.IsNaN(distance) {
		distance = 0
	} else {
		distance = math.Floor(math.Min(distance, 1)*100+0.5) / 100
	}
	if distance == 0 {
		distance = .01
	}
	return false, distance
}
func matchRequestMethod(pattern any, actual string) bool {
	if method, ok := pattern.(string); ok {
		return method == "ANY" || method == actual
	}
	if p, ok := pattern.(map[string]any); ok {
		for _, kind := range []string{"oneOf", "noneOf"} {
			if values, ok := p[kind].([]any); ok {
				found := false
				for _, value := range values {
					found = found || value == actual
				}
				if kind == "noneOf" {
					return !found
				}
				return found
			}
		}
	}
	return pattern == nil
}
