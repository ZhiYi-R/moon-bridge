use std::io;
use std::net::{SocketAddr, TcpListener, TcpStream};
use std::time::Duration;

const DEFAULT_HOST: &str = "127.0.0.1";
const DEFAULT_PORT: u16 = 38440;
const PORT_SCAN_LIMIT: u16 = 32;

/// Pick `127.0.0.1:38440` when free; otherwise the next free ports in range.
/// Reuses the default port only when it already serves the moonbridge console (HTTP 200).
pub fn pick_listen_addr() -> io::Result<String> {
    for offset in 0..PORT_SCAN_LIMIT {
        let port = DEFAULT_PORT.saturating_add(offset);
        if port_available(DEFAULT_HOST, port)? {
            return Ok(format!("{DEFAULT_HOST}:{port}"));
        }
        // Only the default port is eligible for attach-to-existing.
        if offset == 0
            && is_console_ready(&format!("{DEFAULT_HOST}:{port}"), Duration::from_millis(200))
        {
            return Ok(format!("{DEFAULT_HOST}:{port}"));
        }
    }
    Err(io::Error::new(
        io::ErrorKind::AddrInUse,
        format!("no free port near {DEFAULT_HOST}:{DEFAULT_PORT}"),
    ))
}

fn port_available(host: &str, port: u16) -> io::Result<bool> {
    let addr: SocketAddr = format!("{host}:{port}")
        .parse()
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidInput, e))?;
    match TcpListener::bind(addr) {
        Ok(listener) => {
            drop(listener);
            Ok(true)
        }
        Err(err) if err.kind() == io::ErrorKind::AddrInUse => Ok(false),
        Err(err) => Err(err),
    }
}

/// True when `/console/` returns HTTP 200 (embedded console, no auth required).
pub fn is_console_ready(addr: &str, timeout: Duration) -> bool {
    match http_get_status(addr, "/console/", timeout) {
        Ok(status) => status == 200,
        Err(_) => false,
    }
}

/// Minimal HTTP/1.0 GET that returns the status code. Avoids pulling reqwest for a one-shot probe.
fn http_get_status(addr: &str, path: &str, timeout: Duration) -> io::Result<u16> {
    use std::io::{Read, Write};

    let socket_addr: SocketAddr = addr
        .parse()
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidInput, e))?;
    let mut stream = TcpStream::connect_timeout(&socket_addr, timeout)?;
    stream.set_read_timeout(Some(timeout))?;
    stream.set_write_timeout(Some(timeout))?;

    let request = format!(
        "GET {path} HTTP/1.0\r\nHost: {addr}\r\nConnection: close\r\nUser-Agent: moonbridge-desktop\r\n\r\n"
    );
    stream.write_all(request.as_bytes())?;

    let mut buf = [0u8; 128];
    let n = stream.read(&mut buf)?;
    if n == 0 {
        return Err(io::Error::new(io::ErrorKind::UnexpectedEof, "empty response"));
    }
    let text = String::from_utf8_lossy(&buf[..n]);
    // Status line: HTTP/1.x 200 ...
    let status = text
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        .and_then(|code| code.parse::<u16>().ok())
        .ok_or_else(|| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("bad status line: {text}"),
            )
        })?;
    Ok(status)
}

pub fn console_url(addr: &str) -> String {
    format!("http://{addr}/console/")
}

/// True when `url` is a loopback console URL we are willing to navigate to.
pub fn is_safe_console_url(url: &str) -> bool {
    let Ok(parsed) = url::Url::parse(url) else {
        return false;
    };
    let host_ok = matches!(
        parsed.host_str(),
        Some("127.0.0.1") | Some("localhost") | Some("[::1]") | Some("::1")
    );
    let path = parsed.path();
    host_ok && (path == "/console" || path.starts_with("/console/"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn console_url_includes_path() {
        assert_eq!(
            console_url("127.0.0.1:38440"),
            "http://127.0.0.1:38440/console/"
        );
    }

    #[test]
    fn safe_console_url_accepts_loopback_console() {
        assert!(is_safe_console_url("http://127.0.0.1:38440/console/"));
        assert!(is_safe_console_url("http://localhost:38441/console/"));
        assert!(!is_safe_console_url("http://example.com/console/"));
        assert!(!is_safe_console_url("http://127.0.0.1:38440/api/v1/status"));
    }
}
