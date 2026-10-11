use super::*;
fn boolean(b: bool) -> f64 {
    if b { 0.0 } else { 1.0 }
}
fn avg(values: impl Iterator<Item = f64>) -> f64 {
    let v: Vec<_> = values.collect();
    if v.is_empty() {
        0.0
    } else {
        v.iter().sum::<f64>() / v.len() as f64
    }
}
fn levenshtein(a: &str, b: &str) -> f64 {
    let (mut a, mut b) = (
        a.encode_utf16().collect::<Vec<_>>(),
        b.encode_utf16().collect::<Vec<_>>(),
    );
    let max = a.len().max(b.len());
    if max == 0 {
        return 0.0;
    }
    let start = a.iter().zip(&b).take_while(|(a, b)| a == b).count();
    a.drain(..start);
    b.drain(..start);
    while !a.is_empty() && !b.is_empty() && a.last() == b.last() {
        a.pop();
        b.pop();
    }
    let mut row: Vec<usize> = (0..=b.len()).collect();
    for (i, x) in a.iter().enumerate() {
        let mut diagonal = row[0];
        row[0] = i + 1;
        for (j, y) in b.iter().enumerate() {
            let old = row[j + 1];
            row[j + 1] = (row[j] + 1)
                .min(old + 1)
                .min(diagonal + usize::from(x != y));
            diagonal = old
        }
    }
    row[b.len()] as f64 / max as f64
}
pub fn value_distance(p: &Value, a: &str, present: bool) -> f64 {
    for key in [
        "equalToNumber",
        "greaterThanNumber",
        "greaterThanEqualNumber",
        "lessThanNumber",
        "lessThanEqualNumber",
    ] {
        if let Some(expected) = p.get(key) {
            if !present {
                return 1.0;
            }
            if value_present(p, a, present) {
                return 0.0;
            }
            let (Some(mut e), Some(mut g)) = (number(&text(expected)), number(a)) else {
                return 1.0;
            };
            let shift = if e < 0.0 || g < 0.0 { e.min(g) } else { 0.0 };
            e += shift;
            g += shift;
            if e == f64::INFINITY || g == f64::INFINITY {
                return 1.0;
            }
            let d = (e.ln() - g.ln()).abs() / 176.0;
            return if d.is_nan() {
                0.01
            } else {
                ((d.min(1.0) * 100.0 + 0.5).floor() / 100.0).max(0.01)
            };
        }
    }
    if let Some(e) = p["equalTo"].as_str() {
        return if present { levenshtein(e, a) } else { 1.0 };
    }
    if p.get("equalToJson").is_some() {
        return json_distance(p, a);
    }
    if p.get("equalToXml").is_some() {
        return xml::xml_distance(p, a);
    }
    if let Some(path) = p.get("matchesJsonPath") {
        if path.is_string() {
            return boolean(value_present(p, a, present));
        }
        let Ok(doc) = serde_json::from_str(a) else {
            return 1.0;
        };
        let Ok(v) = json_path(&doc, path["expression"].as_str().unwrap_or("")) else {
            return 1.0;
        };
        if v.is_null() {
            return value_distance(path, "", false);
        }
        if v.is_object() || path.get("equalToJson").is_some() {
            return value_distance(path, &v.to_string(), true);
        }
        if let Some(v) = v.as_array() {
            if v.is_empty() {
                return value_distance(path, "", false);
            }
            return v
                .iter()
                .map(|v| value_distance(path, &text(v), true))
                .fold(1.0, f64::min);
        }
        return value_distance(path, &text(&v), true);
    }
    if let Some(path) = p.get("matchesXPath") {
        return xml::xpath_distance(path, a, p);
    }
    if let Some(cs) = p["and"].as_array() {
        return avg(cs.iter().map(|c| value_distance(c, a, present)));
    }
    if let Some(cs) = p["or"].as_array() {
        return cs
            .iter()
            .map(|c| value_distance(c, a, present))
            .fold(1.0, f64::min);
    }
    if let Some(path) = p["matchesPathTemplate"].as_str() {
        if path_params(path, a).is_some() {
            return 0.0;
        }
        return levenshtein(
            &regex::Regex::new(r"\{[^}]*\}")
                .unwrap()
                .replace_all(path, ""),
            a,
        );
    }
    boolean(value_present(p, a, present))
}
fn multi_distance(p: &Value, values: &[String]) -> f64 {
    for k in ["includes", "hasExactly"] {
        if let Some(cs) = p[k].as_array() {
            if values.is_empty() {
                return 1.0;
            }
            let d = avg(cs.iter().map(|c| multi_distance(c, values)));
            return if k == "hasExactly" {
                (d + boolean(cs.len() == values.len())) / 2.0
            } else {
                d
            };
        }
    }
    if values.is_empty() {
        value_distance(p, "", false)
    } else {
        values
            .iter()
            .map(|v| value_distance(p, v, true))
            .fold(1.0, f64::min)
    }
}
fn named(p: &Value, values: impl Fn(&str) -> Vec<String>) -> f64 {
    p.as_object()
        .map(|ps| avg(ps.iter().map(|(k, p)| multi_distance(p, &values(k)))))
        .unwrap_or(0.0)
}
pub fn request_distance(p: &Value, r: &Request) -> f64 {
    let mut sum = 0.0;
    let mut exact = true;
    let mut add = |key: &str, weight: f64, d: f64| {
        sum += weight * d;
        if !matches(&json!({key:p[key]}), r) {
            exact = false
        }
    };
    if p.get("scheme").is_some() {
        add(
            "scheme",
            3.0,
            boolean(p["scheme"].as_str() == Some(&r.scheme)),
        )
    }
    if let Some(host) = p.get("host") {
        let value = if host.is_string() {
            json!({"equalTo":host})
        } else {
            host.clone()
        };
        let h = r
            .header("Host")
            .unwrap_or("")
            .split(':')
            .next()
            .unwrap_or("");
        add("host", 10.0, value_distance(&value, h, true))
    }
    if p.get("port").is_some() {
        add(
            "port",
            10.0,
            boolean(matches(&json!({"port":p["port"]}), r)),
        )
    }
    if let Some(v) = p.get("clientIp") {
        add(
            "clientIp",
            3.0,
            value_distance(v, &r.client_ip, !r.client_ip.is_empty()),
        )
    }
    for k in [
        "url",
        "urlPattern",
        "urlPath",
        "urlPathPattern",
        "urlPathTemplate",
    ] {
        if let Some(v) = p.get(k) {
            let op = match k {
                "urlPattern" | "urlPathPattern" => "matches",
                "urlPathTemplate" => "matchesPathTemplate",
                _ => "equalTo",
            };
            let a = if k.starts_with("urlPath") {
                r.path()
            } else {
                &r.url
            };
            add(k, 10.0, value_distance(&json!({op:v}), a, true));
            break;
        }
    }
    if p.get("method").is_some() {
        add("method", 3.0, boolean(method(&p["method"], &r.method)))
    }
    if !exact {
        return sum / 39.0;
    }
    if let Some(t) = p["urlPathTemplate"].as_str() {
        let params = path_params(t, r.path()).unwrap_or_default();
        sum += named(&p["pathParameters"], |k| {
            params.get(k).cloned().into_iter().collect()
        })
    }
    let mut headers = p["headers"].as_object().cloned().unwrap_or_default();
    if let Some(auth) = p.get("basicAuthCredentials") {
        headers.insert("Authorization".into(),json!({"equalTo":format!("Basic {}",STANDARD.encode(format!("{}:{}",auth["username"].as_str().unwrap_or(""),auth["password"].as_str().unwrap_or(""))))}));
    }
    sum += named(&Value::Object(headers), |k| {
        r.headers
            .iter()
            .filter(|(name, _)| name.eq_ignore_ascii_case(k))
            .map(|(_, v)| v.clone())
            .collect()
    });
    sum += named(&p["queryParameters"], |k| {
        url::form_urlencoded::parse(
            r.url
                .split_once('?')
                .map(|(_, q)| q)
                .unwrap_or("")
                .as_bytes(),
        )
        .filter(|(key, _)| key == k)
        .map(|(_, v)| v.into_owned())
        .collect()
    });
    sum += named(&p["formParameters"], |k| {
        url::form_urlencoded::parse(
            if r.header("Content-Type")
                .unwrap_or("")
                .starts_with("application/x-www-form-urlencoded")
            {
                &r.body
            } else {
                b""
            },
        )
        .filter(|(key, _)| key == k)
        .map(|(_, v)| v.into_owned())
        .collect()
    });
    sum += named(&p["cookies"], |k| {
        r.header("Cookie")
            .unwrap_or("")
            .split(';')
            .filter_map(|s| s.trim().split_once('='))
            .filter(|(key, _)| *key == k)
            .map(|(_, v)| v.into())
            .collect()
    });
    if let Some(ps) = p["bodyPatterns"].as_array() {
        sum += avg(ps.iter().map(|p| {
            value_distance(
                p,
                &r.body_text(),
                !r.body.is_empty() || p.get("binaryEqualTo").is_some(),
            )
        }))
    }
    if let Some(ps) = p["multipartPatterns"].as_array() {
        sum += boolean(multipart(ps, r))
    }
    sum / 46.0
}
fn size(v: &Value) -> usize {
    1 + match v {
        Value::Array(a) => a
            .iter()
            .map(|c| {
                1 + if c.is_array() || c.is_object() {
                    size(c)
                } else {
                    0
                }
            })
            .sum(),
        Value::Object(a) => a
            .values()
            .map(|c| {
                1 + if c.is_array() || c.is_object() {
                    size(c)
                } else {
                    0
                }
            })
            .sum(),
        _ => 0,
    }
}
fn diff(w: &Value, g: &Value, unordered: bool, extra: bool) -> usize {
    if json_equal(w, g, unordered, extra) {
        return 0;
    }
    match (w, g) {
        (Value::Object(w), Value::Object(g)) => {
            w.iter()
                .map(|(k, v)| {
                    g.get(k)
                        .map(|g| diff(v, g, unordered, extra))
                        .unwrap_or_else(|| {
                            if json_equal(v, &Value::Null, unordered, extra) {
                                0
                            } else {
                                size(v)
                            }
                        })
                })
                .sum::<usize>()
                + if extra {
                    0
                } else {
                    g.iter()
                        .filter(|(k, _)| !w.contains_key(*k))
                        .map(|(_, v)| size(v))
                        .sum()
                }
        }
        (Value::Array(w), Value::Array(g)) => {
            let (mut w, mut g) = (w.iter().collect::<Vec<_>>(), g.iter().collect::<Vec<_>>());
            if unordered {
                let mut used = vec![false; g.len()];
                w.retain(|w| {
                    if let Some(j) = g
                        .iter()
                        .enumerate()
                        .position(|(j, g)| !used[j] && json_equal(w, g, unordered, extra))
                    {
                        used[j] = true;
                        false
                    } else {
                        true
                    }
                });
                g = g
                    .into_iter()
                    .enumerate()
                    .filter_map(|(i, g)| if used[i] { None } else { Some(g) })
                    .collect();
                if w.len() > 1 || g.len() > 1 {
                    return w.iter().map(|v| size(v)).sum::<usize>()
                        + if extra {
                            0
                        } else {
                            g.iter().map(|v| size(v)).sum()
                        };
                }
            }
            w.iter()
                .zip(&g)
                .map(|(w, g)| diff(w, g, unordered, extra))
                .sum::<usize>()
                + w.iter().skip(g.len()).map(|v| size(v)).sum::<usize>()
                + if extra {
                    0
                } else {
                    g.iter().skip(w.len()).map(|v| size(v)).sum()
                }
        }
        _ => size(w).max(size(g)),
    }
}
fn json_distance(p: &Value, a: &str) -> f64 {
    let w = if let Some(s) = p["equalToJson"].as_str() {
        serde_json::from_str(s).ok()
    } else {
        Some(p["equalToJson"].clone())
    };
    let Some((w, g)) = w.zip(serde_json::from_str(a).ok()) else {
        return 1.0;
    };
    diff(
        &w,
        &g,
        flag(p, "ignoreArrayOrder"),
        flag(p, "ignoreExtraElements"),
    ) as f64
        / size(&w).max(size(&g)) as f64
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn java_distances() {
        let cs: Value =
            serde_json::from_str(include_str!("../../testdata/matcher_distance_cases.json"))
                .unwrap();
        let mut fail = vec![];
        for c in cs.as_array().unwrap() {
            let actual = c["actual"].as_str().unwrap();
            let r = Request {
                method: "POST".into(),
                url: "/".into(),
                body: actual.as_bytes().to_vec(),
                ..Default::default()
            };
            let got = request_distance(&json!({"bodyPatterns":[c["pattern"]]}), &r);
            let want = c["distance"].as_f64().unwrap();
            if (got - want).abs() > 1e-15 {
                fail.push(format!("{} got {} expected {}", c["name"], got, want))
            }
        }
        assert!(fail.is_empty(), "{} mismatches {:#?}", fail.len(), fail)
    }
}
