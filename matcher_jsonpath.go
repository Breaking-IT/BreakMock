// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/ohler55/ojg/jp"
)

type jsonPathFloat float64
type missingJSONPath struct{}

// evaluateMatcherJSONPath uses OjG for path navigation and evaluates filter
// expressions separately because Jayway's coercions and operators differ.
func evaluateMatcherJSONPath(expression string, document any) (any, error) {
	for _, name := range []string{"length", "size", "min", "max", "sum", "avg", "stddev", "first", "last"} {
		suffix := "." + name + "()"
		if strings.HasSuffix(expression, suffix) {
			value, err := evaluateMatcherJSONPath(strings.TrimSuffix(expression, suffix), document)
			if err != nil {
				return nil, err
			}
			return jsonPathFunction(name, value)
		}
	}
	normalized, filters, err := extractJSONPathFilters(expression)
	if err != nil {
		return nil, err
	}
	path, err := jp.ParseString(normalized)
	if err != nil {
		return nil, err
	}
	filterIndex := 0
	var walk func(jp.Expr, []any) []any
	walk = func(rest jp.Expr, values []any) []any {
		index := -1
		for i, fragment := range rest {
			if _, ok := fragment.(*jp.Filter); ok {
				index = i
				break
			}
		}
		if index < 0 {
			var output []any
			for _, value := range values {
				if len(rest) == 0 {
					output = append(output, value)
				} else {
					output = append(output, rest.Get(value)...)
				}
			}
			return output
		}
		prefix := rest[:index]
		var candidates []any
		for _, value := range values {
			if len(prefix) == 0 {
				candidates = append(candidates, value)
			} else {
				candidates = append(candidates, prefix.Get(value)...)
			}
		}
		filter := filters[filterIndex]
		filterIndex++
		var selected []any
		for _, candidate := range candidates {
			items, ok := candidate.([]any)
			if !ok {
				items = []any{candidate}
			}
			for _, item := range items {
				if evalJSONPathPredicate(filter, item, document) {
					selected = append(selected, item)
				}
			}
		}
		return walk(rest[index+1:], selected)
	}
	values := walk(path, []any{document})
	if !path.Normal() {
		return values, nil
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values[0], nil
}
func extractJSONPathFilters(expression string) (string, []string, error) {
	var out strings.Builder
	var filters []string
	for i := 0; i < len(expression); {
		if strings.HasPrefix(expression[i:], "[?(") {
			start := i + 3
			depth := 1
			quote := byte(0)
			escaped := false
			j := start
			for ; j < len(expression); j++ {
				c := expression[j]
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if quote != 0 {
					if c == quote {
						quote = 0
					}
					continue
				}
				if c == '\'' || c == '"' || c == '/' {
					quote = c
					continue
				}
				if c == '(' {
					depth++
				}
				if c == ')' {
					depth--
					if depth == 0 {
						break
					}
				}
			}
			if depth != 0 || j+1 >= len(expression) || expression[j+1] != ']' {
				return "", nil, fmt.Errorf("invalid JSONPath filter")
			}
			filters = append(filters, expression[start:j])
			out.WriteString("[?(@)]")
			i = j + 2
		} else {
			out.WriteByte(expression[i])
			i++
		}
	}
	return out.String(), filters, nil
}
func jsonPathFunction(name string, value any) (any, error) {
	if name == "length" || name == "size" {
		switch v := value.(type) {
		case []any:
			return len(v), nil
		case map[string]any:
			return len(v), nil
		case string:
			return len([]rune(v)), nil
		}
		return nil, fmt.Errorf("length on scalar")
	}
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("function requires nonempty array")
	}
	if name == "first" {
		return values[0], nil
	}
	if name == "last" {
		return values[len(values)-1], nil
	}
	sum, minValue, maxValue := 0.0, math.Inf(1), math.Inf(-1)
	numbers := []float64{}
	for _, v := range values {
		if n, ok := matcherNumber(v); ok {
			f, _ := n.Float64()
			numbers = append(numbers, f)
			sum += f
			minValue = math.Min(minValue, f)
			maxValue = math.Max(maxValue, f)
		}
	}
	if len(numbers) == 0 {
		return nil, fmt.Errorf("numeric function requires numbers")
	}
	switch name {
	case "min":
		return jsonPathFloat(minValue), nil
	case "max":
		return jsonPathFloat(maxValue), nil
	case "sum":
		return jsonPathFloat(sum), nil
	case "avg":
		return jsonPathFloat(sum / float64(len(numbers))), nil
	case "stddev":
		mean := sum / float64(len(numbers))
		total := 0.0
		for _, n := range numbers {
			total += (n - mean) * (n - mean)
		}
		return jsonPathFloat(math.Sqrt(total / float64(len(numbers)))), nil
	}
	return nil, fmt.Errorf("unknown function")
}
func evalJSONPathPredicate(expression string, item, root any) bool {
	expression = strings.TrimSpace(expression)
	for strings.HasPrefix(expression, "(") && strings.HasSuffix(expression, ")") && matchingOuterParens(expression) {
		expression = strings.TrimSpace(expression[1 : len(expression)-1])
	}
	for _, operator := range []string{"||", "&&"} {
		if index := jsonPathTopLevelOperator(expression, operator); index >= 0 {
			left := evalJSONPathPredicate(expression[:index], item, root)
			if operator == "||" {
				return left || evalJSONPathPredicate(expression[index+len(operator):], item, root)
			}
			return left && evalJSONPathPredicate(expression[index+len(operator):], item, root)
		}
	}
	if strings.HasPrefix(expression, "!") {
		return !evalJSONPathPredicate(strings.TrimSpace(expression[1:]), item, root)
	}
	for _, operator := range []string{"==", "!=", "<=", ">=", "=~", "<", ">", " nin ", " in ", " subsetof ", " anyof ", " noneof ", " size ", " empty "} {
		if index := jsonPathTopLevelOperator(expression, operator); index >= 0 {
			left := jsonPathOperand(strings.TrimSpace(expression[:index]), item, root)
			right := jsonPathOperand(strings.TrimSpace(expression[index+len(operator):]), item, root)
			if _, missing := left.(missingJSONPath); missing {
				return operator == "!="
			}
			if _, missing := right.(missingJSONPath); missing {
				return false
			}
			switch strings.TrimSpace(operator) {
			case "==":
				return jsonPathEqual(left, right)
			case "!=":
				return !jsonPathEqual(left, right)
			case "=~":
				regex, ok := right.(jsonPathRegex)
				return ok && matchRegex(string(regex), fmt.Sprint(left))
			case "in", "nin":
				values, ok := right.([]any)
				if !ok {
					return false
				}
				found := false
				for _, v := range values {
					found = found || jsonPathEqual(left, v)
				}
				if strings.TrimSpace(operator) == "nin" {
					return !found
				}
				return found
			case "subsetof", "anyof", "noneof":
				a, aok := left.([]any)
				b, bok := right.([]any)
				if !aok || !bok {
					return false
				}
				count := 0
				for _, v := range a {
					for _, w := range b {
						if jsonPathEqual(v, w) {
							count++
							break
						}
					}
				}
				switch strings.TrimSpace(operator) {
				case "subsetof":
					return count == len(a)
				case "anyof":
					return count > 0
				default:
					return count == 0
				}
			case "size":
				v, err := jsonPathFunction("size", left)
				return err == nil && jsonPathEqual(v, right)
			case "empty":
				v, err := jsonPathFunction("size", left)
				expected, ok := right.(bool)
				return err == nil && ok && ((v.(int) == 0) == expected)
			default:
				comparison, ok := jsonPathCompare(left, right)
				if !ok {
					return false
				}
				switch operator {
				case "<":
					return comparison < 0
				case ">":
					return comparison > 0
				case "<=":
					return comparison <= 0
				case ">=":
					return comparison >= 0
				}
			}
		}
	}
	value := jsonPathOperand(expression, item, root)
	if _, missing := value.(missingJSONPath); missing {
		return false
	}
	if strings.HasPrefix(expression, "@") || strings.HasPrefix(expression, "$") {
		return true
	}
	b, ok := value.(bool)
	return ok && b
}
func matchingOuterParens(expression string) bool {
	depth := 0
	for i, c := range expression {
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
			if depth == 0 && i != len(expression)-1 {
				return false
			}
		}
	}
	return depth == 0
}
func jsonPathTopLevelOperator(expression, operator string) int {
	depth := 0
	quote := byte(0)
	escaped := false
	for i := 0; i < len(expression); i++ {
		c := expression[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' || c == '/' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			depth--
			continue
		}
		if depth == 0 && strings.HasPrefix(expression[i:], operator) {
			return i
		}
	}
	return -1
}

type jsonPathRegex string

func jsonPathOperand(expression string, item, root any) any {
	expression = strings.TrimSpace(expression)
	if strings.HasPrefix(expression, "/") {
		last := strings.LastIndex(expression, "/")
		if last > 0 {
			pattern := expression[1:last]
			flags := expression[last+1:]
			if flags != "" {
				pattern = "(?" + flags + ")" + pattern
			}
			return jsonPathRegex(pattern)
		}
	}
	if len(expression) >= 2 && ((expression[0] == '\'' && expression[len(expression)-1] == '\'') || (expression[0] == '"' && expression[len(expression)-1] == '"')) {
		if expression[0] == '"' {
			var s string
			if json.Unmarshal([]byte(expression), &s) == nil {
				return s
			}
		}
		return strings.ReplaceAll(strings.ReplaceAll(expression[1:len(expression)-1], `\'`, `'`), `\\`, `\`)
	}
	if strings.HasPrefix(expression, "[") && strings.HasSuffix(expression, "]") {
		rest := expression[1 : len(expression)-1]
		var values []any
		for strings.TrimSpace(rest) != "" {
			index := jsonPathTopLevelOperator(rest, ",")
			if index < 0 {
				values = append(values, jsonPathOperand(rest, item, root))
				break
			}
			values = append(values, jsonPathOperand(rest[:index], item, root))
			rest = rest[index+1:]
		}
		return values
	}
	if strings.HasPrefix(expression, "@") || strings.HasPrefix(expression, "$") {
		source := root
		if expression[0] == '@' {
			source = item
			expression = "$" + expression[1:]
		}
		value, err := evaluateMatcherJSONPath(expression, source)
		if err != nil || value == nil {
			if path, parseErr := jp.ParseString(expression); parseErr == nil && path.Normal() && len(path.Get(source)) > 0 {
				return nil
			}
			if expression == "$" && source == nil {
				return nil
			}
			return missingJSONPath{}
		}
		return value
	}
	switch expression {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if n, err := strconv.ParseFloat(expression, 64); err == nil {
		return n
	}
	return missingJSONPath{}
}
func jsonPathEqual(a, b any) bool {
	if x, ok := matcherNumber(a); ok {
		if s, ok := b.(string); ok {
			n, valid := newJSONPathNumber(s)
			return valid && x.Cmp(n) == 0
		}
		y, valid := matcherNumber(b)
		return valid && x.Cmp(y) == 0
	}
	if _, ok := matcherNumber(b); ok {
		return jsonPathEqual(b, a)
	}
	return matchJSON(a, b, false, false)
}
func newJSONPathNumber(s string) (*big.Rat, bool) { return new(big.Rat).SetString(s) }
func jsonPathCompare(a, b any) (int, bool) {
	if x, ok := matcherNumber(a); ok {
		y, valid := matcherNumber(b)
		if !valid {
			return 0, false
		}
		return x.Cmp(y), true
	}
	x, xok := a.(string)
	y, yok := b.(string)
	if xok && yok {
		return strings.Compare(x, y), true
	}
	return 0, false
}

func matcherJSONPathDefinite(expression string) bool {
	path, err := jp.ParseString(expression)
	return err == nil && path.Normal()
}
