use crate::{Request, Response, State};
use base64::Engine;
use serde_json::{Value, json};
use std::sync::{Arc, Mutex};
pub async fn dispatch(req: Request, mapping: Value, event_id: String, state: Arc<Mutex<State>>) {
    let mut hooks = vec![];
    if let Some(v) = mapping["postServeActions"]["webhook"].as_object() {
        hooks.push(Value::Object(v.clone()))
    }
    if let Some(listeners) = mapping["serveEventListeners"].as_array() {
        for l in listeners {
            if l["name"] == "webhook" {
                hooks.push(l["parameters"].clone());
            }
        }
    }
    for spec in hooks {
        let render = |s: &str| {
            crate::template::render(&s.replace("originalRequest.", "request."), &req, &spec)
                .unwrap_or_default()
        };
        let method = spec["method"].as_str().map(render);
        let Some(method) = method else {
            append(
                &state,
                &event_id,
                json!({"type":"ERROR","data":{"message":"Exception thrown while configuring webhook: Cannot invoke \"String.indexOf(String)\" because \"content\" is null"}}),
            );
            continue;
        };
        let url = render(spec["url"].as_str().unwrap_or(""));
        let Ok(parsed) = url::Url::parse(&url) else {
            continue;
        };
        if let Some(delay) = spec["delay"]["milliseconds"]
            .as_u64()
            .or_else(|| spec["fixedDelayMilliseconds"].as_u64())
        {
            tokio::time::sleep(std::time::Duration::from_millis(delay)).await
        }
        let mut request = Request {
            method: method.clone(),
            url: format!(
                "{}{}",
                parsed.path(),
                parsed.query().map(|q| format!("?{q}")).unwrap_or_default()
            ),
            scheme: parsed.scheme().into(),
            ..Default::default()
        };
        if let Some(h) = spec["headers"].as_object() {
            for (k, v) in h {
                if let Some(a) = v.as_array() {
                    for v in a {
                        request
                            .headers
                            .push((k.clone(), render(v.as_str().unwrap_or(""))))
                    }
                } else {
                    request
                        .headers
                        .push((k.clone(), render(v.as_str().unwrap_or(""))))
                }
            }
        }
        if ["POST", "PUT", "PATCH"].contains(&method.as_str()) {
            request.body = if let Some(body) = spec["body"].as_str() {
                render(body).into_bytes()
            } else {
                base64::engine::general_purpose::STANDARD
                    .decode(spec["base64Body"].as_str().unwrap_or(""))
                    .unwrap_or_default()
            };
        }
        let mut temp = State::default();
        crate::admin::record(
            &request,
            &Response::new(200, vec![]),
            Some(&json!({"response":{"status":200}})),
            &mut temp,
        );
        let mut logged = temp.requests[0]["request"].clone();
        logged["absoluteUrl"] = json!(url);
        logged["host"] = json!(parsed.host_str().unwrap_or(""));
        logged["port"] = json!(parsed.port_or_known_default().unwrap_or(80));
        logged.as_object_mut().unwrap().remove("clientIp");
        logged.as_object_mut().unwrap().remove("protocol");
        append(
            &state,
            &event_id,
            json!({"type":"WEBHOOK_REQUEST","timeOffsetNanos":0,"data":logged}),
        );
        if parsed.scheme() == "http" {
            match raw_http(&request, &parsed).await {
                Ok(response) => {
                    let mut headers = serde_json::Map::new();
                    for (k, v) in response.headers {
                        match headers.get_mut(&k) {
                            None => {
                                headers.insert(k, json!(v));
                            }
                            Some(old) => {
                                if let Some(a) = old.as_array_mut() {
                                    a.push(json!(v))
                                } else {
                                    *old = json!([old.clone(), v])
                                }
                            }
                        }
                    }
                    append(
                        &state,
                        &event_id,
                        json!({"type":"WEBHOOK_RESPONSE","timeOffsetNanos":0,"data":{"status":response.status,"headers":headers,"bodyAsBase64":base64::engine::general_purpose::STANDARD.encode(&response.body),"body":String::from_utf8_lossy(&response.body),"fromProxy":false}}),
                    );
                }
                Err(error) => append(
                    &state,
                    &event_id,
                    json!({"type":"ERROR","data":{"message":error.to_string()}}),
                ),
            }
            continue;
        }
        let Ok(method) = reqwest::Method::from_bytes(method.as_bytes()) else {
            continue;
        };
        let mut builder = reqwest::Client::new().request(method, &url);
        for (k, v) in &request.headers {
            builder = builder.header(k, v)
        }
        let result = builder.body(request.body).send().await;
        match result {
            Ok(response) => {
                let status = response.status().as_u16();
                let mut headers = serde_json::Map::new();
                for (k, v) in response.headers() {
                    let key = k
                        .as_str()
                        .split('-')
                        .map(|s| {
                            let mut chars = s.chars();
                            chars
                                .next()
                                .map(|c| c.to_uppercase().to_string() + chars.as_str())
                                .unwrap_or_default()
                        })
                        .collect::<Vec<_>>()
                        .join("-");
                    headers.insert(key, json!(v.to_str().unwrap_or("")));
                }
                let body = response.bytes().await.unwrap_or_default();
                append(
                    &state,
                    &event_id,
                    json!({"type":"WEBHOOK_RESPONSE","timeOffsetNanos":0,"data":{"status":status,"headers":headers,"bodyAsBase64":base64::engine::general_purpose::STANDARD.encode(&body),"body":String::from_utf8_lossy(&body),"fromProxy":false}}),
                );
            }
            Err(err) => append(
                &state,
                &event_id,
                json!({"type":"ERROR","data":{"message":err.to_string()}}),
            ),
        }
    }
}
fn append(state: &Arc<Mutex<State>>, id: &str, event: Value) {
    if let Ok(mut s) = state.lock() {
        if let Some(e) = s.requests.iter_mut().find(|e| e["id"] == id) {
            if let Some(a) = e["subEvents"].as_array_mut() {
                a.push(event)
            }
        }
    }
}

async fn raw_http(req: &Request, url: &url::Url) -> std::io::Result<Response> {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let host = url.host_str().unwrap_or("localhost");
    let port = url.port_or_known_default().unwrap_or(80);
    let mut stream = tokio::time::timeout(
        std::time::Duration::from_secs(30),
        tokio::net::TcpStream::connect((host, port)),
    )
    .await
    .map_err(std::io::Error::other)??;
    let mut bytes = format!("{} {} HTTP/1.1\r\n", req.method, req.url).into_bytes();
    for (k, v) in &req.headers {
        if k.contains(['\r', '\n']) || v.contains(['\r', '\n']) {
            return Err(std::io::Error::other("Invalid webhook header"));
        }
        bytes.extend_from_slice(format!("{k}: {v}\r\n").as_bytes());
    }
    if req.header("Host").is_none() {
        bytes.extend_from_slice(format!("Host: {host}:{port}\r\n").as_bytes());
    }
    if ["POST", "PUT", "PATCH"].contains(&req.method.as_str()) {
        bytes.extend_from_slice(format!("Content-Length: {}\r\n", req.body.len()).as_bytes());
    }
    bytes.extend_from_slice(b"Connection: close\r\n\r\n");
    bytes.extend_from_slice(&req.body);
    stream.write_all(&bytes).await?;
    let mut raw = vec![];
    tokio::time::timeout(
        std::time::Duration::from_secs(30),
        stream.read_to_end(&mut raw),
    )
    .await
    .map_err(std::io::Error::other)??;
    let mut headers = [httparse::EMPTY_HEADER; 128];
    let mut parsed = httparse::Response::new(&mut headers);
    let offset = match parsed.parse(&raw).map_err(std::io::Error::other)? {
        httparse::Status::Complete(n) => n,
        _ => return Err(std::io::Error::other("Incomplete webhook response")),
    };
    let mut response = Response::new(parsed.code.unwrap_or(502), raw[offset..].to_vec());
    let chunked = parsed.headers.iter().any(|h| {
        h.name.eq_ignore_ascii_case("transfer-encoding") && h.value.eq_ignore_ascii_case(b"chunked")
    });
    response.headers = parsed
        .headers
        .iter()
        .map(|h| (h.name.into(), String::from_utf8_lossy(h.value).into_owned()))
        .collect();
    if chunked {
        let mut pos = offset;
        let mut body = vec![];
        while pos < raw.len() {
            let end = raw[pos..]
                .windows(2)
                .position(|w| w == b"\r\n")
                .ok_or_else(|| std::io::Error::other("Invalid chunk"))?;
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
            if pos + n + 2 > raw.len() {
                return Err(std::io::Error::other("Incomplete chunk"));
            }
            body.extend_from_slice(&raw[pos..pos + n]);
            pos += n + 2;
        }
        response.body = body;
    }
    Ok(response)
}
