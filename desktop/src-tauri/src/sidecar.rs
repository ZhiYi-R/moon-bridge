use std::fs::{self, File, OpenOptions};
use std::io::{self, Write};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::atomic::{AtomicI32, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

use tauri::{AppHandle, Manager};

use crate::port::{console_url, is_console_ready, pick_listen_addr};

const READY_TIMEOUT: Duration = Duration::from_secs(30);

/// Process-group id of the owned sidecar (0 = none). Used by POSIX signal handlers
/// so abrupt SIGTERM/SIGINT still tears down moonbridge.
static OWNED_SIDECAR_PGID: AtomicI32 = AtomicI32::new(0);

#[derive(Debug, thiserror::Error)]
pub enum SidecarError {
    #[error("io: {0}")]
    Io(#[from] io::Error),
    #[error("{0}")]
    Message(String),
}

pub struct SidecarState {
    inner: Mutex<Option<RunningSidecar>>,
}

struct RunningSidecar {
    /// Present only when this app process owns the moonbridge child.
    child: Option<Child>,
    addr: String,
    /// true when we attached to an already-running moonbridge (do not kill on exit)
    external: bool,
    log_path: PathBuf,
}

pub struct StartResult {
    pub addr: String,
    pub console_url: String,
    pub log_path: PathBuf,
    pub reused_existing: bool,
}

impl SidecarState {
    pub fn new() -> Self {
        Self {
            inner: Mutex::new(None),
        }
    }

    pub fn start(&self, app: &AppHandle) -> Result<StartResult, SidecarError> {
        // Phase 1: short critical section — reuse existing owned state if still healthy.
        {
            let mut guard = self
                .inner
                .lock()
                .map_err(|_| SidecarError::Message("sidecar lock poisoned".into()))?;

            if let Some(running) = guard.as_mut() {
                if is_console_ready(&running.addr, Duration::from_millis(300)) {
                    return Ok(StartResult {
                        addr: running.addr.clone(),
                        console_url: console_url(&running.addr),
                        log_path: running.log_path.clone(),
                        reused_existing: running.external,
                    });
                }
                // Stale/unhealthy prior handle: tear down before replacing.
                if let Some(mut old) = guard.take() {
                    if !old.external {
                        if let Some(mut child) = old.child.take() {
                            terminate_child(&mut child);
                        }
                    }
                    clear_owned_pgid();
                }
            }
        }

        let log_path = sidecar_log_path(app)?;
        if let Some(parent) = log_path.parent() {
            fs::create_dir_all(parent)?;
        }

        let addr = pick_listen_addr().map_err(|e| {
            SidecarError::Message(format!("failed to pick listen address: {e}"))
        })?;

        // Attach to an existing healthy moonbridge (do not own lifecycle).
        if is_console_ready(&addr, Duration::from_millis(400)) {
            let result = StartResult {
                console_url: console_url(&addr),
                log_path: log_path.clone(),
                addr: addr.clone(),
                reused_existing: true,
            };
            let mut guard = self
                .inner
                .lock()
                .map_err(|_| SidecarError::Message("sidecar lock poisoned".into()))?;
            *guard = Some(RunningSidecar {
                child: None,
                addr,
                external: true,
                log_path,
            });
            return Ok(result);
        }

        let binary = resolve_sidecar_binary(app)?;
        if !binary.exists() {
            return Err(SidecarError::Message(format!(
                "sidecar binary not found at {}\nRun: make desktop-sidecar  (or npm --prefix desktop run sidecar)",
                binary.display()
            )));
        }

        let log_file = open_log_file(&log_path)?;
        let log_err = log_file.try_clone()?;
        {
            let mut header = open_log_file(&log_path)?;
            writeln!(
                header,
                "\n---- moonbridge sidecar start {} addr={} bin={} ----",
                chrono_like_now(),
                addr,
                binary.display()
            )?;
        }

        let mut cmd = Command::new(&binary);
        cmd.arg("-addr")
            .arg(&addr)
            .stdin(Stdio::null())
            .stdout(Stdio::from(log_file))
            .stderr(Stdio::from(log_err));

        // Put the child in its own process group so we can tear it down reliably.
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            cmd.process_group(0);
        }

        // Inherit default config resolution ($HOME/moonbridge/config.yml) unless overridden.
        if let Ok(config) = std::env::var("MOONBRIDGE_CONFIG") {
            if !config.trim().is_empty() {
                cmd.arg("-config").arg(config);
            }
        }

        let mut child = cmd.spawn().map_err(|e| {
            SidecarError::Message(format!(
                "failed to spawn moonbridge at {}: {e}",
                binary.display()
            ))
        })?;

        remember_owned_pgid(child.id());

        // Phase 2: wait without holding the mutex so Exit can still call shutdown.
        if let Err(e) = wait_for_child_ready(&mut child, &addr, READY_TIMEOUT) {
            terminate_child(&mut child);
            clear_owned_pgid();
            return Err(SidecarError::Message(format!(
                "{e}\nSee log: {}",
                log_path.display()
            )));
        }

        let result = StartResult {
            console_url: console_url(&addr),
            log_path: log_path.clone(),
            addr: addr.clone(),
            reused_existing: false,
        };

        let mut guard = self
            .inner
            .lock()
            .map_err(|_| SidecarError::Message("sidecar lock poisoned".into()))?;
        *guard = Some(RunningSidecar {
            child: Some(child),
            addr,
            external: false,
            log_path,
        });

        Ok(result)
    }

    pub fn shutdown(&self) {
        let mut guard = match self.inner.lock() {
            Ok(g) => g,
            Err(_) => return,
        };
        if let Some(mut running) = guard.take() {
            if running.external {
                clear_owned_pgid();
                return;
            }
            if let Some(mut child) = running.child.take() {
                terminate_child(&mut child);
            }
        }
        clear_owned_pgid();
    }
}

/// Install SIGINT/SIGTERM handlers that kill the owned sidecar process group.
/// Safe to call once during app setup.
pub fn install_signal_cleanup() {
    #[cfg(unix)]
    {
        extern "C" fn on_signal(sig: i32) {
            force_kill_owned_sidecar();
            // Restore default and re-raise so the process still exits.
            unsafe {
                libc_signal_raw(sig, 0); // SIG_DFL
                libc_raise(sig);
            }
        }
        // SAFETY: handlers only call async-signal-safe kill/signal/raise.
        unsafe {
            libc_signal_raw(2, on_signal as *const () as usize); // SIGINT
            libc_signal_raw(15, on_signal as *const () as usize); // SIGTERM
        }
    }
}

fn wait_for_child_ready(
    child: &mut Child,
    addr: &str,
    timeout: Duration,
) -> Result<(), String> {
    let deadline = Instant::now() + timeout;
    loop {
        if is_console_ready(addr, Duration::from_millis(400)) {
            return Ok(());
        }
        match child.try_wait() {
            Ok(Some(status)) => {
                return Err(format!(
                    "moonbridge exited before ready ({status})"
                ));
            }
            Ok(None) => {}
            Err(e) => {
                return Err(format!("failed to poll moonbridge process: {e}"));
            }
        }
        if Instant::now() >= deadline {
            return Err(format!(
                "timeout waiting for moonbridge at {addr} ({}s)",
                timeout.as_secs()
            ));
        }
        std::thread::sleep(Duration::from_millis(200));
    }
}

fn remember_owned_pgid(pid: u32) {
    OWNED_SIDECAR_PGID.store(pid as i32, Ordering::SeqCst);
}

fn clear_owned_pgid() {
    OWNED_SIDECAR_PGID.store(0, Ordering::SeqCst);
}

fn force_kill_owned_sidecar() {
    let pgid = OWNED_SIDECAR_PGID.swap(0, Ordering::SeqCst);
    if pgid > 0 {
        #[cfg(unix)]
        {
            // Best-effort emergency path: TERM then KILL (async-signal-safe only).
            libc_kill(-pgid, 15);
            libc_kill(-pgid, 9);
        }
    }
}

impl Default for SidecarState {
    fn default() -> Self {
        Self::new()
    }
}

impl Drop for SidecarState {
    fn drop(&mut self) {
        self.shutdown();
    }
}

fn terminate_child(child: &mut Child) {
    let pid = child.id();

    #[cfg(unix)]
    {
        // Negative pid = process group (we started the child with process_group(0)).
        libc_kill(-(pid as i32), 15); // SIGTERM
        let deadline = Instant::now() + Duration::from_secs(3);
        while Instant::now() < deadline {
            match child.try_wait() {
                Ok(Some(_)) => return,
                Ok(None) => std::thread::sleep(Duration::from_millis(50)),
                Err(_) => break,
            }
        }
        libc_kill(-(pid as i32), 9); // SIGKILL process group
    }

    let _ = child.kill();
    let _ = child.wait();
}

#[cfg(unix)]
fn libc_kill(pid: i32, sig: i32) {
    extern "C" {
        fn kill(pid: i32, sig: i32) -> i32;
    }
    // SAFETY: standard POSIX kill; negative pid targets process group.
    unsafe {
        let _ = kill(pid, sig);
    }
}

#[cfg(unix)]
unsafe fn libc_signal_raw(sig: i32, handler: usize) {
    extern "C" {
        fn signal(sig: i32, handler: usize) -> usize;
    }
    // handler: 0 = SIG_DFL, function pointer otherwise (platform ABI).
    let _ = signal(sig, handler);
}

#[cfg(unix)]
unsafe fn libc_raise(sig: i32) {
    extern "C" {
        fn raise(sig: i32) -> i32;
    }
    let _ = raise(sig);
}

fn open_log_file(path: &PathBuf) -> io::Result<File> {
    let mut opts = OpenOptions::new();
    opts.create(true).append(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        opts.mode(0o600);
    }
    opts.open(path)
}

fn sidecar_log_path(app: &AppHandle) -> Result<PathBuf, SidecarError> {
    let dir = app
        .path()
        .app_log_dir()
        .map_err(|e| SidecarError::Message(format!("app log dir: {e}")))?;
    Ok(dir.join("moonbridge-sidecar.log"))
}

fn resolve_sidecar_binary(app: &AppHandle) -> Result<PathBuf, SidecarError> {
    if let Ok(path) = std::env::var("MOONBRIDGE_SIDECAR") {
        let p = PathBuf::from(path.trim());
        if p.as_os_str().is_empty() {
            // fall through
        } else if !p.is_absolute() {
            return Err(SidecarError::Message(format!(
                "MOONBRIDGE_SIDECAR must be an absolute path, got: {}",
                p.display()
            )));
        } else if p.exists() {
            return Ok(p);
        } else {
            return Err(SidecarError::Message(format!(
                "MOONBRIDGE_SIDECAR points to missing file: {}",
                p.display()
            )));
        }
    }

    let triple = host_target_triple();
    let dev_candidates = [
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join(format!("moonbridge-{triple}")),
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join("moonbridge"),
    ];
    for candidate in &dev_candidates {
        if candidate.exists() {
            return Ok(candidate.clone());
        }
        #[cfg(windows)]
        {
            let with_exe = candidate.with_extension("exe");
            if with_exe.exists() {
                return Ok(with_exe);
            }
        }
    }

    if let Ok(exe) = std::env::current_exe() {
        if let Some(dir) = exe.parent() {
            let bundled = dir.join(sidecar_file_name());
            if bundled.exists() {
                return Ok(bundled);
            }
            if let Some(parent) = dir.parent() {
                let resources = parent.join("Resources").join(sidecar_file_name());
                if resources.exists() {
                    return Ok(resources);
                }
            }
        }
    }

    if let Ok(path) = app.path().resolve(
        format!("binaries/{}", sidecar_file_name()),
        tauri::path::BaseDirectory::Resource,
    ) {
        if path.exists() {
            return Ok(path);
        }
    }

    Ok(dev_candidates[0].clone())
}

fn sidecar_file_name() -> String {
    #[cfg(windows)]
    {
        "moonbridge.exe".into()
    }
    #[cfg(not(windows))]
    {
        "moonbridge".into()
    }
}

fn host_target_triple() -> String {
    if let Some(target) = option_env!("TARGET") {
        return target.to_string();
    }
    #[cfg(all(target_os = "macos", target_arch = "aarch64"))]
    {
        return "aarch64-apple-darwin".into();
    }
    #[cfg(all(target_os = "macos", target_arch = "x86_64"))]
    {
        return "x86_64-apple-darwin".into();
    }
    #[cfg(all(target_os = "linux", target_arch = "aarch64"))]
    {
        return "aarch64-unknown-linux-gnu".into();
    }
    #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
    {
        return "x86_64-unknown-linux-gnu".into();
    }
    #[cfg(all(target_os = "windows", target_arch = "x86_64"))]
    {
        return "x86_64-pc-windows-msvc".into();
    }
    #[cfg(all(target_os = "windows", target_arch = "aarch64"))]
    {
        return "aarch64-pc-windows-msvc".into();
    }
    #[cfg(not(any(
        all(target_os = "macos", target_arch = "aarch64"),
        all(target_os = "macos", target_arch = "x86_64"),
        all(target_os = "linux", target_arch = "aarch64"),
        all(target_os = "linux", target_arch = "x86_64"),
        all(target_os = "windows", target_arch = "x86_64"),
        all(target_os = "windows", target_arch = "aarch64"),
    )))]
    {
        "unknown".into()
    }
}

fn chrono_like_now() -> String {
    use std::time::SystemTime;
    match SystemTime::now().duration_since(SystemTime::UNIX_EPOCH) {
        Ok(d) => format!("unix={}", d.as_secs()),
        Err(_) => "unix=unknown".into(),
    }
}
