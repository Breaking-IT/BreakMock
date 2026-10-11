// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
package breakmock

import (
	raymond "github.com/Breaking-IT/BreakMock/internal/handlebars"
	"github.com/antchfx/xmlquery"
	"strings"
	"time"
)

func templateXPath(o *raymond.Options, prefix string) any {
	doc, e := xmlquery.Parse(strings.NewReader(o.ParamStr(0)))
	if e != nil {
		return ""
	}
	nodes, e := xmlquery.QueryAll(doc, prefix+o.ParamStr(1))
	if e != nil {
		return ""
	}
	values := templateXMLValues{}
	for _, node := range nodes {
		values = append(values, templateXMLNode{node})
	}
	return values
}
func templateFormatXML(o *raymond.Options) any {
	src := o.ParamStr(0)
	if len(o.Params()) == 0 {
		src = o.Fn()
	}
	doc, e := xmlquery.Parse(strings.NewReader(src))
	if e != nil {
		return ""
	}
	removeTemplateBlankXML(doc)
	var out strings.Builder
	for child := doc.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xmlquery.DeclarationNode || child.Type == xmlquery.TextNode {
			continue
		}
		opts := []xmlquery.OutputOption{xmlquery.WithOutputSelf(), xmlquery.WithEmptyTagSupport()}
		if o.HashStr("format") != "compact" {
			opts = append(opts, xmlquery.WithIndentation("  "))
		}
		out.WriteString(strings.TrimPrefix(child.OutputXMLWithOptions(opts...), "\n"))
		if o.HashStr("format") != "compact" {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

type templateDateValue struct {
	time.Time
	explicitZone bool
}

func (v templateDateValue) String() string {
	t := v.Time
	if !v.explicitZone {
		t = t.UTC()
	}
	return t.Format("2006-01-02T15:04:05Z07:00")
}
func templateParseDate(o *raymond.Options) any {
	format := o.HashStr("format")
	layouts := []string{templateDateLayout(format)}
	if format == "" {
		layouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02", "2006-01-02 15:04:05", time.RFC1123, time.RFC1123Z}
	}
	for _, layout := range layouts {
		t, e := time.Parse(layout, o.ParamStr(0))
		if e == nil {
			return templateDateValue{Time: t, explicitZone: strings.Contains(layout, "07")}
		}
	}
	return nil
}
func templateTruncateDate(o *raymond.Options) any {
	var date templateDateValue
	switch v := o.Param(0).(type) {
	case templateDateValue:
		date = v
	case time.Time:
		date = templateDateValue{Time: v}
	default:
		return ""
	}
	t := date.Time
	if !date.explicitZone {
		t = t.In(time.Local)
	}
	year, month, day := t.Date()
	hour, minute, second := t.Clock()
	switch strings.ToLower(strings.ReplaceAll(o.ParamStr(1), "_", " ")) {
	case "first second of minute":
		second = 0
	case "first minute of hour":
		minute = 0
		second = 0
	case "first hour of day":
		hour = 0
		minute = 0
		second = 0
	case "first day of month":
		day = 1
		hour = 0
		minute = 0
		second = 0
	case "first day of next month":
		month++
		day = 1
		hour = 0
		minute = 0
		second = 0
	case "last day of month":
		month++
		day = 0
		hour = 0
		minute = 0
		second = 0
	case "first day of year":
		month = 1
		day = 1
		hour = 0
		minute = 0
		second = 0
	case "first day of next year":
		year++
		month = 1
		day = 1
		hour = 0
		minute = 0
		second = 0
	case "last day of year":
		month = 12
		day = 31
		hour = 0
		minute = 0
		second = 0
	default:
		return ""
	}
	return templateDateValue{Time: time.Date(year, month, day, hour, minute, second, 0, t.Location()), explicitZone: true}
}

func removeTemplateBlankXML(node *xmlquery.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == xmlquery.TextNode && strings.TrimSpace(child.Data) == "" {
			xmlquery.RemoveFromTree(child)
		} else {
			removeTemplateBlankXML(child)
		}
		child = next
	}
}
func isTemplateParsedDate(v any) bool { _, ok := v.(templateDateValue); return ok }
