// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"encoding/json"
	"fmt"
	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"math"
	"strings"
)

func jsonPathMatchDistance(expected any, actual string) float64 {
	nested, ok := expected.(map[string]any)
	if !ok {
		if matchJSONPath(expected, actual) {
			return 0
		}
		return 1
	}
	expression, _ := nested["expression"].(string)
	var document any
	if json.Unmarshal([]byte(actual), &document) != nil {
		return 1
	}
	value, err := evaluateMatcherJSONPath(expression, document)
	if err != nil {
		return 1
	}
	var values []string
	switch v := value.(type) {
	case nil:
	case []any:
		if _, absent := nested["absent"].(bool); absent && len(v) == 0 && matcherJSONPathDefinite(expression) {
			return ValueMatchDistance(nested, "[]", true)
		}
		if _, jsonEqual := nested["equalToJson"]; jsonEqual {
			values = append(values, matcherJSONValueString(v))
		} else {
			for _, item := range v {
				values = append(values, matcherJSONValueString(item))
			}
		}
	case map[string]any:
		values = append(values, matcherJSONValueString(v))
	default:
		values = append(values, matcherJSONValueString(v))
	}
	if len(values) == 0 {
		if _, absent := nested["absent"]; absent {
			return ValueMatchDistance(nested, "", false)
		}
		return 1
	}
	best := math.Inf(1)
	for _, s := range values {
		best = math.Min(best, ValueMatchDistance(nested, s, true))
	}
	return best
}
func xpathMatchDistance(expected any, p map[string]any, actual string) float64 {
	nested, ok := expected.(map[string]any)
	if !ok || !xpathHasValueMatcher(nested) {
		if matchXPath(expected, p, actual) {
			return 0
		}
		return 1
	}
	expression, _ := nested["expression"].(string)
	doc, err := xmlquery.Parse(strings.NewReader(actual))
	if err != nil {
		return 1
	}
	ns := map[string]string{}
	for _, options := range []map[string]any{p, nested} {
		if values, ok := options["xPathNamespaces"].(map[string]any); ok {
			for k, v := range values {
				if s, ok := v.(string); ok {
					ns[k] = s
				}
			}
		}
	}
	compiled, err := xpath.CompileWithNS(expression, ns)
	if err != nil {
		return 1
	}
	evaluation := compiled.Evaluate(xmlquery.CreateXPathNavigator(doc))
	if _, ok := evaluation.(*xpath.NodeIterator); !ok {
		return ValueMatchDistance(nested, fmt.Sprint(evaluation), true)
	}
	nodes := xmlquery.QuerySelectorAll(doc, compiled)
	if len(nodes) == 0 {
		return 1
	}
	best := math.Inf(1)
	for _, node := range nodes {
		rendered := node.InnerText()
		if node.Type == xmlquery.ElementNode || node.Type == xmlquery.DocumentNode {
			rendered = strings.TrimPrefix(node.OutputXMLWithOptions(xmlquery.WithOutputSelf(), xmlquery.WithIndentation("  "), xmlquery.WithEmptyTagSupport()), "\n") + "\n"
		}
		best = math.Min(best, ValueMatchDistance(nested, rendered, true))
	}
	return best
}
