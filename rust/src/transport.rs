// Copyright 2026 Breaking-IT. Licensed under the Apache License, Version 2.0.
use breakmock_rust::{Request, Response};
use std::io;
const MAX_BODY: usize = 64 * 1024 * 1024;
fn invalid(message: &str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message)
}
pub fn parse_request(bytes: &[u8], client_ip: &str) -> io::Result<Option<(Request, u8, usize)>> {
    let mut headers = [httparse::EMPTY_HEADER; 128];
    let mut parsed = httparse::Request::new(&mut headers);
    let offset = match parsed.parse(bytes) {
        Ok(httparse::Status::Complete(n)) => n,
        Ok(httparse::Status::Partial) => return Ok(None),
        Err(e) => return Err(io::Error::new(io::ErrorKind::InvalidData, e)),
    };
    let mut request = Request {
        method: parsed.method.unwrap_or("GET").into(),
        url: parsed.path.unwrap_or("/").into(),
        headers: parsed
            .headers
            .iter()
            .map(|h| (h.name.into(), String::from_utf8_lossy(h.value).into_owned()))
            .collect(),
        body: Vec::new(),
        client_ip: client_ip.into(),
        scheme: "http".into(),
    };
    let version = parsed.version.unwrap_or(1);
    if request
        .header("Transfer-Encoding")
        .is_some_and(|s| s.eq_ignore_ascii_case("chunked"))
    {
        let mut pos = offset;
        let mut body = Vec::new();
        loop {
            let Some(line) = bytes[pos..].windows(2).position(|v| v == b"\r\n") else {
                return Ok(None);
            };
            let length = usize::from_str_radix(
                std::str::from_utf8(&bytes[pos..pos + line])
                    .unwrap_or("")
                    .split(';')
                    .next()
                    .unwrap_or(""),
                16,
            )
            .map_err(|_| invalid("chunk length"))?;
            pos += line + 2;
            if length == 0 {
                // The last chunk is followed by a trailer section, terminated by an empty line.
                loop {
                    let Some(line) = bytes[pos..].windows(2).position(|v| v == b"\r\n") else {
                        return Ok(None);
                    };
                    if line == 0 {
                        request.body = body;
                        return Ok(Some((request, version, pos + 2)));
                    }
                    if !bytes[pos..pos + line].contains(&b':') {
                        return Err(invalid("chunk trailer"));
                    }
                    pos += line + 2;
                }
            }
            if body.len().checked_add(length).is_none_or(|n| n > MAX_BODY) {
                return Err(invalid("request body too large"));
            }
            let end = pos
                .checked_add(length)
                .ok_or_else(|| invalid("chunk length overflow"))?;
            let next = end
                .checked_add(2)
                .ok_or_else(|| invalid("chunk length overflow"))?;
            if bytes.len() < next {
                return Ok(None);
            }
            if &bytes[end..next] != b"\r\n" {
                return Err(invalid("chunk delimiter"));
            }
            body.extend_from_slice(&bytes[pos..end]);
            pos = next;
        }
    }
    let length = match request.header("Content-Length") {
        Some(v) => v.parse::<usize>().map_err(|_| invalid("content length"))?,
        None => 0,
    };
    if length > MAX_BODY {
        return Err(invalid("request body too large"));
    }
    let end = offset
        .checked_add(length)
        .ok_or_else(|| invalid("content length overflow"))?;
    if bytes.len() < end {
        return Ok(None);
    }
    request.body = bytes[offset..end].to_vec();
    Ok(Some((request, version, end)))
}
pub fn serialize_response(req: &Request, res: &Response, close: bool) -> Vec<u8> {
    let mut b = format!("HTTP/1.1 {} {}\r\n", res.status, reason(res.status)).into_bytes();
    let mut has_length = false;
    for (k, v) in &res.headers {
        if k.eq_ignore_ascii_case("transfer-encoding") {
            continue;
        }
        if k.eq_ignore_ascii_case("content-length") {
            has_length = true
        }
        if !k.contains(['\r', '\n']) && !v.contains(['\r', '\n']) {
            b.extend_from_slice(format!("{k}: {v}\r\n").as_bytes());
        }
    }
    if !has_length && res.status >= 200 && res.status != 204 && res.status != 304 {
        b.extend_from_slice(format!("Content-Length: {}\r\n", res.body.len()).as_bytes())
    }
    if close {
        b.extend_from_slice(b"Connection: close\r\n")
    }
    b.extend_from_slice(b"\r\n");
    if req.method != "HEAD" && res.status >= 200 && res.status != 204 && res.status != 304 {
        b.extend_from_slice(&res.body)
    }
    b
}
pub fn gzip_allowed(req: &Request, res: &Response) -> bool {
    let accepts = req
        .header("Accept-Encoding")
        .unwrap_or("")
        .split(',')
        .any(|part| {
            let mut pieces = part.trim().split(';');
            if !pieces
                .next()
                .unwrap_or("")
                .trim()
                .eq_ignore_ascii_case("gzip")
            {
                return false;
            }
            !pieces.any(|p| {
                let pair: Vec<_> = p.trim().splitn(2, '=').collect();
                pair.len() == 2
                    && pair[0].trim().eq_ignore_ascii_case("q")
                    && pair[1].trim().parse::<f64>() == Ok(0.0)
            })
        });
    if !accepts
        || res.status < 200
        || res.status == 204
        || res.status == 304
        || !matches!(
            req.method.as_str(),
            "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
        )
    {
        return false;
    }
    for (k, v) in &res.headers {
        if k.eq_ignore_ascii_case("Content-Encoding") {
            return false;
        }
        if k.eq_ignore_ascii_case("Content-Length") && v.parse::<usize>().is_ok_and(|n| n < 32) {
            return false;
        }
        if k.eq_ignore_ascii_case("Content-Type") && !gzip_type(v) {
            return false;
        }
    }
    if req.path().ends_with(".svgz") {
        return false;
    }
    let ext = std::path::Path::new(req.path())
        .extension()
        .and_then(|v| v.to_str())
        .unwrap_or("")
        .to_lowercase();
    !EXCLUDED_EXT.contains(&ext.as_str())
}
pub fn reason(status: u16) -> String {
    match status {
        100 => "Continue".into(),
        101 => "Switching Protocols".into(),
        102 => "Processing".into(),
        103 => "Early Hints".into(),
        200 => "OK".into(),
        201 => "Created".into(),
        202 => "Accepted".into(),
        203 => "Non Authoritative Information".into(),
        204 => "No Content".into(),
        205 => "Reset Content".into(),
        206 => "Partial Content".into(),
        207 => "Multi-Status".into(),
        300 => "Multiple Choices".into(),
        301 => "Moved Permanently".into(),
        302 => "Found".into(),
        303 => "See Other".into(),
        304 => "Not Modified".into(),
        305 => "Use Proxy".into(),
        307 => "Temporary Redirect".into(),
        308 => "Permanent Redirect".into(),
        400 => "Bad Request".into(),
        401 => "Unauthorized".into(),
        402 => "Payment Required".into(),
        403 => "Forbidden".into(),
        404 => "Not Found".into(),
        405 => "Method Not Allowed".into(),
        406 => "Not Acceptable".into(),
        407 => "Proxy Authentication Required".into(),
        408 => "Request Timeout".into(),
        409 => "Conflict".into(),
        410 => "Gone".into(),
        411 => "Length Required".into(),
        412 => "Precondition Failed".into(),
        413 => "Payload Too Large".into(),
        414 => "URI Too Long".into(),
        415 => "Unsupported Media Type".into(),
        416 => "Range Not Satisfiable".into(),
        417 => "Expectation Failed".into(),
        418 => "I'm a Teapot".into(),
        420 => "Enhance your Calm".into(),
        421 => "Misdirected Request".into(),
        422 => "Unprocessable Entity".into(),
        423 => "Locked".into(),
        424 => "Failed Dependency".into(),
        426 => "Upgrade Required".into(),
        428 => "Precondition Required".into(),
        429 => "Too Many Requests".into(),
        431 => "Request Header Fields Too Large".into(),
        451 => "Unavailable for Legal Reason".into(),
        500 => "Server Error".into(),
        501 => "Not Implemented".into(),
        502 => "Bad Gateway".into(),
        503 => "Service Unavailable".into(),
        504 => "Gateway Timeout".into(),
        505 => "HTTP Version Not Supported".into(),
        507 => "Insufficient Storage".into(),
        508 => "Loop Detected".into(),
        510 => "Not Extended".into(),
        511 => "Network Authentication Required".into(),
        _ => status.to_string(),
    }
}
fn gzip_type(s: &str) -> bool {
    let t = s.split(';').next().unwrap_or("").trim().to_lowercase();
    ![
        "application/compress",
        "application/zip",
        "application/gzip",
        "application/bzip2",
        "application/x-bzip2",
        "application/brotli",
        "application/x-xz",
        "application/x-rar-compressed",
        "text/event-stream",
        "audio/basic",
        "audio/midi",
        "audio/mpeg",
        "audio/x-aiff",
        "audio/x-pn-realaudio",
        "audio/x-wav",
        "image/apng",
        "image/avif",
        "image/bmp",
        "image/gif",
        "image/ief",
        "image/jpeg",
        "image/jpeg2000",
        "image/png",
        "image/tiff",
        "image/vnd.wap.wbmp",
        "image/webp",
        "image/x-cmu-raster",
        "image/x-icon",
        "image/x-portable-anymap",
        "image/x-portable-bitmap",
        "image/x-portable-graymap",
        "image/x-portable-pixmap",
        "image/x-rgb",
        "image/x-xbitmap",
        "image/x-xpixmap",
        "image/x-xwindowdump",
        "image/xcf",
        "video/mp4",
        "video/mpeg",
        "video/quicktime",
        "video/vnd.rn-realvideo",
        "video/x-msvideo",
        "video/x-sgi-movie",
        "video/x.ms.asf",
        "video/x.ms.asx",
    ]
    .contains(&t.as_str())
}
const EXCLUDED_EXT: &[&str] = &[
    "aif", "aifc", "aiff", "apng", "asf", "asx", "au", "avi", "avif", "bmp", "br", "gif", "gz",
    "gzip", "ico", "ief", "jpe", "jp2", "jpeg", "jpg", "kar", "mid", "midi", "mov", "movie", "mp2",
    "mp3", "mp4", "mpe", "mpeg", "mpg", "mpga", "pbm", "pgm", "png", "pnm", "ppm", "qt", "ra",
    "rar", "ram", "ras", "rgb", "rm", "rv", "snd", "tif", "tiff", "wav", "wbmp", "webp", "xbm",
    "xcf", "xpm", "xwd", "xz", "z", "zip",
];

#[cfg(test)]
mod parser_tests {
    use super::*;
    #[test]
    fn oversized_lengths_are_errors() {
        for value in [
            usize::MAX.to_string(),
            (MAX_BODY + 1).to_string(),
            "invalid".into(),
        ] {
            assert!(
                parse_request(
                    format!("POST / HTTP/1.1\r\nContent-Length: {value}\r\n\r\n").as_bytes(),
                    ""
                )
                .is_err()
            );
        }
        assert!(
            parse_request(
                b"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\nffffffffffffffff\r\n",
                ""
            )
            .is_err()
        );
    }
    #[test]
    fn trailers_preserve_pipeline_boundary() {
        let first=b"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n3;foo=bar\r\nabc\r\n0\r\nX-Foo: bar\r\n\r\n";
        let mut input = first.to_vec();
        input.extend_from_slice(b"GET /next HTTP/1.1\r\n\r\n");
        let (request, _, consumed) = parse_request(&input, "").unwrap().unwrap();
        assert_eq!(request.body, b"abc");
        assert_eq!(consumed, first.len());
        assert_eq!(
            parse_request(&input[consumed..], "")
                .unwrap()
                .unwrap()
                .0
                .url,
            "/next"
        );
        assert!(
            parse_request(&first[..first.len() - 1], "")
                .unwrap()
                .is_none()
        );
    }
    #[test]
    fn rejects_invalid_chunk_delimiter() {
        assert!(
            parse_request(
                b"POST / HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n1\r\naXX",
                ""
            )
            .is_err()
        );
    }
}
