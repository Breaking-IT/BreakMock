use super::*;
use chrono::{DateTime, Datelike, NaiveDate, NaiveDateTime, TimeZone, Timelike, Utc};
fn formats(s: &str) -> Vec<String> {
    if let Some(i) = s.find('[') {
        if let Some(j) = s[i..].find(']') {
            let end = i + j;
            let mut a = formats(&format!("{}{}{}", &s[..i], &s[i + 1..end], &s[end + 1..]));
            a.extend(formats(&format!("{}{}", &s[..i], &s[end + 1..])));
            return a;
        }
    }
    vec![s.to_owned()]
}
fn layout(s: &str) -> String {
    let mut out = String::new();
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        if c == '\'' {
            while let Some(c) = chars.next() {
                if c == '\'' {
                    break;
                }
                out.push(c)
            }
            continue;
        }
        let mut n = 1;
        while chars.peek() == Some(&c) {
            n += 1;
            chars.next();
        }
        out.push_str(match c {
            'y' | 'u' => {
                if n == 2 {
                    "%y"
                } else {
                    "%Y"
                }
            }
            'M' => match n {
                1 | 2 => "%m",
                3 => "%b",
                _ => "%B",
            },
            'd' => "%d",
            'H' => "%H",
            'h' => "%I",
            'm' => "%M",
            's' => "%S",
            'S' => "%f",
            'a' => "%p",
            'X' | 'x' => "%#z",
            'Z' => "%z",
            'E' => {
                if n >= 4 {
                    "%A"
                } else {
                    "%a"
                }
            }
            _ => {
                for _ in 0..n {
                    out.push(c)
                }
                continue;
            }
        })
    }
    out
}
fn parse(s: &str, fmt: &str) -> Option<DateTime<Utc>> {
    if fmt.eq_ignore_ascii_case("epoch") {
        return DateTime::from_timestamp_millis(s.parse().ok()?);
    }
    if fmt.eq_ignore_ascii_case("unix") {
        return DateTime::from_timestamp(s.parse().ok()?, 0);
    }
    if fmt.is_empty() {
        if let Ok(d) = DateTime::parse_from_rfc3339(s) {
            return Some(d.with_timezone(&Utc));
        }
        for f in ["%Y-%m-%dT%H:%M:%S", "%Y-%m-%dT%H:%M", "%Y-%m-%d %H:%M:%S"] {
            if let Ok(d) = NaiveDateTime::parse_from_str(s, f) {
                return Some(d.and_utc());
            }
        }
        return NaiveDate::parse_from_str(s, "%Y-%m-%d")
            .ok()
            .map(|d| d.and_hms_opt(0, 0, 0).unwrap().and_utc());
    }
    // Java's English locales differ in case; parsing is case sensitive.
    if fmt.contains('a') {
        let lowercase = default_locale().replace('-', "_") == "en_NL";
        if (lowercase && (s.ends_with("AM") || s.ends_with("PM")))
            || (!lowercase && (s.ends_with("am") || s.ends_with("pm")))
        {
            return None;
        }
    }
    for f in formats(fmt) {
        let f = layout(&f);
        if let Ok(d) = DateTime::parse_from_str(s, &f) {
            return Some(d.with_timezone(&Utc));
        }
        if let Ok(d) = NaiveDateTime::parse_from_str(s, &f) {
            return Some(d.and_utc());
        }
        if let Ok(d) = NaiveDate::parse_from_str(s, &f) {
            return Some(d.and_hms_opt(0, 0, 0).unwrap().and_utc());
        }
    }
    None
}
fn truncate(d: DateTime<Utc>, s: &str) -> DateTime<Utc> {
    let (y, m, day, h, min, _sec) = (
        d.year(),
        d.month(),
        d.day(),
        d.hour(),
        d.minute(),
        d.second(),
    );
    let (y, m, day, h, min, sec) = match s.to_lowercase().replace('_', " ").as_str() {
        "first second of minute" => (y, m, day, h, min, 0),
        "first minute of hour" => (y, m, day, h, 0, 0),
        "first hour of day" => (y, m, day, 0, 0, 0),
        "first day of month" => (y, m, 1, 0, 0, 0),
        "first day of year" => (y, 1, 1, 0, 0, 0),
        "first day of next year" => (y + 1, 1, 1, 0, 0, 0),
        _ => return d,
    };
    Utc.with_ymd_and_hms(y, m, day, h, min, sec)
        .single()
        .unwrap_or(d)
}
pub fn date_match(op: &str, w: &str, g: &str, p: &Value) -> bool {
    let expected_zoned = DateTime::parse_from_rfc3339(w).is_ok() || w.starts_with("now");
    let g_owned = if !expected_zoned && p["actualFormat"].is_null() {
        DateTime::parse_from_rfc3339(g)
            .ok()
            .map(|d| d.naive_local().format("%Y-%m-%dT%H:%M:%S").to_string())
    } else {
        None
    };
    let Some(mut g) = parse(
        g_owned.as_deref().unwrap_or(g),
        p["actualFormat"].as_str().unwrap_or(""),
    ) else {
        return false;
    };
    let mut w = if w.starts_with("now") {
        let mut d = Utc::now();
        let re = regex::Regex::new(r"(-?\d+)\s+(\w+)").unwrap();
        let (n, unit) = if let Some(c) = re.captures(w) {
            (c[1].parse::<i64>().unwrap_or(0), c[2].to_owned())
        } else {
            (
                p["expectedOffset"].as_i64().unwrap_or(0),
                p["expectedOffsetUnit"].as_str().unwrap_or("seconds").into(),
            )
        };
        if !flag(p, "applyTruncationLast") {
            d = truncate(d, p["truncateExpected"].as_str().unwrap_or(""))
        }
        let secs = match unit.trim_end_matches('s').to_lowercase().as_str() {
            "second" => n,
            "minute" => n * 60,
            "hour" => n * 3600,
            "day" => n * 86400,
            _ => 0,
        };
        d + chrono::Duration::seconds(secs)
    } else {
        let Some(d) = parse(w, "") else { return false };
        d
    };
    if let Some(s) = p["truncateActual"].as_str() {
        if g.to_rfc3339().contains('T') && g.to_rfc3339().ends_with("+00:00") {
            g = truncate(g, s)
        }
    }
    if let Some(s) = p["truncateExpected"].as_str() {
        w = truncate(w, s)
    }
    match op {
        "before" => g < w,
        "after" => g > w,
        _ => g == w,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn locale_sensitive_meridiem() {
        for (locale, accepted, rejected) in [("en_US", "PM", "pm"), ("en_NL", "pm", "PM")] {
            let _locale = super::super::locale::TestLocale::new(locale);
            let fmt = "dd MMMM yyyy 'at' hh:mm a";
            assert!(parse(&format!("02 January 2026 at 03:04 {accepted}"), fmt).is_some());
            assert!(parse(&format!("02 January 2026 at 03:04 {rejected}"), fmt).is_none());
        }
    }
}
