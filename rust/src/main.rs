// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
use base64::{Engine, engine::general_purpose::STANDARD};
use breakmock_rust::{Request, Response, State, admin, matcher, template};
use serde_json::{Value, json};
use std::{
    io::{Read, Write},
    path::Path,
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
};
mod streaming;
mod transport;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    let opt = |key: &str, default: &str| {
        args.windows(2)
            .find(|v| v[0] == key)
            .map(|v| v[1].clone())
            .unwrap_or(default.into())
    };
    let stream_options = streaming::Options {
        idle: opt("--websocket-idle-timeout", "300000")
            .parse()
            .unwrap_or(300000),
        text: opt("--websocket-max-text-message-size", "10485760")
            .parse()
            .unwrap_or(10485760),
        binary: opt("--websocket-max-binary-message-size", "10485760")
            .parse()
            .unwrap_or(10485760),
    };
    let port = opt("--port", "8080");
    let bind = opt("--bind-address", "0.0.0.0");
    let mut state = State {
        root_dir: opt("--root-dir", "."),
        no_journal: args.contains(&"--no-request-journal".into()),
        journal_limit: opt("--max-request-journal-entries", "0")
            .parse()
            .unwrap_or(0),
        global_templating: args.contains(&"--global-response-templating".into()),
        settings: json!({}),
        ..Default::default()
    };
    load_mappings(&mut state);
    breakmock_rust::admin_messaging::load_message_mappings(&mut state)?;
    let listener = TcpListener::bind(format!("{bind}:{port}")).await?;
    let shared = Arc::new(Mutex::new(state));
    eprintln!("BreakMock Rust 0.1.0 listening on {bind}:{port}");
    loop {
        let (stream, peer) = listener.accept().await?;
        stream.set_nodelay(true)?;
        let state = shared.clone();
        tokio::spawn(async move {
            let _ = serve(stream, state, peer.ip().to_string(), stream_options).await;
        });
    }
}
fn load_mappings(state: &mut State) {
    fn visit(path: &Path, out: &mut Vec<Vec<u8>>) {
        if let Ok(entries) = std::fs::read_dir(path) {
            for entry in entries.flatten() {
                let p = entry.path();
                if p.is_dir() {
                    visit(&p, out)
                } else if p.extension().and_then(|s| s.to_str()) == Some("json") {
                    if let Ok(data) = std::fs::read(&p) {
                        if let Ok(text) = std::str::from_utf8(&data) {
                            if let Ok(v) = breakmock_rust::utf16_json::parse(text) {
                                if let Some(breakmock_rust::utf16_json::Node::Array(m)) =
                                    v.get("mappings")
                                {
                                    out.extend(m.iter().map(|n| n.compact().into_bytes()))
                                } else {
                                    out.push(data)
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    let mut values = Vec::new();
    visit(&Path::new(&state.root_dir).join("mappings"), &mut values);
    for value in values {
        let req = Request {
            method: "POST".into(),
            url: "/__admin/mappings".into(),
            body: value,
            ..Default::default()
        };
        breakmock_rust::raw_json::handle(&req, state);
    }
    state.default_mappings = state.mappings.clone();
}
async fn serve(
    mut stream: TcpStream,
    state: Arc<Mutex<State>>,
    client_ip: String,
    stream_options: streaming::Options,
) -> std::io::Result<()> {
    let mut pending = Vec::with_capacity(8192);
    loop {
        let mut continued = false;
        let (mut req, version, used) = loop {
            if let Some(parsed) = transport::parse_request(&pending, &client_ip)? {
                break parsed;
            }
            if pending.len() > 64 * 1024 * 1024 {
                return Ok(());
            }
            let mut chunk = [0u8; 16384];
            let n = match tokio::time::timeout(Duration::from_secs(300), stream.read(&mut chunk))
                .await
            {
                Ok(v) => v?,
                Err(_) => return Ok(()),
            };
            if n == 0 {
                return Ok(());
            }
            pending.extend_from_slice(&chunk[..n]);
            if !continued
                && pending.windows(4).any(|v| v == b"\r\n\r\n")
                && String::from_utf8_lossy(&pending)
                    .to_ascii_lowercase()
                    .contains("expect: 100-continue")
            {
                stream.write_all(b"HTTP/1.1 100 Continue\r\n\r\n").await?;
                continued = true;
            }
        };
        pending.drain(..used);
        if req
            .header("Content-Encoding")
            .is_some_and(|s| s.eq_ignore_ascii_case("gzip"))
        {
            let mut b = Vec::new();
            if flate2::read::GzDecoder::new(req.body.as_slice())
                .take(64 * 1024 * 1024 + 1)
                .read_to_end(&mut b)
                .is_ok()
            {
                if b.len() > 64 * 1024 * 1024 {
                    return Ok(());
                }
                req.body = b;
            }
        }
        let close = version == 0
            || req
                .header("Connection")
                .is_some_and(|v| v.eq_ignore_ascii_case("close"));
        let response = handle(&req, &state).await;
        if let Some(kind) = &response.stream {
            return streaming::serve(stream, state, req, kind, stream_options).await;
        }
        if let Some(fault) = &response.fault {
            if fault == "RANDOM_DATA_THEN_CLOSE" {
                stream
                    .write_all(b"lskdu018973t09sylgasjkfg1][]'./.sdlv")
                    .await?;
            }
            if fault == "MALFORMED_RESPONSE_CHUNK" {
                let extra = if version == 0 {
                    ""
                } else if close {
                    "Connection: close\r\n"
                } else {
                    "Transfer-Encoding: chunked\r\n"
                };
                stream
                    .write_all(
                        format!(
                            "HTTP/1.1 200 OK\r\n{extra}\r\nlskdu018973t09sylgasjkfg1][]'./.sdlv"
                        )
                        .as_bytes(),
                    )
                    .await?;
            }
            if fault == "CONNECTION_RESET_BY_PEER" {
                let socket = stream.into_std()?;
                socket2::SockRef::from(&socket).set_linger(Some(Duration::ZERO))?;
            }
            return Ok(());
        }
        let bytes = transport::serialize_response(&req, &response, close);
        stream.write_all(&bytes).await?;
        if req.path() == "/__admin/shutdown" {
            std::process::exit(0)
        }
        if close
            || response.headers.iter().any(|(k, v)| {
                k.eq_ignore_ascii_case("connection") && v.eq_ignore_ascii_case("close")
            })
        {
            return Ok(());
        }
    }
}
async fn handle(req: &Request, state: &Arc<Mutex<State>>) -> Response {
    if let Some(r) = breakmock_rust::admin_messaging::wait_if_needed(req, state).await {
        return r;
    }
    if req.path().starts_with("/__admin") {
        let mut r = breakmock_rust::raw_json::handle(req, &mut state.lock().unwrap())
            .unwrap_or(Response::new(404, Vec::new()));
        r.headers
            .push(("X-BreakMock-Version".into(), "0.1.0-rust".into()));
        r.headers
            .push(("X-BreakMock-Language".into(), "rust".into()));
        return r;
    }
    let (mapping, root, global, delay, raw_body, journal_enabled) = {
        let mut s = state.lock().unwrap();
        let mut candidates: Vec<_> = s
            .mappings
            .iter()
            .enumerate()
            .filter(|(_, m)| {
                if let Some(name) = m["scenarioName"].as_str() {
                    let current = s
                        .scenarios
                        .get(name)
                        .map(String::as_str)
                        .unwrap_or("Started");
                    if let Some(required) = m["requiredScenarioState"].as_str() {
                        if current != required {
                            return false;
                        }
                    }
                }
                matcher::matches(&m["request"], req)
            })
            .collect();
        candidates.sort_by_key(|(i, m)| (m["priority"].as_i64().unwrap_or(5), *i));
        let selected = candidates.first().map(|(_, v)| (*v).clone());
        if let Some(m) = &selected {
            if let (Some(n), Some(next)) =
                (m["scenarioName"].as_str(), m["newScenarioState"].as_str())
            {
                s.scenarios.insert(n.into(), next.into());
            }
        }
        {
            let raw = selected
                .as_ref()
                .and_then(|m| m["id"].as_str())
                .and_then(|id| s.raw_json_bodies.get(id))
                .map(|v| v.compact());
            (
                selected,
                s.root_dir.clone(),
                s.global_templating,
                s.settings["fixedDelay"].as_u64().unwrap_or(0),
                raw,
                !s.no_journal,
            )
        }
    };
    {
        let mut s = state.lock().unwrap();
        if let Err(e) =
            breakmock_rust::messaging_runtime::http_trigger(&mut s, req, mapping.as_ref())
        {
            return jetty_error(req, &e);
        }
    }
    let mut response = if let Some(m) = &mapping {
        make_response(req, m, &root, global, raw_body.as_deref()).await
    } else {
        {
            let s = state.lock().unwrap();
            let mut ms = s.mappings.clone();
            for m in &mut ms {
                if let Some(name) = m["scenarioName"].as_str() {
                    let status = s
                        .scenarios
                        .get(name)
                        .map(String::as_str)
                        .unwrap_or("Started")
                        .to_string();
                    m["_scenarioState"] = json!(status)
                }
            }
            let (body, ctype) = breakmock_rust::diagnostics::unmatched_response(req, &ms);
            let mut r = Response::new(404, body);
            r.headers.push(("Content-Type".into(), ctype));
            r
        }
    };
    let delay = mapping
        .as_ref()
        .and_then(|m| m["response"]["fixedDelayMilliseconds"].as_u64())
        .unwrap_or(delay);
    let dribble = mapping
        .as_ref()
        .and_then(|m| m["response"]["chunkedDribbleDelay"]["totalDuration"].as_u64())
        .unwrap_or(0);
    if delay + dribble > 0 {
        tokio::time::sleep(Duration::from_millis(delay + dribble)).await;
    }
    if transport::gzip_allowed(req, &response) {
        let mut encoder = flate2::GzBuilder::new()
            .operating_system(255)
            .write(Vec::new(), flate2::Compression::default());
        if encoder.write_all(&response.body).is_ok() {
            if let Ok(bytes) = encoder.finish() {
                response.body = bytes;
                response
                    .headers
                    .retain(|(k, _)| !k.eq_ignore_ascii_case("Content-Length"));
                response
                    .headers
                    .push(("Content-Encoding".into(), "gzip".into()));
            }
        }
    }
    let event_id = if journal_enabled {
        // JSON construction and recursive eviction drops must not serialize all
        // serving tasks behind the shared state mutex. Commit remains synchronous
        // so admin reads and webhooks see the event before this request completes.
        let prepared = admin::prepare_event(req, &response, mapping.as_ref());
        let committed = {
            let mut s = state.lock().unwrap();
            admin::commit_event(prepared, &mut s)
        };
        committed.into_event_id()
    } else {
        String::new()
    };
    if let Some(m) = mapping {
        if m.get("postServeActions").is_some() || m.get("serveEventListeners").is_some() {
            tokio::spawn(breakmock_rust::admin_webhook::dispatch(
                req.clone(),
                m,
                event_id,
                state.clone(),
            ));
        }
    }
    response
}
async fn make_response(
    req: &Request,
    m: &Value,
    root: &str,
    global: bool,
    raw_body: Option<&str>,
) -> Response {
    let r = &m["response"];
    let status = r["status"].as_u64().unwrap_or(200) as u16;
    let mut out = Response::new(status, Vec::new());
    let templated = global
        || r["transformers"]
            .as_array()
            .is_some_and(|v| v.iter().any(|x| x == "response-template"));
    let params = &r["transformerParameters"];
    let render = |source: &str| -> Result<String, String> {
        if templated && source.contains("{{") {
            template::render(source, req, params)
        } else {
            Ok(source.into())
        }
    };
    if let Some(headers) = r["headers"].as_object() {
        for (k, v) in headers {
            for value in v.as_array().cloned().unwrap_or(vec![v.clone()]) {
                let raw = value.as_str().unwrap_or("");
                match render(raw) {
                    Ok(v) => out.headers.push((k.clone(), v)),
                    Err(e) => return template_error(e),
                }
            }
        }
    }
    if r["openWebsocketChannel"] == true || r["openSseChannel"] == true {
        out.stream = Some(
            if r["openWebsocketChannel"] == true {
                "websocket"
            } else {
                "sse"
            }
            .into(),
        );
        if out.stream.as_deref() == Some("websocket")
            && req
                .header("Upgrade")
                .is_some_and(|s| s.eq_ignore_ascii_case("websocket"))
        {
            out.status = 101;
        }
        return out;
    }
    out.fault = r["fault"].as_str().map(String::from);
    if let Some(proxy) = r["proxyBaseUrl"].as_str() {
        match render(proxy) {
            Ok(base) => {
                static CLIENT: std::sync::OnceLock<reqwest::Client> = std::sync::OnceLock::new();
                let client = CLIENT.get_or_init(reqwest::Client::new);
                let path = if let Some(prefix) = r["proxyUrlPrefixToRemove"].as_str() {
                    req.url.strip_prefix(prefix).unwrap_or(&req.url)
                } else {
                    &req.url
                };
                if base.starts_with("http://") {
                    let url = format!("{}{path}", base.trim_end_matches('/'));
                    match raw_proxy(req, r, &url, &render).await {
                        Ok(mut response) => {
                            response.headers.extend(out.headers);
                            return response;
                        }
                        Err(e) => return Response::new(502, e.to_string()),
                    }
                }
                let mut upstream = client
                    .request(
                        reqwest::Method::from_bytes(req.method.as_bytes())
                            .unwrap_or(reqwest::Method::GET),
                        format!("{}{path}", base.trim_end_matches('/')),
                    )
                    .body(req.body.clone());
                let removed = r["removeProxyRequestHeaders"]
                    .as_array()
                    .cloned()
                    .unwrap_or_default();
                let added = r["additionalProxyRequestHeaders"].as_object();
                for (k, v) in &req.headers {
                    if !["host", "content-length", "connection"]
                        .contains(&k.to_lowercase().as_str())
                        && !removed
                            .iter()
                            .any(|n| n.as_str().is_some_and(|n| n.eq_ignore_ascii_case(k)))
                        && !added.is_some_and(|a| a.keys().any(|n| n.eq_ignore_ascii_case(k)))
                    {
                        upstream = upstream.header(k, v)
                    }
                }
                if let Some(added) = added {
                    for (k, v) in added {
                        if k.eq_ignore_ascii_case("host") {
                            continue;
                        }
                        let value = if let Some(v) = v.as_array() {
                            v.first().unwrap_or(&Value::Null)
                        } else {
                            v
                        };
                        let raw = value.as_str().unwrap_or("");
                        match render(raw) {
                            Ok(value) => upstream = upstream.header(k, value),
                            Err(e) => return template_error(e),
                        }
                    }
                }
                match upstream.send().await {
                    Ok(res) => {
                        out.status = res.status().as_u16();
                        let configured = std::mem::take(&mut out.headers);
                        for (k, v) in res.headers() {
                            if !["transfer-encoding", "connection"].contains(&k.as_str()) {
                                out.headers
                                    .push((k.to_string(), v.to_str().unwrap_or("").into()));
                            }
                        }
                        out.headers.extend(configured);
                        out.body = res.bytes().await.unwrap_or_default().to_vec();
                        return out;
                    }
                    Err(e) => return Response::new(502, e.to_string()),
                }
            }
            Err(e) => return template_error(e),
        }
    }
    let body = if let Some(raw) = raw_body {
        Ok(raw.as_bytes().to_vec())
    } else if !r["jsonBody"].is_null() {
        Ok(compact_json(&r["jsonBody"]).into_bytes())
    } else if let Some(v) = r["base64Body"].as_str() {
        STANDARD.decode(v).map_err(|e| e.to_string())
    } else if let Some(file) = r["bodyFileName"].as_str() {
        match render(file) {
            Ok(name) => entity(&json!({"filePath":name}), root),
            Err(e) => return template_error(e),
        }
    } else {
        entity(&r["body"], root)
    };
    match body {
        Ok(mut bytes) => {
            let disable = params["disableBodyFileTemplating"] == true
                && (r.get("bodyFileName").is_some() || r["body"].get("filePath").is_some());
            if templated && !disable {
                let source = decode_text(&bytes, r["body"]["charset"].as_str().unwrap_or("UTF-8"));
                if source.contains("{{") {
                    match render(&source) {
                        Ok(text) => bytes = text.into_bytes(),
                        Err(e) => return template_error(e),
                    }
                }
            }
            out.body = bytes
        }
        Err(e) => return jetty_error(req, &e),
    }
    if let Some(id) = m["id"].as_str() {
        out.headers.push(("Matched-Stub-Id".into(), id.into()));
    }
    if let Some(name) = m["name"].as_str() {
        out.headers.push(("Matched-Stub-Name".into(), name.into()));
    }
    out
}
fn template_error(error: String) -> Response {
    let mut r = Response::new(500, format!("[ERROR] {error}"));
    r.headers = vec![
        ("Content-Type".into(), "text/plain".into()),
        ("Connection".into(), "close".into()),
    ];
    r
}
fn entity(v: &Value, root: &str) -> Result<Vec<u8>, String> {
    if let Some(s) = v.as_str() {
        return Ok(s.as_bytes().to_vec());
    }
    if let Some(s) = v["data"].as_str() {
        return Ok(encode_text(s, v["charset"].as_str().unwrap_or("UTF-8")));
    }
    if v["data"].is_object() || v["data"].is_array() {
        return Ok(compact_json(&v["data"]).into_bytes());
    }
    if let Some(s) = v["base64Data"].as_str() {
        return STANDARD.decode(s).map_err(|e| e.to_string());
    }
    let name = v["filePath"].as_str().or_else(|| {
        if v["dataStore"] == "__files" {
            v["dataRef"].as_str()
        } else {
            None
        }
    });
    if let Some(name) = name {
        let path = Path::new(root).join("__files").join(name);
        if !Path::new(name).components().any(|p| {
            matches!(
                p,
                std::path::Component::ParentDir | std::path::Component::RootDir
            )
        }) {
            if let Ok(b) = std::fs::read(path) {
                return Ok(b);
            }
        }
        if v.get("dataRef").is_none() {
            return Err(format!(
                "com.github.tomakehurst.wiremock.admin.NotFoundException: Not found in blob store: {name}"
            ));
        }
    }
    if v.get("dataRef").is_some() {
        return Err("java.lang.NullPointerException: Cannot invoke \"com.github.tomakehurst.wiremock.common.InputStreamSource.getStream()\" because the return value of \"com.github.tomakehurst.wiremock.common.entity.Entity.getStreamSource()\" is null".into());
    }
    Ok(Vec::new())
}
fn jetty_error(req: &Request, message: &str) -> Response {
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
    let body = format!(
        "<html>\n<head>\n<meta http-equiv=\"Content-Type\" content=\"text/html;charset=ISO-8859-1\"/>\n<title>Error 500 {msg}</title>\n</head>\n<body>\n<h2>HTTP ERROR 500 {msg}</h2>\n<table>\n<tr><th>URI:</th><td>{uri}</td></tr>\n<tr><th>STATUS:</th><td>500</td></tr>\n<tr><th>MESSAGE:</th><td>{msg}</td></tr>\n</table>\n\n</body>\n</html>\n"
    );
    let mut r = Response::new(500, body);
    r.headers = vec![
        ("Content-Type".into(), "text/html;charset=iso-8859-1".into()),
        (
            "Cache-Control".into(),
            "must-revalidate,no-cache,no-store".into(),
        ),
    ];
    r
}

fn encode_text(text: &str, charset: &str) -> Vec<u8> {
    let upper = charset.to_uppercase();
    match upper.as_str() {
        "US-ASCII" | "ASCII" => text
            .chars()
            .map(|c| if c.is_ascii() { c as u8 } else { b'?' })
            .collect(),
        "ISO-8859-1" | "ISO8859_1" | "LATIN1" => text
            .chars()
            .map(|c| if c as u32 <= 255 { c as u8 } else { b'?' })
            .collect(),
        "UTF-16" | "UTF16" | "UTF-16BE" | "UTF-16LE" => {
            let mut b = Vec::new();
            if upper == "UTF-16" || upper == "UTF16" {
                b.extend([254, 255])
            }
            for c in text.encode_utf16() {
                b.extend(if upper == "UTF-16LE" {
                    c.to_le_bytes()
                } else {
                    c.to_be_bytes()
                });
            }
            b
        }
        _ => {
            if let Some(enc) = encoding_rs::Encoding::for_label(charset.as_bytes()) {
                let (b, _, errors) = enc.encode(text);
                if !errors {
                    return b.to_vec();
                }
                let cleaned: String = text
                    .chars()
                    .map(|c| {
                        let t = c.to_string();
                        if enc.encode(&t).2 { '?' } else { c }
                    })
                    .collect();
                enc.encode(&cleaned).0.to_vec()
            } else {
                text.as_bytes().to_vec()
            }
        }
    }
}
fn decode_text(bytes: &[u8], charset: &str) -> String {
    if ["ISO-8859-1", "ISO8859_1", "LATIN1"].contains(&charset.to_uppercase().as_str()) {
        return bytes.iter().map(|b| *b as char).collect();
    }
    encoding_rs::Encoding::for_label(charset.as_bytes())
        .unwrap_or(encoding_rs::UTF_8)
        .decode(bytes)
        .0
        .into_owned()
}

fn compact_json(value: &Value) -> String {
    match value {
        Value::Null => "null".into(),
        Value::Bool(v) => v.to_string(),
        Value::Number(v) => {
            use std::str::FromStr;
            bigdecimal::BigDecimal::from_str(&v.to_string())
                .map(|n| {
                    let s = n.to_plain_string();
                    if s.chars().all(|c| c == '0') {
                        "0".into()
                    } else {
                        s
                    }
                })
                .unwrap_or(v.to_string())
        }
        Value::String(v) => {
            let raw = serde_json::to_string(v).unwrap();
            let mut out = String::new();
            for c in raw.chars() {
                if c as u32 > 0xffff {
                    let n = c as u32 - 0x10000;
                    out.push_str(&format!(
                        "\\u{:04X}\\u{:04X}",
                        0xd800 + (n >> 10),
                        0xdc00 + (n & 1023)
                    ))
                } else {
                    out.push(c)
                }
            }
            out
        }
        Value::Array(values) => format!(
            "[{}]",
            values
                .iter()
                .map(compact_json)
                .collect::<Vec<_>>()
                .join(",")
        ),
        Value::Object(values) => format!(
            "{{{}}}",
            values
                .iter()
                .map(|(k, v)| format!(
                    "{}:{}",
                    compact_json(&Value::String(k.clone())),
                    compact_json(v)
                ))
                .collect::<Vec<_>>()
                .join(",")
        ),
    }
}

async fn raw_proxy(
    req: &Request,
    response: &Value,
    target: &str,
    render: &impl Fn(&str) -> Result<String, String>,
) -> std::io::Result<Response> {
    let url = url::Url::parse(target).map_err(std::io::Error::other)?;
    let host = url.host_str().unwrap_or("localhost");
    let port = url.port_or_known_default().unwrap_or(80);
    let mut stream = TcpStream::connect((host, port)).await?;
    let path = format!(
        "{}{}",
        url.path(),
        url.query().map(|q| format!("?{q}")).unwrap_or_default()
    );
    let mut bytes = format!(
        "{} {path} HTTP/1.1\r\nHost: {host}:{port}\r\nConnection: close\r\n",
        req.method
    )
    .into_bytes();
    let removed = response["removeProxyRequestHeaders"]
        .as_array()
        .cloned()
        .unwrap_or_default();
    let added = response["additionalProxyRequestHeaders"].as_object();
    for (k, v) in &req.headers {
        if ["host", "content-length", "connection", "transfer-encoding"]
            .contains(&k.to_lowercase().as_str())
            || removed
                .iter()
                .any(|n| n.as_str().is_some_and(|n| n.eq_ignore_ascii_case(k)))
            || added.is_some_and(|a| a.keys().any(|n| n.eq_ignore_ascii_case(k)))
        {
            continue;
        }
        bytes.extend_from_slice(format!("{k}: {v}\r\n").as_bytes());
    }
    if let Some(added) = added {
        for (k, v) in added {
            if k.eq_ignore_ascii_case("host") {
                continue;
            }
            let value = v
                .as_array()
                .and_then(|v| v.first())
                .unwrap_or(v)
                .as_str()
                .unwrap_or("");
            let text = render(value).map_err(std::io::Error::other)?;
            bytes.extend_from_slice(format!("{k}: {text}\r\n").as_bytes());
        }
    }
    bytes.extend_from_slice(format!("Content-Length: {}\r\n\r\n", req.body.len()).as_bytes());
    bytes.extend_from_slice(&req.body);
    stream.write_all(&bytes).await?;
    let mut raw = Vec::new();
    tokio::time::timeout(Duration::from_secs(30), stream.read_to_end(&mut raw))
        .await
        .map_err(std::io::Error::other)??;
    let mut headers = [httparse::EMPTY_HEADER; 128];
    let mut parsed = httparse::Response::new(&mut headers);
    let offset = match parsed.parse(&raw).map_err(std::io::Error::other)? {
        httparse::Status::Complete(n) => n,
        _ => return Err(std::io::Error::other("Incomplete proxy response")),
    };
    let chunked = parsed.headers.iter().any(|h| {
        h.name.eq_ignore_ascii_case("transfer-encoding") && h.value.eq_ignore_ascii_case(b"chunked")
    });
    let mut result = Response::new(parsed.code.unwrap_or(502), raw[offset..].to_vec());
    result.headers = parsed
        .headers
        .iter()
        .filter(|h| !["connection", "transfer-encoding"].contains(&h.name.to_lowercase().as_str()))
        .map(|h| (h.name.into(), String::from_utf8_lossy(h.value).into_owned()))
        .collect();
    if chunked {
        let mut pos = offset;
        let mut body = Vec::new();
        while pos < raw.len() {
            let Some(end) = raw[pos..].windows(2).position(|s| s == b"\r\n") else {
                break;
            };
            let n = usize::from_str_radix(
                std::str::from_utf8(&raw[pos..pos + end])
                    .unwrap_or("")
                    .split(';')
                    .next()
                    .unwrap_or(""),
                16,
            )
            .map_err(std::io::Error::other)?;
            pos += end + 2;
            if n == 0 {
                break;
            }
            if pos + n > raw.len() {
                return Err(std::io::Error::other("Incomplete proxy chunk"));
            }
            body.extend_from_slice(&raw[pos..pos + n]);
            pos += n + 2;
        }
        result.body = body;
    }
    Ok(result)
}
