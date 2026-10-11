// Copyright (C) 2016-2025 Thomas Akehurst; Copyright 2026 Breaking-IT.
// Apache-2.0. Adapted from WireMock Diff / PlainTextDiffRenderer.
use crate::{Request, matcher};
use base64::{Engine, engine::general_purpose::STANDARD};
use serde_json::{Value, json};
#[derive(Default)]
struct Line {
    left: String,
    right: String,
    message: String,
}
fn pat(p: &Value) -> (String, String) {
    for k in [
        "equalTo",
        "equalToJson",
        "equalToXml",
        "binaryEqualTo",
        "contains",
        "doesNotContain",
        "matches",
        "doesNotMatch",
        "matchesJsonPath",
        "matchesXPath",
        "absent",
        "before",
        "after",
        "equalToDateTime",
        "matchesPathTemplate",
        "hasExactly",
        "includes",
        "and",
        "or",
        "not",
    ] {
        if let Some(v) = p.get(k) {
            return (
                k.into(),
                if k == "absent" {
                    "(absent)".into()
                } else {
                    v.as_str()
                        .map(str::to_owned)
                        .unwrap_or_else(|| v.to_string())
                },
            );
        }
    }
    ("equalTo".into(), p.as_str().unwrap_or("").into())
}
fn op(name: &str, equality: &str) -> String {
    if ["equalTo", "equalToJson", "equalToXml", "binaryEqualTo"].contains(&name) {
        equality.into()
    } else {
        format!(" [{name}] ")
    }
}
fn add(lines: &mut Vec<Line>, attr: &str, left: String, right: String, exact: bool) {
    let message = if right.is_empty() {
        format!("{attr} is not present")
    } else if !exact {
        format!("{attr} does not match")
    } else {
        String::new()
    };
    lines.push(Line {
        left,
        right,
        message,
    })
}
fn blank(lines: &mut Vec<Line>) {
    lines.push(Line::default())
}
fn text(v: &Value) -> String {
    v.as_str()
        .map(str::to_owned)
        .unwrap_or_else(|| v.to_string())
}
fn multi(p: &Value, values: &[String]) -> bool {
    if values.is_empty() {
        matcher::value_present(p, "", false)
    } else {
        values.iter().any(|s| matcher::value_matches(p, s))
    }
}
pub fn unmatched_response(req: &Request, mappings: &[Value]) -> (String, String) {
    if mappings.is_empty() {
        return (
            "No response could be served as there are no stub mappings in this WireMock instance."
                .into(),
            "text/plain".into(),
        );
    }
    let best = mappings
        .iter()
        .min_by(|a, b| {
            matcher::request_distance(&a["request"], req)
                .total_cmp(&matcher::request_distance(&b["request"], req))
        })
        .unwrap();
    (render(best, req), "text/plain".into())
}
pub fn render(mapping: &Value, req: &Request) -> String {
    let p = &mapping["request"];
    let mut lines = vec![];
    let abs = url::Url::parse(&format!(
        "{}://{}{}",
        if req.scheme.is_empty() {
            "http"
        } else {
            &req.scheme
        },
        req.header("Host").unwrap_or("localhost"),
        req.url
    ))
    .ok();
    if let Some(v) = p.get("host") {
        let (n, s) = pat(v);
        let actual = abs
            .as_ref()
            .and_then(|u| u.host_str())
            .unwrap_or("")
            .to_owned();
        add(
            &mut lines,
            "Host",
            format!("{}{}", op(&n, ""), s).trim().into(),
            actual.clone(),
            matcher::value_matches(v, &actual),
        )
    }
    if let Some(v) = p.get("port") {
        let actual = abs
            .as_ref()
            .and_then(|u| u.port_or_known_default())
            .unwrap_or(80)
            .to_string();
        add(
            &mut lines,
            "Port",
            text(v),
            actual.clone(),
            text(v) == actual,
        )
    }
    if let Some(v) = p.get("clientIp") {
        add(
            &mut lines,
            "ClientIp",
            pat(v).1,
            req.client_ip.clone(),
            matcher::value_matches(v, &req.client_ip),
        )
    }
    if let Some(v) = p["scheme"].as_str() {
        add(
            &mut lines,
            "Scheme",
            v.into(),
            req.scheme.clone(),
            v == req.scheme,
        )
    }
    let method = p["method"].as_str().unwrap_or("ANY");
    add(
        &mut lines,
        "HTTP method",
        method.into(),
        req.method.clone(),
        method == "ANY" || method == req.method,
    );
    let (mut url_key, mut expected, mut label) = ("urlPattern", ".*", "");
    for (k, l) in [
        ("url", ""),
        ("urlPattern", "[regex] "),
        ("urlPath", "[path] "),
        ("urlPathPattern", "[path regex] "),
        ("urlPathTemplate", "[path template] "),
    ] {
        if let Some(s) = p[k].as_str() {
            url_key = k;
            expected = s;
            label = l;
            break;
        }
    }
    let exact = matcher::matches(&json!({url_key:expected}), req);
    add(
        &mut lines,
        "URL",
        if expected == ".*" && p["urlPattern"].is_null() {
            "(always)".into()
        } else {
            format!("{label}{expected}")
        },
        req.url.clone(),
        exact,
    );
    if url_key.contains("Pattern") && expected.contains('?') && !expected.contains("\\?") {
        lines
            .last_mut()
            .unwrap()
            .message
            .push_str(". When using a regex, \"?\" should be \"\\?\"")
    }
    if expected != ".*" && !expected.starts_with('/') {
        lines
            .last_mut()
            .unwrap()
            .message
            .push_str(". URLs must start with a /")
    }
    blank(&mut lines);
    let mut headers = p["headers"].as_object().cloned().unwrap_or_default();
    if let Some(auth) = p.get("basicAuthCredentials") {
        headers.insert("Authorization".into(),json!({"equalTo":format!("Basic {}",STANDARD.encode(format!("{}:{}",auth["username"].as_str().unwrap_or(""),auth["password"].as_str().unwrap_or(""))))}));
    }
    for (key, pattern) in &headers {
        let (n, v) = pat(pattern);
        let name = req
            .headers
            .iter()
            .find(|(k, _)| k.eq_ignore_ascii_case(key))
            .map(|(k, _)| k.as_str())
            .unwrap_or(key);
        let values: Vec<_> = req
            .headers
            .iter()
            .filter(|(k, _)| k.eq_ignore_ascii_case(key))
            .map(|(_, v)| v.clone())
            .collect();
        let actual = values
            .iter()
            .map(|v| format!("{name}: {v}"))
            .collect::<Vec<_>>()
            .join("\n");
        add(
            &mut lines,
            "Header",
            format!(
                "{name}{}{}",
                op(&n, ""),
                if v.is_empty() {
                    String::new()
                } else {
                    format!(": {v}")
                }
            ),
            actual,
            multi(pattern, &values),
        )
    }
    if !headers.is_empty() {
        blank(&mut lines)
    }
    if url_key == "urlPathTemplate" && exact {
        let values = matcher::path_params(expected, req.path()).unwrap_or_default();
        if let Some(ps) = p["pathParameters"].as_object() {
            for (k, v) in ps {
                let (n, s) = pat(v);
                let actual = values.get(k).cloned().unwrap_or_default();
                add(
                    &mut lines,
                    "Path parameter",
                    format!("Path parameter: {k}{}{s}", op(&n, " = ")),
                    actual.clone(),
                    matcher::value_present(v, &actual, values.contains_key(k)),
                )
            }
            if !ps.is_empty() {
                blank(&mut lines)
            }
        }
    }
    for (key, attribute, prefix, raw) in [
        (
            "queryParameters",
            "Query",
            "Query: ",
            req.url
                .split_once('?')
                .map(|(_, s)| s)
                .unwrap_or("")
                .to_owned(),
        ),
        ("formParameters", "Form data", "Form: ", req.body_text()),
    ] {
        if let Some(ps) = p[key].as_object() {
            for (k, v) in ps {
                let (n, s) = pat(v);
                let values: Vec<_> = url::form_urlencoded::parse(raw.as_bytes())
                    .filter(|(key, _)| key == k)
                    .map(|(_, v)| v.into_owned())
                    .collect();
                add(
                    &mut lines,
                    attribute,
                    format!("{prefix}{k}{}{s}", op(&n, " = ")),
                    values
                        .iter()
                        .map(|v| format!("{k}: {v}"))
                        .collect::<Vec<_>>()
                        .join("\n"),
                    multi(v, &values),
                )
            }
            if !ps.is_empty() {
                blank(&mut lines)
            }
        }
    }
    if let Some(cs) = p["cookies"].as_object() {
        for (k, v) in cs {
            let (n, s) = pat(v);
            let actual = req
                .header("Cookie")
                .unwrap_or("")
                .split(';')
                .filter_map(|s| s.trim().split_once('='))
                .find(|(name, _)| *name == k)
                .map(|(_, v)| v);
            add(
                &mut lines,
                "Cookie",
                format!("Cookie: {k}{}{s}", op(&n, "=")),
                actual.unwrap_or("").into(),
                matcher::value_present(v, actual.unwrap_or(""), actual.is_some()),
            )
        }
        if !cs.is_empty() {
            blank(&mut lines)
        }
    }
    if let Some(ps) = p["bodyPatterns"].as_array() {
        for pattern in ps {
            if matcher::value_present(
                pattern,
                &req.body_text(),
                !req.body.is_empty() || pattern.get("binaryEqualTo").is_some(),
            ) {
                continue;
            }
            let (n, mut v) = pat(pattern);
            let mut actual = req.body_text();
            if n == "equalToJson" {
                v = pretty_json(&v);
                actual = pretty_json(&actual)
            }
            if n == "equalToXml" {
                v = pretty_xml(&v);
                actual = pretty_xml(&actual)
            }
            let (mut left, mut right) = (format!("[{n}]\n{v}"), format!("\n{actual}"));
            if n == "binaryEqualTo" {
                left = v.clone();
                right = STANDARD.encode(&req.body)
            }
            if n == "matchesJsonPath" || n == "matchesXPath" {
                left = v;
                right = req.body_text()
            }
            if n == "matchesJsonPath" {
                let nested = &pattern[&n];
                if nested.is_object() {
                    let expr = nested["expression"].as_str().unwrap_or("");
                    let (op, val) = pat(nested);
                    left = format!("{expr} [{op}] {val}");
                    if let Ok(doc) = serde_json::from_slice(&req.body) {
                        if let Ok(v) = matcher::json_path(&doc, expr) {
                            if !v.is_null() {
                                right = text(&v)
                            }
                        }
                    }
                }
            }
            add(&mut lines, "Body", left, right, false)
        }
    }
    if let (Some(scenario), Some(expected)) = (
        mapping["scenarioName"].as_str(),
        mapping["requiredScenarioState"].as_str(),
    ) {
        let state = mapping["_scenarioState"].as_str().unwrap_or("Started");
        add(
            &mut lines,
            "Scenario",
            format!("[Scenario '{scenario}' state: {expected}]"),
            format!("[Scenario '{scenario}' state: {state}]"),
            expected == state,
        )
    }
    let width = req
        .header("X-WireMock-Console-Width")
        .and_then(|s| s.parse::<usize>().ok())
        .filter(|w| *w >= 20 && *w <= 4096)
        .unwrap_or(119);
    let middle = width / 2 - 1;
    let title = "Request was not matched";
    let spaces = " ".repeat(middle.saturating_sub(title.len() / 2));
    let bar = "-".repeat(width);
    let mut out = format!(
        "\n{spaces}{title}\n{spaces}{}\n\n{bar}\n|{}|{}|\n{bar}\n",
        "=".repeat(title.len()),
        pad(" Closest stub", middle),
        pad(" Request", middle)
    );
    fn write(out: &mut String, l: &Line, width: usize, middle: usize) {
        let left = wrap(&l.left, width / 2 - 2);
        let right = wrap(&l.right, width / 2 - 2);
        for i in 0..left.len().max(right.len()) {
            let a = left.get(i).map(String::as_str).unwrap_or("");
            let b = right.get(i).map(String::as_str).unwrap_or("");
            out.push_str(&pad(a, middle + 1));
            out.push('|');
            let message = if i == 0 { &l.message } else { "" };
            if !b.is_empty() {
                out.push(' ');
                if !message.is_empty() {
                    out.push_str(&format!("{}<<<<< {message}", pad(b, middle - 6)))
                } else {
                    out.push_str(b)
                }
            } else if !message.is_empty() {
                out.push_str(&format!("{}<<<<< {message}", pad(b, middle - 5)))
            }
            out.push('\n')
        }
    }
    write(&mut out, &Line::default(), width, middle);
    if let Some(name) = mapping["name"].as_str() {
        write(
            &mut out,
            &Line {
                left: name.into(),
                ..Default::default()
            },
            width,
            middle,
        );
        write(&mut out, &Line::default(), width, middle)
    }
    for line in lines {
        write(&mut out, &line, width, middle)
    }
    write(&mut out, &Line::default(), width, middle);
    out.push_str(&bar);
    out.push('\n');
    out
}
fn pad(s: &str, n: usize) -> String {
    format!(
        "{}{}",
        s,
        " ".repeat(n.saturating_sub(s.encode_utf16().count()))
    )
}
fn wrap(s: &str, width: usize) -> Vec<String> {
    let mut s = s.replace("\r\n", "\n").replace('\r', "\n");
    if s.split('\n').any(|s| s.encode_utf16().count() > width) {
        let mut chars = s.encode_utf16().collect::<Vec<_>>();
        let mut out = vec![];
        while chars.len() > width {
            if chars[0] == 32 {
                chars.remove(0);
                continue;
            }
            let at = chars
                .iter()
                .take(width + 1)
                .rposition(|c| *c == 32)
                .unwrap_or(width);
            out.extend_from_slice(&chars[..at]);
            out.push(10);
            let n = at + usize::from(chars.get(at) == Some(&32));
            chars.drain(..n);
        }
        out.extend(chars);
        s = String::from_utf16_lossy(&out)
            .split('\n')
            .filter(|l| !l.trim().is_empty())
            .collect::<Vec<_>>()
            .join("\n")
    }
    s.trim_end_matches('\n')
        .split('\n')
        .map(str::to_owned)
        .collect()
}
fn pretty_json(s: &str) -> String {
    serde_json::from_str(s)
        .map(|v| crate::admin_json::serialize(&v))
        .unwrap_or(s.into())
}
fn pretty_xml(s: &str) -> String {
    let r = Request {
        body: s.as_bytes().to_vec(),
        ..Default::default()
    };
    crate::template::render("{{formatXml request.body}}", &r, &Value::Null).unwrap_or(s.into())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn url_golden() {
        let req = Request {
            method: "GET".into(),
            url: "/actual".into(),
            ..Default::default()
        };
        let mappings = vec![json!({"request":{"method":"GET","url":"/expected"}})];
        assert_eq!(
            unmatched_response(&req, &mappings).0,
            include_str!("../../testdata/diagnostics/url-mismatch.txt")
        )
    }
}
