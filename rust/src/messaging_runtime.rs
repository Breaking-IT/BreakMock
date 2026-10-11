// Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
use crate::{Request, State, matcher, template};
use base64::{Engine, engine::general_purpose::STANDARD};
use serde_json::{Value, json};
pub fn record(s: &mut State, id: &str, event: &str, message: Value, mapping: Option<&Value>) {
    if s.no_journal {
        return;
    }
    let Some(channel) = s.channels.iter().find(|c| c["id"] == id).cloned() else {
        return;
    };
    let mut entry = json!({"id":uuid::Uuid::new_v4().to_string(),"eventType":event,"channel":channel,"message":message,"wasMatched":mapping.is_some()||event=="sent","timestamp":chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis,true)});
    if let Some(m) = mapping {
        entry["stubMapping"] = m.clone();
    }
    s.messages.insert(0, entry);
    s.message_notify.notify_waiters();
    if s.journal_limit > 0 {
        s.messages.truncate(s.journal_limit)
    }
}
pub fn send(s: &mut State, id: &str, message: Value) -> Result<(), String> {
    if let Some(tx) = s.channel_senders.get(id) {
        tx.try_send(message.clone())
            .map_err(|_| "Channel closed".to_string())?;
    }
    record(s, id, "sent", message, None);
    Ok(())
}
pub fn close(s: &mut State, id: &str) {
    if let Some(tx) = s.channel_senders.remove(id) {
        let _ = tx.try_send(json!({"_close":true}));
    }
    s.channel_requests.remove(id);
    s.channel_types.remove(id);
    s.channels.retain(|c| c["id"] != id);
}
fn ordered(s: &State) -> Vec<Value> {
    let mut v: Vec<_> = s.message_mappings.iter().enumerate().collect();
    v.sort_by_key(|(i, m)| (m["priority"].as_i64().unwrap_or(5), *i));
    v.into_iter().map(|(_, m)| m.clone()).collect()
}
pub fn incoming(s: &mut State, id: &str, message: Value) -> Result<(), String> {
    let req = s.channel_requests.get(id).cloned().unwrap_or_default();
    let typ = s
        .channel_types
        .get(id)
        .map(String::as_str)
        .unwrap_or("fixed");
    let channel = s
        .channels
        .iter()
        .find(|c| c["id"] == id)
        .cloned()
        .unwrap_or(Value::Null);
    for m in ordered(s) {
        let t = &m["trigger"];
        if !t["type"].is_null() && t["type"] != "message" {
            continue;
        }
        let cp = &t["channel"];
        if let Some(kind) = cp["type"].as_str() {
            if kind != typ {
                continue;
            }
        }
        if cp.get("initiatingRequestPattern").is_some()
            && !matcher::matches(&cp["initiatingRequestPattern"], &req)
        {
            continue;
        }
        if cp["type"] == "fixed"
            && (cp["providerName"] != channel["providerName"]
                || cp["channelName"] != channel["channelName"])
        {
            continue;
        }
        let body = message["body"]
            .as_str()
            .map(String::from)
            .unwrap_or_else(|| {
                String::from_utf8_lossy(
                    &STANDARD
                        .decode(message["base64Body"].as_str().unwrap_or(""))
                        .unwrap_or_default(),
                )
                .into_owned()
            });
        if t["message"].get("body").is_some()
            && !matcher::value_matches(&t["message"]["body"], &body)
        {
            continue;
        }
        actions(s, &m, Some(id), &req, &message)?;
        record(s, id, "received", message, Some(&m));
        return Ok(());
    }
    record(s, id, "received", message, None);
    Ok(())
}
pub fn http_trigger(s: &mut State, req: &Request, mapping: Option<&Value>) -> Result<(), String> {
    for m in ordered(s) {
        let t = &m["trigger"];
        let matched = match t["type"].as_str() {
            Some("http-stub") => mapping.is_some_and(|m| m["id"] == t["stubId"]),
            Some("http-request") => matcher::matches(&t["requestPattern"], req),
            _ => false,
        };
        if matched {
            return actions(s, &m, None, req, &Value::Null);
        }
    }
    Ok(())
}
fn message(
    def: &Value,
    action: &Value,
    req: &Request,
    incoming: &Value,
    root: &str,
) -> Result<Value, String> {
    let body = &def["body"];
    let binary = body["format"] == "binary";
    let file = body["filePath"].as_str().or_else(|| {
        if body["dataStore"] == "__files" {
            body["dataRef"].as_str()
        } else {
            None
        }
    });
    let data = if let Some(file) = file {
        std::fs::read(std::path::Path::new(root).join("__files").join(file))
            .map_err(|_| format!("Not found in blob store: {file}"))?
    } else if let Some(b) = body["base64Data"].as_str() {
        STANDARD.decode(b).unwrap_or_default()
    } else if let Some(b) = body["data"].as_str().or_else(|| body.as_str()) {
        b.as_bytes().to_vec()
    } else {
        Vec::new()
    };
    let mut m = json!({});
    if binary {
        m["base64Body"] = json!(STANDARD.encode(data))
    } else {
        let mut text = String::from_utf8_lossy(&data).into_owned();
        if file.is_none() {
            let model = json!({"request":template::request_model(req),"message":incoming,"parameters":action["transformerParameters"]});
            text = template::render_with_model(&text, &model).map_err(|e| {
                let hash = text
                    .encode_utf16()
                    .fold(0i32, |a, c| a.wrapping_mul(31).wrapping_add(c as i32));
                format!("inline@{:x}:{e}", hash.wrapping_abs() as u32)
            })?;
        }
        m["body"] = json!(text)
    }
    if let Some(h) = def.get("headers") {
        let mut headers = h.clone();
        let model = json!({"request":template::request_model(req),"message":incoming,"parameters":action["transformerParameters"]});
        if let Some(map) = headers.as_object_mut() {
            for (_, v) in map {
                if let Some(a) = v.as_array_mut() {
                    for v in a {
                        if let Some(t) = v.as_str() {
                            *v = json!(template::render_with_model(t, &model)?);
                        }
                    }
                } else if let Some(t) = v.as_str() {
                    *v = json!(template::render_with_model(t, &model)?);
                }
            }
        }
        m["headers"] = headers;
    }
    Ok(m)
}
fn actions(
    s: &mut State,
    m: &Value,
    origin: Option<&str>,
    req: &Request,
    incoming: &Value,
) -> Result<(), String> {
    if let Some(actions) = m["actions"].as_array() {
        for action in actions {
            if action["type"] != "send" {
                continue;
            }
            let mut msg = message(&action["message"], action, req, incoming, &s.root_dir)?;
            if origin.is_some() {
                msg.as_object_mut().unwrap().remove("headers");
            }
            let t = &action["channelTarget"];
            let kind = t["type"].as_str().unwrap_or("originating");
            let ids: Vec<String> = match kind {
                "originating" => origin.into_iter().map(String::from).collect(),
                "fixed-channel" => s
                    .channels
                    .iter()
                    .filter(|c| {
                        c["type"] == "fixed"
                            && c["providerName"] == t["providerName"]
                            && c["channelName"] == t["channelName"]
                    })
                    .take(1)
                    .filter_map(|c| c["id"].as_str().map(String::from))
                    .collect(),
                "request-initiated" => s
                    .channel_requests
                    .iter()
                    .filter(|(id, r)| {
                        t["channelType"]
                            .as_str()
                            .is_none_or(|k| s.channel_types.get(*id).is_some_and(|ct| ct == k))
                            && matcher::matches(&t["requestPattern"], r)
                    })
                    .map(|(id, _)| id.clone())
                    .collect(),
                _ => vec![],
            };
            if kind == "fixed-channel" && ids.is_empty() {
                return Err(format!(
                    "No fixed channel named '{}' exists on provider '{}'",
                    t["channelName"].as_str().unwrap_or(""),
                    t["providerName"].as_str().unwrap_or("")
                ));
            }
            for id in ids {
                send(s, &id, msg.clone())?
            }
        }
    }
    Ok(())
}
pub fn admin_send(s: &mut State, v: &Value) -> Value {
    let ids: Vec<String> = s
        .channel_requests
        .iter()
        .filter(|(id, r)| {
            v["type"]
                .as_str()
                .is_none_or(|t| s.channel_types.get(*id).is_some_and(|ct| ct == t))
                && matcher::matches(&v["initiatingRequest"], r)
        })
        .map(|(id, _)| id.clone())
        .collect();
    let mut channels = Vec::new();
    for id in ids {
        if let Ok(mut msg) = message(
            &v["message"],
            &Value::Null,
            &Request::default(),
            &Value::Null,
            &s.root_dir,
        ) {
            msg.as_object_mut().unwrap().remove("headers");
            if send(s, &id, msg).is_ok() {
                if let Some(c) = s.channels.iter().find(|c| c["id"] == id) {
                    channels.push(c.clone())
                }
            }
        }
    }
    json!({"channelsMessaged":channels.len(),"channels":channels})
}
