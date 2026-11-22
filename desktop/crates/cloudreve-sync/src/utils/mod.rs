pub mod app;
#[cfg(windows)]
pub mod toast;
#[cfg(target_os = "linux")]
#[path = "toast_linux.rs"]
pub mod toast;
