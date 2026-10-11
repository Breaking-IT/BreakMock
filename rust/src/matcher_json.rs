use super::*;
use jsonpath_rust::JsonPath;
pub fn json_path(doc: &Value, path: &str) -> Result<Value, String> {
    for name in [
        "length", "size", "min", "max", "sum", "avg", "stddev", "first", "last",
    ] {
        if let Some(base) = path.strip_suffix(&format!(".{name}()")) {
            let v = json_path(doc, base)?;
            return match name {
                "length" | "size" => Ok(json!(
                    v.as_array()
                        .map(Vec::len)
                        .or_else(|| v.as_object().map(|m| m.len()))
                        .or_else(|| v.as_str().map(str::len))
                        .unwrap_or(0)
                )),
                "first" => Ok(v
                    .as_array()
                    .and_then(|a| a.first())
                    .cloned()
                    .unwrap_or(Value::Null)),
                "last" => Ok(v
                    .as_array()
                    .and_then(|a| a.last())
                    .cloned()
                    .unwrap_or(Value::Null)),
                _ => {
                    let nums: Vec<f64> = v
                        .as_array()
                        .ok_or("not array")?
                        .iter()
                        .filter_map(Value::as_f64)
                        .collect();
                    if nums.is_empty() {
                        return Err("empty numbers".into());
                    }
                    let avg = nums.iter().sum::<f64>() / nums.len() as f64;
                    Ok(json!(match name {
                        "min" => nums.iter().copied().fold(f64::INFINITY, f64::min),
                        "max" => nums.iter().copied().fold(f64::NEG_INFINITY, f64::max),
                        "sum" => nums.iter().sum(),
                        "avg" => avg,
                        _ => (nums.iter().map(|n| (n - avg).powi(2)).sum::<f64>()
                            / nums.len() as f64)
                            .sqrt(),
                    }))
                }
            };
        }
    }
    if let Some(start) = path.find("[?(") {
        let mut depth = 1;
        let mut quote = '\0';
        let mut escape = false;
        let mut end = None;
        for (i, c) in path[start + 3..].char_indices() {
            if escape {
                escape = false;
                continue;
            }
            if c == '\\' {
                escape = true;
                continue;
            }
            if quote != '\0' {
                if c == quote {
                    quote = '\0'
                }
                continue;
            }
            if c == '\'' || c == '"' || c == '/' {
                quote = c;
                continue;
            }
            if c == '(' {
                depth += 1
            }
            if c == ')' {
                depth -= 1;
                if depth == 0 {
                    end = Some(start + 3 + i);
                    break;
                }
            }
        }
        let end = end.ok_or("invalid filter")?;
        if path.as_bytes().get(end + 1) != Some(&b']') {
            return Err("invalid filter".into());
        }
        let prefix = &path[..start];
        let candidates = doc.query(prefix).map_err(|e| e.to_string())?;
        let tail = &path[end + 2..];
        let mut out = vec![];
        for candidate in candidates {
            let items = if let Some(a) = candidate.as_array() {
                a.iter().collect::<Vec<_>>()
            } else {
                vec![candidate]
            };
            for item in items {
                if predicate(&path[start + 3..end], item, doc) {
                    if tail.is_empty() {
                        out.push(item.clone())
                    } else {
                        let v = json_path(item, &format!("${tail}"))?;
                        if indefinite(&format!("${tail}")) {
                            if let Some(a) = v.as_array() {
                                out.extend(a.clone())
                            }
                        } else if !v.is_null() {
                            out.push(v)
                        }
                    }
                }
            }
        }
        return Ok(Value::Array(out));
    }
    let result = doc.query(path).map_err(|e| e.to_string())?;
    if indefinite(path) {
        Ok(Value::Array(result.into_iter().cloned().collect()))
    } else {
        Ok(result.first().map(|v| (*v).clone()).unwrap_or(Value::Null))
    }
}
fn indefinite(s: &str) -> bool {
    s.contains('*') || s.contains("..") || s.contains("[?") || s.contains(',') || s.contains(':')
}
fn split_op<'a>(s: &'a str, op: &str) -> Option<(&'a str, &'a str)> {
    let mut level = 0;
    let mut quote = '\0';
    let mut escape = false;
    for (i, c) in s.char_indices() {
        if escape {
            escape = false;
            continue;
        }
        if c == '\\' {
            escape = true;
            continue;
        }
        if quote != '\0' {
            if c == quote {
                quote = '\0'
            }
            continue;
        }
        if c == '\'' || c == '"' || c == '/' {
            quote = c;
            continue;
        }
        if level == 0 && s[i..].starts_with(op) {
            return Some((s[..i].trim(), s[i + op.len()..].trim()));
        }
        if c == '(' || c == '[' {
            level += 1
        }
        if c == ')' || c == ']' {
            level -= 1
        }
    }
    None
}
fn operand(s: &str, item: &Value, root: &Value) -> Option<Value> {
    let s = s.trim();
    if s.starts_with('@') || s.starts_with('$') {
        let base = if s.starts_with('@') { item } else { root };
        json_path(base, &format!("${}", &s[1..])).ok().filter(|v| {
            !v.is_null()
                || base
                    .query(&format!("${}", &s[1..]))
                    .ok()
                    .map(|a| !a.is_empty())
                    .unwrap_or(false)
        })
    } else if s.starts_with('\'') && s.ends_with('\'') {
        Some(Value::String(s[1..s.len() - 1].into()))
    } else {
        serde_json::from_str(s).ok()
    }
}
fn eq(a: &Value, b: &Value) -> bool {
    if a == b {
        return true;
    }
    let num = |v: &Value| {
        v.as_f64()
            .or_else(|| v.as_str().and_then(|s| s.parse::<f64>().ok()))
    };
    num(a).zip(num(b)).map(|(a, b)| a == b).unwrap_or(false)
}
fn predicate(s: &str, item: &Value, root: &Value) -> bool {
    let s = s.trim();
    for op in ["||", "&&"] {
        if let Some((a, b)) = split_op(s, op) {
            return if op == "||" {
                predicate(a, item, root) || predicate(b, item, root)
            } else {
                predicate(a, item, root) && predicate(b, item, root)
            };
        }
    }
    if s.starts_with('(') && s.ends_with(')') {
        return predicate(&s[1..s.len() - 1], item, root);
    }
    if let Some(s) = s.strip_prefix('!') {
        return !predicate(s, item, root);
    }
    for op in [
        "==",
        "!=",
        "<=",
        ">=",
        "=~",
        "<",
        ">",
        " nin ",
        " in ",
        " subsetof ",
        " anyof ",
        " noneof ",
        " empty ",
    ] {
        if let Some((left, right)) = split_op(s, op) {
            let a = operand(left, item, root);
            if op == "=~" {
                let Some(a) = a else { return false };
                if !right.starts_with('/') {
                    return false;
                }
                let Some(end) = right.rfind('/') else {
                    return false;
                };
                let re = format!(
                    "{}{}",
                    if right[end + 1..].contains('i') {
                        "(?i)"
                    } else {
                        ""
                    },
                    &right[1..end]
                );
                return regex_match(&re, &text(&a));
            }
            let b = operand(right, item, root);
            if op == "!=" {
                return match (a, b) {
                    (Some(a), Some(b)) => !eq(&a, &b),
                    (None, _) => true,
                    _ => false,
                };
            }
            let (Some(a), Some(b)) = (a, b) else {
                return false;
            };
            return match op {
                "==" => eq(&a, &b),
                "<" | ">" | "<=" | ">=" => {
                    let order = if let (Some(a), Some(b)) = (a.as_f64(), b.as_f64()) {
                        a.partial_cmp(&b)
                    } else {
                        a.as_str().zip(b.as_str()).map(|(a, b)| a.cmp(b))
                    };
                    order
                        .map(|c| match op {
                            "<" => c.is_lt(),
                            ">" => c.is_gt(),
                            "<=" => !c.is_gt(),
                            _ => !c.is_lt(),
                        })
                        .unwrap_or(false)
                }
                " in " | " nin " => {
                    let found = b
                        .as_array()
                        .map(|bs| bs.iter().any(|v| eq(&a, v)))
                        .unwrap_or(false);
                    if op == " nin " { !found } else { found }
                }
                " subsetof " | " anyof " | " noneof " => {
                    let Some(a) = a.as_array() else { return false };
                    let Some(b) = b.as_array() else { return false };
                    let test = |v: &Value| b.iter().any(|w| eq(v, w));
                    match op {
                        " subsetof " => a.iter().all(test),
                        " anyof " => a.iter().any(test),
                        _ => !a.iter().any(test),
                    }
                }
                " empty " => {
                    let empty = a
                        .as_array()
                        .map(Vec::is_empty)
                        .or_else(|| a.as_str().map(str::is_empty))
                        .unwrap_or(false);
                    b.as_bool() == Some(empty)
                }
                _ => false,
            };
        }
    }
    operand(s, item, root).is_some()
}
pub fn json_path_match(p: &Value, a: &str) -> bool {
    let Ok(doc) = serde_json::from_str(a) else {
        return false;
    };
    let expression = p
        .as_str()
        .or_else(|| p["expression"].as_str())
        .unwrap_or("");
    let Ok(result) = json_path(&doc, expression) else {
        return false;
    };
    if p.is_string() {
        return !result.is_null()
            && (!result.is_array()
                || !result.as_array().unwrap().is_empty()
                || !indefinite(expression));
    }
    if result.is_null() {
        return value_present(p, "", false);
    }
    if let Some(values) = result.as_array() {
        if p.get("equalToJson").is_some() {
            return value_matches(p, &result.to_string());
        }
        if values.is_empty() {
            return value_present(p, "", false);
        }
        return values.iter().any(|v| value_matches(p, &text(v)));
    }
    value_matches(p, &text(&result))
}
