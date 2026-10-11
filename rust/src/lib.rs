// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
use serde_json::Value;
use std::collections::HashMap;

pub mod admin;
pub mod diagnostics;
pub mod matcher;
pub mod messaging_runtime;
pub mod raw_json;
pub mod template;
pub mod utf16_json;

#[derive(Clone, Debug, Default)]
pub struct Request {
    pub method: String,
    pub url: String,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
    pub client_ip: String,
    pub scheme: String,
}
impl Request {
    pub fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.as_str())
    }
    pub fn path(&self) -> &str {
        self.url.split('?').next().unwrap_or(&self.url)
    }
    pub fn body_text(&self) -> String {
        String::from_utf8_lossy(&self.body).into_owned()
    }
}
#[derive(Clone, Debug, Default)]
pub struct Response {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
    pub fault: Option<String>,
    pub stream: Option<String>,
}
impl Response {
    pub fn new(status: u16, body: impl Into<Vec<u8>>) -> Self {
        Self {
            status,
            body: body.into(),
            ..Default::default()
        }
    }
    pub fn json(status: u16, value: &Value) -> Self {
        let mut r = Self::new(status, java_json(value).into_bytes());
        r.headers
            .push(("Content-Type".into(), "application/json".into()));
        r
    }
}
#[derive(Default)]
pub struct State {
    pub mappings: Vec<Value>,
    pub default_mappings: Vec<Value>,
    pub raw_json_bodies: HashMap<String, utf16_json::Node>,
    pub providers: Vec<Value>,
    pub requests: std::collections::VecDeque<Value>,
    pub scenarios: HashMap<String, String>,
    pub settings: Value,
    pub recording: Value,
    pub recording_status: String,
    pub root_dir: String,
    pub no_journal: bool,
    pub journal_limit: usize,
    pub global_templating: bool,
    pub message_mappings: Vec<Value>,
    pub channels: Vec<Value>,
    pub channel_senders: HashMap<String, tokio::sync::mpsc::Sender<Value>>,
    pub channel_requests: HashMap<String, Request>,
    pub channel_types: HashMap<String, String>,
    pub messages: std::collections::VecDeque<Value>,
    pub message_notify: std::sync::Arc<tokio::sync::Notify>,
}
pub mod admin_assets;
pub mod admin_json;
pub mod admin_messaging;
pub mod admin_near;
pub mod admin_recording;
pub mod admin_webhook;
pub fn java_json(v: &Value) -> String {
    admin_json::serialize(v)
}
