use crate::{Request, Response, State};
use serde_json::{Value, json};
fn reply(status: u16, v: Value) -> Response {
    Response::json(status, &v)
}
fn empty() -> Response {
    reply(200, json!({}))
}
fn id() -> Value {
    json!(uuid::Uuid::new_v4().to_string())
}
fn defaults(mut v: Value) -> Value {
    if !v.is_object() {
        v = json!({})
    }
    let m = v.as_object_mut().unwrap();
    m.remove("uuid");
    m.remove("$schema");
    m.entry("id").or_insert_with(id);
    m.entry("trigger").or_insert(json!({"type":"message"}));
    if let Some(a) = m.get_mut("actions").and_then(Value::as_array_mut) {
        for action in a {
            if action["type"] == "send" && action.get("channelTarget").is_none() {
                action["channelTarget"] = json!({"type":"originating"});
            }
        }
    }
    if m.get("actions")
        .and_then(Value::as_array)
        .is_some_and(|a| a.is_empty())
    {
        m.remove("actions");
    }
    normalize_nested(&mut v);
    v
}
fn error(code: u16, title: String) -> Response {
    let mut e = json!({"code":if code==422{10}else{code},"title":title});
    if title.contains("cannot be changed") {
        e["source"] = json!({"pointer":title.split_whitespace().next().unwrap_or("")});
    }
    reply(code, json!({"errors":[e]}))
}
fn metadata(v: &Value, p: &Value) -> bool {
    crate::matcher::value_matches(p, &serde_json::to_string(&v["metadata"]).unwrap())
}
fn message_matches(e: &Value, p: &Value) -> bool {
    use base64::Engine;
    let body = e["message"]["body"]
        .as_str()
        .map(|s| s.as_bytes().to_vec())
        .unwrap_or_else(|| {
            base64::engine::general_purpose::STANDARD
                .decode(e["message"]["base64Body"].as_str().unwrap_or(""))
                .unwrap_or_default()
        });
    if let Some(pattern) = p.get("body") {
        if let Some(expected) = pattern["binaryEqualTo"].as_str() {
            if base64::engine::general_purpose::STANDARD
                .decode(expected)
                .ok()
                .as_deref()
                != Some(body.as_slice())
            {
                return false;
            }
        } else if !crate::matcher::value_matches(pattern, &String::from_utf8_lossy(&body)) {
            return false;
        }
    }
    if let Some(channel) = p.get("channel").filter(|v| !v.is_null()) {
        if e["channel"]["initiatingRequest"].is_null() {
            return false;
        }
        if !crate::matcher::matches(
            channel,
            &crate::admin::logged_request(&e["channel"]["initiatingRequest"]),
        ) {
            return false;
        }
    }
    true
}

pub fn handle(req: &Request, s: &mut State) -> Option<Response> {
    let p = req.path().strip_prefix("/__admin")?;
    if ![
        "/message-mappings",
        "/messages",
        "/channels",
        "/channel-providers",
    ]
    .iter()
    .any(|prefix| p.starts_with(prefix))
    {
        return None;
    }
    let v: Value = serde_json::from_slice(&req.body).unwrap_or(json!({}));
    let method = req.method.as_str();
    if method == "POST" || method == "PUT" {
        let keys: &[&str] = if p == "/channel-providers" {
            &["name", "driverType"]
        } else if p == "/channels" {
            &["providerName", "channelName"]
        } else if p == "/channels/send" {
            &["message"]
        } else {
            &[]
        };
        for k in keys {
            if let Some(e) = required(&v, k) {
                return Some(e);
            }
        }
        if p.starts_with("/message-mappings")
            && p != "/message-mappings/find-by-metadata"
            && p != "/message-mappings/remove-by-metadata"
        {
            if let Some(e) = validate(&v) {
                return Some(e);
            }
        }
    }
    let response = match (method, p) {
        ("GET", "/message-mappings") => reply(
            200,
            json!({"messageMappings":s.message_mappings,"meta":{"total":s.message_mappings.len()}}),
        ),
        ("POST", "/message-mappings") => {
            let m = defaults(v);
            s.message_mappings.insert(0, m.clone());
            reply(201, m)
        }
        ("DELETE", "/message-mappings") => {
            s.message_mappings.clear();
            empty()
        }
        ("POST", "/message-mappings/find-by-metadata") => {
            let a: Vec<_> = s
                .message_mappings
                .iter()
                .filter(|m| metadata(m, &v))
                .cloned()
                .collect();
            reply(200, json!({"messageMappings":a,"meta":{"total":a.len()}}))
        }
        ("POST", "/message-mappings/remove-by-metadata") => {
            s.message_mappings.retain(|m| !metadata(m, &v));
            empty()
        }
        ("GET", "/messages") => reply(
            200,
            json!({"messageServeEvents":s.messages,"messageJournalDisabled":s.no_journal}),
        ),
        ("DELETE", "/messages") => {
            s.messages.clear();
            empty()
        }
        ("POST", "/messages/count") => reply(
            200,
            json!({"count":s.messages.iter().filter(|e|message_matches(e,&v)).count(),"messageJournalDisabled":s.no_journal}),
        ),
        ("POST", "/messages/find") => reply(
            200,
            json!({"messageServeEvents":s.messages.iter().filter(|e|message_matches(e,&v)).cloned().collect::<Vec<_>>()}),
        ),
        ("POST", "/messages/wait") => reply(
            200,
            s.messages
                .iter()
                .find(|e| message_matches(e, &v["pattern"]))
                .cloned()
                .unwrap_or(Value::Null),
        ),
        ("POST", "/messages/wait-for-count") => {
            let a: Vec<_> = s
                .messages
                .iter()
                .filter(|e| message_matches(e, &v["pattern"]))
                .cloned()
                .collect();
            reply(
                200,
                json!({"messageServeEvents":a,"messageJournalDisabled":s.no_journal}),
            )
        }
        ("POST", "/messages/remove") => {
            let removed: Vec<_> = s
                .messages
                .iter()
                .filter(|e| message_matches(e, &v))
                .cloned()
                .collect();
            s.messages.retain(|e| !message_matches(e, &v));
            reply(200, json!({"messageServeEvents":removed}))
        }
        ("GET", "/channel-providers") => {
            let mut providers = s.providers.clone();
            providers.sort_by_key(|p| java_hash(p["name"].as_str().unwrap_or("")) & 15);
            reply(
                200,
                json!({"channelProviders":providers,"meta":{"total":providers.len()}}),
            )
        }
        ("POST", "/channel-providers") => {
            if v["name"].is_null() {
                error(422, "name is required".into())
            } else if v["driverType"].is_null() {
                error(422, "driverType is required".into())
            } else if v["driverType"] != "in-memory" {
                error(
                    422,
                    format!(
                        "No driver registered for type: {}",
                        v["driverType"].as_str().unwrap_or("")
                    ),
                )
            } else {
                let mut v = v;
                if v["settings"].is_null() {
                    v["settings"] = json!({})
                }
                s.providers.retain(|p| p["name"] != v["name"]);
                s.providers.push(v.clone());
                reply(201, v)
            }
        }
        ("GET", "/channels") => reply(
            200,
            json!({"channels":s.channels,"meta":{"total":s.channels.len()}}),
        ),
        ("POST", "/channels") => {
            if v["providerName"].is_null() {
                error(422, "providerName is required".into())
            } else if v["channelName"].is_null() {
                error(422, "channelName is required".into())
            } else if !s.providers.iter().any(|p| p["name"] == v["providerName"]) {
                error(
                    422,
                    format!(
                        "No channel provider registered with name: {}",
                        v["providerName"].as_str().unwrap_or("")
                    ),
                )
            } else if s.channels.iter().any(|c| {
                c["providerName"] == v["providerName"] && c["channelName"] == v["channelName"]
            }) {
                error(
                    409,
                    format!(
                        "Channel {} already exists via provider {}",
                        v["channelName"].as_str().unwrap_or(""),
                        v["providerName"].as_str().unwrap_or("")
                    ),
                )
            } else {
                let c = json!({"type":"fixed","id":id(),"open":true,"providerName":v["providerName"],"channelName":v["channelName"]});
                s.channels.push(c.clone());
                reply(201, c)
            }
        }
        ("POST", "/channels/send") => {
            if v["message"].is_null() {
                error(422, "Message must not be null".into())
            } else if v["type"] == "fixed" {
                if let Some(c) = s.channels.iter().find(|c| {
                    c["providerName"] == v["providerName"] && c["channelName"] == v["channelName"]
                }) {
                    let chosen = s
                        .message_mappings
                        .iter()
                        .find(|m| {
                            m["trigger"]["type"] == "message"
                                && (m["trigger"]["message"]["body"].is_null()
                                    || crate::matcher::value_matches(
                                        &m["trigger"]["message"]["body"],
                                        v["message"]["body"].as_str().unwrap_or(""),
                                    ))
                        })
                        .cloned();
                    if let Some(m) = &chosen {
                        if let Some(actions) = m["actions"].as_array() {
                            for action in actions {
                                let t = &action["channelTarget"];
                                if t["type"] == "fixed-channel"
                                    && !s.channels.iter().any(|c| {
                                        c["providerName"] == t["providerName"]
                                            && c["channelName"] == t["channelName"]
                                    })
                                {
                                    return Some(Response::new(404, vec![]));
                                }
                            }
                        }
                    }
                    if !s.no_journal {
                        let message = if v["message"]["body"].is_object() {
                            json!({"body":v["message"]["body"]["data"]})
                        } else {
                            v["message"].clone()
                        };
                        s.messages.insert(0,json!({"id":id(),"eventType":"received","channel":c,"message":message,"stubMapping":chosen,"wasMatched":chosen.is_some(),"timestamp":chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis,true)}));
                        s.message_notify.notify_waiters();
                    }
                }
                Response::new(200, vec![])
            } else {
                reply(200, crate::messaging_runtime::admin_send(s, &v))
            }
        }
        _ if p.starts_with("/message-mappings/") => {
            let ident = &p[18..];
            match method {
                "GET" => s
                    .message_mappings
                    .iter()
                    .find(|m| m["id"] == ident)
                    .map(|m| reply(200, m.clone()))
                    .unwrap_or_else(|| Response::new(404, vec![])),
                "DELETE" => {
                    s.message_mappings.retain(|m| m["id"] != ident);
                    empty()
                }
                "PUT" => {
                    if let Some(i) = s.message_mappings.iter().position(|m| m["id"] == ident) {
                        let mut m = defaults(v);
                        m["id"] = json!(ident);
                        s.message_mappings.remove(i);
                        s.message_mappings.push(m.clone());
                        reply(200, m)
                    } else {
                        Response::new(404, vec![])
                    }
                }
                _ => Response::new(405, vec![]),
            }
        }
        _ if p.starts_with("/channel-providers/") => {
            let name = &p[19..];
            if method == "DELETE" {
                s.providers.retain(|p| p["name"] != name);
                empty()
            } else if let Some(i) = s.providers.iter().position(|p| p["name"] == name) {
                if method == "PUT" {
                    if s.providers[i]["driverType"] != v["driverType"] {
                        error(
                            422,
                            "driverType cannot be changed; use delete and re-register".into(),
                        )
                    } else if s.providers[i]["settings"] != v["settings"] {
                        error(
                            422,
                            "settings cannot be changed; use delete and re-register".into(),
                        )
                    } else {
                        s.providers[i] = v.clone();
                        reply(200, v)
                    }
                } else {
                    reply(200, s.providers[i].clone())
                }
            } else {
                Response::new(404, vec![])
            }
        }
        _ if p.starts_with("/channels/") => {
            let ident = &p[10..];
            if method == "DELETE" {
                crate::messaging_runtime::close(s, ident);
                empty()
            } else {
                s.channels
                    .iter()
                    .find(|c| c["id"] == ident)
                    .map(|c| reply(200, c.clone()))
                    .unwrap_or_else(|| Response::new(404, vec![]))
            }
        }
        _ if p.starts_with("/messages/") => {
            let ident = &p[10..];
            if method == "DELETE" {
                s.messages.retain(|c| c["id"] != ident);
                empty()
            } else {
                reply(
                    200,
                    s.messages
                        .iter()
                        .find(|c| c["id"] == ident)
                        .cloned()
                        .unwrap_or(Value::Null),
                )
            }
        }
        _ => Response::new(404, vec![]),
    };
    Some(response)
}
fn required(v: &Value, key: &str) -> Option<Response> {
    if v[key].is_null() {
        Some(reply(
            422,
            json!({"errors":[{"code":10,"source":{"pointer":key},"title":format!("{key} is required")}]}),
        ))
    } else {
        None
    }
}
fn validate(v: &Value) -> Option<Response> {
    if v["trigger"]["type"] == "http-request" {
        if let Some(e) = required(&v["trigger"], "requestPattern") {
            return Some(e);
        }
    }
    if v["trigger"]["type"] == "http-stub" {
        if let Some(e) = required(&v["trigger"], "stubId") {
            return Some(e);
        }
    }
    if let Some(a) = v["actions"].as_array() {
        for action in a {
            if action["type"] == "send" {
                if let Some(e) = required(action, "message") {
                    return Some(e);
                }
            }
            if action["channelTarget"]["type"] == "fixed-channel" {
                for k in ["providerName", "channelName"] {
                    if let Some(e) = required(&action["channelTarget"], k) {
                        return Some(e);
                    }
                }
            }
        }
    }
    None
}
fn normalize_nested(v: &mut Value) {
    match v {
        Value::Object(m) => {
            for (k, v) in m.iter_mut() {
                if ["metadata", "data", "transformerParameters"].contains(&k.as_str()) {
                    continue;
                }
                if k == "requestPattern" || k == "initiatingRequestPattern" {
                    if let Some(p) = v.as_object_mut() {
                        p.entry("method").or_insert(json!("ANY"));
                    }
                }
                normalize_nested(v);
            }
        }
        Value::Array(a) => {
            for v in a {
                normalize_nested(v)
            }
        }
        _ => {}
    }
}
fn java_hash(s: &str) -> u32 {
    let mut h = 0u32;
    for c in s.encode_utf16() {
        h = h.wrapping_mul(31).wrapping_add(c as u32)
    }
    h ^ (h >> 16)
}

/// Wait without holding the shared state mutex. Register before checking the journal
/// so an event arriving between the check and await cannot be missed.
pub async fn wait_if_needed(
    req: &Request,
    state: &std::sync::Arc<std::sync::Mutex<State>>,
) -> Option<Response> {
    let path = req.path();
    if req.method != "POST"
        || !["/__admin/messages/wait", "/__admin/messages/wait-for-count"].contains(&path)
    {
        return None;
    }
    let value: Value = match serde_json::from_slice(&req.body) {
        Ok(value) => value,
        Err(error) => {
            return Some(reply(
                422,
                json!({"errors":[{"code":10,"title":"Error parsing JSON","detail":error.to_string()}]}),
            ));
        }
    };
    let multiple = path.ends_with("wait-for-count");
    let count = if multiple {
        value["count"].as_u64().unwrap_or(1) as usize
    } else {
        1
    };
    let timeout = value["timeoutMillis"].as_u64().unwrap_or(0);
    let deadline = tokio::time::Instant::now() + std::time::Duration::from_millis(timeout);
    if multiple && value["count"].as_i64().is_some_and(|n| n < 0) {
        return Some(wait_error(
            req,
            &format!(
                "java.lang.IllegalArgumentException: {}",
                value["count"].as_i64().unwrap()
            ),
        ));
    }
    let notify = state.lock().unwrap().message_notify.clone();
    loop {
        let notified = notify.notified();
        tokio::pin!(notified);
        notified.as_mut().enable();
        let (mut found, disabled) = {
            let state = state.lock().unwrap();
            (
                state
                    .messages
                    .iter()
                    .filter(|e| message_matches(e, &value["pattern"]))
                    .cloned()
                    .collect::<Vec<_>>(),
                state.no_journal,
            )
        };
        if disabled {
            return Some(wait_error(
                req,
                "com.github.tomakehurst.wiremock.verification.MessageJournalDisabledException: The message journal is disabled, so no verification or message searching operations are available",
            ));
        }
        if found.len() >= count || tokio::time::Instant::now() >= deadline {
            found.truncate(count);
            return Some(if multiple {
                reply(
                    200,
                    json!({"messageServeEvents":found,"messageJournalDisabled":disabled}),
                )
            } else {
                reply(200, found.into_iter().next().unwrap_or(Value::Null))
            });
        }
        tokio::select! {_=&mut notified=>{},_=tokio::time::sleep_until(deadline)=>{}}
    }
}

/// Load native v4 message mapping documents separately from HTTP mapping files.
pub fn load_message_mappings(state: &mut State) -> std::io::Result<usize> {
    fn visit(path: &std::path::Path, paths: &mut Vec<std::path::PathBuf>) -> std::io::Result<()> {
        if !path.exists() {
            return Ok(());
        }
        for entry in std::fs::read_dir(path)? {
            let entry = entry?;
            let path = entry.path();
            if entry.file_type()?.is_dir() {
                visit(&path, paths)?
            } else if path.extension().is_some_and(|s| s == "json") {
                paths.push(path)
            }
        }
        Ok(())
    }
    let mut paths = vec![];
    visit(
        &std::path::Path::new(&state.root_dir).join("message-mappings"),
        &mut paths,
    )?;
    paths.sort();
    let mut loaded = vec![];
    for path in paths {
        let raw = std::fs::read(&path)?;
        let value: Value = serde_json::from_slice(&raw).map_err(|e| {
            std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                format!("{}: {e}", path.display()),
            )
        })?;
        let mappings = value
            .get("messageMappings")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_else(|| vec![value]);
        for mapping in mappings {
            if !mapping.is_object() {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::InvalidData,
                    format!("{}: message mapping must be an object", path.display()),
                ));
            }
            if let Some(error) = validate(&mapping) {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::InvalidData,
                    format!(
                        "{}: {}",
                        path.display(),
                        String::from_utf8_lossy(&error.body)
                    ),
                ));
            }
            loaded.push(defaults(mapping));
        }
    }
    let count = loaded.len();
    for mapping in loaded {
        state.message_mappings.insert(0, mapping);
    }
    Ok(count)
}

#[cfg(test)]
mod async_tests {
    use super::*;
    use std::sync::{Arc, Mutex};
    fn req(path: &str, body: Value) -> Request {
        Request {
            method: "POST".into(),
            url: format!("/__admin{path}"),
            body: serde_json::to_vec(&body).unwrap(),
            ..Default::default()
        }
    }
    fn state() -> Arc<Mutex<State>> {
        let mut s = State::default();
        s.channels.push(
            json!({"id":"channel","type":"fixed","open":true,"providerName":"p","channelName":"c"}),
        );
        Arc::new(Mutex::new(s))
    }
    fn incoming(state: &Arc<Mutex<State>>, body: &str) {
        let mut s = state.lock().unwrap();
        crate::messaging_runtime::incoming(&mut s, "channel", json!({"body":body})).unwrap();
    }
    #[tokio::test]
    async fn wait_observes_future_event_and_wakes_all_waiters() {
        let state = state();
        let mut tasks = vec![];
        for _ in 0..16 {
            let state = state.clone();
            tasks.push(tokio::spawn(async move {
                wait_if_needed(
                    &req(
                        "/messages/wait",
                        json!({"pattern":{"body":{"equalTo":"ready"}},"timeoutMillis":1000}),
                    ),
                    &state,
                )
                .await
                .unwrap()
            }));
        }
        tokio::time::sleep(std::time::Duration::from_millis(20)).await;
        incoming(&state, "other");
        tokio::time::sleep(std::time::Duration::from_millis(10)).await;
        assert!(tasks.iter().all(|t| !t.is_finished()));
        incoming(&state, "ready");
        for task in tasks {
            let response = tokio::time::timeout(std::time::Duration::from_millis(200), task)
                .await
                .unwrap()
                .unwrap();
            assert_eq!(
                serde_json::from_slice::<Value>(&response.body).unwrap()["message"]["body"],
                "ready"
            );
        }
    }
    #[tokio::test]
    async fn wait_count_partial_timeout_and_existing_limit() {
        let state = state();
        incoming(&state, "first");
        let start = tokio::time::Instant::now();
        let response = wait_if_needed(
            &req(
                "/messages/wait-for-count",
                json!({"count":2,"timeoutMillis":40}),
            ),
            &state,
        )
        .await
        .unwrap();
        assert!(start.elapsed() >= std::time::Duration::from_millis(35));
        assert_eq!(
            serde_json::from_slice::<Value>(&response.body).unwrap()["messageServeEvents"]
                .as_array()
                .unwrap()
                .len(),
            1
        );
        incoming(&state, "second");
        let response = wait_if_needed(
            &req(
                "/messages/wait-for-count",
                json!({"count":1,"timeoutMillis":1000}),
            ),
            &state,
        )
        .await
        .unwrap();
        let body: Value = serde_json::from_slice(&response.body).unwrap();
        assert_eq!(body["messageServeEvents"].as_array().unwrap().len(), 1);
        assert_eq!(body["messageServeEvents"][0]["message"]["body"], "second");
    }
    #[test]
    fn disk_message_mappings_single_bulk_and_atomic_failure() {
        let dir =
            std::env::temp_dir().join(format!("breakmock-rust-messages-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(dir.join("message-mappings/nested")).unwrap();
        std::fs::write(dir.join("message-mappings/a.json"), r#"{"name":"first"}"#).unwrap();
        std::fs::write(
            dir.join("message-mappings/nested/b.json"),
            r#"{"messageMappings":[{"name":"second"},{"name":"third"}]}"#,
        )
        .unwrap();
        let mut state = State {
            root_dir: dir.to_string_lossy().into_owned(),
            ..Default::default()
        };
        assert_eq!(load_message_mappings(&mut state).unwrap(), 3);
        assert_eq!(state.message_mappings[0]["name"], "third");
        std::fs::write(
            dir.join("message-mappings/invalid.json"),
            r#"{"actions":[{"type":"send"}]}"#,
        )
        .unwrap();
        assert!(load_message_mappings(&mut state).is_err());
        assert_eq!(state.message_mappings.len(), 3);
        std::fs::remove_dir_all(dir).unwrap();
    }
}

#[cfg(test)]
mod pattern_tests {
    use super::*;
    #[test]
    fn message_verification_matches_binary_and_initiating_request() {
        let event = json!({"channel":{"type":"websocket","initiatingRequest":{"method":"GET","url":"/stream?topic=a","headers":{"Host":"example.test"}}},"message":{"base64Body":"AP+A"}});
        assert!(message_matches(
            &event,
            &json!({"channel":{"urlPath":"/stream"},"body":{"binaryEqualTo":"AP+A"}})
        ));
        assert!(!message_matches(
            &event,
            &json!({"channel":{"urlPath":"/other"}})
        ));
        assert!(!message_matches(
            &event,
            &json!({"body":{"binaryEqualTo":"AA=="}})
        ));
        let fixed = json!({"channel":{"type":"fixed"},"message":{"body":"hi"}});
        assert!(!message_matches(&fixed, &json!({"channel":{}})));
    }
}

fn wait_error(req: &Request, message: &str) -> Response {
    let esc = |s: &str| {
        s.replace('&', "&amp;")
            .replace('<', "&lt;")
            .replace('>', "&gt;")
            .replace('"', "&quot;")
            .replace('\'', "&apos;")
    };
    let uri = esc(&format!(
        "http://{}{}",
        req.header("Host").unwrap_or("localhost"),
        req.url
    ));
    let msg = esc(message);
    let mut response = Response::new(
        500,
        format!(
            "<html>\n<head>\n<meta http-equiv=\"Content-Type\" content=\"text/html;charset=ISO-8859-1\"/>\n<title>Error 500 {msg}</title>\n</head>\n<body>\n<h2>HTTP ERROR 500 {msg}</h2>\n<table>\n<tr><th>URI:</th><td>{uri}</td></tr>\n<tr><th>STATUS:</th><td>500</td></tr>\n<tr><th>MESSAGE:</th><td>{msg}</td></tr>\n</table>\n\n</body>\n</html>\n"
        ),
    );
    response.headers = vec![
        ("Content-Type".into(), "text/html;charset=iso-8859-1".into()),
        (
            "Cache-Control".into(),
            "must-revalidate,no-cache,no-store".into(),
        ),
    ];
    response
}
