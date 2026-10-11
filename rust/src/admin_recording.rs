use crate::{Request, Response, State};
use base64::Engine;
use serde_json::{Value, json};
pub fn handle(req: &Request, s: &mut State) -> Option<Response> {
    let p = req.path().strip_prefix("/__admin/recordings/")?;
    let v: Value = serde_json::from_slice(&req.body).unwrap_or(json!({}));
    let out = match p {
        "status" => Response::json(
            200,
            &json!({"status":if s.recording_status.is_empty(){"NeverStarted"}else{&s.recording_status}}),
        ),
        "start" => {
            s.recording = v;
            if let Some(first) = s.requests.front() {
                s.recording["_startId"] = first["id"].clone();
            }
            if let Some(target) = s.recording["targetBaseUrl"].as_str() {
                let m = crate::admin::normalize(
                    json!({"request":{"urlPattern":".*"},"response":{"proxyBaseUrl":target}}),
                );
                s.recording["_proxyId"] = m["id"].clone();
                s.mappings.insert(0, m);
            }
            s.recording_status = "Recording".into();
            Response::json(200, &json!({}))
        }
        "stop" => {
            if s.recording_status != "Recording" {
                Response::json(
                    400,
                    &json!({"errors":[{"code":30,"title":"Not currently recording."}]}),
                )
            } else {
                s.recording_status = "Stopped".into();
                let spec = s.recording.clone();
                s.mappings.retain(|m| m["id"] != spec["_proxyId"]);
                let entries = s
                    .requests
                    .iter()
                    .take_while(|e| e["id"] != spec["_startId"])
                    .cloned()
                    .collect();
                Response::json(200, &snapshot(entries, &spec, s))
            }
        }
        "snapshot" => Response::json(200, &snapshot(s.requests.iter().cloned().collect(), &v, s)),
        _ => Response::new(404, vec![]),
    };
    Some(out)
}
fn snapshot(entries: Vec<Value>, spec: &Value, s: &mut State) -> Value {
    let mut mappings = vec![];
    let mut seen = std::collections::HashSet::new();
    for e in entries {
        if e["responseDefinition"]["proxyBaseUrl"].is_null()
            && spec["filters"]["allowNonProxied"] != true
        {
            continue;
        }
        let r = &e["request"];
        let raw = r["url"].as_str().unwrap_or("/");
        let u = url::Url::parse(&format!("http://localhost{raw}")).unwrap();
        let mut request = json!({"method":r["method"],"url":raw});
        if u.query().is_some() {
            request.as_object_mut().unwrap().remove("url");
            request["urlPath"] = json!(u.path());
            let mut q = serde_json::Map::new();
            for (k, v) in u.query_pairs() {
                q.entry(k.to_string())
                    .or_insert_with(|| json!([]))
                    .as_array_mut()
                    .unwrap()
                    .push(json!({"equalTo":v}));
            }
            for v in q.values_mut() {
                let a = v.as_array().unwrap();
                *v = if a.len() == 1 {
                    a[0].clone()
                } else {
                    json!({"hasExactly":a})
                };
            }
            request["queryParameters"] = json!(q);
        }
        if let Some(capture) = spec["captureHeaders"].as_object() {
            for (k, _) in capture {
                if let Some(value) = r["headers"]
                    .as_object()
                    .and_then(|h| h.iter().find(|(name, _)| name.eq_ignore_ascii_case(k)))
                    .map(|(_, v)| v)
                {
                    request["headers"][k] = json!({"equalTo":value});
                }
            }
        }
        let key = serde_json::to_string(&request).unwrap();
        if !seen.insert(key) && spec["repeatsAsScenarios"] == false {
            continue;
        }
        let mut response = json!({"status":e["response"]["status"]});
        let mut headers = serde_json::Map::new();
        let mut ct = String::new();
        if let Some(h) = e["response"]["headers"].as_object() {
            for (k, v) in h {
                if k.eq_ignore_ascii_case("Content-Type") {
                    ct = v.as_str().unwrap_or("").into();
                }
                if !["content-length", "content-encoding", "transfer-encoding"]
                    .contains(&k.to_lowercase().as_str())
                {
                    headers.insert(k.clone(), v.clone());
                }
            }
        }
        if !headers.is_empty() {
            response["headers"] = json!(headers)
        }
        let body = base64::engine::general_purpose::STANDARD
            .decode(e["response"]["bodyAsBase64"].as_str().unwrap_or(""))
            .unwrap_or_default();
        let text = ct.starts_with("text/") || ct.contains("json") || ct.contains("xml");
        if !body.is_empty() {
            response[if text { "body" } else { "base64Body" }] = if text {
                json!(String::from_utf8_lossy(&body))
            } else {
                json!(base64::engine::general_purpose::STANDARD.encode(&body))
            };
        }
        let name = u.path().trim_matches('/').replace('/', "_").to_lowercase();
        let mut m =
            crate::admin::normalize(json!({"name":name,"request":request,"response":response}));
        if spec["persist"] != false {
            m["persistent"] = json!(true)
        }
        if !body.is_empty() && spec["extractBodyCriteria"].is_object() {
            let threshold = spec["extractBodyCriteria"][if text {
                "textSizeThreshold"
            } else {
                "binarySizeThreshold"
            }]
            .as_u64()
            .unwrap_or(0);
            if body.len() as u64 > threshold {
                let filename = format!(
                    "{}-{}.{}",
                    name,
                    m["id"].as_str().unwrap(),
                    if ct.contains("json") {
                        "json"
                    } else if ct.contains("xml") {
                        "xml"
                    } else if text {
                        "txt"
                    } else {
                        "bin"
                    }
                );
                let dir = std::path::Path::new(&s.root_dir).join("__files");
                let _ = std::fs::create_dir_all(&dir);
                let _ = std::fs::write(dir.join(&filename), body);
                let response = m["response"].as_object_mut().unwrap();
                response.remove("body");
                response.remove("base64Body");
                response.insert("bodyFileName".into(), json!(filename));
            }
        }
        s.mappings.insert(0, m.clone());
        mappings.push(m);
    }
    if spec["repeatsAsScenarios"] != false {
        let mut groups: std::collections::BTreeMap<String, Vec<usize>> =
            std::collections::BTreeMap::new();
        for (i, m) in mappings.iter().enumerate() {
            groups
                .entry(serde_json::to_string(&m["request"]).unwrap())
                .or_default()
                .push(i);
        }
        let mut count = 0;
        for indices in groups.values() {
            if indices.len() < 2 {
                continue;
            }
            count += 1;
            let name = format!(
                "scenario-{}-{}",
                count,
                mappings[indices[0]]["name"]
                    .as_str()
                    .unwrap_or("")
                    .replace('_', "-")
            );
            for (step, index) in indices.iter().rev().enumerate() {
                let m = &mut mappings[*index];
                m["scenarioName"] = json!(name);
                m["requiredScenarioState"] = json!(if step == 0 {
                    "Started".into()
                } else {
                    format!("{name}-{}", step + 1)
                });
                if step + 1 < indices.len() {
                    m["newScenarioState"] = json!(format!("{name}-{}", step + 2));
                }
                if let Some(old) = s.mappings.iter_mut().find(|old| old["id"] == m["id"]) {
                    *old = m.clone();
                }
            }
        }
    }
    json!({"mappings":mappings,"errors":[]})
}
