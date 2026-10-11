use super::*;
use roxmltree::{Document, Node};
fn exempt(p: &Value, key: &str) -> bool {
    p["exemptedComparisons"]
        .as_array()
        .map(|v| v.iter().any(|v| v.as_str() == Some(key)))
        .unwrap_or(false)
}
fn same_text(w: &str, g: &str, p: &Value) -> bool {
    if flag(p, "enablePlaceholders") {
        let open = p["placeholderOpeningDelimiterRegex"]
            .as_str()
            .unwrap_or("\\$\\{");
        let close = p["placeholderClosingDelimiterRegex"]
            .as_str()
            .unwrap_or("\\}");
        let expression = format!("{}xmlunit.ignore{}", open, close);
        if regex::Regex::new(&expression)
            .map(|r| r.is_match(w))
            .unwrap_or(false)
        {
            return true;
        }
    }
    w.trim() == g.trim()
}
fn children<'a, 'input>(n: Node<'a, 'input>) -> Vec<Node<'a, 'input>> {
    n.children()
        .filter(|n| {
            n.is_element()
                || n.is_pi()
                || (n.is_text() && !n.text().unwrap_or("").trim().is_empty())
        })
        .collect()
}
fn equal(w: Node<'_, '_>, g: Node<'_, '_>, p: &Value) -> bool {
    if w.node_type() != g.node_type() {
        return false;
    }
    if w.is_text() {
        if !exempt(p, "NODE_TYPE")
            && w.document().input_text()[w.range()].starts_with("<![CDATA[")
                != g.document().input_text()[g.range()].starts_with("<![CDATA[")
        {
            return false;
        }
        return exempt(p, "TEXT_VALUE")
            || same_text(w.text().unwrap_or(""), g.text().unwrap_or(""), p);
    }
    if w.is_pi() {
        let a = w.pi().unwrap();
        let b = g.pi().unwrap();
        return a.target == b.target
            && (exempt(p, "PROCESSING_INSTRUCTION_DATA") || a.value == b.value);
    }
    if w.is_element() {
        if !exempt(p, "ELEMENT_TAG_NAME") && w.tag_name().name() != g.tag_name().name() {
            return false;
        }
        if !exempt(p, "NAMESPACE_URI") && w.tag_name().namespace() != g.tag_name().namespace() {
            return false;
        }
        let attrs = |n: Node<'_, '_>| {
            n.attributes()
                .filter(|a| {
                    !(a.namespace() == Some("http://www.w3.org/2001/XMLSchema-instance")
                        && ((a.name() == "schemaLocation" && exempt(p, "SCHEMA_LOCATION"))
                            || (a.name() == "noNamespaceSchemaLocation"
                                && exempt(p, "NO_NAMESPACE_SCHEMA_LOCATION"))))
                })
                .map(|a| {
                    (
                        (a.namespace().unwrap_or("").to_owned(), a.name().to_owned()),
                        a.value().to_owned(),
                    )
                })
                .collect::<HashMap<_, _>>()
        };
        let a = attrs(w);
        let b = attrs(g);
        if !exempt(p, "ELEMENT_NUM_ATTRIBUTES") && a.len() != b.len() {
            return false;
        }
        for (k, v) in a {
            if !exempt(p, "ATTR_VALUE") && !b.get(&k).map(|s| same_text(&v, s, p)).unwrap_or(false)
            {
                return false;
            }
        }
    }
    let a = children(w);
    let b = children(g);
    if a.len() != b.len() {
        return false;
    }
    if a.len() > 1 && a.iter().any(Node::is_pi) {
        return false;
    }
    let mut a = a;
    let mut b = b;
    let key = |n: &Node<'_, '_>| {
        if n.is_element() {
            n.tag_name().name().to_owned()
        } else {
            String::new()
        }
    };
    a.sort_by_key(key);
    b.sort_by_key(key);
    if flag(p, "ignoreOrderOfSameNode") {
        let key = |n: &Node<'_, '_>| {
            (
                n.tag_name().name().to_owned(),
                n.text().unwrap_or("").to_owned(),
            )
        };
        a.sort_by_key(key);
        b.sort_by_key(key)
    }
    a.iter().zip(b).all(|(a, b)| equal(*a, b, p))
}
pub fn xml_match(w: &str, g: &str, p: &Value) -> bool {
    let (Ok(w), Ok(g)) = (Document::parse(w), Document::parse(g)) else {
        return false;
    };
    equal(w.root(), g.root(), p)
}
pub fn xpath_match(p: &Value, a: &str, options: &Value) -> bool {
    let expression = p
        .as_str()
        .or_else(|| p["expression"].as_str())
        .unwrap_or("");
    let Ok(package) = sxd_document::parser::parse(a) else {
        return false;
    };
    let factory = sxd_xpath::Factory::new();
    let Ok(Some(path)) = factory.build(expression) else {
        return false;
    };
    let mut ctx = sxd_xpath::Context::new();
    for source in [options, p] {
        if let Some(ns) = source["xPathNamespaces"].as_object() {
            for (k, v) in ns {
                if let Some(v) = v.as_str() {
                    ctx.set_namespace(k, v)
                }
            }
        }
    }
    let Ok(result) = path.evaluate(&ctx, package.as_document().root()) else {
        return false;
    };
    match result {
        sxd_xpath::Value::Nodeset(nodes) => {
            if p.is_string() {
                return nodes.size() > 0;
            }
            nodes.document_order().iter().any(|n| {
                let s = n.string_value();
                value_matches(p, &s)
                    || n.element()
                        .map(|el| {
                            let mut out = String::new();
                            render_element(el, &mut out);
                            out.push('\n');
                            value_matches(p, &out)
                        })
                        .unwrap_or(false)
            })
        }
        v => p.is_string() || value_matches(p, &v.string()),
    }
}
fn render_element(el: sxd_document::dom::Element<'_>, out: &mut String) {
    render_element_depth(el, out, 0)
}
fn render_element_depth(el: sxd_document::dom::Element<'_>, out: &mut String, depth: usize) {
    out.push('<');
    out.push_str(el.name().local_part());
    for a in el.attributes() {
        out.push(' ');
        out.push_str(a.name().local_part());
        out.push_str("=\"");
        out.push_str(a.value());
        out.push('"')
    }
    if el.children().is_empty() {
        out.push_str("/>");
        return;
    }
    out.push('>');
    let nested = el.children().iter().any(|c| c.element().is_some());
    if nested {
        out.push('\n')
    }
    for c in el.children() {
        if let Some(e) = c.element() {
            out.push_str(&"  ".repeat(depth + 1));
            render_element_depth(e, out, depth + 1);
            out.push('\n')
        } else if let Some(t) = c.text() {
            out.push_str(t.text())
        }
    }
    if nested {
        out.push_str(&"  ".repeat(depth))
    }
    out.push_str("</");
    out.push_str(el.name().local_part());
    out.push('>')
}
pub fn xpath_distance(p: &Value, a: &str, options: &Value) -> f64 {
    if p.is_string() {
        return if xpath_match(p, a, options) { 0.0 } else { 1.0 };
    }
    let expression = p["expression"].as_str().unwrap_or("");
    let Ok(package) = sxd_document::parser::parse(a) else {
        return 1.0;
    };
    let Ok(Some(path)) = sxd_xpath::Factory::new().build(expression) else {
        return 1.0;
    };
    let mut ctx = sxd_xpath::Context::new();
    for source in [options, p] {
        if let Some(ns) = source["xPathNamespaces"].as_object() {
            for (k, v) in ns {
                if let Some(v) = v.as_str() {
                    ctx.set_namespace(k, v)
                }
            }
        }
    }
    let Ok(result) = path.evaluate(&ctx, package.as_document().root()) else {
        return 1.0;
    };
    match result {
        sxd_xpath::Value::Nodeset(nodes) => nodes
            .document_order()
            .iter()
            .map(|n| {
                let s = if let Some(el) = n.element() {
                    let mut out = String::new();
                    render_element(el, &mut out);
                    out.push('\n');
                    out
                } else {
                    n.string_value()
                };
                value_distance(p, &s, true)
            })
            .fold(1.0, f64::min),
        v => value_distance(p, &v.string(), true),
    }
}
pub fn xml_distance(p: &Value, actual: &str) -> f64 {
    let Some(expected) = p["equalToXml"].as_str() else {
        return 1.0;
    };
    let (Ok(a), Ok(b)) = (Document::parse(expected), Document::parse(actual)) else {
        return 1.0;
    };
    let mut counts = (0, 0);
    fn compare(p: &Value, c: &mut (usize, usize), kind: &str, eq: bool) {
        if !exempt(p, kind) {
            c.0 += 1;
            if !eq {
                c.1 += 1
            }
        }
    }
    fn cdata(n: Node<'_, '_>) -> bool {
        n.is_text() && n.document().input_text()[n.range()].starts_with("<![CDATA[")
    }
    fn walk(a: Node<'_, '_>, b: Node<'_, '_>, p: &Value, c: &mut (usize, usize)) {
        compare(
            p,
            c,
            "NODE_TYPE",
            a.node_type() == b.node_type() && cdata(a) == cdata(b),
        );
        let mode = p["namespaceAwareness"].as_str().unwrap_or("");
        if mode != "NONE" && a.tag_name().namespace().is_some() {
            compare(
                p,
                c,
                "NAMESPACE_URI",
                a.tag_name().namespace() == b.tag_name().namespace(),
            )
        }
        let ac = children(a);
        let bc = children(b);
        compare(p, c, "CHILD_NODELIST_LENGTH", ac.len() == bc.len());
        if a.is_element() && b.is_element() {
            compare(
                p,
                c,
                "ELEMENT_TAG_NAME",
                a.tag_name().name() == b.tag_name().name(),
            );
            let mut attrs = vec![];
            let mut battrs = vec![];
            for attr in a.attributes() {
                if mode != "NONE"
                    && attr.namespace() == Some("http://www.w3.org/2001/XMLSchema-instance")
                    && (attr.name() == "schemaLocation"
                        || attr.name() == "noNamespaceSchemaLocation")
                {
                    compare(
                        p,
                        c,
                        if attr.name() == "schemaLocation" {
                            "SCHEMA_LOCATION"
                        } else {
                            "NO_NAMESPACE_SCHEMA_LOCATION"
                        },
                        b.attribute((attr.namespace().unwrap(), attr.name())) == Some(attr.value()),
                    );
                } else {
                    attrs.push(attr)
                }
            }
            for attr in b.attributes() {
                if !(mode != "NONE"
                    && attr.namespace() == Some("http://www.w3.org/2001/XMLSchema-instance")
                    && (attr.name() == "schemaLocation"
                        || attr.name() == "noNamespaceSchemaLocation"))
                {
                    battrs.push(attr)
                }
            }
            compare(p, c, "ELEMENT_NUM_ATTRIBUTES", attrs.len() == battrs.len());
            for attr in attrs {
                let found = battrs
                    .iter()
                    .find(|v| v.name() == attr.name() && v.namespace() == attr.namespace());
                compare(p, c, "ATTR_NAME_LOOKUP", found.is_some());
                if let Some(other) = found {
                    compare(p, c, "NODE_TYPE", true);
                    if mode != "NONE" && attr.namespace().is_some() {
                        compare(p, c, "NAMESPACE_URI", attr.namespace() == other.namespace())
                    }
                    compare(
                        p,
                        c,
                        "ATTR_VALUE",
                        same_text(attr.value(), other.value(), p),
                    )
                }
            }
        } else if a.is_pi() && b.is_pi() {
            compare(
                p,
                c,
                "PROCESSING_INSTRUCTION_TARGET",
                a.pi().unwrap().target == b.pi().unwrap().target,
            );
            compare(
                p,
                c,
                "PROCESSING_INSTRUCTION_DATA",
                a.pi().unwrap().value == b.pi().unwrap().value,
            )
        } else if a.is_text() && b.is_text() {
            compare(
                p,
                c,
                "TEXT_VALUE",
                same_text(a.text().unwrap_or(""), b.text().unwrap_or(""), p),
            )
        }
        let mut used = vec![false; bc.len()];
        for x in ac {
            if let Some(j) = bc
                .iter()
                .enumerate()
                .position(|(j, y)| !used[j] && x.node_type() == y.node_type())
            {
                used[j] = true;
                walk(x, bc[j], p, c)
            } else {
                compare(p, c, "CHILD_LOOKUP", false)
            }
        }
    }
    walk(a.root(), b.root(), p, &mut counts);
    if counts.0 == 0 {
        0.0
    } else {
        counts.1 as f64 / counts.0 as f64
    }
}
pub fn comparison_warning(expected: &str, actual: &str) -> bool {
    [expected, actual].iter().any(|s| {
        Document::parse(s)
            .map(|doc| {
                doc.descendants().any(|n| {
                    let cs = children(n);
                    cs.len() > 1 && cs.iter().any(Node::is_pi)
                })
            })
            .unwrap_or(false)
    })
}
