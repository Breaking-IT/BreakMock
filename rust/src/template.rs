// Copyright 2026 Breaking-IT. Apache-2.0.
// Native implementation of WireMock template behavior; no external runtime.
use crate::Request;
#[path = "template_date.rs"]
mod dates;
#[path = "template_json.rs"]
mod wire_json;
#[path = "template_xml.rs"]
mod xml;
use base64::Engine;

use handlebars::{
    Context, Handlebars, Helper, HelperDef, HelperResult, Output, RenderContext, RenderError,
    RenderErrorReason, Renderable, ScopedJson, StringOutput,
};
use serde_json::{Map, Value, json};
use std::sync::OnceLock;

fn text(v: &Value) -> String {
    match v {
        Value::Null => String::new(),
        Value::String(s) => s.clone(),
        Value::Array(a) => format!("[{}]", a.iter().map(text).collect::<Vec<_>>().join(", ")),
        Value::Object(m) => {
            if let Some(date) = m.get("__date") {
                text(date)
            } else if let Some(xml) = m.get("__xml") {
                text(xml)
            } else {
                pretty(v)
            }
        }
        _ => v.to_string(),
    }
}
fn pretty(v: &Value) -> String {
    match v {
        Value::Object(m) => {
            if m.is_empty() {
                "{ }".into()
            } else {
                format!(
                    "{{\n{}\n}}",
                    m.iter()
                        .map(|(k, v)| format!(
                            "  {} : {}",
                            json!(k),
                            pretty(v).replace('\n', "\n  ")
                        ))
                        .collect::<Vec<_>>()
                        .join(",\n")
                )
            }
        }
        Value::Array(a) => {
            if a.is_empty() {
                "[ ]".into()
            } else {
                format!(
                    "[ {} ]",
                    a.iter().map(pretty).collect::<Vec<_>>().join(", ")
                )
            }
        }
        Value::String(s) => java_quote(s),
        _ => v.to_string(),
    }
}
fn number(v: &Value) -> f64 {
    v.as_f64().unwrap_or_else(|| text(v).parse().unwrap_or(0.0))
}
fn truth(v: &Value) -> bool {
    match v {
        Value::Null => false,
        Value::Bool(b) => *b,
        Value::String(s) => !s.is_empty(),
        Value::Array(a) => !a.is_empty(),
        Value::Number(n) => n.as_f64() != Some(0.),
        _ => true,
    }
}
fn error(s: impl Into<String>) -> RenderError {
    RenderErrorReason::Other(s.into()).into()
}
fn model(req: &Request, parameters: &Value) -> Value {
    let host = req.header("Host").unwrap_or("localhost");
    let scheme = if req.scheme.is_empty() {
        "http"
    } else {
        &req.scheme
    };
    let absolute = if req.url.starts_with("http") {
        req.url.clone()
    } else {
        format!("{scheme}://{host}{}", req.url)
    };
    let mut query = Map::new();
    if let Some(q) = req.url.split_once('?') {
        for (k, v) in url::form_urlencoded::parse(q.1.as_bytes()) {
            query
                .entry(k.into_owned())
                .or_insert(json!([]))
                .as_array_mut()
                .unwrap()
                .push(json!(v));
        }
    }
    let mut headers = Map::new();
    for (k, v) in &req.headers {
        headers
            .entry(k.to_lowercase())
            .or_insert(json!([]))
            .as_array_mut()
            .unwrap()
            .push(json!(v));
    }
    let mut cookies = Map::new();
    if let Some(c) = req.header("Cookie") {
        for part in c.split(';') {
            if let Some((k, v)) = part.trim().split_once('=') {
                cookies
                    .entry(k.to_string())
                    .or_insert(json!([]))
                    .as_array_mut()
                    .unwrap()
                    .push(json!(v));
            }
        }
    }
    let segments: Vec<_> = req.path().split('/').filter(|v| !v.is_empty()).collect();
    let sizes = json!({"query":query.iter().map(|(k,v)|(k.clone(),json!(v.as_array().map_or(0,Vec::len)))).collect::<Map<String,Value>>(),"headers":headers.iter().map(|(k,v)|(k.clone(),json!(v.as_array().map_or(0,Vec::len)))).collect::<Map<String,Value>>(),"cookies":cookies.iter().map(|(k,v)|(k.clone(),json!(v.as_array().map_or(0,Vec::len)))).collect::<Map<String,Value>>()});
    let mut result = json!({"request":{"method":req.method,"url":req.url,"path":req.path(),"pathSegments":segments,"absoluteUrl":absolute,"baseUrl":format!("{scheme}://{host}"),"scheme":scheme,"host":host.split(':').next().unwrap_or(host),"port":host.split(':').nth(1).and_then(|p|p.parse::<u16>().ok()).unwrap_or(if scheme=="https"{443}else{80}),"body":req.body_text(),"bodyAsBase64":base64::engine::general_purpose::STANDARD.encode(&req.body),"headers":headers,"query":query,"cookies":cookies,"clientIp":req.client_ip,"multipart":false,"binary":false},"parameters":parameters});
    result["request"]["__sizes"] = sizes;
    result["request"]["absoluteUrl"] = Value::Null;
    result["request"]["binary"] = json!(
        req.header("Content-Type")
            .is_some_and(|v| v.starts_with("application/octet-stream"))
    );
    if let Some(parts) = multipart_model(req) {
        result["request"]["multipart"] = json!(true);
        result["request"]["parts"] = parts;
    }
    result["request"]["requestLine"] = result["request"].clone();
    result
}
fn rewrite(source: &str, model: &Value) -> String {
    static EXPR: OnceLock<regex::Regex> = OnceLock::new();
    static PATH: OnceLock<regex::Regex> = OnceLock::new();
    EXPR.get_or_init(|| regex::Regex::new(r"(?s)\{\{.*?\}\}").unwrap())
        .replace_all(source, |caps: &regex::Captures| {
            PATH.get_or_init(|| {
                regex::Regex::new(
                    r"request\.(query|headers|cookies)\.([A-Za-z0-9_\-]+)(?:\.(first|last|size))?",
                )
                .unwrap()
            })
            .replace_all(&caps[0], |p: &regex::Captures| {
                let kind = &p[1];
                let key = if kind == "headers" {
                    p[2].to_lowercase()
                } else {
                    p[2].to_string()
                };
                let base = format!("request.{kind}.{key}");
                let end = p.get(0).unwrap().end();
                let rest = &caps[0][end..];
                if rest.starts_with('.') || rest.starts_with('[') {
                    return base;
                }
                let len = model
                    .pointer(&format!("/request/{kind}/{key}"))
                    .and_then(Value::as_array)
                    .map_or(0, Vec::len);
                match p.get(3).map(|m| m.as_str()) {
                    Some("size") => format!("request.__sizes.{kind}.{key}"),
                    Some("last") => format!("{base}.[{}]", len.saturating_sub(1)),
                    _ => format!("{base}.[0]"),
                }
            })
            .into_owned()
        })
        .into_owned()
}
pub fn request_model(req: &Request) -> Value {
    model(req, &Value::Null)["request"].clone()
}
pub fn render(source: &str, req: &Request, parameters: &Value) -> Result<String, String> {
    if let Some(value) = fast_render(source, req) {
        return Ok(value);
    }
    render_with_model(source, &model(req, parameters))
}
pub fn render_with_model(source: &str, model: &Value) -> Result<String, String> {
    static REGISTRY: OnceLock<Handlebars<'static>> = OnceLock::new();
    let registry = REGISTRY.get_or_init(|| {
        let mut r = Handlebars::new();
        r.register_escape_fn(handlebars::no_escape);
        for name in HELPERS.split_whitespace() {
            r.register_helper(name, Box::new(WireHelper(name)));
        }
        r
    });
    validate_helpers(source)?;
    let mut rewritten = rewrite(source, model).replace("request.path.[", "request.pathSegments.[");
    rewritten = rewritten
        .replace("{{request.pathSegments}}", "{{request.path}}")
        .replace("{{request.requestLine.pathSegments}}", "{{request.path}}");
    static ASSIGN: OnceLock<regex::Regex> = OnceLock::new();
    for cap in ASSIGN
        .get_or_init(|| regex::Regex::new(r"\{\{#assign\s+'([^']+)'").unwrap())
        .captures_iter(source)
    {
        let name = &cap[1];
        rewritten = rewritten.replace(&format!("{{{{{name}}}}}"), &format!("{{{{@root.{name}}}}}"));
    }
    static FORM: OnceLock<regex::Regex> = OnceLock::new();
    for cap in FORM
        .get_or_init(|| regex::Regex::new(r"\{\{formData\s+(?:'[^']*'|[^ ]+)\s+'([^']+)'").unwrap())
        .captures_iter(source)
    {
        let re = regex::Regex::new(&format!(
            r"\{{\{{({}\.[A-Za-z0-9_-]+)\}}\}}",
            regex::escape(&cap[1])
        ))
        .unwrap();
        rewritten = re.replace_all(&rewritten, "{{__first $1}}").into_owned();
    }
    rewritten = preserve_literal_backslashes(&rewritten);
    static NOARGS: OnceLock<regex::Regex> = OnceLock::new();
    rewritten = NOARGS
        .get_or_init(|| {
            regex::Regex::new(r"\{\{#([A-Za-z][A-Za-z0-9_]*)\}\}.*?\{\{/[^}]+\}\}").unwrap()
        })
        .replace_all(&rewritten, |c: &regex::Captures| {
            if HELPERS
                .split_whitespace()
                .chain(["each", "if", "unless", "with"])
                .any(|n| n == &c[1])
            {
                c[0].to_string()
            } else {
                String::new()
            }
        })
        .into_owned();
    static CACHE: OnceLock<
        std::sync::Mutex<std::collections::HashMap<String, std::sync::Arc<handlebars::Template>>>,
    > = OnceLock::new();
    let cache = CACHE.get_or_init(|| std::sync::Mutex::new(std::collections::HashMap::new()));
    let compiled = {
        let mut guard = cache.lock().unwrap();
        if let Some(t) = guard.get(&rewritten) {
            t.clone()
        } else {
            let t = std::sync::Arc::new(
                handlebars::Template::compile(&rewritten).map_err(|e| e.to_string())?,
            );
            if guard.len() >= 4096 {
                guard.clear()
            }
            guard.insert(rewritten, t.clone());
            t
        }
    };
    let context = Context::wraps(model).map_err(|e| e.to_string())?;
    let mut rc = RenderContext::new(None);
    let mut output = StringOutput::new();
    compiled
        .render(registry, &context, &mut rc, &mut output)
        .map_err(|e| {
            let message = e.reason().to_string();
            let first = source.find("{{").unwrap_or(0);
            let column = if message.contains("ArithmeticException") {
                source.find("math ").unwrap_or(first + 2)
            } else if source[first..].starts_with("{{#") {
                3
            } else {
                2
            };
            if message.starts_with("java.") {
                format!("1:{column}: {message}")
            } else {
                message
            }
        })?;
    output.into_string().map_err(|e| e.to_string())
}
const HELPERS: &str = "array range arrayAdd arrayRemove arrayJoin size length val assign math trim upper lower capitalize capitalizeFirst replace cut substring contains matches regexExtract base64 urlEncode formData parseJson toJson formatJson jsonPath jsonMerge jsonRemove jsonArrayAdd eq neq gt gte lt lte and or not isEven isOdd stripes center ljust rjust slugify stripTags abbreviate wordWrap yesno stringFormat randomInt randomDecimal randomValue pickRandom now date parseDate truncateDate dateFormat systemValue __first hostname join split reverse default defaultIfEmpty numberFormat log xPath soapXPath formatXml";
struct WireHelper(&'static str);
impl HelperDef for WireHelper {
    fn call_inner<'reg: 'rc, 'rc>(
        &self,
        h: &Helper<'rc>,
        r: &'reg Handlebars<'reg>,
        ctx: &'rc Context,
        rc: &mut RenderContext<'reg, 'rc>,
    ) -> Result<ScopedJson<'rc>, RenderError> {
        Ok(ScopedJson::Derived(self.eval(h, r, ctx, rc)?))
    }
    fn call<'reg: 'rc, 'rc>(
        &self,
        h: &Helper<'rc>,
        r: &'reg Handlebars<'reg>,
        ctx: &'rc Context,
        rc: &mut RenderContext<'reg, 'rc>,
        out: &mut dyn Output,
    ) -> HelperResult {
        let value = self.eval(h, r, ctx, rc)?;
        if h.template().is_some()
            && matches!(
                self.0,
                "contains"
                    | "matches"
                    | "eq"
                    | "neq"
                    | "gt"
                    | "gte"
                    | "lt"
                    | "lte"
                    | "and"
                    | "or"
                    | "not"
                    | "isEven"
                    | "isOdd"
            )
        {
            if let Some(t) = if truth(&value) {
                h.template()
            } else if matches!(self.0, "contains" | "matches") {
                None
            } else {
                h.inverse()
            } {
                t.render(r, ctx, rc, out)?;
            }
            return Ok(());
        }
        let s = if self.0 == "randomDecimal" && value.is_number() {
            let n = number(&value);
            if n.fract() == 0. {
                format!("{n:.1}")
            } else {
                n.to_string()
            }
        } else if matches!(self.0, "xPath" | "soapXPath") {
            if let Some(a) = value.as_array() {
                if h.param(1)
                    .is_some_and(|p| text(p.value()).ends_with("text()"))
                {
                    a.iter().map(text).collect::<String>()
                } else {
                    a.first().map(text).unwrap_or_default()
                }
            } else {
                text(&value)
            }
        } else if matches!(self.0, "jsonPath")
            && matches!(value, Value::Object(_) | Value::Array(_))
        {
            pretty(&value)
        } else if self.0 == "parseJson" {
            java_display(&value)
        } else if matches!(self.0, "jsonMerge" | "jsonRemove" | "jsonArrayAdd")
            && !value.is_string()
        {
            value.to_string()
        } else if self.0 == "isEven" || self.0 == "isOdd" {
            if truth(&value) {
                if self.0 == "isEven" {
                    "even".into()
                } else {
                    "odd".into()
                }
            } else {
                String::new()
            }
        } else {
            text(&value)
        };
        out.write(&s)?;
        Ok(())
    }
}
impl WireHelper {
    fn eval<'reg: 'rc, 'rc>(
        &self,
        h: &Helper<'rc>,
        r: &'reg Handlebars<'reg>,
        ctx: &'rc Context,
        rc: &mut RenderContext<'reg, 'rc>,
    ) -> Result<Value, RenderError> {
        let p: Vec<Value> = h.params().iter().map(|p| p.value().clone()).collect();
        let get = |i: usize| p.get(i).cloned().unwrap_or(Value::Null);
        let s = |i| text(&get(i));
        let n = |i| number(&get(i));
        let hash = |k: &str| {
            h.hash_get(k)
                .map(|v| v.value().clone())
                .unwrap_or(Value::Null)
        };
        let block = |rc: &mut RenderContext<'reg, 'rc>| -> Result<String, RenderError> {
            let mut out = StringOutput::new();
            if let Some(t) = h.template() {
                t.render(r, ctx, rc, &mut out)?;
            }
            out.into_string().map_err(|e| error(e.to_string()))
        };
        let set = |key: String,
                   value: Value,
                   rc: &mut RenderContext<'reg, 'rc>|
         -> Result<(), RenderError> {
            let mut data = rc
                .context()
                .map(|c| c.data().clone())
                .unwrap_or_else(|| ctx.data().clone());
            data[key] = value;
            rc.set_context(Context::wraps(data)?);
            Ok(())
        };
        Ok(match self.0 {
            "__first" => get(0)
                .as_array()
                .and_then(|a| a.first())
                .cloned()
                .unwrap_or(get(0)),
            "assign" => {
                set(s(0), json!(block(rc)?), rc)?;
                Value::Null
            }
            "val" => {
                let mut v = get(0);
                if v.is_null() {
                    v = hash("default");
                    if v.is_null() {
                        v = hash("or")
                    }
                }
                if let Some(k) = hash("assign").as_str() {
                    set(k.into(), v, rc)?;
                    Value::Null
                } else {
                    v
                }
            }
            "array" => json!(p),
            "range" => {
                if p.len() < 2 {
                    return Ok(json!(
                        "[ERROR: The range helper requires both lower and upper bounds as integer parameters]"
                    ));
                }
                let (a, b) = (n(0) as i64, n(1) as i64);
                if b < a {
                    return Err(error(format!(
                        "java.lang.IllegalArgumentException: {}",
                        (b as i128) - (a as i128) + 1
                    )));
                }
                if (b as i128) - (a as i128) > 1_000_000 {
                    return Err(error(
                        "Template range exceeds native limit of 1000001 items",
                    ));
                }
                json!(if a <= b {
                    (a..=b).map(|i| json!(i)).collect::<Vec<_>>()
                } else {
                    Vec::new()
                })
            }
            "arrayAdd" => {
                if get(0).is_null() {
                    return Ok(json!([]));
                }
                let mut a = get(0).as_array().cloned().unwrap_or_default();
                if p.len() < 2 {
                    return Ok(json!(
                        "[ERROR: Missing required parameter: additional value to add to list]"
                    ));
                }
                if p.len() > 1 {
                    let pos = hash("position");
                    if pos.is_string() && pos != "start" && pos != "end" {
                        return Ok(json!(
                            "[ERROR: position must be 'start', 'end' or an integer]"
                        ));
                    }
                    if pos.as_i64().is_some_and(|i| i < 0 || i as usize > a.len()) {
                        return Ok(json!(
                            "[ERROR: position must be greater than or equal to 0 and less than or equal to the size of the list]"
                        ));
                    }
                    let i = if pos == "start" {
                        0
                    } else {
                        pos.as_u64()
                            .map(|x| x as usize)
                            .unwrap_or(a.len())
                            .min(a.len())
                    };
                    a.insert(i, get(1));
                }
                json!(a)
            }
            "arrayRemove" => {
                let mut a = get(0).as_array().cloned().unwrap_or_default();
                let pos = hash("position");
                if !pos.is_null() {
                    if pos.is_string() && pos != "start" && pos != "end" {
                        return Ok(json!(
                            "[ERROR: position must be 'start', 'end' or an integer]"
                        ));
                    }
                    if pos.as_i64().is_some_and(|i| i < 0 || i as usize > a.len()) {
                        return Ok(json!(
                            "[ERROR: position must be greater than or equal to 0 and less than or equal to the size of the list]"
                        ));
                    }
                    let i = if pos == "start" {
                        0
                    } else if pos == "end" {
                        a.len().saturating_sub(1)
                    } else {
                        number(&pos) as usize
                    };
                    if i < a.len() {
                        a.remove(i);
                    }
                } else {
                    if a.is_empty() {
                        return Err(error(
                            "java.lang.IndexOutOfBoundsException: Index -1 out of bounds for length 0",
                        ));
                    }
                    a.pop();
                }
                json!(a)
            }
            "arrayJoin" | "join" => {
                let (array, delimiter) = if self.0 == "join" {
                    (get(0).as_array().cloned().unwrap_or_default(), s(1))
                } else if let Some(a) = get(1).as_array() {
                    (a.clone(), s(0))
                } else {
                    (p.iter().skip(1).cloned().collect(), s(0))
                };
                if array.is_empty() {
                    return Ok(json!(""));
                }
                let mut values = Vec::new();
                for value in array {
                    if let Some(t) = h.template() {
                        let context = Context::wraps(value)?;
                        let mut local = RenderContext::new(None);
                        let mut output = StringOutput::new();
                        t.render(r, &context, &mut local, &mut output)?;
                        values.push(output.into_string().map_err(|e| error(e.to_string()))?)
                    } else {
                        values.push(text(&value))
                    }
                }
                json!(format!(
                    "{}{}{}",
                    text(&hash("prefix")),
                    values.join(&delimiter),
                    text(&hash("suffix"))
                ))
            }
            "size" | "length" => json!(match get(0) {
                Value::Array(a) => a.len(),
                Value::Object(a) => a.len(),
                v => text(&v).encode_utf16().count(),
            }),
            "math" => {
                if p.len() < 3 {
                    return Ok(json!(
                        "[ERROR: All maths functions require two operands and an operator as parameters e.g. 3 '+' 2]"
                    ));
                }
                let (a, b) = (n(0), n(2));
                let result = match s(1).as_str() {
                    "+" => a + b,
                    "-" => a - b,
                    "*" => a * b,
                    "/" => {
                        if b == 0. {
                            return Err(error("java.lang.ArithmeticException: / by zero"));
                        } else {
                            (a / b).round()
                        }
                    }
                    "%" => a % b,
                    _ => return Err(error("unknown math operator")),
                };
                if result.fract() == 0.0
                    && !get(0).as_number().is_some_and(|v| v.is_f64())
                    && !get(2).as_number().is_some_and(|v| v.is_f64())
                {
                    json!(result as i64)
                } else {
                    json!(result)
                }
            }
            "trim" => json!(if h.template().is_some() {
                block(rc)?.trim().to_string()
            } else {
                s(0).trim().to_string()
            }),
            "upper" => json!(s(0).to_uppercase()),
            "lower" => json!(s(0).to_lowercase()),
            "capitalize" => json!(
                s(0).split(' ')
                    .map(capitalize)
                    .collect::<Vec<_>>()
                    .join(" ")
            ),
            "capitalizeFirst" => json!(capitalize(&s(0))),
            "replace" => json!(s(0).replace(&s(1), &s(2))),
            "cut" => json!(s(0).replace(&s(1), "")),
            "substring" => {
                let a: Vec<u16> = s(0).encode_utf16().collect();
                let start = (n(1).max(0.) as usize).min(a.len());
                let end = if p.len() > 2 {
                    (n(2).max(0.) as usize).min(a.len())
                } else {
                    a.len()
                };
                json!(String::from_utf16_lossy(&a[start..end.max(start)]))
            }
            "contains" if p.len() < 2 => json!(
                "[ERROR: You must specify the string or array to be matched and the regular expression]"
            ),
            "matches" if p.len() < 2 => json!(
                "[ERROR: You must specify the string to be matched and the regular expression]"
            ),
            "contains" => json!(match get(0) {
                Value::Array(a) => a.contains(&get(1)),
                _ => s(0).contains(&s(1)),
            }),
            "matches" => json!(
                fancy_regex::Regex::new(&format!("^(?:{})$", s(1)))
                    .ok()
                    .and_then(|re| re.is_match(&s(0)).ok())
                    .unwrap_or(false)
            ),
            "regexExtract" => {
                let re = match fancy_regex::Regex::new(&s(1)) {
                    Ok(re) => re,
                    Err(_) => return Ok(json!(format!("[ERROR: Invalid regex string {}]", s(1)))),
                };
                let input = s(0);
                let found = re
                    .captures_iter(&input)
                    .filter_map(Result::ok)
                    .collect::<Vec<_>>();
                if found.is_empty() {
                    return Ok(if !hash("default").is_null() {
                        hash("default")
                    } else {
                        json!(format!("[ERROR: Nothing matched {}]", s(1)))
                    });
                }
                if p.len() > 2 {
                    let mut a = Vec::new();
                    for captures in found {
                        for i in 1..captures.len() {
                            a.push(json!(captures.get(i).map(|m| m.as_str()).unwrap_or("")))
                        }
                    }
                    set(s(2), json!(a), rc)?;
                    Value::Null
                } else {
                    json!(found[0].get(0).map(|m| m.as_str()).unwrap_or(""))
                }
            }
            "base64" => {
                let input = if h.template().is_some() {
                    block(rc)?
                } else {
                    s(0)
                };
                if truth(&hash("decode")) {
                    json!(
                        String::from_utf8_lossy(
                            &base64::engine::general_purpose::STANDARD
                                .decode(input)
                                .map_err(|e| error(e.to_string()))?
                        )
                        .to_string()
                    )
                } else {
                    json!(base64::engine::general_purpose::STANDARD.encode(input))
                }
            }
            "urlEncode" => {
                let input = if h.template().is_some() {
                    block(rc)?
                } else {
                    s(0)
                };
                if truth(&hash("decode")) {
                    json!(
                        url::form_urlencoded::parse(format!("x={input}").as_bytes())
                            .next()
                            .map(|(_, v)| v.into_owned())
                            .unwrap_or_default()
                    )
                } else {
                    json!(
                        url::form_urlencoded::byte_serialize(input.as_bytes()).collect::<String>()
                    )
                }
            }
            "formData" => {
                let mut m = Map::new();
                for part in s(0).split('&') {
                    let pieces: Vec<_> = part.split('=').collect();
                    if pieces.len() < 2 || pieces[1].is_empty() {
                        continue;
                    }
                    let (key, value) = (pieces[0], pieces[1]);
                    let value = if truth(&hash("urlDecode")) {
                        url::form_urlencoded::parse(format!("x={value}").as_bytes())
                            .next()
                            .unwrap()
                            .1
                            .into_owned()
                    } else {
                        value.into()
                    };
                    if let Some(existing) = m.get_mut(key) {
                        if !existing.is_array() {
                            *existing = json!([existing.clone()])
                        }
                        existing.as_array_mut().unwrap().push(json!(value));
                    } else {
                        m.insert(key.into(), json!(value));
                    }
                }
                set(s(1), Value::Object(m), rc)?;
                Value::Null
            }
            "parseJson" => {
                let input = if h.template().is_some() {
                    block(rc)?
                } else {
                    s(0)
                };
                let value: Value = wire_json::parse(&input).map_err(error)?;
                if !value.is_null() && !value.is_object() && !value.is_array() {
                    return Err(error(match &value{
                  Value::Number(_)=>"Cannot deserialize value of type `java.util.LinkedHashMap<java.lang.String,java.lang.Object>` from Integer value (token `JsonToken.VALUE_NUMBER_INT`)".into(),
                  Value::Bool(b)=>format!("Cannot deserialize value of type `java.util.LinkedHashMap<java.lang.String,java.lang.Object>` from Boolean value (token `JsonToken.VALUE_{}`)",if *b{"TRUE"}else{"FALSE"}),
                  _=>format!("Cannot construct instance of `java.util.LinkedHashMap` (although at least one Creator exists): no String-argument constructor/factory method to deserialize from String value ('{}')",text(&value))
                }));
                }
                if h.template().is_some() {
                    set(s(0), value, rc)?;
                    Value::Null
                } else {
                    value
                }
            }
            "toJson" => {
                if get(0).is_null() {
                    Value::Null
                } else {
                    let mut v = get(0);
                    strip_nulls(&mut v);
                    json!(pretty(&v))
                }
            }
            "xPath" | "soapXPath" => {
                xml::xpath(&s(0), &s(1), self.0 == "soapXPath").map_err(error)?
            }
            "formatXml" => xml::format(
                &if h.template().is_some() {
                    block(rc)?
                } else {
                    s(0)
                },
                hash("format") == "compact",
            )
            .map_err(error)?,
            "formatJson" => {
                let input = if h.template().is_some() {
                    block(rc)?
                } else {
                    s(0)
                };
                let value: Value = wire_json::parse(&input).map_err(error)?;
                json!(if hash("format") == "compact" {
                    value.to_string()
                } else {
                    pretty(&value)
                })
            }
            "jsonPath" => {
                let value = if get(0).is_string() {
                    serde_json::from_str(&s(0)).unwrap_or(Value::Null)
                } else {
                    get(0)
                };
                if value.is_null() && !get(0).is_null() {
                    return Ok(json!(format!("[ERROR: {} is not valid JSON]", s(0))));
                }
                let found = crate::matcher::json_path(&value, &s(1)).unwrap_or(Value::Null);
                if found.is_null() {
                    hash("default")
                } else {
                    found
                }
            }
            "jsonMerge" | "jsonRemove" | "jsonArrayAdd" => {
                let hashes = h
                    .hash()
                    .iter()
                    .map(|(k, v)| (k.to_string(), v.value().clone()))
                    .collect();
                wire_json::mutate(
                    self.0,
                    &p,
                    &hashes,
                    if h.template().is_some() {
                        Some(block(rc)?)
                    } else {
                        None
                    },
                )
            }
            "eq" => json!(get(0) == get(1)),
            "neq" => json!(get(0) != get(1)),
            "gt" | "gte" | "lt" | "lte" => {
                if get(0).is_number() && get(1).is_string() {
                    return Err(error(
                        "java.lang.ClassCastException: class java.lang.String cannot be cast to class java.lang.Integer (java.lang.String and java.lang.Integer are in module java.base of loader 'bootstrap')",
                    ));
                }
                let cmp = if get(0).is_number() {
                    n(0).partial_cmp(&n(1))
                } else {
                    Some(s(0).cmp(&s(1)))
                };
                json!(match self.0 {
                    "gt" => cmp == Some(std::cmp::Ordering::Greater),
                    "gte" => cmp != Some(std::cmp::Ordering::Less),
                    "lt" => cmp == Some(std::cmp::Ordering::Less),
                    _ => cmp != Some(std::cmp::Ordering::Greater),
                })
            }
            "and" => json!(p.iter().all(truth)),
            "or" => json!(p.iter().any(truth)),
            "not" => json!(!truth(&get(0))),
            "isEven" => json!(n(0) as i64 % 2 == 0),
            "isOdd" => json!(n(0) as i64 % 2 != 0),
            "stripes" => json!(if n(0) as i64 % 2 == 0 {
                if p.len() > 1 { s(1) } else { "even".into() }
            } else {
                if p.len() > 2 { s(2) } else { "odd".into() }
            }),
            "center" | "ljust" | "rjust" => {
                let input = s(0);
                if hash("size").is_null() {
                    return Err(error(
                        "java.lang.NullPointerException: found 'null', expected 'size'",
                    ));
                }
                let size = number(&hash("size"));
                if size > 1_000_000. {
                    return Err(error(
                        "Template padding exceeds native limit of 1000000 characters",
                    ));
                }
                let count = (size as usize).saturating_sub(input.len());
                let left = if self.0 == "rjust" {
                    count
                } else if self.0 == "center" {
                    count / 2
                } else {
                    0
                };
                json!(format!(
                    "{}{}{}",
                    text(&hash("pad"))
                        .chars()
                        .chain(std::iter::repeat(' '))
                        .take(0)
                        .collect::<String>()
                        + &pad(&hash("pad"), left),
                    input,
                    pad(&hash("pad"), count - left)
                ))
            }
            "stripTags" => json!(
                regex::Regex::new("<[^>]*>")
                    .unwrap()
                    .replace_all(&s(0), "")
                    .to_string()
            ),
            "slugify" => json!(
                regex::Regex::new(r"[^\p{L}0-9]+")
                    .unwrap()
                    .replace_all(&s(0).to_lowercase(), "-")
                    .trim_matches('-')
            ),
            "abbreviate" => {
                let st = s(0);
                let width = n(1) as usize;
                json!(if st.len() > width && width >= 4 {
                    format!("{}...", st.chars().take(width - 3).collect::<String>())
                } else {
                    st
                })
            }
            "wordWrap" => {
                let width = n(1) as usize;
                let mut out = String::new();
                let mut col = 0;
                for word in s(0).split_whitespace() {
                    if col > 0 {
                        if col + 1 + word.len() > width {
                            out.push('\n');
                            col = 0
                        } else {
                            out.push(' ');
                            col += 1
                        }
                    }
                    out.push_str(word);
                    col += word.len()
                }
                json!(out)
            }
            "yesno" => {
                let choices = s(1);
                let a: Vec<_> = choices.split(',').collect();
                json!(
                    a.get(if get(0).is_null() {
                        2
                    } else if truth(&get(0)) {
                        0
                    } else {
                        1
                    })
                    .copied()
                    .unwrap_or("")
                )
            }
            "numberFormat" => json!(number_format(
                n(0),
                &s(1),
                &s(2),
                &h.hash()
                    .iter()
                    .map(|(k, v)| (k.to_string(), v.value().clone()))
                    .collect()
            )),
            "stringFormat" => {
                let mut st = s(0);
                for v in p.iter().skip(1) {
                    if let Some(i) = st.find("%s") {
                        st.replace_range(i..i + 2, &text(v))
                    }
                }
                json!(st)
            }
            "randomInt" => {
                let low = number(&hash("lower")) as i64;
                let upper = hash("upper").as_i64().unwrap_or(i32::MAX as i64);
                json!(if upper > low {
                    rand::random_range(low..upper)
                } else {
                    low
                })
            }
            "randomDecimal" => {
                let low = number(&hash("lower"));
                let upper = hash("upper").as_f64().unwrap_or(1.);
                json!(if upper > low {
                    rand::random_range(low..upper)
                } else {
                    low
                })
            }
            "pickRandom" => {
                let a = if p.len() == 1 && get(0).is_array() {
                    get(0).as_array().unwrap().clone()
                } else {
                    p.clone()
                };
                if let Some(count) = hash("count").as_u64() {
                    return Ok(json!(
                        a.into_iter().take(count as usize).collect::<Vec<_>>()
                    ));
                }
                if a.is_empty() {
                    Value::Null
                } else {
                    a[rand::random_range(0..a.len())].clone()
                }
            }
            "randomValue" => {
                let len = hash("length").as_u64().unwrap_or(16).min(100000) as usize;
                let kind = text(&hash("type"));
                let alphabet = match kind.as_str() {
                    "NUMERIC" => b"0123456789".as_slice(),
                    "ALPHABETIC" => {
                        b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ".as_slice()
                    }
                    "HEXADECIMAL" => b"0123456789abcdef".as_slice(),
                    _ => {
                        b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789".as_slice()
                    }
                };
                if kind == "UUID" {
                    json!(uuid::Uuid::new_v4().to_string())
                } else {
                    json!(
                        (0..len)
                            .map(|_| alphabet[rand::random_range(0..alphabet.len())] as char)
                            .collect::<String>()
                    )
                }
            }
            "parseDate" | "date" | "now" | "dateFormat" | "truncateDate" => {
                let hashes = h
                    .hash()
                    .iter()
                    .map(|(k, v)| (k.to_string(), v.value().clone()))
                    .collect();
                dates::eval(self.0, &p, &hashes).map_err(error)?
            }
            "systemValue" => {
                let key = text(&hash("key"));
                if key.is_empty() {
                    return Ok(json!("[ERROR: The key cannot be empty]"));
                }
                if !key.starts_with("wiremock.") && !key.starts_with("WIREMOCK_") {
                    return Ok(json!(format!("[ERROR: Access to {key} is denied]")));
                }
                std::env::var(key)
                    .map(Value::String)
                    .unwrap_or_else(|_| hash("default"))
            }
            "hostname" => json!(
                std::process::Command::new("hostname")
                    .output()
                    .ok()
                    .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
                    .unwrap_or_default()
            ),
            "split" => json!(s(0).split(&s(1)).collect::<Vec<_>>()),
            "reverse" => json!(s(0).chars().rev().collect::<String>()),
            "default" | "defaultIfEmpty" => {
                if truth(&get(0)) {
                    get(0)
                } else {
                    get(1)
                }
            }
            _ => Value::Null,
        })
    }
}
fn capitalize(s: &str) -> String {
    let mut c = s.chars();
    c.next()
        .map(|f| f.to_uppercase().collect::<String>() + c.as_str())
        .unwrap_or_default()
}
fn merge(a: &mut Value, b: Value, remove: bool) {
    if let (Some(am), Some(bm)) = (a.as_object_mut(), b.as_object()) {
        for (k, v) in bm {
            if remove && v.is_null() {
                am.remove(k);
            } else if let Some(old) = am.get_mut(k) {
                merge(old, v.clone(), remove)
            } else {
                am.insert(k.clone(), v.clone());
            }
        }
    } else {
        *a = b
    }
}
fn strip_nulls(v: &mut Value) {
    match v {
        Value::Object(m) => {
            m.retain(|_, v| !v.is_null());
            for v in m.values_mut() {
                strip_nulls(v)
            }
        }
        Value::Array(a) => {
            for v in a {
                strip_nulls(v)
            }
        }
        _ => {}
    }
}
fn java_display(v: &Value) -> String {
    match v {
        Value::Object(m) => format!(
            "{{{}}}",
            m.iter()
                .map(|(k, v)| format!("{k}={}", java_display(v)))
                .collect::<Vec<_>>()
                .join(", ")
        ),
        Value::Array(a) => format!(
            "[{}]",
            a.iter().map(java_display).collect::<Vec<_>>().join(", ")
        ),
        _ => text(v),
    }
}
fn remove_path(v: &mut Value, path: &str) {
    if let Some(key) = path.strip_prefix("$..") {
        if let Some(m) = v.as_object_mut() {
            m.remove(key);
            for child in m.values_mut() {
                remove_path(child, path)
            }
        } else if let Some(a) = v.as_array_mut() {
            for child in a {
                remove_path(child, path)
            }
        }
        return;
    }
    let parts: Vec<String> = path
        .trim_start_matches('$')
        .trim_start_matches('.')
        .replace('[', ".")
        .replace(']', "")
        .split('.')
        .filter(|p| !p.is_empty())
        .map(|p| p.trim_matches('\'').to_string())
        .collect();
    fn walk(v: &mut Value, parts: &[String]) {
        if parts.is_empty() {
            return;
        }
        let key = &parts[0];
        if parts.len() == 1 {
            if let Some(m) = v.as_object_mut() {
                m.remove(key);
            } else if let Some(a) = v.as_array_mut() {
                if let Ok(i) = key.parse::<usize>() {
                    if i < a.len() {
                        a.remove(i);
                    }
                }
            }
            return;
        }
        if key == "*" {
            if let Some(a) = v.as_array_mut() {
                for child in a {
                    walk(child, &parts[1..])
                }
            } else if let Some(m) = v.as_object_mut() {
                for child in m.values_mut() {
                    walk(child, &parts[1..])
                }
            }
        } else if let Some(child) = if let Ok(i) = key.parse::<usize>() {
            v.get_mut(i)
        } else {
            v.get_mut(key)
        } {
            walk(child, &parts[1..])
        }
    }
    walk(v, &parts);
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn basic() {
        let r = Request {
            method: "POST".into(),
            url: "/x?q=one&q=two".into(),
            ..Default::default()
        };
        assert_eq!(
            render(
                "{{request.method}} {{request.query.q}} {{request.query.q.[1]}} {{math 2 '+' 3}}",
                &r,
                &json!({})
            )
            .unwrap(),
            "POST one two 5"
        );
        assert_eq!(
            render("{{#assign 'x'}}hello{{/assign}}{{x}}", &r, &json!({})).unwrap(),
            "hello"
        );
    }
}

#[cfg(test)]
mod regression_tests {
    use super::*;
    #[test]
    fn quoted_helper_text_and_malformed_unicode_are_data() {
        let req = Request::default();
        assert_eq!(
            render("{{val '(unknown 1)'}} (alsoUnknown 1)", &req, &json!({})).unwrap(),
            "(unknown 1) (alsoUnknown 1)"
        );
        assert!(
            render(
                "{{parseJson request.body}}",
                &Request {
                    body: r#"{"x":"\uabc😀"}"#.as_bytes().to_vec(),
                    ..Default::default()
                },
                &json!({})
            )
            .is_err()
        );
    }
    #[test]
    fn assignment_and_compilation_cache_are_request_local() {
        let mut threads = Vec::new();
        for i in 0..16 {
            threads.push(std::thread::spawn(move || {
                let req = Request {
                    url: format!("/x?q={i}"),
                    ..Default::default()
                };
                for _ in 0..20 {
                    assert_eq!(
                        render(
                            "{{#assign 'x'}}{{request.query.q}}{{/assign}}{{x}}",
                            &req,
                            &json!({})
                        )
                        .unwrap(),
                        i.to_string()
                    );
                }
            }));
        }
        for thread in threads {
            thread.join().unwrap();
        }
    }
    #[test]
    fn optimized_and_general_renderers_agree() {
        for query in ["Ada", "%3C%26%22", "one&q=two", "%F0%9F%98%80"] {
            let req = Request {
                url: format!("/bench?q={query}"),
                ..Default::default()
            };
            let source = "Hello {{request.query.q}}: {{math 2 '+' 3}}";
            assert_eq!(
                render(source, &req, &json!({})).unwrap(),
                render_with_model(source, &model(&req, &json!({}))).unwrap()
            );
        }
    }
    #[test]
    fn json_mutation_uses_selected_paths_and_bounds_array() {
        let req = Request::default();
        assert_eq!(
            render(
                "{{jsonRemove parameters.input '$[?(@.id == 2)].secret'}}",
                &req,
                &json!({"input":"[{\"id\":1,\"secret\":true},{\"id\":2,\"secret\":true}]"})
            )
            .unwrap(),
            "[{\"id\":1,\"secret\":true},{\"id\":2}]"
        );
        assert_eq!(
            render(
                "{{jsonArrayAdd '[1,2]' '[3,4]' flatten=true maxItems=2}}",
                &req,
                &json!({})
            )
            .unwrap(),
            "[3,4]"
        );
    }
}

fn validate_helpers(source: &str) -> Result<(), String> {
    static REGEX: OnceLock<regex::Regex> = OnceLock::new();
    let optimized = source.find("{{").map(|i| &source[i..]).unwrap_or(source);
    let masked = mask_helper_literals(optimized);
    for cap in REGEX
        .get_or_init(|| regex::Regex::new(r"(\{\{#?|\()([A-Za-z][A-Za-z0-9_]*)\s+").unwrap())
        .captures_iter(&masked)
    {
        let name = &cap[2];
        if HELPERS
            .split_whitespace()
            .chain(["if", "unless", "each", "with", "lookup"])
            .any(|n| n == name)
        {
            continue;
        }
        let token = cap.get(0).unwrap();
        let prefix = &cap[1];
        if prefix == "{{#" {
            return Err(format!(
                "java.lang.IllegalArgumentException: could not find helper: '{name}'"
            ));
        }
        let pos = token.start() + prefix.len();
        let before = &optimized[..pos];
        let line = before.bytes().filter(|b| *b == b'\n').count() + 1;
        let col = before.rfind('\n').map(|i| pos - i - 1).unwrap_or(pos);
        let source_line = optimized.lines().nth(line - 1).unwrap_or(optimized);
        return Err(format!(
            "{line}:{col}: could not find helper: '{name}'\n{source_line}\n{}^\n",
            " ".repeat(col)
        ));
    }
    Ok(())
}

fn pad(value: &Value, count: usize) -> String {
    let p = value.as_str().filter(|s| !s.is_empty()).unwrap_or(" ");
    p.chars().cycle().take(count).collect()
}
fn number_format(mut n: f64, pattern: &str, locale: &str, hash: &Map<String, Value>) -> String {
    let default_locale = crate::matcher::default_locale();
    let locale = if locale.is_empty() {
        default_locale.as_str()
    } else {
        locale
    };
    let decimal = if locale.starts_with("en_US") || locale.starts_with("en_GB") {
        '.'
    } else {
        ','
    };
    let group = if decimal == '.' { ',' } else { '.' };
    let percent = pattern == "percent";
    if percent {
        n *= 100.
    }
    let mut digits = if pattern == "integer" || percent {
        0
    } else if let Some((_, tail)) = pattern.split_once('.') {
        tail.len()
    } else {
        3
    };
    if let Some(d) = hash.get("maximumFractionDigits").and_then(Value::as_u64) {
        digits = d as usize
    }
    digits = digits.min(20);
    let mut formatted = format!("{n:.digits$}");
    if !pattern.contains('0') {
        while formatted.contains('.') && formatted.ends_with('0') {
            formatted.pop();
        }
        if formatted.ends_with('.') {
            formatted.pop();
        }
    }
    let (whole, fraction) = formatted.split_once('.').unwrap_or((&formatted, ""));
    let grouping = hash.get("groupingUsed") != Some(&json!(false));
    let mut out = String::new();
    for (i, c) in whole.chars().enumerate() {
        if grouping && i > 0 && (whole.len() - i) % 3 == 0 && c != '-' {
            out.push(group)
        }
        out.push(c)
    }
    if !fraction.is_empty() {
        out.push(decimal);
        out.push_str(fraction)
    }
    if percent {
        out.push('%')
    }
    out
}

fn java_quote(s: &str) -> String {
    let mut out = String::from("\"");
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            c if c < ' ' => out.push_str(&format!("\\u{:04X}", c as u32)),
            _ => out.push(c),
        }
    }
    out.push('"');
    out
}

#[derive(Clone)]
enum FastPart {
    Literal(String),
    Request(String),
}
fn fast_render(source: &str, req: &Request) -> Option<String> {
    static CACHE: OnceLock<
        std::sync::Mutex<std::collections::HashMap<String, Option<Vec<FastPart>>>>,
    > = OnceLock::new();
    let cache = CACHE.get_or_init(|| std::sync::Mutex::new(std::collections::HashMap::new()));
    let plan = {
        let mut guard = cache.lock().ok()?;
        if !guard.contains_key(source) {
            let plan = compile_fast(source);
            if guard.len() > 4096 {
                guard.clear()
            }
            guard.insert(source.into(), plan);
        }
        guard.get(source)?.clone()?
    };
    let mut out = String::with_capacity(source.len());
    for part in plan {
        match part {
            FastPart::Literal(s) => out.push_str(&s),
            FastPart::Request(path) => match path.as_str() {
                "method" => out.push_str(&req.method),
                "url" => out.push_str(&req.url),
                "path" => out.push_str(req.path()),
                "body" => out.push_str(&req.body_text()),
                "absoluteUrl" => {}
                _ => {
                    if let Some(key) = path.strip_prefix("query.") {
                        if let Some((_, q)) = req.url.split_once('?') {
                            if let Some((_, v)) =
                                url::form_urlencoded::parse(q.as_bytes()).find(|(k, _)| k == key)
                            {
                                out.push_str(&v)
                            }
                        }
                    } else if let Some(key) = path.strip_prefix("headers.") {
                        out.push_str(req.header(key).unwrap_or(""))
                    }
                }
            },
        }
    }
    Some(out)
}
fn compile_fast(source: &str) -> Option<Vec<FastPart>> {
    let mut rest = source;
    let mut parts = Vec::new();
    while let Some(start) = rest.find("{{") {
        parts.push(FastPart::Literal(rest[..start].into()));
        rest = &rest[start + 2..];
        let triple = rest.starts_with('{');
        if triple {
            rest = &rest[1..]
        }
        let end = rest.find(if triple { "}}}" } else { "}}" })?;
        let expression = rest[..end].trim();
        if let Some(path) = expression.strip_prefix("request.") {
            let known = matches!(path, "method" | "url" | "path" | "body" | "absoluteUrl")
                || (path.starts_with("query.") || path.starts_with("headers."))
                    && path.split('.').count() == 2
                    && path
                        .chars()
                        .all(|c| c.is_alphanumeric() || "._-".contains(c));
            if !known {
                return None;
            }
            parts.push(FastPart::Request(path.into()));
        } else if expression.starts_with("math ") {
            static RE: OnceLock<regex::Regex> = OnceLock::new();
            if !RE.get_or_init(||regex::Regex::new(r#"^math\s+-?[0-9]+(?:\.[0-9]+)?\s+['"][+*/%\-]['"]\s+-?[0-9]+(?:\.[0-9]+)?$"#).unwrap()).is_match(expression){return None}
            let value = render_with_model(&format!("{{{{{expression}}}}}"), &json!({})).ok()?;
            parts.push(FastPart::Literal(value));
        } else {
            return None;
        }
        rest = &rest[end + if triple { 3 } else { 2 }..];
    }
    parts.push(FastPart::Literal(rest.into()));
    Some(parts)
}

fn preserve_literal_backslashes(s: &str) -> String {
    let mut out = String::new();
    let mut quote = None;
    let mut braces = false;
    let chars: Vec<char> = s.chars().collect();
    let mut i = 0;
    while i < chars.len() {
        let c = chars[i];
        if !braces && c == '{' && chars.get(i + 1) == Some(&'{') {
            braces = true
        }
        if braces {
            if let Some(q) = quote {
                if c == '\\' {
                    if chars.get(i + 1) == Some(&q) {
                        out.push(c);
                        out.push(q);
                        i += 2;
                        continue;
                    }
                    out.push('\\');
                } else if c == q {
                    quote = None
                }
            } else if c == '\'' || c == '"' {
                quote = Some(c)
            } else if c == '}' && chars.get(i + 1) == Some(&'}') {
                braces = false
            }
        }
        out.push(c);
        i += 1;
    }
    out
}

fn multipart_model(req: &Request) -> Option<Value> {
    let content_type = req.header("Content-Type")?;
    if !content_type.starts_with("multipart/") {
        return None;
    }
    let (_, boundary) = content_type.split_once("boundary=")?;
    let boundary = boundary.trim_matches('"');
    let mut parts = Map::new();
    for part in req.body_text().split(&format!("--{boundary}")) {
        let Some((head, body)) = part.trim_start_matches("\r\n").split_once("\r\n\r\n") else {
            continue;
        };
        let mut headers = Map::new();
        let mut name = None;
        for line in head.split("\r\n") {
            if let Some((key, value)) = line.split_once(':') {
                headers.insert(key.to_lowercase(), json!(value.trim()));
                if key.eq_ignore_ascii_case("Content-Disposition") {
                    for attr in value.split(';') {
                        if let Some((k, v)) = attr.trim().split_once('=') {
                            if k == "name" {
                                name = Some(v.trim_matches('"').to_string())
                            }
                        }
                    }
                }
            }
        }
        if let Some(name) = name {
            let body = body.strip_suffix("\r\n").unwrap_or(body);
            parts.insert(name.clone(),json!({"name":name,"body":body,"bodyAsBase64":base64::engine::general_purpose::STANDARD.encode(body),"binary":false,"headers":headers}));
        }
    }
    Some(json!(parts))
}

fn mask_helper_literals(s: &str) -> String {
    let bytes = s.as_bytes();
    let mut out = bytes.to_vec();
    let mut inside = false;
    let mut quote = None;
    let mut i = 0;
    while i < bytes.len() {
        if !inside {
            if bytes[i..].starts_with(b"{{") {
                inside = true;
                i += 2;
                continue;
            }
            if bytes[i] != b'\n' {
                out[i] = b' ';
            }
            i += 1;
            continue;
        }
        if let Some(q) = quote {
            if bytes[i] != b'\n' {
                out[i] = b' ';
            }
            if bytes[i] == b'\\' && bytes.get(i + 1) == Some(&q) {
                out[i + 1] = b' ';
                i += 2;
                continue;
            }
            if bytes[i] == q {
                quote = None
            }
            i += 1;
            continue;
        }
        if bytes[i] == b'\'' || bytes[i] == b'"' {
            quote = Some(bytes[i]);
            out[i] = b' ';
        } else if bytes[i..].starts_with(b"}}") {
            inside = false;
            i += 2;
            continue;
        }
        i += 1;
    }
    String::from_utf8(out).unwrap_or_default()
}
