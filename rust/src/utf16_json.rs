// Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
//! Ordered JSON syntax tree preserving every Java UTF-16 string code unit.
//!
//! Unlike serde_json::Value, this representation accepts isolated surrogate
//! escapes. No Unicode scalar or other valid user value is reserved as a marker.
use serde_json::{Map, Value};
use std::str::FromStr;

#[derive(Clone, Debug, PartialEq)]
pub enum Node {
    Null,
    Bool(bool),
    Number(String),
    String(Vec<u16>),
    Array(Vec<Node>),
    Object(Vec<(Vec<u16>, Node)>),
}

impl Node {
    pub fn get(&self, key: &str) -> Option<&Node> {
        let key: Vec<_> = key.encode_utf16().collect();
        match self {
            Self::Object(fields) => fields.iter().find(|(k, _)| k == &key).map(|(_, v)| v),
            _ => None,
        }
    }
    pub fn get_mut(&mut self, key: &str) -> Option<&mut Node> {
        let key: Vec<_> = key.encode_utf16().collect();
        match self {
            Self::Object(fields) => fields.iter_mut().find(|(k, _)| k == &key).map(|(_, v)| v),
            _ => None,
        }
    }
    pub fn as_str(&self) -> Option<String> {
        match self {
            Self::String(units) => String::from_utf16(units).ok(),
            _ => None,
        }
    }
    /// Returns a scalar-safe view for native matchers and admin fields. Only
    /// isolated UTF-16 surrogates become '?'; valid strings remain unchanged.
    pub fn to_value(&self) -> Value {
        match self {
            Self::Null => Value::Null,
            Self::Bool(v) => Value::Bool(*v),
            Self::Number(v) => serde_json::from_str(v).unwrap_or(Value::Null),
            Self::String(v) => Value::String(lossy(v)),
            Self::Array(v) => Value::Array(v.iter().map(Self::to_value).collect()),
            Self::Object(v) => {
                let mut map = Map::new();
                for (key, value) in v {
                    map.insert(lossy(key), value.to_value());
                }
                Value::Object(map)
            }
        }
    }
    pub fn from_value(value: &Value) -> Self {
        match value {
            Value::Null => Self::Null,
            Value::Bool(v) => Self::Bool(*v),
            Value::Number(v) => Self::Number(v.to_string()),
            Value::String(v) => Self::String(v.encode_utf16().collect()),
            Value::Array(v) => Self::Array(v.iter().map(Self::from_value).collect()),
            Value::Object(v) => Self::Object(
                v.iter()
                    .map(|(k, v)| (k.encode_utf16().collect(), Self::from_value(v)))
                    .collect(),
            ),
        }
    }
    pub fn compact(&self) -> String {
        let mut out = String::new();
        self.write(&mut out, None);
        out
    }
    /// Jackson default pretty layout: two spaces per object level, inline
    /// arrays, and a space on both sides of the object-member colon.
    pub fn pretty(&self, indent_depth: usize) -> String {
        let mut out = String::new();
        self.write(&mut out, Some(indent_depth));
        out
    }
    fn write(&self, out: &mut String, depth: Option<usize>) {
        match self {
            Self::Null => out.push_str("null"),
            Self::Bool(v) => out.push_str(if *v { "true" } else { "false" }),
            Self::Number(v) => out.push_str(&canonical_number(v)),
            Self::String(v) => write_string(v, out),
            Self::Array(values) => {
                out.push('[');
                if depth.is_some() {
                    out.push(' ')
                }
                for (index, value) in values.iter().enumerate() {
                    if index > 0 {
                        out.push(',');
                        if depth.is_some() {
                            out.push(' ')
                        }
                    }
                    value.write(out, depth);
                }
                if depth.is_some() && !values.is_empty() {
                    out.push(' ')
                }
                out.push(']');
            }
            Self::Object(fields) => {
                out.push('{');
                if let Some(d) = depth {
                    if fields.is_empty() {
                        out.push_str(" }");
                        return;
                    }
                    out.push('\n');
                    for (index, (key, value)) in fields.iter().enumerate() {
                        if index > 0 {
                            out.push_str(",\n")
                        }
                        out.push_str(&"  ".repeat(d + 1));
                        write_string(key, out);
                        out.push_str(" : ");
                        value.write(out, Some(d + 1));
                    }
                    out.push('\n');
                    out.push_str(&"  ".repeat(d));
                    out.push('}');
                } else {
                    for (index, (key, value)) in fields.iter().enumerate() {
                        if index > 0 {
                            out.push(',')
                        }
                        write_string(key, out);
                        out.push(':');
                        value.write(out, None)
                    }
                    out.push('}');
                }
            }
        }
    }
}
fn lossy(units: &[u16]) -> String {
    char::decode_utf16(units.iter().copied())
        .map(|c| c.unwrap_or('?'))
        .collect()
}
fn canonical_number(raw: &str) -> String {
    bigdecimal::BigDecimal::from_str(raw)
        .map(|v| {
            let s = v.to_plain_string();
            if s.chars().all(|c| c == '0') {
                "0".into()
            } else {
                s
            }
        })
        .unwrap_or_else(|_| raw.to_string())
}
fn write_string(units: &[u16], out: &mut String) {
    out.push('"');
    for &unit in units {
        match unit {
            0x22 => out.push_str("\\\""),
            0x5c => out.push_str("\\\\"),
            8 => out.push_str("\\b"),
            9 => out.push_str("\\t"),
            10 => out.push_str("\\n"),
            12 => out.push_str("\\f"),
            13 => out.push_str("\\r"),
            0..=31 | 0xd800..=0xdfff => {
                use std::fmt::Write;
                let _ = write!(out, "\\u{unit:04X}");
            }
            _ => out.push(char::from_u32(unit as u32).unwrap()),
        }
    }
    out.push('"');
}

pub fn parse(input: &str) -> Result<Node, String> {
    let mut parser = Parser { input, offset: 0 };
    let value = parser.value(0)?;
    parser.whitespace();
    if parser.offset != input.len() {
        return Err(parser.error("unexpected trailing input"));
    }
    Ok(value)
}
struct Parser<'a> {
    input: &'a str,
    offset: usize,
}
impl Parser<'_> {
    fn error(&self, message: &str) -> String {
        format!("{message} at byte {}", self.offset)
    }
    fn whitespace(&mut self) {
        while self
            .input
            .as_bytes()
            .get(self.offset)
            .is_some_and(|b| matches!(b, b' ' | b'\t' | b'\r' | b'\n'))
        {
            self.offset += 1
        }
    }
    fn consume(&mut self, byte: u8) -> bool {
        if self.input.as_bytes().get(self.offset) == Some(&byte) {
            self.offset += 1;
            true
        } else {
            false
        }
    }
    fn value(&mut self, depth: usize) -> Result<Node, String> {
        if depth > 256 {
            return Err(self.error("JSON nesting exceeds 256 levels"));
        }
        self.whitespace();
        match self.input.as_bytes().get(self.offset).copied() {
            Some(b'"') => self.string().map(Node::String),
            Some(b'{') => {
                self.offset += 1;
                self.whitespace();
                let mut fields = Vec::new();
                if self.consume(b'}') {
                    return Ok(Node::Object(fields));
                }
                loop {
                    self.whitespace();
                    let key = self.string()?;
                    self.whitespace();
                    if !self.consume(b':') {
                        return Err(self.error("expected ':'"));
                    }
                    let value = self.value(depth + 1)?;
                    if let Some((_, old)) = fields.iter_mut().find(|(k, _)| k == &key) {
                        *old = value
                    } else {
                        fields.push((key, value))
                    }
                    self.whitespace();
                    if self.consume(b'}') {
                        break;
                    }
                    if !self.consume(b',') {
                        return Err(self.error("expected ',' or '}'"));
                    }
                }
                Ok(Node::Object(fields))
            }
            Some(b'[') => {
                self.offset += 1;
                self.whitespace();
                let mut values = Vec::new();
                if self.consume(b']') {
                    return Ok(Node::Array(values));
                }
                loop {
                    values.push(self.value(depth + 1)?);
                    self.whitespace();
                    if self.consume(b']') {
                        break;
                    }
                    if !self.consume(b',') {
                        return Err(self.error("expected ',' or ']'"));
                    }
                }
                Ok(Node::Array(values))
            }
            Some(b'n') => {
                self.literal("null")?;
                Ok(Node::Null)
            }
            Some(b't') => {
                self.literal("true")?;
                Ok(Node::Bool(true))
            }
            Some(b'f') => {
                self.literal("false")?;
                Ok(Node::Bool(false))
            }
            Some(b'-' | b'0'..=b'9') => self.number().map(Node::Number),
            _ => Err(self.error("expected JSON value")),
        }
    }
    fn literal(&mut self, value: &str) -> Result<(), String> {
        if self.input[self.offset..].starts_with(value) {
            self.offset += value.len();
            Ok(())
        } else {
            Err(self.error("invalid literal"))
        }
    }
    fn number(&mut self) -> Result<String, String> {
        let start = self.offset;
        self.consume(b'-');
        if !self.consume(b'0') {
            if !self
                .input
                .as_bytes()
                .get(self.offset)
                .is_some_and(|b| matches!(b, b'1'..=b'9'))
            {
                return Err(self.error("expected number digit"));
            }
            self.offset += 1;
            while self
                .input
                .as_bytes()
                .get(self.offset)
                .is_some_and(u8::is_ascii_digit)
            {
                self.offset += 1
            }
        }
        if self.consume(b'.') {
            let start = self.offset;
            while self
                .input
                .as_bytes()
                .get(self.offset)
                .is_some_and(u8::is_ascii_digit)
            {
                self.offset += 1
            }
            if start == self.offset {
                return Err(self.error("expected fractional digit"));
            }
        }
        if self.consume(b'e') || self.consume(b'E') {
            let exponent_start = self.offset;
            if !self.consume(b'+') {
                self.consume(b'-');
            }
            let start = self.offset;
            while self
                .input
                .as_bytes()
                .get(self.offset)
                .is_some_and(u8::is_ascii_digit)
            {
                self.offset += 1
            }
            if start == self.offset {
                return Err(self.error("expected exponent digit"));
            }
            // Bound expansion during BigDecimal's plain serialization. This is
            // an explicit parser resource limit, not silent numeric rounding.
            let exponent = self.input[exponent_start..self.offset]
                .parse::<i64>()
                .map_err(|_| self.error("JSON exponent exceeds resource limit"))?;
            if exponent.unsigned_abs() > 1_000_000 {
                return Err(self.error("JSON exponent exceeds resource limit"));
            }
        }
        Ok(self.input[start..self.offset].to_string())
    }
    fn string(&mut self) -> Result<Vec<u16>, String> {
        if !self.consume(b'"') {
            return Err(self.error("expected string"));
        }
        let mut units = Vec::new();
        loop {
            let Some(&byte) = self.input.as_bytes().get(self.offset) else {
                return Err(self.error("unterminated string"));
            };
            if byte == b'"' {
                self.offset += 1;
                return Ok(units);
            }
            if byte == b'\\' {
                self.offset += 1;
                let Some(&escape) = self.input.as_bytes().get(self.offset) else {
                    return Err(self.error("unterminated escape"));
                };
                self.offset += 1;
                match escape {
                    b'"' => units.push(0x22),
                    b'\\' => units.push(0x5c),
                    b'/' => units.push(0x2f),
                    b'b' => units.push(8),
                    b'f' => units.push(12),
                    b'n' => units.push(10),
                    b'r' => units.push(13),
                    b't' => units.push(9),
                    b'u' => {
                        let bytes = self
                            .input
                            .as_bytes()
                            .get(self.offset..self.offset + 4)
                            .ok_or_else(|| self.error("incomplete Unicode escape"))?;
                        let digits = std::str::from_utf8(bytes)
                            .map_err(|_| self.error("invalid Unicode escape"))?;
                        let unit = u16::from_str_radix(digits, 16)
                            .map_err(|_| self.error("invalid Unicode escape"))?;
                        units.push(unit);
                        self.offset += 4;
                    }
                    _ => return Err(self.error("invalid string escape")),
                }
            } else {
                if byte < 32 {
                    return Err(self.error("unescaped control character"));
                }
                let c = self.input[self.offset..]
                    .chars()
                    .next()
                    .ok_or_else(|| self.error("invalid string"))?;
                let mut buf = [0; 2];
                units.extend_from_slice(c.encode_utf16(&mut buf));
                self.offset += c.len_utf8();
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn preserves_surrogates_without_reserving_valid_strings() {
        let raw = r#"{"high":"\uD800","low":"\uDC00","pair":"\uD83D\uDE00","question":"?","replacement":"�","private":"\uE000","literal":"\\uD800"}"#;
        let node = parse(raw).unwrap();
        let compact = node.compact();
        assert_eq!(parse(&compact).unwrap(), node);
        assert_eq!(node.get("high").unwrap().as_str(), None);
        assert_eq!(node.get("pair").unwrap().as_str().as_deref(), Some("😀"));
        let value = node.to_value();
        assert_eq!(value["high"], "?");
        assert_eq!(value["pair"], "😀");
        assert_eq!(value["private"], "\u{e000}");
        assert_eq!(value["literal"], "\\uD800");
        assert!(compact.contains(r#""high":"\uD800""#));
        assert!(compact.contains(r#""pair":"\uD83D\uDE00""#));
    }
    #[test]
    fn ordered_objects_arrays_and_decimal_scale() {
        let node = parse(r#"{"z":1.2300e2,"a":[-0.000,1e-7,{"k":"é漢😀"}]}"#).unwrap();
        assert_eq!(
            node.compact(),
            r#"{"z":123.00,"a":[0.000,0.0000001,{"k":"é漢\uD83D\uDE00"}]}"#
        );
        assert_eq!(
            parse(r#"{"a":1,"a":2,"b":3}"#).unwrap().compact(),
            r#"{"a":2,"b":3}"#
        );
        assert_eq!(
            parse(r#"{"a":[],"b":{}}"#).unwrap().pretty(0),
            "{\n  \"a\" : [ ],\n  \"b\" : { }\n}"
        );
    }
    #[test]
    fn rejects_malformed_unicode_and_json_without_panicking() {
        for raw in [
            r#""\uabc😀""#,
            r#""\u12""#,
            r#""\q""#,
            r#"{"a":1,}"#,
            "01",
            "1.",
            "1e",
            "true null",
            "[1,]",
            "1e999999999",
        ] {
            assert!(parse(raw).is_err(), "{raw}");
        }
    }
}
