use std::collections::HashMap;
use std::sync::{Mutex, OnceLock};
#[derive(Clone)]
enum Compiled {
    Fast(regex::Regex),
    Slow(fancy_regex::Regex),
    Invalid,
}
static CACHE: OnceLock<Mutex<HashMap<String, Compiled>>> = OnceLock::new();
pub fn is_match(pattern: &str, value: &str) -> bool {
    let cache = CACHE.get_or_init(|| Mutex::new(HashMap::new()));
    let cached = cache.lock().unwrap().get(pattern).cloned();
    let compiled = cached.unwrap_or_else(|| {
        let p = translate(pattern);
        let wrapped = format!("\\A(?:{p})\\z");
        let c = if let Ok(r) = regex::Regex::new(&wrapped) {
            Compiled::Fast(r)
        } else {
            fancy_regex::RegexBuilder::new(&wrapped)
                .backtrack_limit(usize::MAX)
                .build()
                .map(Compiled::Slow)
                .unwrap_or(Compiled::Invalid)
        };
        let mut cache = cache.lock().unwrap();
        if cache.len() > 4096 {
            cache.clear()
        }
        cache.insert(pattern.into(), c.clone());
        c
    });
    match compiled {
        Compiled::Fast(r) => r.is_match(value),
        Compiled::Slow(r) => r.is_match(value).unwrap_or(false),
        Compiled::Invalid => false,
    }
}
fn translate(pattern: &str) -> String {
    let mut p = String::new();
    let mut rest = pattern;
    while let Some(i) = rest.find("\\Q") {
        p.push_str(&rest[..i]);
        rest = &rest[i + 2..];
        if let Some(j) = rest.find("\\E") {
            p.push_str(&regex::escape(&rest[..j]));
            rest = &rest[j + 2..]
        } else {
            p.push_str(&regex::escape(rest));
            rest = "";
            break;
        }
    }
    p.push_str(rest);
    let unicode = p.contains("(?U)");
    p = p.replace("(?U)", "").replace(
        "\\R",
        "(?:\\r\\n|[\\n\\r\\x0b\\x0c\\x85\\u{2028}\\u{2029}])",
    );
    if !unicode {
        p = p
            .replace("\\d", "[0-9]")
            .replace("\\D", "[^0-9]")
            .replace("\\w", "[a-zA-Z_0-9]")
            .replace("\\W", "[^a-zA-Z_0-9]")
            .replace("\\s", "[ \\t\\n\\x0b\\x0c\\r]")
            .replace("\\S", "[^ \\t\\n\\x0b\\x0c\\r]")
    }
    p = p.replace("(?iu)", "(?i)").replace("(?iu:", "(?i:");
    // Java ASCII case folding is the default; Unicode folding requires the u flag.
    if !pattern.contains("iu") && !unicode {
        p = p
            .replace("(?i)[^a-z]", "[^a-zA-Z]")
            .replace("(?i)", "(?i-u)")
            .replace("(?i:", "(?i-u:")
            .replace("(?i-m:", "(?i-um:")
    }
    if pattern.contains("iu") {
        p = p
            .replace("(?i)i", "(?i)[iIıİ]")
            .replace("(?i:i)", "(?i:[iIıİ])")
    }
    // Rust regex supports set intersections; fancy-regex adds atomic groups.
    let bytes = p.as_bytes();
    let mut out = String::new();
    let mut atoms = vec![];
    let mut groups = vec![];
    let mut i = 0;
    while i < bytes.len() {
        let start = out.len();
        match bytes[i] {
            b'\\' => {
                let end = i + 1 + p[i + 1..].chars().next().map(char::len_utf8).unwrap_or(0);
                out.push_str(&p[i..end]);
                atoms.push(start);
                i = end
            }
            b'[' => {
                let mut j = i + 1;
                let mut depth = 1;
                while j < bytes.len() {
                    if bytes[j] == b'\\' {
                        j += 2;
                        continue;
                    }
                    if bytes[j] == b'[' {
                        depth += 1
                    }
                    if bytes[j] == b']' {
                        depth -= 1;
                        if depth == 0 {
                            j += 1;
                            break;
                        }
                    }
                    j += 1
                }
                out.push_str(&p[i..j.min(bytes.len())]);
                atoms.push(start);
                i = j
            }
            b'(' => {
                groups.push(start);
                out.push('(');
                i += 1
            }
            b')' => {
                out.push(')');
                if let Some(s) = groups.pop() {
                    atoms.push(s)
                }
                i += 1
            }
            b'+' | b'*' | b'?' => {
                let mut end = i + 1;
                if end < bytes.len() && bytes[end] == b'+' {
                    if let Some(&atom) = atoms.last() {
                        let content = out[atom..].to_owned();
                        out.truncate(atom);
                        out.push_str("(?>");
                        out.push_str(&content);
                        out.push(bytes[i] as char);
                        out.push(')');
                        end += 1
                    } else {
                        out.push(bytes[i] as char)
                    }
                } else {
                    out.push(bytes[i] as char)
                }
                i = end
            }
            b'{' => {
                if let Some(j) = p[i..].find('}') {
                    let end = i + j + 1;
                    if bytes
                        .get(i + 1)
                        .map(|b| b.is_ascii_digit())
                        .unwrap_or(false)
                        && end < bytes.len()
                        && bytes[end] == b'+'
                    {
                        if let Some(&atom) = atoms.last() {
                            let content = out[atom..].to_owned();
                            out.truncate(atom);
                            out.push_str("(?>");
                            out.push_str(&content);
                            out.push_str(&p[i..end]);
                            out.push(')');
                            i = end + 1;
                            continue;
                        }
                    }
                    out.push_str(&p[i..end]);
                    i = end
                } else {
                    out.push('{');
                    i += 1
                }
            }
            _ => {
                let c = p[i..].chars().next().unwrap();
                out.push(c);
                if c != '|' {
                    atoms.push(start)
                }
                i += c.len_utf8()
            }
        }
    }
    out
}
