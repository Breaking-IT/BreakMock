use chrono::{
    DateTime, Datelike, Duration, FixedOffset, Months, NaiveDate, NaiveDateTime, TimeZone,
    Timelike, Utc,
};
use serde_json::{Value, json};
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
                1 => "%-m",
                2 => "%m",
                3 => "%b",
                _ => "%B",
            },
            'd' => {
                if n == 1 {
                    "%-d"
                } else {
                    "%d"
                }
            }
            'H' => "%H",
            'h' => {
                if n == 1 {
                    "%-I"
                } else {
                    "%I"
                }
            }
            'm' => "%M",
            's' => "%S",
            'S' => "%3f",
            'a' => "%p",
            'X' | 'x' => {
                if n == 3 {
                    "%:z"
                } else {
                    "%z"
                }
            }
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
                    if c == '%' {
                        out.push('%')
                    }
                    out.push(c)
                }
                continue;
            }
        })
    }
    out
}
fn parse(s: &str, format: &str) -> Result<DateTime<FixedOffset>, String> {
    if format == "epoch" || format == "unix" {
        return s
            .parse::<i64>()
            .ok()
            .and_then(|n| {
                if format == "epoch" {
                    DateTime::from_timestamp_millis(n)
                } else {
                    DateTime::from_timestamp(n, 0)
                }
            })
            .map(|d| d.fixed_offset())
            .ok_or("Invalid timestamp".into());
    }
    if let Ok(d) = DateTime::parse_from_rfc3339(s) {
        return Ok(d);
    }
    let f = if format.is_empty() {
        "%Y-%m-%d".into()
    } else {
        layout(format)
    };
    if let Ok(d) = DateTime::parse_from_str(s, &f) {
        return Ok(d);
    }
    if let Ok(d) = NaiveDateTime::parse_from_str(s, &f) {
        return Ok(d.and_utc().fixed_offset());
    }
    if let Ok(d) = NaiveDate::parse_from_str(s, &f) {
        return Ok(d.and_hms_opt(0, 0, 0).unwrap().and_utc().fixed_offset());
    }
    if format == "yyyy" {
        return parse(&format!("{s}-01-01"), "");
    }
    if format == "yyyy-MM" {
        return parse(&format!("{s}-01"), "");
    }
    Err(format!("Cannot parse date {s}"))
}
fn iso(d: DateTime<FixedOffset>) -> String {
    d.to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true)
}
pub fn eval(
    name: &str,
    p: &[Value],
    hash: &serde_json::Map<String, Value>,
) -> Result<Value, String> {
    let s = |i: usize| p.get(i).map(super::text).unwrap_or_default();
    let h = |k: &str| hash.get(k).map(super::text).unwrap_or_default();
    if name == "parseDate" {
        if h("format").is_empty() && !s(0).contains('T') {
            return Ok(Value::Null);
        }
        let mut d = parse(&s(0), &h("format"))?;
        if h("format") == "epoch" {
            d = d.with_nanosecond(0).unwrap_or(d)
        }
        return Ok(json!({"__date":iso(d),"__formatted":!h("format").is_empty()}));
    }
    if name == "dateFormat" {
        let format = if h("format").is_empty() {
            s(1)
        } else {
            h("format")
        };
        if p.first().is_none_or(Value::is_null) {
            return Ok(json!(format));
        }
        if p[0].get("__date").is_none() {
            let typ = if p[0].is_number() {
                "Integer"
            } else {
                "String"
            };
            return Err(format!(
                "java.lang.IllegalArgumentException: found instance of {typ} with value '{}', but expected instance of TemporalAccessor",
                s(0)
            ));
        }
        let mut d = parse(&s(0), "")?;
        let tz = h("tz");
        if let Ok(tz) = tz.parse::<chrono_tz::Tz>() {
            d = d.with_timezone(&tz).fixed_offset()
        } else if !h("time").is_empty() {
            d = d.with_timezone(&chrono::Local).fixed_offset()
        }
        let locale = s(2);
        let style = format.as_str();
        let localized = match (style, locale.as_str()) {
            ("short", "en_US") => d.format("%-m/%-d/%y").to_string(),
            ("short", "de_DE") => d.format("%d.%m.%y").to_string(),
            ("short", _) => d.format("%d/%m/%Y").to_string(),
            ("medium", "de_DE") => d.format("%d.%m.%Y").to_string(),
            ("medium", "fr_FR") => {
                let months = [
                    "janv.", "févr.", "mars", "avr.", "mai", "juin", "juil.", "août", "sept.",
                    "oct.", "nov.", "déc.",
                ];
                format!("{} {} {}", d.day(), months[d.month0() as usize], d.year())
            }
            ("medium", _) => d.format("%b %-d, %Y").to_string(),
            ("full", _) => d.format("%A, %B %-d, %Y").to_string(),
            ("long", _) => d.format("%B %-d, %Y").to_string(),
            _ => format_pattern(d, &format, &locale),
        };
        let time = h("time");
        let result = if time.is_empty() {
            localized
        } else {
            let clock = if time == "short" {
                d.format("%-I:%M\u{202f}%p").to_string()
            } else {
                format!(
                    "{} {}",
                    d.format("%-I:%M:%S\u{202f}%p"),
                    if d.offset().local_minus_utc() == 3600 {
                        "CET"
                    } else {
                        "UTC"
                    }
                )
            };
            format!("{localized}, {clock}")
        };
        return Ok(json!(result));
    }
    let mut d = if name == "now" {
        Utc::now().fixed_offset()
    } else {
        parse(&s(0), "")?
    };
    let timezone = if name == "now" {
        h("timezone")
    } else {
        String::new()
    };
    if !timezone.is_empty() {
        if let Ok(tz) = timezone.parse::<chrono_tz::Tz>() {
            d = d.with_timezone(&tz).fixed_offset();
        } else if timezone == "UTC" {
            d = d.with_timezone(&Utc).fixed_offset();
        }
    }
    let offset = h("offset");
    let parts: Vec<_> = offset.split_whitespace().collect();
    if parts.len() == 2 {
        let n: i64 = parts[0].parse().unwrap_or(0);
        let unit = parts[1].trim_end_matches('s');
        d = match unit {
            "second" => d + Duration::seconds(n),
            "minute" => d + Duration::minutes(n),
            "hour" => d + Duration::hours(n),
            "day" => d + Duration::days(n),
            "month" | "year" => {
                let months = n * if unit == "year" { 12 } else { 1 };
                if months >= 0 {
                    d.checked_add_months(Months::new(months as u32))
                        .unwrap_or(d)
                } else {
                    d.checked_sub_months(Months::new(-months as u32))
                        .unwrap_or(d)
                }
            }
            _ => {
                return Err(format!(
                    "java.lang.IllegalArgumentException: No enum constant com.github.tomakehurst.wiremock.common.DateTimeUnit.{}",
                    parts[1].to_uppercase()
                ));
            }
        };
    }
    if name == "truncateDate" {
        if p.first()
            .and_then(|v| v.get("__formatted"))
            .and_then(Value::as_bool)
            == Some(true)
        {
            d = d.with_timezone(&chrono::Local).fixed_offset()
        }
        let (y, m, day, h, min) = (d.year(), d.month(), d.day(), d.hour(), d.minute());
        let phrase = s(1);
        let (y, m, day, h, min, sec) = match phrase.as_str() {
            "first second of minute" => (y, m, day, h, min, 0),
            "first minute of hour" => (y, m, day, h, 0, 0),
            "first hour of day" => (y, m, day, 0, 0, 0),
            "first day of month" => (y, m, 1, 0, 0, 0),
            "first day of year" => (y, 1, 1, 0, 0, 0),
            "first day of next year" => (y + 1, 1, 1, 0, 0, 0),
            "last day of year" => (y, 12, 31, 0, 0, 0),
            "first day of next month" => {
                if m == 12 {
                    (y + 1, 1, 1, 0, 0, 0)
                } else {
                    (y, m + 1, 1, 0, 0, 0)
                }
            }
            "last day of month" => {
                let next = if m == 12 {
                    NaiveDate::from_ymd_opt(y + 1, 1, 1)
                } else {
                    NaiveDate::from_ymd_opt(y, m + 1, 1)
                }
                .unwrap()
                    - Duration::days(1);
                (y, m, next.day(), 0, 0, 0)
            }
            _ => return Ok(json!(iso(d))),
        };
        d = d
            .timezone()
            .with_ymd_and_hms(y, m, day, h, min, sec)
            .single()
            .unwrap_or(d);
        return Ok(json!({"__date":iso(d)}));
    }
    let format = if name == "dateFormat" {
        s(1)
    } else {
        h("format")
    };
    if name == "dateFormat" && !matches!(format.as_str(), "full" | "long" | "medium" | "short") {
        return Ok(json!(format));
    }
    Ok(json!(match format.as_str() {
        "epoch" => d.timestamp_millis().to_string(),
        "unix" => d.timestamp().to_string(),
        "" => iso(d),
        _ => format_pattern(d, &format, ""),
    }))
}

// Match the Go implementation's Java locale symbols rather than assuming that
// AM/PM casing follows the operating system. en-NL uses "pm", en-US uses "PM",
// and other locales can use entirely different day-period text.
fn locale_period(locale: &str, hour: u32) -> String {
    use std::io::Read;
    use std::sync::OnceLock;
    static DATA: OnceLock<Value> = OnceLock::new();
    let data = DATA.get_or_init(|| {
        let compressed = include_bytes!("../../internal/localeassets/java-date-locales.json.gz");
        let mut decoder = flate2::read::GzDecoder::new(compressed.as_slice());
        let mut decoded = Vec::new();
        if decoder.read_to_end(&mut decoded).is_err() {
            return Value::Null;
        }
        serde_json::from_slice(&decoded).unwrap_or(Value::Null)
    });
    let locale = if locale.is_empty() {
        crate::matcher::default_locale()
    } else {
        locale.to_string()
    };
    let mut tag = locale.replace('_', "-");
    loop {
        if let Some(period) = data["locales"][&tag]["symbols"]["a"]
            .get((hour * 2) as usize)
            .and_then(Value::as_str)
        {
            return period.to_string();
        }
        if let Some(index) = tag.rfind('-') {
            tag.truncate(index);
        } else if tag != "und" {
            tag = "und".into();
        } else {
            return if hour < 12 { "AM" } else { "PM" }.into();
        }
    }
}
fn format_pattern(date: DateTime<FixedOffset>, pattern: &str, locale: &str) -> String {
    let translated = layout(pattern);
    if !translated.contains("%p") {
        return date.format(&translated).to_string();
    }
    let period = locale_period(locale, date.hour()).replace('%', "%%");
    let mut chars = translated.chars().peekable();
    let mut localized = String::new();
    while let Some(c) = chars.next() {
        if c == '%' {
            if chars.peek() == Some(&'%') {
                localized.push_str("%%");
                chars.next();
                continue;
            }
            if chars.peek() == Some(&'p') {
                localized.push_str(&period);
                chars.next();
                continue;
            }
        }
        localized.push(c);
    }
    date.format(&localized).to_string()
}
#[cfg(test)]
mod locale_period_tests {
    use super::*;
    #[test]
    fn java_day_period_depends_on_locale_not_platform() {
        let date = DateTime::parse_from_rfc3339("2020-02-03T14:05:06+02:00").unwrap();
        assert_eq!(
            format_pattern(date, "yy-M-d h:mm a", "en_US"),
            "20-2-3 2:05 PM"
        );
        assert_eq!(
            format_pattern(date, "yy-M-d h:mm a", "en_NL"),
            "20-2-3 2:05 pm"
        );
        assert_eq!(format_pattern(date, "h:mm a", "nl_NL"), "2:05 p.m.");
        assert_eq!(format_pattern(date, "h:mm 'a'", "en_US"), "2:05 a");
        let morning = date.with_hour(2).unwrap();
        assert_eq!(format_pattern(morning, "h:mm a", "en_US"), "2:05 AM");
        assert_eq!(format_pattern(morning, "h:mm a", "en_NL"), "2:05 am");
    }
}
