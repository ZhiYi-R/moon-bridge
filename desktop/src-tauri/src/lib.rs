mod port;
mod sidecar;

use std::time::Duration;

use tauri::{AppHandle, Manager, RunEvent, WebviewUrl, WebviewWindowBuilder};
use url::Url;

use port::is_safe_console_url;
use sidecar::SidecarState;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .manage(SidecarState::new())
        .setup(|app| {
            sidecar::install_signal_cleanup();
            let handle = app.handle().clone();
            // Start sidecar on a background thread so the loading page can paint first.
            std::thread::spawn(move || {
                // Brief delay so the splash HTML is visible.
                std::thread::sleep(Duration::from_millis(50));
                boot_sidecar_and_navigate(&handle);
            });
            Ok(())
        })
        .build(tauri::generate_context!())
        .expect("error while building Moon Bridge desktop")
        .run(|app_handle, event| {
            match event {
                RunEvent::Exit | RunEvent::ExitRequested { .. } => {
                    if let Some(state) = app_handle.try_state::<SidecarState>() {
                        state.shutdown();
                    }
                }
                _ => {}
            }
        });
}

fn boot_sidecar_and_navigate(app: &AppHandle) {
    let state = app.state::<SidecarState>();
    match state.start(app) {
        Ok(started) => {
            eprintln!(
                "moonbridge ready at {} (reused_existing={}, log={})",
                started.addr,
                started.reused_existing,
                started.log_path.display()
            );
            if started.reused_existing {
                eprintln!(
                    "attached to existing moonbridge at {}; desktop will not stop it on exit",
                    started.addr
                );
            }
            navigate_main(app, &started.console_url);
        }
        Err(err) => {
            eprintln!("sidecar start failed: {err}");
            show_error_page(app, &err.to_string());
        }
    }
}

fn navigate_main(app: &AppHandle, url: &str) {
    if !is_safe_console_url(url) {
        show_error_page(
            app,
            &format!("refusing to navigate to non-loopback console url: {url}"),
        );
        return;
    }

    let parsed = match Url::parse(url) {
        Ok(u) => u,
        Err(e) => {
            show_error_page(app, &format!("invalid console url {url}: {e}"));
            return;
        }
    };

    if let Some(window) = app.get_webview_window("main") {
        if let Err(e) = window.navigate(parsed) {
            eprintln!("navigate failed: {e}");
            show_error_page(app, &format!("navigate failed: {e}"));
        }
        let _ = window.set_focus();
        return;
    }

    // Fallback: create the main window if config window is missing.
    if let Err(e) = WebviewWindowBuilder::new(app, "main", WebviewUrl::External(parsed))
        .title("Moon Bridge")
        .inner_size(1280.0, 840.0)
        .build()
    {
        eprintln!("create window failed: {e}");
        show_error_page(app, &format!("create window failed: {e}"));
    }
}

fn show_error_page(app: &AppHandle, message: &str) {
    let escaped = html_escape(message);
    let html = format!(
        r#"<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <title>Moon Bridge — 启动失败</title>
  <style>
    body {{
      margin: 0; min-height: 100vh; display: grid; place-items: center;
      font-family: system-ui, sans-serif; background: #f4f1ea; color: #111;
    }}
    .card {{
      width: min(520px, calc(100vw - 32px)); padding: 24px;
      border: 2px solid #111; background: #fff; box-shadow: 6px 6px 0 #111;
    }}
    h1 {{ margin: 0 0 12px; font-size: 1.35rem; }}
    pre {{
      white-space: pre-wrap; word-break: break-word; color: #e30613;
      background: #faf7f2; padding: 12px; border: 1px solid #111;
    }}
    p {{ color: #444; line-height: 1.5; }}
  </style>
</head>
<body>
  <main class="card">
    <h1>无法启动 Moon Bridge</h1>
    <p>本地 sidecar 未能就绪。请确认已构建 sidecar，并查看日志。</p>
    <pre>{escaped}</pre>
    <p>构建命令：<code>make desktop-sidecar</code> 或 <code>npm --prefix desktop run sidecar</code></p>
  </main>
</body>
</html>"#
    );

    // data: URL navigation for the error surface
    let data_url = format!("data:text/html;charset=utf-8,{}", urlencoding_minimal(&html));
    if let Ok(parsed) = Url::parse(&data_url) {
        if let Some(window) = app.get_webview_window("main") {
            if let Err(e) = window.navigate(parsed) {
                eprintln!("error page navigate failed: {e}");
            }
        }
    }
}

fn html_escape(input: &str) -> String {
    input
        .replace('&', "&amp;")
        .replace('<', "&lt;")
        .replace('>', "&gt;")
        .replace('"', "&quot;")
}

fn urlencoding_minimal(input: &str) -> String {
    let mut out = String::with_capacity(input.len() * 3);
    for b in input.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            // data: URLs must use %20; '+' is form-urlencoded only.
            b' ' => out.push_str("%20"),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::urlencoding_minimal;

    #[test]
    fn data_url_encoding_uses_percent_twenty_for_space() {
        assert_eq!(urlencoding_minimal("a b"), "a%20b");
        assert!(!urlencoding_minimal("min-height: 100vh").contains('+'));
    }
}
