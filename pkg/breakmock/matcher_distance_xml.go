// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"github.com/antchfx/xmlquery"
	"strings"
)

// XMLUnit distance compares document-order nodes (the exact-match path uses a
// different, order-invariant node matcher) and counts only selected comparisons.
func xmlMatchDistance(options map[string]any, actual string) float64 {
	expected, ok := options["equalToXml"].(string)
	if !ok || actual == "" {
		return 1
	}
	a, err := xmlquery.Parse(strings.NewReader(expected))
	if err != nil {
		return 1
	}
	b, err := xmlquery.Parse(strings.NewReader(actual))
	if err != nil {
		return 1
	}
	exempt := map[string]bool{}
	if values, ok := options["exemptedComparisons"].([]any); ok {
		for _, v := range values {
			if s, ok := v.(string); ok {
				exempt[s] = true
			}
		}
	}
	count, different := 0, 0
	compare := func(kind string, equal bool) {
		if !exempt[kind] {
			count++
			if !equal {
				different++
			}
		}
	}
	mode, _ := options["namespaceAwareness"].(string)
	placeholders, _ := options["enablePlaceholders"].(bool)
	opening, _ := options["placeholderOpeningDelimiterRegex"].(string)
	if opening == "" {
		opening = `\$\{`
	}
	closing, _ := options["placeholderClosingDelimiterRegex"].(string)
	if closing == "" {
		closing = `\}`
	}
	valueEqual := func(x, y string) bool {
		return x == y || placeholders && matchRegex(opening+`xmlunit\.ignore`+closing, x)
	}
	children := func(n *xmlquery.Node) []*xmlquery.Node {
		var values []*xmlquery.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xmlquery.CommentNode || c.Type == xmlquery.DeclarationNode {
				continue
			}
			if (c.Type == xmlquery.TextNode || c.Type == xmlquery.CharDataNode) && strings.TrimSpace(c.Data) == "" {
				continue
			}
			values = append(values, c)
		}
		return values
	}
	attrs := func(n *xmlquery.Node) map[string]xmlquery.Attr {
		out := map[string]xmlquery.Attr{}
		for _, a := range n.Attr {
			if mode != "NONE" && a.NamespaceURI == "http://www.w3.org/2001/XMLSchema-instance" && (a.Name.Local == "schemaLocation" || a.Name.Local == "noNamespaceSchemaLocation") {
				continue
			}
			if mode != "NONE" && (a.Name.Local == "xmlns" || a.Name.Space == "xmlns") {
				continue
			}
			key := a.Name.Local
			if mode == "NONE" {
				key = a.Name.Space + ":" + key
			} else {
				key = a.NamespaceURI + ":" + key
			}
			out[key] = a
		}
		return out
	}
	name := func(n *xmlquery.Node) string {
		if mode == "NONE" && n.Prefix != "" {
			return n.Prefix + ":" + n.Data
		}
		return n.Data
	}
	var walk func(*xmlquery.Node, *xmlquery.Node)
	walk = func(a, b *xmlquery.Node) {
		compare("NODE_TYPE", a.Type == b.Type)
		if mode != "NONE" && a.NamespaceURI != "" {
			compare("NAMESPACE_URI", a.NamespaceURI == b.NamespaceURI)
		}
		ac, bc := children(a), children(b)
		compare("CHILD_NODELIST_LENGTH", len(ac) == len(bc))
		if a.Type == xmlquery.ElementNode && b.Type == xmlquery.ElementNode {
			compare("ELEMENT_TAG_NAME", name(a) == name(b))
			if mode != "NONE" {
				for _, attr := range a.Attr {
					if attr.NamespaceURI == "http://www.w3.org/2001/XMLSchema-instance" {
						if attr.Name.Local == "schemaLocation" {
							compare("SCHEMA_LOCATION", xmlSchemaAttribute(b, "schemaLocation") == attr.Value)
						}
						if attr.Name.Local == "noNamespaceSchemaLocation" {
							compare("NO_NAMESPACE_SCHEMA_LOCATION", xmlSchemaAttribute(b, "noNamespaceSchemaLocation") == attr.Value)
						}
					}
				}
			}
			aa, ba := attrs(a), attrs(b)
			compare("ELEMENT_NUM_ATTRIBUTES", len(aa) == len(ba))
			for key, av := range aa {
				bv, exists := ba[key]
				compare("ATTR_NAME_LOOKUP", exists)
				if exists {
					compare("NODE_TYPE", true)
					if mode != "NONE" && av.NamespaceURI != "" {
						compare("NAMESPACE_URI", av.NamespaceURI == bv.NamespaceURI)
					}
					compare("ATTR_VALUE", valueEqual(av.Value, bv.Value))
				}
			}
		} else if a.Type == xmlquery.ProcessingInstruction && b.Type == xmlquery.ProcessingInstruction {
			compare("PROCESSING_INSTRUCTION_TARGET", a.ProcInst.Target == b.ProcInst.Target)
			compare("PROCESSING_INSTRUCTION_DATA", a.ProcInst.Inst == b.ProcInst.Inst)
		} else if (a.Type == xmlquery.TextNode || a.Type == xmlquery.CharDataNode) && (b.Type == xmlquery.TextNode || b.Type == xmlquery.CharDataNode) {
			compare("TEXT_VALUE", valueEqual(strings.TrimSpace(a.Data), strings.TrimSpace(b.Data)))
		}
		// XMLUnit pairs children by node type, retaining order within each type.
		used := make([]bool, len(bc))
		for _, x := range ac {
			found := -1
			for j, y := range bc {
				if !used[j] && (x.Type == y.Type || (x.Type == xmlquery.TextNode && y.Type == xmlquery.CharDataNode) || (x.Type == xmlquery.CharDataNode && y.Type == xmlquery.TextNode)) {
					found = j
					break
				}
			}
			if found < 0 {
				compare("CHILD_LOOKUP", false)
			} else {
				used[found] = true
				walk(x, bc[found])
			}
		}
	}
	walk(a, b)
	if count == 0 {
		return 0
	}
	return float64(different) / float64(count)
}

func xmlSchemaAttribute(n *xmlquery.Node, name string) string {
	for _, a := range n.Attr {
		if a.NamespaceURI == "http://www.w3.org/2001/XMLSchema-instance" && a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
