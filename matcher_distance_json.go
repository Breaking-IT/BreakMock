// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

// jsonDeepSize mirrors WireMock Json.deepSize, including an additional node
// count for container children.
func jsonDeepSize(v any) int {
	size := 1
	switch x := v.(type) {
	case map[string]any:
		for _, c := range x {
			size++
			switch c.(type) {
			case map[string]any, []any:
				size += jsonDeepSize(c)
			}
		}
	case []any:
		for _, c := range x {
			size++
			switch c.(type) {
			case map[string]any, []any:
				size += jsonDeepSize(c)
			}
		}
	}
	return size
}
func jsonDiffSize(expected, actual any, unordered, extras bool) int {
	if matchJSON(expected, actual, unordered, extras) {
		return 0
	}
	switch e := expected.(type) {
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok {
			break
		}
		n := 0
		for k, v := range e {
			if got, exists := a[k]; exists {
				n += jsonDiffSize(v, got, unordered, extras)
			} else if !matchJSON(v, nil, unordered, extras) {
				n += jsonDeepSize(v)
			}
		}
		if !extras {
			for k, v := range a {
				if _, exists := e[k]; !exists {
					n += jsonDeepSize(v)
				}
			}
		}
		return n
	case []any:
		a, ok := actual.([]any)
		if !ok {
			break
		}
		n := 0
		if unordered {
			// JsonUnit first pairs exact members, then compares remaining members.
			used := make([]bool, len(a))
			left := []any{}
			for _, v := range e {
				found := -1
				for j, w := range a {
					if !used[j] && matchJSON(v, w, unordered, extras) {
						found = j
						break
					}
				}
				if found < 0 {
					left = append(left, v)
				} else {
					used[found] = true
				}
			}
			right := []any{}
			for j, v := range a {
				if !used[j] {
					right = append(right, v)
				}
			}
			e, a = left, right
			if len(e) > 1 || len(a) > 1 {
				for _, v := range e {
					n += jsonDeepSize(v)
				}
				if !extras {
					for _, v := range a {
						n += jsonDeepSize(v)
					}
				}
				return n
			}
		}
		for i := 0; i < min(len(e), len(a)); i++ {
			n += jsonDiffSize(e[i], a[i], unordered, extras)
		}
		for i := len(a); i < len(e); i++ {
			n += jsonDeepSize(e[i])
		}
		if !extras {
			for i := len(e); i < len(a); i++ {
				n += jsonDeepSize(a[i])
			}
		}
		return n
	}
	return max(jsonDeepSize(expected), jsonDeepSize(actual))
}
func jsonMatchDistance(p map[string]any, actual string) float64 {
	expected := p["equalToJson"]
	if text, ok := expected.(string); ok {
		if decodeMatcherJSON([]byte(text), &expected) != nil {
			return 1
		}
	}
	var got any
	if decodeMatcherJSON([]byte(actual), &got) != nil {
		return 1
	}
	unordered, _ := p["ignoreArrayOrder"].(bool)
	extras, _ := p["ignoreExtraElements"].(bool)
	return float64(jsonDiffSize(expected, got, unordered, extras)) / float64(max(jsonDeepSize(expected), jsonDeepSize(got)))
}
