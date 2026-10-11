// Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
use crate::{
    Request, Response, State, admin,
    utf16_json::{self, Node},
};
use serde_json::Value;
use std::collections::HashSet;
// Keep UTF-16 JSON body syntax beside the scalar-safe native matcher/admin model.
pub fn handle(req: &Request, state: &mut State) -> Option<Response> {
    let mut adjusted = req.clone();
    let parsed = std::str::from_utf8(&req.body)
        .ok()
        .and_then(|s| utf16_json::parse(s).ok());
    let body = parsed
        .as_ref()
        .and_then(|v| v.get("response"))
        .and_then(|v| v.get("jsonBody"))
        .cloned();
    if body.is_some() && serde_json::from_slice::<Value>(&req.body).is_err() {
        adjusted.body = serde_json::to_vec(&parsed.as_ref().unwrap().to_value()).ok()?;
    }
    let response = admin::handle(&adjusted, state)?;
    if response.status < 300 && matches!(req.method.as_str(), "POST" | "PUT") {
        if let Some(body) = body {
            let response_value: Value =
                serde_json::from_slice(&response.body).unwrap_or(Value::Null);
            let ident = response_value["id"]
                .as_str()
                .map(String::from)
                .or_else(|| {
                    parsed
                        .as_ref()
                        .and_then(|v| v.get("id"))
                        .and_then(Node::as_str)
                })
                .or_else(|| {
                    req.path()
                        .strip_prefix("/__admin/mappings/")
                        .filter(|s| uuid::Uuid::parse_str(s).is_ok())
                        .map(String::from)
                });
            if let Some(id) = ident {
                state.raw_json_bodies.insert(id, body);
            }
        }
    }
    let mut retained: HashSet<String> = state
        .mappings
        .iter()
        .chain(state.default_mappings.iter())
        .filter_map(|m| m["id"].as_str().map(String::from))
        .collect();
    for e in &state.requests {
        if let Some(id) = e["stubMapping"]["id"].as_str() {
            retained.insert(id.into());
        }
    }
    state.raw_json_bodies.retain(|id, _| retained.contains(id));

    Some(response)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn surrogate_mapping_admin_and_runtime_differ() {
        let mut s = State::default();
        let input=br#"{"id":"11111111-1111-4111-8111-111111111111","request":{"url":"/x"},"response":{"jsonBody":{"v":"\uD800","p":"\uE000"}}}"#;
        let r = handle(
            &Request {
                method: "POST".into(),
                url: "/__admin/mappings".into(),
                body: input.to_vec(),
                ..Default::default()
            },
            &mut s,
        )
        .unwrap();
        assert_eq!(r.status, 201);
        let text = String::from_utf8(r.body).unwrap();
        assert!(text.contains("\"v\" : \"?\""));
        assert!(text.contains('\u{e000}'));
        assert!(
            s.raw_json_bodies
                .values()
                .next()
                .unwrap()
                .compact()
                .contains("\\uD800")
        );
    }
}
