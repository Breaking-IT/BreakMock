// Copyright 2026 Breaking-IT. Apache-2.0.
use crate::Request;
use base64::{Engine, engine::general_purpose::STANDARD};
use serde_json::{Value, json};
use std::collections::HashMap;
fn text(v: &Value) -> String {
    v.as_str()
        .map(str::to_owned)
        .unwrap_or_else(|| v.to_string())
}
fn flag(v: &Value, k: &str) -> bool {
    v[k].as_bool().unwrap_or(false)
}
#[path = "matcher_regex.rs"]
mod java_regex;
fn regex_match(pattern: &str, value: &str) -> bool {
    java_regex::is_match(pattern, value)
}

fn method(p: &Value, a: &str) -> bool {
    if p.is_null() {
        return true;
    }
    if let Some(s) = p.as_str() {
        return s == "ANY" || s == a;
    }
    for k in ["oneOf", "noneOf"] {
        if let Some(v) = p[k].as_array() {
            let found = v.iter().any(|v| v.as_str() == Some(a));
            return if k == "noneOf" { !found } else { found };
        }
    }
    false
}
pub fn path_params(template: &str, path: &str) -> Option<HashMap<String, String>> {
    let mut names = vec![];
    let mut exp = String::from("^");
    let mut rest = template;
    while let Some(i) = rest.find('{') {
        exp.push_str(&regex::escape(&rest[..i]));
        rest = &rest[i + 1..];
        let j = rest.find('}')?;
        names.push(rest[..j].to_owned());
        exp.push_str("([^/]+)");
        rest = &rest[j + 1..];
    }
    exp.push_str(&regex::escape(rest));
    exp.push('$');
    let re = regex::Regex::new(&exp).ok()?;
    let cap = re.captures(path)?;
    Some(
        names
            .into_iter()
            .enumerate()
            .map(|(i, n)| (n, cap[i + 1].to_owned()))
            .collect(),
    )
}
fn multi(p: &Value, values: &[String]) -> bool {
    for key in ["includes", "hasExactly"] {
        if let Some(children) = p[key].as_array() {
            return !values.is_empty()
                && (key != "hasExactly" || children.len() == values.len())
                && children
                    .iter()
                    .all(|c| values.iter().any(|s| value_present(c, s, true)));
        }
    }
    if values.is_empty() {
        value_present(p, "", false)
    } else {
        values.iter().any(|s| value_present(p, s, true))
    }
}
pub fn matches(p: &Value, r: &Request) -> bool {
    if !method(&p["method"], &r.method) || p.get("customMatcher").is_some() {
        return false;
    }
    if p.get("clientIp").is_some()
        && !value_present(&p["clientIp"], &r.client_ip, !r.client_ip.is_empty())
    {
        return false;
    }
    for key in [
        "url",
        "urlPattern",
        "urlPath",
        "urlPathPattern",
        "urlPathTemplate",
    ] {
        if let Some(s) = p[key].as_str() {
            let ok = match key {
                "url" => s == r.url,
                "urlPath" => s == r.path(),
                "urlPattern" => regex_match(s, &r.url),
                "urlPathPattern" => regex_match(s, r.path()),
                _ => path_params(s, r.path()).is_some(),
            };
            if !ok {
                return false;
            }
            break;
        }
    }
    if let Some(s) = p["scheme"].as_str() {
        if s != r.scheme {
            return false;
        }
    }
    let host = r.header("Host").unwrap_or("");
    let abs = url::Url::parse(&format!(
        "{}://{}{}",
        if r.scheme.is_empty() {
            "http"
        } else {
            &r.scheme
        },
        host,
        r.url
    ));
    if let Some(h) = p.get("host") {
        let h = if h.is_string() {
            json!({"equalTo":h})
        } else {
            h.clone()
        };
        if !value_matches(
            &h,
            abs.as_ref().ok().and_then(|u| u.host_str()).unwrap_or(""),
        ) {
            return false;
        }
    }
    if let Some(port) = p.get("port") {
        if abs
            .as_ref()
            .ok()
            .and_then(|u| u.port_or_known_default())
            .map(|v| v.to_string())
            != Some(text(port))
        {
            return false;
        }
    }
    for key in ["headers", "queryParameters", "formParameters", "cookies"] {
        if let Some(items) = p[key].as_object() {
            for (name, child) in items {
                let vals: Vec<String> = match key {
                    "headers" => {
                        if name.eq_ignore_ascii_case("Authorization")
                            && p.get("basicAuthCredentials").is_some()
                        {
                            continue;
                        }
                        r.headers
                            .iter()
                            .filter(|(k, _)| k.eq_ignore_ascii_case(name))
                            .map(|(_, v)| v.clone())
                            .collect()
                    }
                    "queryParameters" => url::form_urlencoded::parse(
                        r.url
                            .split_once('?')
                            .map(|(_, q)| q)
                            .unwrap_or("")
                            .as_bytes(),
                    )
                    .filter(|(k, _)| k == name)
                    .map(|(_, v)| v.into_owned())
                    .collect(),
                    "formParameters" => {
                        let body = if r
                            .header("Content-Type")
                            .unwrap_or("")
                            .to_lowercase()
                            .starts_with("application/x-www-form-urlencoded")
                        {
                            r.body.as_slice()
                        } else {
                            b""
                        };
                        url::form_urlencoded::parse(body)
                            .filter(|(k, _)| k == name)
                            .map(|(_, v)| v.into_owned())
                            .collect()
                    }
                    _ => r
                        .headers
                        .iter()
                        .filter(|(k, _)| k.eq_ignore_ascii_case("Cookie"))
                        .flat_map(|(_, s)| s.split(';'))
                        .filter_map(|s| s.trim().split_once('='))
                        .filter(|(k, _)| k == name)
                        .map(|(_, v)| v.trim_matches('"').to_owned())
                        .collect(),
                };
                if !multi(child, &vals) {
                    return false;
                }
            }
        }
    }
    if let Some(auth) = p.get("basicAuthCredentials") {
        let value = r.header("Authorization").unwrap_or("");
        let actual = value
            .split_once(' ')
            .filter(|(k, _)| k.eq_ignore_ascii_case("Basic"))
            .and_then(|(_, s)| STANDARD.decode(s).ok());
        if actual
            != Some(
                format!(
                    "{}:{}",
                    auth["username"].as_str().unwrap_or(""),
                    auth["password"].as_str().unwrap_or("")
                )
                .into_bytes(),
            )
        {
            return false;
        }
    }
    if let Some(t) = p["urlPathTemplate"].as_str() {
        if ["url", "urlPattern", "urlPath", "urlPathPattern"]
            .iter()
            .all(|k| p.get(k).is_none())
        {
            if let Some(ps) = p["pathParameters"].as_object() {
                let values = path_params(t, r.path()).unwrap_or_default();
                for (k, v) in ps {
                    if !value_present(
                        v,
                        values.get(k).map(String::as_str).unwrap_or(""),
                        values.contains_key(k),
                    ) {
                        return false;
                    }
                }
            }
        }
    }
    if let Some(body) = p["bodyPatterns"].as_array() {
        for child in body {
            if let Some(s) = child["binaryEqualTo"].as_str() {
                if STANDARD.decode(s).ok().as_ref() != Some(&r.body) {
                    return false;
                }
            } else if !value_present(child, &r.body_text(), !r.body.is_empty()) {
                return false;
            }
        }
    }
    if let Some(parts) = p["multipartPatterns"].as_array() {
        if !multipart(parts, r) {
            return false;
        }
    }
    true
}
pub fn value_matches(p: &Value, value: &str) -> bool {
    value_present(p, value, true)
}
pub fn value_present(p: &Value, a: &str, present: bool) -> bool {
    if p.get("absent").and_then(Value::as_bool).is_some() {
        return !present;
    }
    if p.get("anything").is_some() {
        return true;
    }
    if let Some(c) = p.get("not") {
        return !value_present(c, a, present);
    }
    for key in ["and", "or"] {
        if let Some(cs) = p[key].as_array() {
            return if key == "and" {
                cs.iter().all(|c| value_present(c, a, present))
            } else {
                cs.iter().any(|c| value_present(c, a, present))
            };
        }
    }
    if !present {
        return p.get("doesNotMatch").is_some() || p.get("doesNotContain").is_some();
    }
    let Some(obj) = p.as_object() else {
        return false;
    };
    for (k, v) in obj {
        let s = v.as_str().unwrap_or("");
        match k.as_str() {
            "equalTo" => {
                return if flag(p, "caseInsensitive") {
                    java_case(a) == java_case(s)
                } else {
                    a == s
                };
            }
            "contains" => return a.contains(s),
            "doesNotContain" => return !a.contains(s),
            "matches" => return regex_match(s, a),
            "doesNotMatch" => return !regex_match(s, a),
            "matchesPathTemplate" => return path_params(s, a).is_some(),
            "binaryEqualTo" => return STANDARD.decode(s).ok().as_deref() == Some(a.as_bytes()),
            "equalToJson" => {
                let want = if v.is_string() {
                    serde_json::from_str(s).ok()
                } else {
                    Some(v.clone())
                };
                return want
                    .zip(serde_json::from_str(a).ok())
                    .map(|(w, g)| {
                        json_equal(
                            &w,
                            &g,
                            flag(p, "ignoreArrayOrder"),
                            flag(p, "ignoreExtraElements"),
                        )
                    })
                    .unwrap_or(false);
            }
            "matchesJsonPath" => return json_path_match(v, a),
            "matchesJsonSchema" => return schema_match(v, a, p),
            "equalToXml" => return xml_match(s, a, p),
            "matchesXPath" => return xpath_match(v, a, p),
            "before" | "after" | "equalToDateTime" => return date_match(k, s, a, p),
            "equalToNumber"
            | "greaterThanNumber"
            | "greaterThanEqualNumber"
            | "lessThanNumber"
            | "lessThanEqualNumber" => {
                let Some(e) = number(&text(v)) else {
                    return false;
                };
                let Some(g) = number(a) else { return false };
                return match k.as_str() {
                    "equalToNumber" => g == e,
                    "greaterThanNumber" => g > e,
                    "greaterThanEqualNumber" => g >= e,
                    "lessThanNumber" => g < e,
                    _ => g <= e,
                };
            }
            _ => {}
        }
    }
    false
}
fn java_case(s: &str) -> String {
    s.chars()
        .map(|c| {
            if c == 'İ' || c == 'ı' {
                'i'
            } else {
                c.to_lowercase().next().unwrap_or(c)
            }
        })
        .collect()
}
fn number(s: &str) -> Option<f64> {
    let s = s.trim_matches(|c: char| c as u32 <= 32);
    match s {
        "NaN" | "+NaN" | "-NaN" => return Some(f64::NAN),
        "Infinity" | "+Infinity" => return Some(f64::INFINITY),
        "-Infinity" => return Some(f64::NEG_INFINITY),
        _ => {}
    }
    if s.contains('_') || s.to_lowercase().contains("nan") || s.to_lowercase().contains("inf") {
        return None;
    }
    {
        let s = s.trim_end_matches(['d', 'D', 'f', 'F']);
        let sign = if s.starts_with('-') { -1.0 } else { 1.0 };
        let raw = s.trim_start_matches(['-', '+']);
        if let Some(hex) = raw.strip_prefix("0x").or_else(|| raw.strip_prefix("0X")) {
            let (mantissa, exp) = hex.split_once(['p', 'P'])?;
            let mut n = 0.0;
            let mut fractional = false;
            let mut scale = 1.0;
            for c in mantissa.chars() {
                if c == '.' {
                    fractional = true;
                    continue;
                }
                let d = c.to_digit(16)? as f64;
                if fractional {
                    scale /= 16.0;
                    n += d * scale
                } else {
                    n = n * 16.0 + d
                }
            }
            return Some(sign * n * 2f64.powi(exp.parse().ok()?));
        }
        s.parse().ok()
    }
}
fn json_equal(w: &Value, g: &Value, unordered: bool, extra: bool) -> bool {
    if let Some(s) = w.as_str() {
        match s {
            "${json-unit.ignore}" | "${json-unit.anything}" => return true,
            "${json-unit.any-string}" => return g.is_string(),
            "${json-unit.any-number}" => return g.is_number(),
            "${json-unit.any-boolean}" => return g.is_boolean(),
            _ => {}
        }
        if let Some(p) = s.strip_prefix("${json-unit.regex}") {
            return g.as_str().map(|g| regex_match(p, g)).unwrap_or(false);
        }
    }
    match (w, g) {
        (Value::Object(w), Value::Object(g)) => {
            let ignored = w
                .iter()
                .filter(|(_, v)| v.as_str() == Some("${json-unit.ignore-element}"))
                .count();
            let removed = w
                .iter()
                .filter(|(k, v)| {
                    v.as_str() == Some("${json-unit.ignore-element}") && g.contains_key(*k)
                })
                .count();
            (extra || w.len() - ignored == g.len() - removed)
                && w.iter().all(|(k, v)| {
                    v.as_str() == Some("${json-unit.ignore-element}")
                        || g.get(k)
                            .map(|a| json_equal(v, a, unordered, extra))
                            .unwrap_or(false)
                })
        }
        (Value::Array(w), Value::Array(g)) => {
            if w.len() > g.len() || (!extra && w.len() != g.len()) {
                return false;
            }
            if !unordered {
                return w
                    .iter()
                    .zip(g)
                    .all(|(w, g)| json_equal(w, g, unordered, extra));
            }
            fn assign(i: usize, w: &[Value], g: &[Value], used: &mut [bool], extra: bool) -> bool {
                if i == w.len() {
                    return true;
                }
                for j in 0..g.len() {
                    if !used[j] && json_equal(&w[i], &g[j], true, extra) {
                        used[j] = true;
                        if assign(i + 1, w, g, used, extra) {
                            return true;
                        }
                        used[j] = false
                    }
                }
                false
            }
            assign(0, w, g, &mut vec![false; g.len()], extra)
        }
        (Value::Number(w), Value::Number(g)) => {
            w.to_string().parse::<bigdecimal::BigDecimal>().ok()
                == g.to_string().parse::<bigdecimal::BigDecimal>().ok()
        }
        _ => w == g,
    }
}
#[path = "matcher_json.rs"]
mod json;
pub use json::json_path;
use json::json_path_match;
fn schema_match(p: &Value, a: &str, _o: &Value) -> bool {
    let schema = if let Some(s) = p.as_str() {
        serde_json::from_str(s).ok()
    } else {
        Some(p.clone())
    };
    let Some(schema) = schema else { return false };
    let Ok(v) = jsonschema::validator_for(&schema) else {
        return false;
    };
    let actual: Value = serde_json::from_str(a).unwrap_or(Value::String(a.into()));
    v.is_valid(&actual)
        || (!actual.is_object()
            && !actual.is_array()
            && !actual.is_string()
            && v.is_valid(&Value::String(a.into())))
}
#[path = "matcher_xml.rs"]
mod xml;
use xml::{xml_match, xpath_match};
#[path = "matcher_date.rs"]
mod date;
use date::date_match;
fn multipart(patterns: &[Value], r: &Request) -> bool {
    let content = r.header("Content-Type").unwrap_or("");
    let Some(boundary) = content
        .split(';')
        .find_map(|s| s.trim().strip_prefix("boundary="))
        .map(|s| s.trim_matches('"'))
    else {
        return false;
    };
    let marker = format!("--{boundary}").into_bytes();
    let mut parts = vec![];
    let positions: Vec<usize> = r
        .body
        .windows(marker.len())
        .enumerate()
        .filter(|(i, w)| *w == marker.as_slice() && (*i == 0 || r.body[..*i].ends_with(b"\r\n")))
        .map(|(i, _)| i)
        .collect();
    for pair in positions.windows(2) {
        let chunk = &r.body[pair[0] + marker.len()..pair[1]];
        if chunk.starts_with(b"--") {
            break;
        }
        let chunk = chunk.strip_prefix(b"\r\n").unwrap_or(chunk);
        let Some(end) = chunk.windows(4).position(|w| w == b"\r\n\r\n") else {
            continue;
        };
        let headers = String::from_utf8_lossy(&chunk[..end]);
        let body = &chunk[end + 4..];
        let headers: Vec<(String, String)> = headers
            .split("\r\n")
            .filter_map(|s| s.split_once(':'))
            .map(|(k, v)| (k.into(), v.trim().into()))
            .collect();
        parts.push(Request {
            headers,
            body: body.strip_suffix(b"\r\n").unwrap_or(body).to_vec(),
            ..Default::default()
        });
    }
    patterns.iter().all(|p| {
        let check = |r: &Request| {
            if let Some(filename) = p["fileName"].as_str() {
                let cd = r.header("Content-Disposition").unwrap_or("");
                if !cd.split(';').any(|s| {
                    s.trim()
                        .strip_prefix("filename=")
                        .map(|s| s.trim_matches('"') == filename)
                        .unwrap_or(false)
                }) {
                    return false;
                }
            }
            matches(p, r)
        };
        !parts.is_empty()
            && if p["matchingType"] == "ALL" {
                parts.iter().all(check)
            } else {
                parts.iter().any(check)
            }
    })
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn java_fixtures() {
        let _locale = locale::TestLocale::new("en_NL"); // Captured oracle fixture locale.
        let cases: Value =
            serde_json::from_str(include_str!("../../testdata/matcher_cases.json")).unwrap();
        let mut failures = vec![];
        let mut count = 0;
        for c in cases.as_array().unwrap() {
            let r = Request {
                method: "POST".into(),
                url: "/test".into(),
                body: c["body"].as_str().unwrap_or("").as_bytes().to_vec(),
                ..Default::default()
            };
            let got = matches(&c["pattern"], &r);
            count += 1;
            if got != c["matches"].as_bool().unwrap() {
                failures.push(c["name"].as_str().unwrap().to_owned())
            }
        }
        assert!(
            failures.is_empty(),
            "{}/{} mismatches: {:#?}",
            failures.len(),
            count,
            failures
        );
    }
}
#[path = "matcher_distance.rs"]
mod distance;
pub use distance::{request_distance, value_distance};
/// Verification pattern for a received message; binary messages have no string view.
pub fn message_matches(
    pattern: &Value,
    channel: Option<&Request>,
    body: &[u8],
    present: bool,
    binary: bool,
) -> bool {
    if let Some(p) = pattern.get("channel") {
        if !channel.map(|r| matches(p, r)).unwrap_or(false) {
            return false;
        }
    }
    if let Some(p) = pattern.get("body") {
        if let Some(expected) = p["binaryEqualTo"].as_str() {
            return present && STANDARD.decode(expected).ok().as_deref() == Some(body);
        }
        return value_present(
            p,
            if binary {
                ""
            } else {
                std::str::from_utf8(body).unwrap_or("")
            },
            present && !binary,
        );
    }
    true
}
pub fn message_channel_matches(
    pattern: &Value,
    channel: Option<&Request>,
    provider: &str,
    name: &str,
) -> bool {
    if pattern["type"]
        .as_str()
        .map(|s| s.eq_ignore_ascii_case("fixed"))
        .unwrap_or(false)
    {
        return channel.is_none()
            && pattern["channelProvider"].as_str() == Some(provider)
            && pattern
                .get("channelName")
                .map(|p| value_matches(p, name))
                .unwrap_or(true);
    }
    pattern
        .get("initiatingRequestPattern")
        .map(|p| channel.map(|r| matches(p, r)).unwrap_or(false))
        .unwrap_or(true)
}
#[cfg(test)]
mod message_tests {
    use super::*;
    #[test]
    fn java_messages() {
        let cases: Value =
            serde_json::from_str(include_str!("../../testdata/matcher_message_cases.json"))
                .unwrap();
        for c in cases.as_array().unwrap() {
            let channel = c.get("channel").filter(|v| v.is_object()).map(|v| Request {
                method: v["method"].as_str().unwrap_or("").into(),
                url: v["url"].as_str().unwrap_or("").into(),
                ..Default::default()
            });
            let got = message_matches(
                &c["pattern"],
                channel.as_ref(),
                c["body"].as_str().unwrap_or("").as_bytes(),
                c["present"].as_bool().unwrap_or(false),
                c["binary"].as_bool().unwrap_or(false),
            );
            assert_eq!(got, c["matches"].as_bool().unwrap(), "{}", c["name"]);
        }
    }
}
#[path = "matcher_locale.rs"]
mod locale;
pub use locale::default_locale;
#[cfg(test)]
mod multipart_binary_tests {
    use super::*;
    #[test]
    fn preserves_binary_part_bytes() {
        let mut body = b"--b\r\nContent-Disposition: form-data; name=\"file\"\r\n\r\n".to_vec();
        body.extend([0, 255, 128, 1]);
        body.extend(b"\r\n--b--\r\n");
        let req = Request {
            headers: vec![(
                "Content-Type".into(),
                "multipart/form-data; boundary=b".into(),
            )],
            body,
            ..Default::default()
        };
        assert!(matches(
            &json!({"multipartPatterns":[{"bodyPatterns":[{"binaryEqualTo":"AP+AAQ=="}]}]}),
            &req
        ));
    }
}
pub fn xml_comparison_warning(expected: &str, actual: &str) -> bool {
    xml::comparison_warning(expected, actual)
}
pub fn comparison_warnings(pattern: &Value, request: &Request) -> Vec<Value> {
    pattern["bodyPatterns"].as_array().map(|ps|ps.iter().filter(|p|p["equalToXml"].as_str().map(|s|xml_comparison_warning(s,&request.body_text())).unwrap_or(false)).map(|_|json!({"type":"WARNING","data":{"message":"Caught exception during comparison"}})).collect()).unwrap_or_default()
}
