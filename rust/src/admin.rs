// Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
use crate::{Request, Response, State};
use base64::Engine;
use serde_json::{Value, json};
fn id() -> String {
    uuid::Uuid::new_v4().to_string()
}
fn answer(status: u16, v: Value) -> Response {
    Response::json(status, &v)
}
fn empty() -> Response {
    answer(200, json!({}))
}
pub(crate) fn normalize(mut v: Value) -> Value {
    if !v.is_object() {
        v = json!({})
    }
    let m = v.as_object_mut().unwrap();
    m.remove("uuid");
    m.remove("$schema");
    m.entry("id").or_insert_with(|| json!(id()));
    m.entry("request").or_insert(json!({}));
    m.entry("response").or_insert(json!({}));
    if let Some(r) = m.get_mut("request").and_then(Value::as_object_mut) {
        r.entry("method").or_insert(json!("ANY"));
    }
    if let Some(r) = m.get_mut("response").and_then(Value::as_object_mut) {
        let status = r
            .get("status")
            .and_then(|s| {
                s.as_i64()
                    .or_else(|| s.as_str().and_then(|s| s.parse().ok()))
            })
            .filter(|n| *n != 0)
            .unwrap_or(200);
        r.insert("status".into(), json!(status));
        if r.contains_key("jsonBody") {
            r.remove("body");
            r.remove("base64Body");
            r.remove("bodyFileName");
        } else if r.contains_key("base64Body") {
            r.remove("body");
            r.remove("bodyFileName");
        } else if r.contains_key("bodyFileName") {
            r.remove("body");
        }
    }
    v
}
pub fn handle(req: &Request, state: &mut State) -> Option<Response> {
    let p = req.path().strip_prefix("/__admin")?;
    if !req.body.is_empty()
        && !p.starts_with("/files/")
        && (req.method == "POST" || req.method == "PUT" || req.method == "PATCH")
    {
        if let Err(error) = serde_json::from_slice::<Value>(&req.body) {
            return Some(answer(
                422,
                json!({"errors":[{"code":10,"title":"Error parsing JSON","detail":error.to_string()}]}),
            ));
        }
    }
    if let Some(r) = crate::admin_near::handle(req, state) {
        return Some(r);
    }
    if let Some(r) = crate::admin_recording::handle(req, state) {
        return Some(r);
    };
    if let Some(r) = crate::admin_assets::handle(req) {
        return Some(r);
    };
    if let Some(r) = crate::admin_messaging::handle(req, state) {
        return Some(r);
    };
    let v: Value = serde_json::from_slice(&req.body).unwrap_or(json!({}));
    let method = req.method.as_str();
    if (method == "POST" || method == "PUT")
        && (p == "/mappings"
            || p == "/mappings/edit"
            || p.starts_with("/mappings/") && uuid::Uuid::parse_str(&p[10..]).is_ok())
    {
        if let Some(error) = validation(&v) {
            return Some(error);
        }
    }
    let out = match (method, p) {
        ("GET", "/version") => {
            if req.header("Accept") == Some("text/plain") {
                Response::new(200, b"4.0.0-beta.40".to_vec())
            } else {
                answer(200, json!({"version":"4.0.0-beta.40"}))
            }
        }
        ("GET", "/health") => answer(
            200,
            json!({"status":"healthy","message":"Wiremock is ok","version":"4.0.0-beta.40","uptimeInSeconds":0,"timestamp":chrono::Utc::now().to_rfc3339()}),
        ),
        ("GET", "/certs/wiremock-ca.crt") => Response::new(500, vec![]),
        ("POST", "/reset") => {
            state.mappings = state.default_mappings.clone();
            state.requests.clear();
            state.scenarios.clear();
            Response::new(200, vec![])
        }
        ("GET", "/settings") => answer(
            200,
            json!({"settings":if state.settings.is_null(){json!({"proxyPassThrough":false})}else{state.settings.clone()}}),
        ),
        ("POST" | "PUT", "/settings") => {
            state.settings = v;
            if state.settings.get("proxyPassThrough").is_none() {
                state.settings["proxyPassThrough"] = json!(false)
            }
            Response::new(200, vec![])
        }
        ("PATCH", "/settings/extended") => {
            if let (Some(ext), Some(patch)) = (
                state.settings["extended"].as_object_mut(),
                v["extended"].as_object(),
            ) {
                for (k, v) in patch {
                    ext.insert(k.clone(), v.clone());
                }
                state.settings["proxyPassThrough"] = json!(true);
                empty()
            } else {
                Response::new(500, vec![])
            }
        }
        ("GET", "/mappings" | "/") => answer(
            200,
            json!({"mappings":state.mappings,"meta":{"total":state.mappings.len()}}),
        ),
        ("POST", "/mappings") => {
            let m = normalize(v);
            if state.mappings.iter().any(|old| old["id"] == m["id"]) {
                answer(
                    422,
                    json!({"errors":[{"code":109,"title":"Duplicate stub mapping ID","detail":format!("ID of the provided stub mapping '{}' is already taken by another stub mapping",m["id"].as_str().unwrap())}]}),
                )
            } else {
                persist(&m, state);
                state.mappings.insert(0, m.clone());
                answer(201, m)
            }
        }
        ("POST", "/mappings/edit") => {
            if let Some(i) = state.mappings.iter().position(|m| m["id"] == v["id"]) {
                state.mappings[i] = normalize(v);
                Response::new(204, vec![])
            } else {
                Response::new(404, vec![])
            }
        }
        ("DELETE", "/mappings") => {
            state.mappings.clear();
            empty()
        }
        ("POST", "/mappings/reset") => {
            state.mappings = state.default_mappings.clone();
            Response::new(200, vec![])
        }
        ("POST", "/mappings/save") => {
            let dir = std::path::Path::new(&state.root_dir).join("mappings");
            let _ = std::fs::create_dir_all(&dir);
            for m in &state.mappings {
                let _ = std::fs::write(
                    dir.join(format!("{}.json", m["id"].as_str().unwrap_or("mapping"))),
                    crate::java_json(m),
                );
            }
            Response::new(200, vec![])
        }
        ("POST", "/mappings/find-by-metadata") => answer(
            200,
            json!({"mappings":state.mappings.iter().filter(|m|metadata_matches(&v,m)).cloned().collect::<Vec<_>>(),"meta":{"total":state.mappings.iter().filter(|m|metadata_matches(&v,m)).count()}}),
        ),
        ("POST", "/mappings/remove-by-metadata") => {
            state.mappings.retain(|m| !metadata_matches(&v, m));
            empty()
        }
        ("POST", "/requests/remove") => {
            state
                .requests
                .retain(|e| !crate::matcher::matches(&v, &logged_request(&e["request"])));
            empty()
        }
        ("POST", "/requests/remove-by-metadata") => {
            let removed: Vec<_> = state
                .requests
                .iter()
                .filter(|e| metadata_matches(&v, &e["stubMapping"]))
                .cloned()
                .collect();
            state
                .requests
                .retain(|e| !metadata_matches(&v, &e["stubMapping"]));
            answer(200, json!({"serveEvents":removed}))
        }
        ("POST", "/mappings/import") => {
            if let Some(ms) = v["mappings"].as_array() {
                for m in ms {
                    let m = normalize(m.clone());
                    state.mappings.retain(|old| old["id"] != m["id"]);
                    state.mappings.insert(0, m);
                }
            }
            Response::new(200, vec![])
        }
        ("GET", "/requests") => journal_list(req, state),
        ("DELETE", "/requests") | ("POST", "/requests/reset") => {
            state.requests.clear();
            empty()
        }
        ("POST", "/requests/count") => answer(
            200,
            json!({"count":state.requests.iter().filter(|e|crate::matcher::matches(&v,&logged_request(&e["request"]))).count(),"requestJournalDisabled":state.no_journal}),
        ),
        ("POST", "/requests/find") => answer(
            200,
            json!({"requests":state.requests.iter().filter(|e|crate::matcher::matches(&v,&logged_request(&e["request"]))).map(|e|e["request"].clone()).collect::<Vec<_>>(),"requestJournalDisabled":state.no_journal}),
        ),
        ("GET", "/requests/unmatched") => answer(
            200,
            json!({"requests":state.requests.iter().filter(|e|e["wasMatched"]==false).map(|e|e["request"].clone()).collect::<Vec<_>>(),"requestJournalDisabled":state.no_journal}),
        ),
        ("GET", "/files") => {
            let entries = std::fs::read_dir(std::path::Path::new(&state.root_dir).join("__files"))
                .map(|entries| {
                    entries
                        .flatten()
                        .filter(|e| e.path().is_file())
                        .map(|e| json!(e.file_name().to_string_lossy()))
                        .collect::<Vec<_>>()
                })
                .unwrap_or_default();
            answer(200, json!(entries))
        }
        ("GET", "/mappings/unmatched") => {
            let maps: Vec<_> = state
                .mappings
                .iter()
                .filter(|m| {
                    !state
                        .requests
                        .iter()
                        .any(|e| e["stubMapping"]["id"] == m["id"])
                })
                .cloned()
                .collect();
            answer(200, json!({"mappings":maps,"meta":{"total":maps.len()}}))
        }
        ("GET", "/requests/unmatched/near-misses") => answer(200, json!({"nearMisses":[]})),
        ("GET", "/scenarios") => answer(200, scenarios(state)),
        ("POST", "/scenarios/reset") => {
            state.scenarios.clear();
            empty()
        }
        _ if p.starts_with("/files/") => {
            if let Some(path) = safe_file(&state.root_dir, &p[7..]) {
                match method {
                    "GET" => std::fs::read(path)
                        .map(|b| Response::new(200, b))
                        .unwrap_or_else(|_| Response::new(404, vec![])),
                    "PUT" => {
                        if let Some(parent) = path.parent() {
                            let _ = std::fs::create_dir_all(parent);
                        }
                        match std::fs::write(path, &req.body) {
                            Ok(_) => answer(
                                200,
                                json!(base64::engine::general_purpose::STANDARD.encode(&req.body)),
                            ),
                            Err(_) => Response::new(500, vec![]),
                        }
                    }
                    "DELETE" => {
                        let _ = std::fs::remove_file(path);
                        Response::new(200, vec![])
                    }
                    _ => Response::new(405, vec![]),
                }
            } else {
                Response::new(403, vec![])
            }
        }
        _ if p.starts_with("/scenarios/") => {
            let tail = &p[11..];
            let name = tail.strip_suffix("/state").unwrap_or(tail);
            if method == "PUT" && tail.ends_with("/state") {
                if !state.mappings.iter().any(|m| m["scenarioName"] == name) {
                    return Some(problem(
                        404,
                        404,
                        &format!("Scenario {name} does not exist"),
                    ));
                }
                if !req.body.is_empty()
                    && !state.mappings.iter().any(|m| {
                        m["scenarioName"] == name
                            && (m["newScenarioState"] == v["state"]
                                || m["requiredScenarioState"] == v["state"])
                            && !v["state"].is_null()
                    })
                {
                    return Some(problem(
                        422,
                        11,
                        &format!(
                            "Scenario {name} does not support state {}",
                            v["state"].as_str().unwrap_or("null")
                        ),
                    ));
                }
                if !req.body.is_empty() && v["state"].is_null() {
                    return Some(problem(
                        422,
                        11,
                        &format!("Scenario {name} does not support state null"),
                    ));
                }
                state
                    .scenarios
                    .insert(name.into(), v["state"].as_str().unwrap_or("Started").into());
                empty()
            } else if method == "GET" {
                scenarios(state)["scenarios"]
                    .as_array()
                    .and_then(|a| a.iter().find(|s| s["name"] == name))
                    .map(|s| answer(200, s.clone()))
                    .unwrap_or_else(|| Response::new(404, vec![]))
            } else {
                Response::new(404, vec![])
            }
        }
        _ if p.starts_with("/requests/") => {
            let id = &p[10..];
            if method == "DELETE" {
                state.requests.retain(|e| e["id"] != id);
                empty()
            } else {
                state
                    .requests
                    .iter()
                    .find(|e| e["id"] == id)
                    .map(|e| answer(200, e.clone()))
                    .unwrap_or_else(|| Response::new(404, vec![]))
            }
        }
        _ if p.starts_with("/mappings/") => {
            let ident = &p[10..];
            let found = state
                .mappings
                .iter()
                .position(|m| m["id"].as_str() == Some(ident));
            match (method, found) {
                ("GET", Some(i)) => answer(200, state.mappings[i].clone()),
                ("DELETE", _) => {
                    state.mappings.retain(|m| m["id"].as_str() != Some(ident));
                    state
                        .default_mappings
                        .retain(|m| m["id"].as_str() != Some(ident));
                    let _ = std::fs::remove_file(
                        std::path::Path::new(&state.root_dir)
                            .join("mappings")
                            .join(format!("{ident}.json")),
                    );
                    empty()
                }
                ("PUT", Some(i)) => {
                    let mut m = normalize(v);
                    m["id"] = json!(ident);
                    state.mappings[i] = m.clone();
                    answer(200, m)
                }
                _ => Response::new(404, vec![]),
            }
        }
        _ => Response::new(404, vec![]),
    };
    Some(out)
}
/// A fully owned journal entry, constructed without accessing shared server state.
/// Prepare outside the state mutex; commit order determines journal order.
pub struct PreparedEvent {
    event_id: String,
    event: Value,
}

/// Owns values removed from the journal so their recursive destruction can happen
/// after the caller releases the state mutex. A normal bounded insertion evicts
/// at most one value and does not allocate an eviction collection.
pub struct JournalCommit {
    event_id: String,
    retired: Option<Value>,
    excess: Vec<Value>,
}
impl JournalCommit {
    /// Call after releasing the state guard, including when no event was inserted.
    pub fn into_event_id(self) -> String {
        let Self {
            event_id,
            retired,
            excess,
        } = self;
        drop(retired);
        drop(excess);
        event_id
    }
}

pub fn prepare_event(req: &Request, response: &Response, mapping: Option<&Value>) -> PreparedEvent {
    let now = chrono::Utc::now();
    let mut headers = serde_json::Map::new();
    for (k, v) in &req.headers {
        headers.insert(k.clone(), json!(v));
    }
    let absolute = format!(
        "{}://{}{}",
        if req.scheme.is_empty() {
            "http"
        } else {
            &req.scheme
        },
        req.header("host").unwrap_or("localhost"),
        req.url
    );
    let mut logged = json!({"url":req.url,"absoluteUrl":absolute,"method":req.method,"clientIp":req.client_ip,"headers":headers,"cookies":{},"browserProxyRequest":false,"loggedDate":now.timestamp_millis(),"bodyAsBase64":base64::engine::general_purpose::STANDARD.encode(&req.body),"body":req.body_text(),"protocol":"HTTP/1.1","scheme":"http","host":req.header("host").unwrap_or("localhost").split(':').next().unwrap_or("localhost"),"port":80,"loggedDateString":now.to_rfc3339_opts(chrono::SecondsFormat::Millis,true),"queryParams":{},"formParams":{}});
    let mut cookies = serde_json::Map::new();
    if let Some(raw) = req.header("cookie") {
        for pair in raw.split(';') {
            if let Some((k, v)) = pair.trim().split_once('=') {
                cookies.insert(k.into(), json!(v));
            }
        }
    }
    logged["cookies"] = json!(cookies);
    logged["queryParams"] = query_params(req.url.split_once('?').map(|(_, q)| q).unwrap_or(""));
    if req
        .header("Content-Type")
        .is_some_and(|v| v.starts_with("application/x-www-form-urlencoded"))
    {
        logged["formParams"] = query_params(&req.body_text());
    }
    let mut h = serde_json::Map::new();
    for (k, v) in &response.headers {
        h.insert(k.clone(), json!(v));
    }
    let mut event = json!({"id":id(),"request":logged,"responseDefinition":mapping.map(|m|m["response"].clone()).unwrap_or(json!({"status":404,"fromConfiguredStub":false})),"response":{"status":response.status,"headers":h,"bodyAsBase64":base64::engine::general_purpose::STANDARD.encode(&response.body),"body":String::from_utf8_lossy(&response.body),"fromProxy":false},"wasMatched":mapping.is_some(),"timing":{"addedDelay":0,"processTime":0,"responseSendTime":0,"serveTime":0,"totalTime":0},"subEvents":[],"stubMapping":mapping});
    if mapping.is_none() {
        event["response"] = json!({"status":404,"bodyAsBase64":"","body":"","fromProxy":false});
        event["stubMapping"] = json!({"id":id(),"request":{"method":"ANY"},"response":{"status":404,"fromConfiguredStub":false}});
        event["subEvents"] = json!([{"type":"REQUEST_NOT_MATCHED","timeOffsetNanos":0,"data":{"status":404,"contentType":"text/plain","report":String::from_utf8_lossy(&response.body)}}]);
    }
    let event_id = event["id"].as_str().unwrap().to_string();
    PreparedEvent { event_id, event }
}

/// Insert an already prepared entry under the state mutex. The returned owner must
/// be consumed outside that mutex so evicted bodies and mappings are freed there.
pub fn commit_event(prepared: PreparedEvent, state: &mut State) -> JournalCommit {
    let PreparedEvent { event_id, event } = prepared;
    if state.no_journal {
        return JournalCommit {
            event_id: String::new(),
            retired: Some(event),
            excess: Vec::new(),
        };
    }
    state.requests.push_front(event);
    let mut retired = None;
    let mut excess = Vec::new();
    if state.journal_limit > 0 && state.requests.len() > state.journal_limit {
        retired = state.requests.pop_back();
        // Normally the invariant requires just one pop. Also handle a limit that
        // was reduced after entries were recorded, without dropping under lock.
        while state.requests.len() > state.journal_limit {
            if let Some(entry) = state.requests.pop_back() {
                excess.push(entry);
            }
        }
    }
    JournalCommit {
        event_id,
        retired,
        excess,
    }
}

/// Convenience wrapper for callers that already exclusively own their State.
/// Shared-state request handling should prepare, commit, and dispose separately.
pub fn record(
    req: &Request,
    response: &Response,
    mapping: Option<&Value>,
    state: &mut State,
) -> String {
    if state.no_journal {
        return String::new();
    }
    commit_event(prepare_event(req, response, mapping), state).into_event_id()
}

pub(crate) fn logged_request(v: &Value) -> Request {
    let headers = v["headers"]
        .as_object()
        .map(|m| {
            m.iter()
                .flat_map(|(k, v)| {
                    if let Some(a) = v.as_array() {
                        a.iter()
                            .map(|v| (k.clone(), v.as_str().unwrap_or("").to_string()))
                            .collect()
                    } else {
                        vec![(k.clone(), v.as_str().unwrap_or("").to_string())]
                    }
                })
                .collect()
        })
        .unwrap_or_default();
    Request {
        method: v["method"].as_str().unwrap_or("GET").into(),
        url: v["url"].as_str().unwrap_or("/").into(),
        headers,
        body: v["body"].as_str().unwrap_or("").as_bytes().to_vec(),
        client_ip: v["clientIp"].as_str().unwrap_or("").into(),
        scheme: v["scheme"].as_str().unwrap_or("http").into(),
    }
}
fn metadata_matches(pattern: &Value, m: &Value) -> bool {
    crate::matcher::value_matches(pattern, &serde_json::to_string(&m["metadata"]).unwrap())
}
fn problem(status: u16, code: u16, title: &str) -> Response {
    answer(status, json!({"errors":[{"code":code,"title":title}]}))
}
fn scenarios(state: &State) -> Value {
    let mut groups: serde_json::Map<String, Value> = serde_json::Map::new();
    for m in &state.mappings {
        if let Some(name) = m["scenarioName"].as_str() {
            let group=groups.entry(name.to_string()).or_insert_with(||json!({"id":name,"name":name,"state":state.scenarios.get(name).map(String::as_str).unwrap_or("Started"),"possibleStates":["Started"],"mappings":[]}));
            for k in ["requiredScenarioState", "newScenarioState"] {
                if let Some(s) = m[k].as_str() {
                    let a = group["possibleStates"].as_array_mut().unwrap();
                    if !a.contains(&json!(s)) {
                        a.push(json!(s))
                    }
                }
            }
            group["mappings"].as_array_mut().unwrap().push(m.clone());
        }
    }
    json!({"scenarios":groups.into_values().collect::<Vec<_>>()})
}
fn safe_file(root: &str, name: &str) -> Option<std::path::PathBuf> {
    use std::path::{Component, Path};
    let p = Path::new(name);
    if p.components().any(|c| !matches!(c, Component::Normal(_))) {
        return None;
    }
    let base = Path::new(root).join("__files");
    let path = base.join(p);
    if let Ok(real) = path.canonicalize() {
        if !real.starts_with(base.canonicalize().ok()?) {
            return None;
        }
    }
    Some(path)
}
fn validation(v: &Value) -> Option<Response> {
    for (section, class, allowed) in [
        (
            "",
            "com.github.tomakehurst.wiremock.stubbing.StubMapping",
            "$schema id uuid name request newRequest response persistent priority scenarioName requiredScenarioState newScenarioState postServeActions serveEventListeners metadata",
        ),
        (
            "request",
            "com.github.tomakehurst.wiremock.matching.RequestPattern",
            "scheme host port clientIp url urlPattern urlPath urlPathPattern urlPathTemplate method headers pathParameters queryParameters formParameters cookies basicAuthCredentials bodyPatterns multipartPatterns customMatcher",
        ),
        (
            "response",
            "com.github.tomakehurst.wiremock.http.ResponseDefinition",
            "status statusMessage body jsonBody base64Body bodyFileName openWebsocketChannel openSseChannel headers additionalProxyRequestHeaders removeProxyRequestHeaders fixedDelayMilliseconds delayDistribution chunkedDribbleDelay proxyBaseUrl proxyUrlPrefixToRemove fault transformers transformerParameters fromConfiguredStub",
        ),
    ] {
        if let Some(m) = if section.is_empty() {
            v.as_object()
        } else {
            v[section].as_object()
        } {
            for k in m.keys() {
                if !allowed.split_whitespace().any(|a| a == k) {
                    let pointer = if section.is_empty() {
                        format!("/{k}")
                    } else {
                        format!("/{section}/{k}")
                    };
                    return Some(answer(
                        422,
                        json!({"errors":[{"code":10,"source":{"pointer":pointer},"title":"Error parsing JSON","detail":format!("Unrecognized field \"{k}\" (class {class}), not marked as ignorable")}]}),
                    ));
                }
            }
        }
    }
    if let Some(id) = v["id"].as_str() {
        if uuid::Uuid::parse_str(id).is_err() {
            return Some(answer(
                422,
                json!({"errors":[{"code":10,"source":{"pointer":"/id"},"title":"Error parsing JSON","detail":format!("Cannot deserialize value of type `java.util.UUID` from String \"{id}\": UUID has to be represented by standard 36-char representation")}]}),
            ));
        }
    }
    None
}
fn persist(m: &Value, state: &mut State) {
    if m["persistent"] != true {
        return;
    }
    let dir = std::path::Path::new(&state.root_dir).join("mappings");
    if std::fs::create_dir_all(&dir).is_ok() {
        let _ = std::fs::write(
            dir.join(format!("{}.json", m["id"].as_str().unwrap_or("mapping"))),
            crate::java_json(m),
        );
    }
    state.default_mappings.retain(|old| old["id"] != m["id"]);
    state.default_mappings.insert(0, m.clone());
}

pub(crate) fn query_params(raw: &str) -> Value {
    let mut map = serde_json::Map::new();
    for (k, v) in url::form_urlencoded::parse(raw.as_bytes()) {
        let item = map
            .entry(k.to_string())
            .or_insert_with(|| json!({"key":k,"values":[]}));
        item["values"].as_array_mut().unwrap().push(json!(v));
    }
    json!(map)
}
fn journal_list(req: &Request, s: &State) -> Response {
    let params: std::collections::HashMap<_, _> = url::form_urlencoded::parse(
        req.url
            .split_once('?')
            .map(|(_, q)| q)
            .unwrap_or("")
            .as_bytes(),
    )
    .into_owned()
    .collect();
    let since = if let Some(raw) = params.get("since") {
        match chrono::DateTime::parse_from_rfc3339(raw) {
            Ok(v) => Some(v.timestamp_millis()),
            Err(_) => {
                return answer(
                    400,
                    json!({"errors":[{"code":10,"source":{"pointer":"since"},"title":format!("{raw} is not a valid ISO8601 date")}]}),
                );
            }
        }
    } else {
        None
    };
    if let Some(raw) = params.get("matchingStub") {
        if uuid::Uuid::parse_str(raw).is_err() {
            return problem(
                400,
                15,
                &format!("Query parameter matchingStub value '{raw}' is not a valid UUID"),
            );
        }
    }
    let events: Vec<_> = s
        .requests
        .iter()
        .filter(|e| {
            params
                .get("unmatched")
                .map(|v| (e["wasMatched"] == false) == (v == "true"))
                .unwrap_or(true)
                && params
                    .get("matchingStub")
                    .map(|v| e["stubMapping"]["id"] == v.as_str())
                    .unwrap_or(true)
        })
        .collect();
    let total = events.len();
    let offset = params
        .get("offset")
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    let limit = params
        .get("limit")
        .and_then(|v| v.parse().ok())
        .unwrap_or(usize::MAX);
    let events: Vec<_> = events
        .into_iter()
        .filter(|e| {
            since
                .map(|since| e["request"]["loggedDate"].as_i64().unwrap_or(0) > since)
                .unwrap_or(true)
        })
        .skip(offset)
        .take(limit)
        .collect();
    answer(
        200,
        json!({"requests":events,"meta":{"total":total},"requestJournalDisabled":s.no_journal}),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    fn request(method: &str, path: &str, body: &str) -> Request {
        Request {
            method: method.into(),
            url: format!("/__admin{path}"),
            body: body.as_bytes().to_vec(),
            ..Default::default()
        }
    }
    #[test]
    fn persistent_mapping_reset_and_delete() {
        let root = std::env::temp_dir().join(format!("breakmock-rust-admin-{}", id()));
        let mut state = State {
            root_dir: root.to_string_lossy().into(),
            ..Default::default()
        };
        let mapping_id = "11111111-1111-4111-8111-111111111111";
        let r = handle(
            &request(
                "POST",
                "/mappings",
                &format!(
                    r#"{{"id":"{mapping_id}","persistent":true,"response":{{"body":"stored"}}}}"#
                ),
            ),
            &mut state,
        )
        .unwrap();
        assert_eq!(r.status, 201);
        let file = root.join("mappings").join(format!("{mapping_id}.json"));
        assert!(file.exists());
        state.mappings.clear();
        handle(&request("POST", "/mappings/reset", ""), &mut state);
        assert_eq!(state.mappings.len(), 1);
        handle(
            &request("DELETE", &format!("/mappings/{mapping_id}"), ""),
            &mut state,
        );
        handle(&request("POST", "/mappings/reset", ""), &mut state);
        assert!(state.mappings.is_empty());
        assert!(!file.exists());
        std::fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn malformed_mapping_never_becomes_catch_all() {
        let mut state = State::default();
        let response = handle(
            &request("POST", "/mappings", r#"{"response":{"jsonBody":"\uD800"}}"#),
            &mut state,
        )
        .unwrap();
        assert_eq!(response.status, 422);
        assert!(state.mappings.is_empty());
    }
    #[test]
    fn bounded_journal_keeps_newest_and_filters() {
        let mut state = State {
            journal_limit: 2,
            ..Default::default()
        };
        for url in ["/first", "/second", "/third"] {
            record(
                &Request {
                    method: "GET".into(),
                    url: url.into(),
                    headers: vec![("Host".into(), "localhost".into())],
                    ..Default::default()
                },
                &Response::new(200, b"body".to_vec()),
                Some(&json!({"response":{"status":200}})),
                &mut state,
            );
        }
        assert_eq!(state.requests.len(), 2);
        assert_eq!(state.requests[0]["request"]["url"], "/third");
        let response = handle(
            &request("POST", "/requests/count", r#"{"url":"/second"}"#),
            &mut state,
        )
        .unwrap();
        assert_eq!(
            serde_json::from_slice::<Value>(&response.body).unwrap()["count"],
            1
        );
    }
}

#[cfg(test)]
mod journal_commit_tests {
    use super::*;

    fn prepared(url: &str) -> PreparedEvent {
        prepare_event(
            &Request {
                method: "GET".into(),
                url: url.into(),
                ..Default::default()
            },
            &Response::new(200, b"response".to_vec()),
            Some(&json!({"response":{"status":200}})),
        )
    }

    #[test]
    fn commit_order_and_cap_keep_evictions_owned_by_caller() {
        let mut state = State {
            journal_limit: 2,
            ..Default::default()
        };
        let first = prepared("/first-prepared");
        let second = prepared("/second-prepared");
        let third = prepared("/third-prepared");
        // Preparation order need not match completion/commit order.
        commit_event(second, &mut state).into_event_id();
        commit_event(first, &mut state).into_event_id();
        let commit = commit_event(third, &mut state);
        assert_eq!(state.requests.len(), 2);
        assert_eq!(state.requests[0]["request"]["url"], "/third-prepared");
        assert_eq!(state.requests[1]["request"]["url"], "/first-prepared");
        assert_eq!(
            commit.retired.as_ref().unwrap()["request"]["url"],
            "/second-prepared"
        );
        assert_eq!(
            commit.excess.capacity(),
            0,
            "ordinary eviction allocates no overflow collection"
        );
        assert_eq!(
            commit.into_event_id(),
            state.requests[0]["id"].as_str().unwrap()
        );
    }

    #[test]
    fn reduced_cap_and_disabled_journal_retire_without_mutating_remaining_entries() {
        let mut state = State::default();
        for path in ["/one", "/two", "/three", "/four"] {
            commit_event(prepared(path), &mut state).into_event_id();
        }
        state.journal_limit = 1;
        let commit = commit_event(prepared("/five"), &mut state);
        assert_eq!(state.requests.len(), 1);
        assert_eq!(commit.retired.as_ref().unwrap()["request"]["url"], "/one");
        assert_eq!(commit.excess.len(), 3);
        commit.into_event_id();
        state.no_journal = true;
        let commit = commit_event(prepared("/discarded"), &mut state);
        assert!(commit.event_id.is_empty());
        assert_eq!(
            commit.retired.as_ref().unwrap()["request"]["url"],
            "/discarded"
        );
        assert_eq!(state.requests[0]["request"]["url"], "/five");
        assert!(commit.into_event_id().is_empty());
    }

    #[test]
    fn prepared_event_preserves_owned_body_bytes_headers_and_mapping_snapshot() {
        let mut req = Request {
            method: "POST".into(),
            url: "/journal?q=one&q=two".into(),
            headers: vec![
                ("Z-First".into(), "z".into()),
                ("A-Second".into(), "a".into()),
                ("Cookie".into(), "session=abc".into()),
                (
                    "Content-Type".into(),
                    "application/x-www-form-urlencoded".into(),
                ),
            ],
            body: b"field=a&field=b".to_vec(),
            ..Default::default()
        };
        let mut response = Response::new(201, vec![0, 255, 128, 65]);
        response.headers = vec![
            ("Z-Response".into(), "z".into()),
            ("A-Response".into(), "a".into()),
        ];
        let mut mapping = json!({"id":"11111111-1111-4111-8111-111111111111","request":{"urlPath":"/journal"},"response":{"status":201},"metadata":{"z":1,"a":null}});
        let prepared = prepare_event(&req, &response, Some(&mapping));
        let wire = crate::java_json(&prepared.event);
        req.body.fill(b'x');
        response.body.fill(42);
        mapping["metadata"]["z"] = json!(99);
        let mut state = State::default();
        commit_event(prepared, &mut state).into_event_id();
        let event = &state.requests[0];
        assert_eq!(crate::java_json(event).as_bytes(), wire.as_bytes());
        assert_eq!(event["request"]["body"], "field=a&field=b");
        assert_eq!(event["response"]["bodyAsBase64"], "AP+AQQ==");
        assert_eq!(event["request"]["cookies"]["session"], "abc");
        assert_eq!(
            event["request"]["queryParams"]["q"]["values"],
            json!(["one", "two"])
        );
        assert_eq!(
            event["request"]["formParams"]["field"]["values"],
            json!(["a", "b"])
        );
        assert_eq!(event["stubMapping"]["metadata"]["z"], 1);
        assert!(wire.find("\"Z-First\"").unwrap() < wire.find("\"A-Second\"").unwrap());
        assert!(wire.find("\"Z-Response\"").unwrap() < wire.find("\"A-Response\"").unwrap());
    }
}
