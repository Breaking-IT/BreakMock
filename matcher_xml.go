// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	"github.com/antchfx/xmlquery"
	"sort"
	"strings"
)

func matchXML(expected, actual string, options map[string]any) bool {
	a, err := xmlquery.Parse(strings.NewReader(expected))
	if err != nil {
		return false
	}
	b, err := xmlquery.Parse(strings.NewReader(actual))
	if err != nil {
		return false
	}
	exempt := map[string]bool{}
	if raw, ok := options["exemptedComparisons"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				exempt[s] = true
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
	valueEqual := func(a, b string) bool {
		return a == b || placeholders && matchRegex(opening+`xmlunit\.ignore`+closing, a)
	}
	name := func(n *xmlquery.Node) string {
		if mode == "NONE" && n.Prefix != "" {
			return n.Prefix + ":" + n.Data
		}
		return n.Data
	}
	attrs := func(n *xmlquery.Node) map[string]string {
		out := map[string]string{}
		for _, a := range n.Attr {
			if mode != "NONE" && (a.Name.Local == "xmlns" || a.Name.Space == "xmlns") {
				continue
			}
			key := a.Name.Local
			if mode == "NONE" || mode == "LEGACY" {
				if a.Name.Space != "" {
					key = a.Name.Space + ":" + key
				}
			} else if !exempt["NAMESPACE_URI"] {
				key = a.NamespaceURI + ":" + key
			}
			out[key] = a.Value
		}
		return out
	}
	children := func(n *xmlquery.Node) []*xmlquery.Node {
		var out []*xmlquery.Node
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xmlquery.CommentNode || c.Type == xmlquery.DeclarationNode || c.Type == xmlquery.NotationNode {
				continue
			}
			if (c.Type == xmlquery.TextNode || c.Type == xmlquery.CharDataNode) && strings.TrimSpace(c.Data) == "" {
				continue
			}
			out = append(out, c)
		}
		ignoreSame, _ := options["ignoreOrderOfSameNode"].(bool)
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Data == out[j].Data && ignoreSame {
				return out[i].InnerText() < out[j].InnerText()
			}
			if out[i].Type != xmlquery.ElementNode || out[j].Type != xmlquery.ElementNode {
				return false
			}
			return out[i].Data < out[j].Data
		})
		return out
	}
	var equal func(*xmlquery.Node, *xmlquery.Node) bool
	equal = func(a, b *xmlquery.Node) bool {
		at, bt := a.Type, b.Type
		if at != bt && !exempt["NODE_TYPE"] {
			return false
		}
		if at == xmlquery.TextNode || at == xmlquery.CharDataNode {
			return exempt["TEXT_VALUE"] || valueEqual(strings.TrimSpace(a.Data), strings.TrimSpace(b.Data))
		}
		if at == xmlquery.ElementNode {
			if name(a) != name(b) && !exempt["ELEMENT_TAG_NAME"] {
				return false
			}
			if mode != "NONE" && mode != "LEGACY" && a.NamespaceURI != b.NamespaceURI && !exempt["NAMESPACE_URI"] {
				return false
			}
			aa, ba := attrs(a), attrs(b)
			if len(aa) != len(ba) && !exempt["ELEMENT_NUM_ATTRIBUTES"] {
				return false
			}
			for key, av := range aa {
				bv, ok := ba[key]
				if !ok {
					if exempt["ATTR_NAME_LOOKUP"] {
						continue
					}
					return false
				}
				if !exempt["ATTR_VALUE"] && !valueEqual(av, bv) {
					return false
				}
			}
		}
		ac, bc := children(a), children(b)
		if len(ac) != len(bc) && !exempt["CHILD_NODELIST_LENGTH"] {
			return false
		}
		for i := 0; i < len(ac) && i < len(bc); i++ {
			if !equal(ac[i], bc[i]) {
				return false
			}
		}
		return true
	}
	return equal(a, b)
}
