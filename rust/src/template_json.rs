use jsonpath_rust::JsonPath;
use serde_json::{Map, Value, json};
fn fail(s: impl AsRef<str>) -> Value {
    json!(format!("[ERROR: {}]", s.as_ref()))
}
pub fn mutate(name: &str, p: &[Value], hash: &Map<String, Value>, block: Option<String>) -> Value {
    let input = p.first().unwrap_or(&Value::Null);
    let second = block
        .map(Value::String)
        .unwrap_or_else(|| p.get(1).cloned().unwrap_or(Value::Null));
    let (base_label, base_invalid) = match name {
        "jsonMerge" => ("Base JSON parameter must be a string", "Base JSON"),
        "jsonRemove" => ("Input JSON must be a string", "Input JSON string"),
        _ => ("Base JSON must be a string", "Base JSON"),
    };
    let Some(input) = input.as_str() else {
        return fail(base_label);
    };
    let mut root: Value = match serde_json::from_str(input) {
        Ok(v) => v,
        Err(_) => return fail(format!("{base_invalid} is not valid JSON ('{input}')")),
    };
    if name == "jsonMerge" {
        if !root.is_object() {
            return fail(format!("Base JSON is not a JSON object ('{input}')"));
        }
        let Some(second) = second.as_str() else {
            return fail("JSON to merge must be a string");
        };
        let other: Value = match serde_json::from_str(second) {
            Ok(v) => v,
            Err(_) => return fail(format!("JSON to merge is not valid JSON ('{second}')")),
        };
        if !other.is_object() {
            return fail(format!("JSON to merge is not a JSON object ('{second}')"));
        }
        super::merge(
            &mut root,
            other,
            hash.get("removeNulls") == Some(&json!(true)),
        );
        return root;
    }
    if name == "jsonRemove" {
        if p.len() != 2 {
            return fail("A single JSONPath expression parameter must be supplied");
        }
        let Some(path) = second.as_str() else {
            return fail("JSONPath parameter must be a string");
        };
        if path.is_empty() {
            return fail("JSONPath parameter is not a valid JSONPath expression ('')");
        }
        if path == "$" {
            return fail("Delete operation cannot be applied to JSONPath expression ('$')");
        }
        if let Ok(paths) = root.query_only_path(&path.replace(".[", "[")) {
            for path in paths.into_iter().rev() {
                super::remove_path(&mut root, &path);
            }
        }
        return root;
    }
    let path = match hash.get("jsonPath") {
        Some(Value::String(p)) => {
            if p.is_empty() || root.query_only_path(p).is_err() {
                return fail(format!(
                    "jsonPath option is not valid JSONPath expression ('{p}')"
                ));
            }
            Some(p.as_str())
        }
        Some(_) => return fail("jsonPath option must be a string"),
        None => None,
    };
    let pointer = path
        .and_then(|p| root.query_only_path(p).ok())
        .and_then(|p| p.into_iter().next())
        .map(|p| json_pointer(&p));
    let target = if path.is_some() {
        pointer.as_ref().and_then(|p| root.pointer_mut(p))
    } else {
        Some(&mut root)
    };
    let Some(Value::Array(array)) = target else {
        return fail(if let Some(path) = path {
            format!("Target JSON is not a JSON array (root: '{input}', jsonPath: '{path}')")
        } else {
            format!("Target JSON is not a JSON array ('{input}')")
        });
    };
    let Some(second) = second.as_str() else {
        return fail("Item-to-add JSON must be a string");
    };
    let item: Value = match serde_json::from_str(second) {
        Ok(v) => v,
        Err(_) => return fail(format!("Item-to-add JSON is not valid JSON ('{second}')")),
    };
    let flatten = match hash.get("flatten") {
        Some(Value::Bool(v)) => *v,
        Some(_) => return fail("flatten option must be a boolean"),
        None => false,
    };
    let max = match hash.get("maxItems") {
        Some(v) => match v.as_i64() {
            Some(v) if v >= 0 => Some(v as usize),
            Some(_) => return fail("maxItems option integer must be positive"),
            _ => return fail("maxItems option must be an integer"),
        },
        None => None,
    };
    if flatten && item.is_array() {
        array.extend(item.as_array().unwrap().clone())
    } else {
        array.push(item)
    }
    if let Some(max) = max {
        let drop = array.len().saturating_sub(max);
        array.drain(..drop);
    }
    root
}
fn json_pointer(path: &str) -> String {
    let re = regex::Regex::new(r"\['([^']*)'\]|\[(\d+)\]|\.([A-Za-z0-9_-]+)").unwrap();
    re.captures_iter(path)
        .map(|c| {
            format!(
                "/{}",
                c.get(1)
                    .or_else(|| c.get(2))
                    .or_else(|| c.get(3))
                    .unwrap()
                    .as_str()
                    .replace('~', "~0")
                    .replace('/', "~1")
            )
        })
        .collect()
}
pub fn parse(input: &str) -> Result<Value, String> {
    let normalized = replace_surrogates(input);
    let mut value: Value = serde_json::from_str(&normalized).map_err(|e| e.to_string())?;
    normalize(&mut value);
    Ok(value)
}
fn normalize(v: &mut Value) {
    match v {
        Value::Object(m) => {
            for v in m.values_mut() {
                normalize(v)
            }
        }
        Value::Array(a) => {
            for v in a {
                normalize(v)
            }
        }
        Value::Number(n) => {
            let s = n.to_string();
            if s.contains('e') || s.contains('E') {
                if let Some(f) = n.as_f64() {
                    *v = if f.fract() == 0. {
                        json!(f as i64)
                    } else {
                        json!(f)
                    }
                }
            }
        }
        _ => {}
    }
}
fn replace_surrogates(s: &str) -> String {
    let bytes = s.as_bytes();
    let mut result = String::new();
    let mut i = 0;
    while i < bytes.len() {
        if i + 6 <= bytes.len() && &bytes[i..i + 2] == b"\\u" {
            if let Some(code) = std::str::from_utf8(&bytes[i + 2..i + 6])
                .ok()
                .and_then(|s| u16::from_str_radix(s, 16).ok())
            {
                if (0xD800..=0xDBFF).contains(&code) {
                    let paired = i + 12 <= bytes.len()
                        && &bytes[i + 6..i + 8] == b"\\u"
                        && std::str::from_utf8(&bytes[i + 8..i + 12])
                            .ok()
                            .and_then(|s| u16::from_str_radix(s, 16).ok())
                            .is_some_and(|n| (0xDC00..=0xDFFF).contains(&n));
                    if paired {
                        result.push_str(&s[i..i + 12]);
                        i += 12;
                        continue;
                    }
                    result.push('?');
                    i += 6;
                    continue;
                } else if (0xDC00..=0xDFFF).contains(&code) {
                    result.push('?');
                    i += 6;
                    continue;
                }
            }
        }
        let c = s[i..].chars().next().unwrap();
        result.push(c);
        i += c.len_utf8();
    }
    result
}
