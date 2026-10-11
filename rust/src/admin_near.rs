use crate::{Request, Response, State};
use serde_json::{Value, json};
pub fn handle(req: &Request, state: &State) -> Option<Response> {
    let p = req.path();
    let mut v: Value = serde_json::from_slice(&req.body).unwrap_or(json!({}));
    let mut misses = vec![];
    if p == "/__admin/near-misses/request-pattern" {
        if v.get("method").is_none() {
            v["method"] = json!("ANY")
        }
        for e in &state.requests {
            let request = crate::admin::logged_request(&e["request"]);
            let d = crate::matcher::request_distance(&v, &request);
            misses.push(json!({"request":e["request"],"requestPattern":v,"matchResult":{"subEvents":warnings(&v,&request),"diffDescriptions":[],"distance":d}}));
        }
    } else if p == "/__admin/near-misses/request" {
        normalize_logged(&mut v);
        let r = crate::admin::logged_request(&v);
        if v.get("browserProxyRequest").is_none() {
            v["browserProxyRequest"] = json!(false)
        }
        for m in &state.mappings {
            let mut pattern = m["request"].clone();
            if v.get("bodyAsBase64").is_none() {
                if let Some(p) = pattern.as_object_mut() {
                    p.remove("bodyPatterns");
                }
            }
            let d = crate::matcher::request_distance(&pattern, &r);
            misses.push(json!({"request":v,"stubMapping":m,"matchResult":{"subEvents":warnings(&pattern,&r),"diffDescriptions":[],"distance":d}}));
        }
    } else if p == "/__admin/requests/unmatched/near-misses" {
        for e in state.requests.iter().filter(|e| e["wasMatched"] == false) {
            let r = crate::admin::logged_request(&e["request"]);
            for m in &state.mappings {
                let d = crate::matcher::request_distance(&m["request"], &r);
                misses.push(json!({"request":e["request"],"stubMapping":m,"matchResult":{"subEvents":warnings(&m["request"],&r),"diffDescriptions":[],"distance":d}}));
            }
        }
    } else {
        return None;
    }
    misses.sort_by(|a, b| {
        a["matchResult"]["distance"]
            .as_f64()
            .partial_cmp(&b["matchResult"]["distance"].as_f64())
            .unwrap()
    });
    misses.truncate(3);
    Some(Response::json(200, &json!({"nearMisses":misses})))
}

fn normalize_logged(v: &mut Value) {
    use base64::Engine;
    if let Some(base64) = v["bodyAsBase64"].as_str() {
        if let Ok(bytes) = base64::engine::general_purpose::STANDARD.decode(base64) {
            v["body"] = json!(String::from_utf8_lossy(&bytes));
        }
    }
    v["port"] = json!(-1);
    if let Some(url) = v["absoluteUrl"]
        .as_str()
        .and_then(|s| url::Url::parse(s).ok())
    {
        v["scheme"] = json!(url.scheme());
        v["host"] = json!(url.host_str().unwrap_or(""));
        v["port"] = json!(url.port_or_known_default().unwrap_or(80));
    }
    if let Some(date) = v["loggedDate"]
        .as_i64()
        .and_then(chrono::DateTime::from_timestamp_millis)
    {
        v["loggedDateString"] = json!(date.to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true));
    }
    v["queryParams"] = crate::admin::query_params(
        v["url"]
            .as_str()
            .unwrap_or("")
            .split_once('?')
            .map(|(_, q)| q)
            .unwrap_or(""),
    );
    v["formParams"] = json!({});
    if v["headers"].as_object().is_some_and(|h| {
        h.iter().any(|(k, v)| {
            k.eq_ignore_ascii_case("content-type")
                && v.as_str()
                    .is_some_and(|v| v.starts_with("application/x-www-form-urlencoded"))
        })
    }) {
        v["formParams"] = crate::admin::query_params(v["body"].as_str().unwrap_or(""));
    }
}

fn warnings(pattern: &Value, request: &Request) -> Vec<Value> {
    if pattern["bodyPatterns"].as_array().is_some_and(|p| {
        p.iter().any(|p| {
            p["equalToXml"].as_str().is_some_and(|expected| {
                crate::matcher::xml_comparison_warning(expected, &request.body_text())
            })
        })
    }) {
        vec![json!({"type":"WARNING","data":{"message":"Caught exception during comparison"}})]
    } else {
        vec![]
    }
}
