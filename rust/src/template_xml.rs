use serde_json::{Map, Value, json};
use sxd_document::dom::{ChildOfElement, Element};
fn escape(s: &str, attribute: bool) -> String {
    let s = s
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;");
    if attribute {
        s.replace('"', "&quot;")
    } else {
        s
    }
}
fn element(el: Element<'_>, pretty: bool, depth: usize) -> String {
    let mut s = format!("<{}", el.name().local_part());
    let mut attrs = el.attributes();
    attrs.sort_by_key(|a| a.name().local_part());
    for a in attrs {
        s.push_str(&format!(
            " {}=\"{}\"",
            a.name().local_part(),
            escape(a.value(), true)
        ))
    }
    let children: Vec<_> = el
        .children()
        .into_iter()
        .filter(|n| {
            n.text()
                .map(|t| !t.text().trim().is_empty())
                .unwrap_or(true)
        })
        .collect();
    if children.is_empty() {
        s.push_str("/>");
        return s;
    }
    s.push('>');
    let nested = pretty && children.iter().any(|n| n.element().is_some());
    if nested {
        s.push('\n')
    }
    for child in children {
        if nested {
            s.push_str(&"  ".repeat(depth + 1))
        }
        match child {
            ChildOfElement::Element(el) => s.push_str(&element(el, pretty, depth + 1)),
            ChildOfElement::Text(t) => s.push_str(&escape(t.text(), false)),
            ChildOfElement::Comment(c) => s.push_str(&format!("<!--{}-->", c.text())),
            _ => {}
        }
        if nested {
            s.push('\n')
        }
    }
    if nested {
        s.push_str(&"  ".repeat(depth))
    }
    s.push_str(&format!("</{}>", el.name().local_part()));
    s
}
pub fn format(input: &str, compact: bool) -> Result<Value, String> {
    let doc = roxmltree::Document::parse(input).map_err(|e| e.to_string())?;
    let mut out = format_node(doc.root_element(), !compact, 0);
    if !compact {
        out.push('\n')
    }
    Ok(json!(out))
}
fn format_node(node: roxmltree::Node<'_, '_>, pretty: bool, depth: usize) -> String {
    if node.is_text() {
        let raw = &node.document().input_text()[node.range()];
        return if raw.starts_with("<![CDATA[") {
            raw.into()
        } else {
            escape(node.text().unwrap_or(""), false)
        };
    }
    if node.is_comment() {
        return format!("<!--{}-->", node.text().unwrap_or(""));
    }
    if !node.is_element() {
        return String::new();
    }
    let qualified = |name: &str, namespace: Option<&str>| -> String {
        if let Some(prefix) = namespace.and_then(|ns| node.lookup_prefix(ns)) {
            format!("{prefix}:{name}")
        } else {
            name.into()
        }
    };
    let name = qualified(node.tag_name().name(), node.tag_name().namespace());
    let mut out = format!("<{name}");
    let parent = node.parent_element();
    for ns in node.namespaces() {
        if parent.is_some_and(|p| {
            p.namespaces()
                .any(|other| other.name() == ns.name() && other.uri() == ns.uri())
        }) {
            continue;
        }
        let key = ns
            .name()
            .map(|p| format!("xmlns:{p}"))
            .unwrap_or_else(|| "xmlns".into());
        out.push_str(&format!(" {key}=\"{}\"", escape(ns.uri(), true)))
    }
    let mut attrs: Vec<_> = node
        .attributes()
        .map(|a| (qualified(a.name(), a.namespace()), a.value()))
        .collect();
    attrs.sort_by(|a, b| a.0.cmp(&b.0));
    for (k, v) in attrs {
        out.push_str(&format!(" {k}=\"{}\"", escape(v, true)))
    }
    let children: Vec<_> = node
        .children()
        .filter(|n| !n.is_text() || !n.text().unwrap_or("").trim().is_empty())
        .collect();
    if children.is_empty() {
        out.push_str("/>");
        return out;
    }
    out.push('>');
    let nested = pretty && children.iter().any(|n| n.is_element() || n.is_comment());
    if nested {
        out.push('\n')
    }
    for child in children {
        if nested {
            out.push_str(&"  ".repeat(depth + 1))
        }
        out.push_str(&format_node(child, pretty, depth + 1));
        if nested {
            out.push('\n')
        }
    }
    if nested {
        out.push_str(&"  ".repeat(depth))
    }
    out.push_str(&format!("</{name}>"));
    out
}
pub fn xpath(input: &str, expression: &str, soap: bool) -> Result<Value, String> {
    let p = sxd_document::parser::parse(input).map_err(|e| e.to_string())?;
    let expression = if soap {
        format!("/*[local-name()='Envelope']/*[local-name()='Body']/{expression}")
    } else {
        expression.into()
    };
    let path = sxd_xpath::Factory::new()
        .build(&expression)
        .map_err(|e| e.to_string())?
        .ok_or("Invalid XPath")?;
    let result = path
        .evaluate(&sxd_xpath::Context::new(), p.as_document().root())
        .map_err(|e| e.to_string())?;
    Ok(match result {
        sxd_xpath::Value::Nodeset(nodes) => {
            let a:Vec<Value>=nodes.document_order().into_iter().map(|n|if let Some(el)=n.element(){let attrs:Map<String,Value>=el.attributes().into_iter().map(|a|(a.name().local_part().to_string(),json!(a.value()))).collect();json!({"__xml":format!("{}\n",element(el,true,0)),"name":el.name().local_part(),"text":n.string_value(),"attributes":attrs})}else{json!(n.string_value())}).collect();
            json!(a)
        }
        sxd_xpath::Value::Number(n) => json!(if n.fract() == 0.0 {
            (n as i64).to_string()
        } else {
            n.to_string()
        }),
        v => json!(v.string()),
    })
}
