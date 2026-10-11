// Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
use serde_json::{Map, Value};
pub fn serialize(v: &Value) -> String {
    if v.as_object().is_some_and(|m| m.is_empty()) {
        return "{}".into();
    }
    let mut s = String::new();
    write(v, 0, "", &mut s);
    s
}
fn order<'a>(m: &'a Map<String, Value>, c: &str) -> Vec<&'a str> {
    let schema = if m.contains_key("channels") {
        "channels meta channelsMessaged"
    } else if m.contains_key("type") && m.contains_key("data") {
        "type timeOffsetNanos data"
    } else if m.contains_key("matchResult") {
        "request stubMapping requestPattern matchResult"
    } else if m.contains_key("eventType") {
        "id eventType channel message stubMapping wasMatched timestamp subEvents"
    } else if m.contains_key("trigger") {
        "id name priority trigger actions metadata"
    } else if m.contains_key("open") {
        "type id open initiatingRequest providerName channelName"
    } else if m.contains_key("driverType") {
        "name driverType settings"
    } else if m.contains_key("wasMatched") {
        "id request responseDefinition response wasMatched timing subEvents stubMapping"
    } else if m.contains_key("absoluteUrl") || m.contains_key("browserProxyRequest") {
        "url absoluteUrl method clientIp headers cookies browserProxyRequest loggedDate bodyAsBase64 body protocol scheme host port loggedDateString queryParams formParams"
    } else if m.contains_key("request") && m.contains_key("response") {
        "id name request newRequest response persistent priority scenarioName requiredScenarioState newScenarioState postServeActions serveEventListeners metadata"
    } else if m.contains_key("possibleStates") {
        "id name state possibleStates mappings"
    } else if m.contains_key("bodyAsBase64") && m.contains_key("status") {
        "status headers bodyAsBase64 body fromProxy"
    } else if m.contains_key("code") {
        "code source title detail"
    } else {
        match c {
            "request" | "requestPattern" | "initiatingRequestPattern" => {
                "scheme host port url urlPattern urlPath urlPathPattern urlPathTemplate clientIp method headers pathParameters queryParameters formParameters cookies basicAuthCredentials bodyPatterns multipartPatterns customMatcher"
            }
            "response" | "responseDefinition" => {
                "status statusMessage body jsonBody base64Body bodyFileName headers additionalProxyRequestHeaders removeProxyRequestHeaders fixedDelayMilliseconds delayDistribution chunkedDribbleDelay proxyBaseUrl proxyUrlPrefixToRemove fault transformers transformerParameters fromConfiguredStub"
            }
            "matchResult" => "subEvents diffDescriptions distance",
            "timing" => "addedDelay processTime responseSendTime serveTime totalTime",
            "trigger" => "type channel message requestPattern stubId",
            "actions" => "type message channelTarget transformers transformerParameters",
            "channelTarget" => "type channelType requestPattern providerName channelName",
            "message" => "body base64Body headers",
            "body" => {
                if m.get("format") == Some(&Value::String("json".into())) {
                    "data format"
                } else {
                    "format compression charset data base64Data filePath dataStore dataRef"
                }
            }
            _ => "",
        }
    };
    let mut keys = Vec::new();
    for k in schema.split_whitespace() {
        if let Some((key, v)) = m.get_key_value(k) {
            if !v.is_null() || k == "jsonBody" {
                keys.push(key.as_str())
            }
        }
    }
    for (k, v) in m {
        if !keys.contains(&k.as_str()) && (!v.is_null() || k == "jsonBody") {
            keys.push(k)
        }
    }
    keys
}
fn write(v: &Value, d: usize, c: &str, s: &mut String) {
    if matches!(c, "metadata" | "jsonBody" | "transformerParameters")
        || c == "data" && v.get("browserProxyRequest").is_none() && v.get("fromProxy").is_none()
    {
        write_raw(v, d, s);
        return;
    }
    let projected = project(v, c);
    let v = projected.as_ref().unwrap_or(v);
    match v {
        Value::Object(m) => {
            let keys = order(m, c);
            if keys.is_empty() {
                s.push_str("{ }");
                return;
            }
            s.push_str("{\n");
            for (i, k) in keys.iter().enumerate() {
                if i > 0 {
                    s.push_str(",\n")
                }
                s.push_str(&"  ".repeat(d + 1));
                s.push_str(&serde_json::to_string(k).unwrap());
                s.push_str(" : ");
                write(&m[*k], d + 1, k, s)
            }
            s.push('\n');
            s.push_str(&"  ".repeat(d));
            s.push('}')
        }
        Value::Array(a) => {
            s.push_str("[ ");
            for (i, v) in a.iter().enumerate() {
                if i > 0 {
                    s.push_str(", ")
                }
                write(v, d, c, s)
            }
            if !a.is_empty() {
                s.push(' ')
            }
            s.push(']')
        }
        Value::Number(n) if c == "distance" || c.ends_with("Number") => {
            if let Some(f) = n.as_f64() {
                s.push_str(&java_double(f))
            } else {
                s.push_str(&n.to_string())
            }
        }
        _ => s.push_str(&serde_json::to_string(v).unwrap()),
    }
}

fn project(v: &Value, c: &str) -> Option<Value> {
    use base64::Engine;
    let m = v.as_object()?;
    if c == "body"
        && (m.contains_key("data") || m.contains_key("filePath") || m.contains_key("base64Data"))
    {
        let mut m = m.clone();
        let data = m.get("data").cloned();
        let binary = m.get("format").and_then(Value::as_str) == Some("binary")
            || m.contains_key("compression");
        if data.as_ref().is_some_and(|d| d.is_object() || d.is_array()) {
            m.insert("format".into(), Value::String("json".into()));
        } else if binary {
            if let Some(Value::String(data)) = data {
                m.remove("data");
                m.insert(
                    "base64Data".into(),
                    Value::String(base64::engine::general_purpose::STANDARD.encode(data)),
                );
            }
            m.remove("format");
        } else if m.get("format").and_then(Value::as_str) == Some("text") {
            m.remove("format");
        }
        return Some(Value::Object(m));
    }
    if c == "response" || c == "responseDefinition" {
        let body = m.get("body")?.as_object()?;
        let mut m = m.clone();
        m.remove("body");
        if let Some(file) = body.get("filePath") {
            m.insert("bodyFileName".into(), file.clone());
        } else if let Some(data) = body.get("base64Data") {
            m.insert("base64Body".into(), data.clone());
        } else if let Some(data) = body.get("data") {
            if data.is_object()
                || data.is_array()
                || body.get("format").and_then(Value::as_str) == Some("json")
            {
                m.insert("jsonBody".into(), data.clone());
            } else if body.get("format").and_then(Value::as_str) == Some("binary")
                || body.contains_key("compression")
            {
                let text = data.as_str().unwrap_or("");
                let bytes = if body.get("charset").and_then(Value::as_str) == Some("UTF-16LE") {
                    text.encode_utf16().flat_map(u16::to_le_bytes).collect()
                } else {
                    text.as_bytes().to_vec()
                };
                m.insert(
                    "base64Body".into(),
                    Value::String(base64::engine::general_purpose::STANDARD.encode(bytes)),
                );
            } else {
                m.insert("body".into(), data.clone());
            }
        }
        return Some(Value::Object(m));
    }
    None
}

fn write_raw(v: &Value, d: usize, s: &mut String) {
    match v {
        Value::Object(m) => {
            if m.is_empty() {
                s.push_str("{ }");
                return;
            }
            s.push_str("{\n");
            for (i, (k, v)) in m.iter().enumerate() {
                if i > 0 {
                    s.push_str(",\n")
                }
                s.push_str(&"  ".repeat(d + 1));
                s.push_str(&serde_json::to_string(k).unwrap());
                s.push_str(" : ");
                write_raw(v, d + 1, s)
            }
            s.push('\n');
            s.push_str(&"  ".repeat(d));
            s.push('}')
        }
        Value::Array(a) => {
            s.push_str("[ ");
            for (i, v) in a.iter().enumerate() {
                if i > 0 {
                    s.push_str(", ")
                }
                write_raw(v, d, s)
            }
            if !a.is_empty() {
                s.push(' ')
            }
            s.push(']')
        }
        _ => s.push_str(&serde_json::to_string(v).unwrap()),
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn arbitrary_metadata_preserves_nulls_order_and_reserved_names() {
        let value: Value = serde_json::from_str(
            r#"{"metadata":{"response":null,"request":{"method":null,"url":"x"},"z":1,"a":2}}"#,
        )
        .unwrap();
        let raw = serialize(&value);
        assert!(raw.contains("\"response\" : null"));
        assert!(raw.find("\"z\"").unwrap() < raw.find("\"a\"").unwrap());
        assert!(raw.contains("\"method\" : null"));
    }
}

fn java_double(f: f64) -> String {
    if f != 0.0 && (f.abs() < 0.001 || f.abs() >= 1e7) {
        let raw = format!("{f:e}");
        let (m, e) = raw.split_once('e').unwrap();
        format!(
            "{}E{}",
            if m.contains('.') {
                m.to_string()
            } else {
                format!("{m}.0")
            },
            e.parse::<i32>().unwrap()
        )
    } else {
        let raw = f.to_string();
        if raw.contains('.') {
            raw
        } else {
            format!("{raw}.0")
        }
    }
}
