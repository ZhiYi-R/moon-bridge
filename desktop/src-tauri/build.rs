fn main() {
    // Expose cargo target triple to rustc for sidecar path resolution.
    if let Ok(target) = std::env::var("TARGET") {
        println!("cargo:rustc-env=TARGET={target}");
    }
    tauri_build::build()
}
