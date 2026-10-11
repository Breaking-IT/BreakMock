// Copyright 2026 Breaking-IT. Licensed under Apache-2.0.
use base64::{Engine, engine::general_purpose::STANDARD};
use breakmock_rust::{Request, Response, State, admin, messaging_runtime as messages};
use serde_json::{Value, json};
use sha1::{Digest, Sha1};
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpStream, tcp::OwnedReadHalf},
    sync::mpsc,
};
#[derive(Clone, Copy)]
pub struct Options {
    pub idle: u64,
    pub text: u64,
    pub binary: u64,
}
fn register(s: &mut State, req: &Request, typ: &str, tx: mpsc::Sender<Value>) -> String {
    let id = uuid::Uuid::new_v4().to_string();
    let mut temp = State::default();
    admin::record(req, &Response::new(101, Vec::new()), None, &mut temp);
    let mut logged = temp
        .requests
        .front()
        .map(|e| e["request"].clone())
        .unwrap_or(json!({}));
    if let Some(m) = logged.as_object_mut() {
        m.retain(|_, v| {
            v != &json!(false)
                && v != &json!(-1)
                && !v.as_array().is_some_and(|a| a.is_empty())
                && !v.as_object().is_some_and(|a| a.is_empty())
        });
    }
    let channel = json!({"type":"websocket","id":id,"open":true,"initiatingRequest":logged});
    s.channels.push(channel);
    s.channel_requests.insert(id.clone(), req.clone());
    s.channel_types.insert(id.clone(), typ.into());
    s.channel_senders.insert(id.clone(), tx);
    id
}
pub async fn serve(
    mut stream: TcpStream,
    state: Arc<Mutex<State>>,
    req: Request,
    kind: &str,
    options: Options,
) -> std::io::Result<()> {
    if kind == "websocket"
        && (req.method != "GET"
            || req.header("Sec-WebSocket-Version") != Some("13")
            || !req
                .header("Upgrade")
                .is_some_and(|v| v.eq_ignore_ascii_case("websocket")))
    {
        stream
            .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
            .await?;
        return Ok(());
    }
    if kind == "websocket" && req.header("Sec-WebSocket-Key").unwrap_or("").is_empty() {
        let mut response = crate::jetty_error(&req, "Missing request header 'Sec-WebSocket-Key'");
        response.status = 400;
        response.body = String::from_utf8_lossy(&response.body)
            .replace("500", "400")
            .into_bytes();
        stream
            .write_all(&crate::transport::serialize_response(&req, &response, true))
            .await?;
        return Ok(());
    }
    let (tx, mut rx) = mpsc::channel(64);
    let id = register(&mut state.lock().unwrap(), &req, kind, tx);
    let _cleanup = ChannelCleanup {
        state: state.clone(),
        id: id.clone(),
    };
    if kind == "sse" {
        stream.write_all(b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nCache-Control: no-cache\r\n\r\n: ok\r\n\r\n").await?;
        while let Some(msg) = rx.recv().await {
            if msg["_close"] == true {
                break;
            }
            if stream.write_all(sse_frame(&msg).as_bytes()).await.is_err() {
                break;
            }
        }
    } else {
        let accept = STANDARD.encode(Sha1::digest(format!(
            "{}258EAFA5-E914-47DA-95CA-C5AB0DC85B11",
            req.header("Sec-WebSocket-Key").unwrap()
        )));
        let compressed = req
            .header("Sec-WebSocket-Extensions")
            .is_some_and(|s| s.contains("permessage-deflate"));
        let ext = if compressed {
            "Sec-WebSocket-Extensions: permessage-deflate; server_no_context_takeover; client_no_context_takeover\r\n"
        } else {
            ""
        };
        stream.write_all(format!("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\n{ext}\r\n").as_bytes()).await?;
        let (mut reader, mut writer) = stream.into_split();
        let (frames_tx, mut frames_rx) = mpsc::channel(1);
        let task = tokio::spawn(async move {
            loop {
                let next = if options.idle == 0 {
                    read_frame(&mut reader, compressed, options).await
                } else {
                    match tokio::time::timeout(
                        Duration::from_millis(options.idle),
                        read_frame(&mut reader, compressed, options),
                    )
                    .await
                    {
                        Ok(v) => v,
                        Err(_) => Err((1001, "Connection Idle Timeout".into())),
                    }
                };
                let stop = next.is_err();
                if frames_tx.send(next).await.is_err() || stop {
                    break;
                }
            }
        });
        let _reader_cleanup = AbortReader(task.abort_handle());
        let mut fragmented = Vec::new();
        let mut opcode = 0u8;
        let mut deflated = false;
        loop {
            let bytes = tokio::select! {
             msg=rx.recv()=>{let Some(msg)=msg else{break};if msg["_close"]==true{Some(close_frame(1000,"Closed by server"))}else{let(binary,data)=if let Some(v)=msg["base64Body"].as_str(){(true,STANDARD.decode(v).unwrap_or_default())}else{(false,msg["body"].as_str().unwrap_or("").as_bytes().to_vec())};Some(data_frames(if binary{2}else{1},&data))}},
             incoming_frame=frames_rx.recv()=>{match incoming_frame{
              None=>break,Some(Err((0,_)))=>break,Some(Err((code,reason)))=>Some(close_frame(code,&reason)),
              Some(Ok((op,fin,rsv,data)))=>{match op{
               8=>Some(close_frame(1000,"Closed by server")),9=>Some(frame(10,true,&data)),10=>None,
               _=>{if op!=0{opcode=op;deflated=rsv;fragmented.clear()}if let Err(reason)=check_fragment_size(fragmented.len(),data.len(),opcode,deflated,options) {Some(close_frame(1009,&reason))} else {fragmented.extend(data);if !fin{None}else{
                let mut body=std::mem::take(&mut fragmented);if deflated{body.extend([0,0,255,255]);let mut dec=flate2::Decompress::new(false);let mut decoded=Vec::new();let mut consumed=0;loop{let mut chunk=[0u8;8192];let before=dec.total_in();let before_out=dec.total_out();let result=dec.decompress(&body[consumed..],&mut chunk,flate2::FlushDecompress::Sync);consumed+=(dec.total_in()-before)as usize;let count=(dec.total_out()-before_out)as usize;decoded.extend_from_slice(&chunk[..count]);let limit=if opcode==2{options.binary}else{options.text};if (limit>0&&decoded.len()as u64>limit)||decoded.len()>64*1024*1024{break}if count==0||result.is_err()||consumed>=body.len(){break}}body=decoded;}
                let limit=if opcode==2{options.binary}else{options.text};if body.len()>64*1024*1024 {Some(close_frame(1009,"Message too large"))}else if limit>0&&body.len()as u64>limit{Some(close_frame(1009,&format!("{} message too large: {} > {}",if opcode==2{"Binary"}else{"Text"},group(if deflated{(body.len().min(8192))as u64}else{body.len()as u64}),group(limit))))}else if opcode==1&&std::str::from_utf8(&body).is_err(){Some(close_frame(1007,"Invalid UTF-8"))}else{
                 let message=if opcode==2{json!({"base64Body":STANDARD.encode(body)})}else{json!({"body":String::from_utf8_lossy(&body)})};let result=messages::incoming(&mut state.lock().unwrap(),&id,message);match result{Ok(())=>None,Err(e)=>Some(close_frame(1011,&e))}
                }
               }}}
              }}
             }}
            };
            if let Some(bytes) = bytes {
                let close = bytes.first() == Some(&0x88);
                let result = if options.idle > 0 {
                    tokio::time::timeout(
                        Duration::from_millis(options.idle),
                        writer.write_all(&bytes),
                    )
                    .await
                    .unwrap_or_else(|_| {
                        Err(std::io::Error::new(std::io::ErrorKind::TimedOut, "write"))
                    })
                } else {
                    writer.write_all(&bytes).await
                };
                if result.is_err() || close {
                    break;
                }
            }
        }
        task.abort();
    }
    Ok(())
}
fn group(n: u64) -> String {
    let raw = n.to_string();
    let mut out = String::new();
    for (i, c) in raw.chars().enumerate() {
        if i > 0 && (raw.len() - i) % 3 == 0 {
            out.push(
                if breakmock_rust::matcher::default_locale().starts_with("en_US") {
                    ','
                } else {
                    '.'
                },
            )
        }
        out.push(c)
    }
    out
}
async fn read_frame(
    r: &mut OwnedReadHalf,
    compression: bool,
    options: Options,
) -> Result<(u8, bool, bool, Vec<u8>), (u16, String)> {
    let mut h = [0u8; 2];
    r.read_exact(&mut h).await.map_err(|_| (0, String::new()))?;
    let op = h[0] & 15;
    let fin = h[0] & 128 != 0;
    let rsv = h[0] & 64 != 0;
    let err = if h[1] & 128 == 0 {
        Some("Client MUST mask all frames (RFC-6455: Section 5.1)".into())
    } else if rsv && !compression {
        Some("RSV1 not allowed to be set".into())
    } else if h[0] & 32 != 0 {
        Some("RSV2 not allowed to be set".into())
    } else if h[0] & 16 != 0 {
        Some("RSV3 not allowed to be set".into())
    } else if ![0, 1, 2, 8, 9, 10].contains(&op) {
        Some(format!("Unknown opcode: {op}"))
    } else if op >= 8 && !fin {
        Some(format!(
            "Fragmented Control Frame [{}]",
            match op {
                8 => "CLOSE",
                9 => "PING",
                _ => "PONG",
            }
        ))
    } else {
        None
    };
    if let Some(e) = err {
        return Err((1002, e));
    }
    let mut len = (h[1] & 127) as u64;
    if len == 126 {
        let mut b = [0; 2];
        r.read_exact(&mut b).await.map_err(|_| (0, String::new()))?;
        len = u16::from_be_bytes(b) as u64
    } else if len == 127 {
        let mut b = [0; 8];
        r.read_exact(&mut b).await.map_err(|_| (0, String::new()))?;
        len = u64::from_be_bytes(b)
    }
    if op == 8 && len == 1 {
        return Err((1002, "Invalid CLOSE payload".into()));
    }
    let limit = if op == 2 {
        options.binary
    } else {
        options.text
    };
    if !rsv && matches!(op, 1 | 2) && limit > 0 && len > limit {
        return Err((
            1009,
            format!(
                "{} message too large: {} > {}",
                if op == 2 { "Binary" } else { "Text" },
                group(len),
                group(limit)
            ),
        ));
    }
    if len > 64 * 1024 * 1024 {
        return Err((1009, "Frame too large".into()));
    }
    let mut mask = [0; 4];
    r.read_exact(&mut mask)
        .await
        .map_err(|_| (0, String::new()))?;
    let mut body = vec![0; len as usize];
    r.read_exact(&mut body)
        .await
        .map_err(|_| (0, String::new()))?;
    for (i, b) in body.iter_mut().enumerate() {
        *b ^= mask[i % 4]
    }
    Ok((op, fin, rsv, body))
}
fn frame(op: u8, fin: bool, data: &[u8]) -> Vec<u8> {
    let mut b = vec![op | if fin { 128 } else { 0 }];
    if data.len() < 126 {
        b.push(data.len() as u8)
    } else if data.len() <= 65535 {
        b.push(126);
        b.extend_from_slice(&(data.len() as u16).to_be_bytes())
    } else {
        b.push(127);
        b.extend_from_slice(&(data.len() as u64).to_be_bytes())
    }
    b.extend_from_slice(data);
    b
}
fn data_frames(op: u8, data: &[u8]) -> Vec<u8> {
    if data.len() <= 65536 {
        return frame(op, true, data);
    }
    let mut out = Vec::new();
    let count = data.len().div_ceil(65536);
    for (i, chunk) in data.chunks(65536).enumerate() {
        out.extend(frame(if i == 0 { op } else { 0 }, i + 1 == count, chunk))
    }
    out
}
fn close_frame(code: u16, reason: &str) -> Vec<u8> {
    let mut end = reason.len().min(123);
    while !reason.is_char_boundary(end) {
        end -= 1
    }
    let mut data = code.to_be_bytes().to_vec();
    data.extend_from_slice(reason[..end].as_bytes());
    frame(8, true, &data)
}
fn sse_frame(msg: &Value) -> String {
    let mut out = String::new();
    for (key, prefix) in [
        ("comment", ": "),
        ("id", "id: "),
        ("event", "event: "),
        ("retry", "retry: "),
    ] {
        let mut v = msg["headers"]
            .as_object()
            .and_then(|h| {
                h.iter()
                    .find(|(k, _)| k.eq_ignore_ascii_case(key))
                    .map(|(_, v)| v)
            })
            .unwrap_or(&Value::Null);
        if let Some(a) = v.as_array() {
            v = a.first().unwrap_or(&Value::Null)
        }
        if let Some(v) = v.as_str() {
            out.push_str(prefix);
            out.push_str(v);
            out.push_str("\r\n")
        }
    }
    let data = msg["body"]
        .as_str()
        .or_else(|| msg["base64Body"].as_str())
        .unwrap_or("")
        .replace("\r\n", "\n")
        .replace('\r', "\n");
    for line in data.split('\n') {
        out.push_str("data:");
        if !line.is_empty() {
            out.push(' ');
            out.push_str(line)
        }
        out.push_str("\r\n")
    }
    out.push_str("\r\n");
    out
}

// Cleanup must also run when the handshake fails or the serving future is cancelled.
struct ChannelCleanup {
    state: Arc<Mutex<State>>,
    id: String,
}
impl Drop for ChannelCleanup {
    fn drop(&mut self) {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner());
        messages::close(&mut state, &self.id);
    }
}
struct AbortReader(tokio::task::AbortHandle);
impl Drop for AbortReader {
    fn drop(&mut self) {
        self.0.abort();
    }
}
fn check_fragment_size(
    current: usize,
    additional: usize,
    opcode: u8,
    compressed: bool,
    options: Options,
) -> Result<(), String> {
    let configured = if opcode == 2 {
        options.binary
    } else {
        options.text
    };
    let total = current
        .checked_add(additional)
        .ok_or_else(|| "Message too large".to_string())?;
    if !compressed && configured > 0 && total as u64 > configured {
        return Err(format!(
            "{} message too large: {} > {}",
            if opcode == 2 { "Binary" } else { "Text" },
            group(total as u64),
            group(configured)
        ));
    }
    if total > 64 * 1024 * 1024 {
        return Err("Message too large".into());
    }
    Ok(())
}
#[cfg(test)]
mod streaming_limits_tests {
    use super::*;
    #[test]
    fn accumulated_fragments_are_bounded_before_append() {
        let options = Options {
            idle: 0,
            text: 10,
            binary: 20,
        };
        assert!(check_fragment_size(6, 4, 1, false, options).is_ok());
        assert!(check_fragment_size(6, 5, 1, false, options).is_err());
        assert!(check_fragment_size(6, 5, 2, false, options).is_ok());
        assert!(check_fragment_size(usize::MAX, 1, 2, false, options).is_err());
        assert!(check_fragment_size(64 * 1024 * 1024, 1, 1, true, options).is_err());
    }
    #[test]
    fn channel_cleanup_unregisters_even_without_handshake() {
        let state = Arc::new(Mutex::new(State::default()));
        let (tx, _rx) = mpsc::channel(64);
        let id = register(
            &mut state.lock().unwrap(),
            &Request::default(),
            "websocket",
            tx,
        );
        {
            let _guard = ChannelCleanup {
                state: state.clone(),
                id: id.clone(),
            };
        }
        let state = state.lock().unwrap();
        assert!(!state.channel_senders.contains_key(&id));
        assert!(!state.channel_requests.contains_key(&id));
        assert!(!state.channels.iter().any(|c| c["id"] == id));
    }
}
