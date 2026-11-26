use std::process::Command;

use tauri::WebviewWindow;
use url::Url;

const MAX_EXTERNAL_URL_LEN: usize = 2048;

/// 仅允许合法的 http/https 外链地址，拒绝本地文件或脚本协议。
pub(crate) fn parse_external_http_url(raw: &str) -> Result<Url, String> {
    if raw.is_empty()
        || raw.len() > MAX_EXTERNAL_URL_LEN
        || raw != raw.trim()
        || raw.chars().any(char::is_control)
    {
        return Err("INVALID_EXTERNAL_URL".to_string());
    }
    let parsed = Url::parse(raw).map_err(|_| "INVALID_EXTERNAL_URL".to_string())?;
    if !matches!(parsed.scheme(), "http" | "https") || parsed.host_str().is_none() {
        return Err("UNSUPPORTED_EXTERNAL_URL_SCHEME".to_string());
    }
    Ok(parsed)
}

/// 直接调用系统默认浏览器打开已校验 URL，不经过 shell 拼接。
pub(crate) fn spawn_system_browser(url: &Url) -> Result<(), String> {
    let target = url.as_str();
    #[cfg(target_os = "macos")]
    let mut command = {
        let mut cmd = Command::new("open");
        cmd.arg(target);
        cmd
    };
    #[cfg(target_os = "linux")]
    let mut command = {
        let mut cmd = Command::new("xdg-open");
        cmd.arg(target);
        cmd
    };
    #[cfg(target_os = "windows")]
    let mut command = {
        let mut cmd = Command::new("rundll32");
        cmd.args(["url.dll,FileProtocolHandler", target]);
        cmd
    };
    #[cfg(not(any(target_os = "macos", target_os = "linux", target_os = "windows")))]
    {
        let _ = target;
        return Err("UNSUPPORTED_PLATFORM".to_string());
    }
    command
        .spawn()
        .map(|_| ())
        .map_err(|_| "OPEN_EXTERNAL_URL_FAILED".to_string())
}

#[tauri::command]
pub async fn open_external_url(window: WebviewWindow, url: String) -> Result<(), String> {
    if window.label() != "main" {
        return Err("EXTERNAL_URL_UNAVAILABLE".to_string());
    }
    let parsed = parse_external_http_url(&url)?;
    tauri::async_runtime::spawn_blocking(move || spawn_system_browser(&parsed))
        .await
        .map_err(|_| "OPEN_EXTERNAL_URL_FAILED".to_string())?
}

#[cfg(test)]
mod tests {
    use super::parse_external_http_url;

    #[test]
    fn accepts_http_and_https_urls() {
        assert!(parse_external_http_url("http://198.51.100.10:8080/health?ok=1").is_ok());
        assert!(parse_external_http_url("https://example.test/api/v1").is_ok());
    }

    #[test]
    fn rejects_non_http_and_malformed_urls() {
        for invalid in [
            "",
            "   https://example.test",
            "https://example.test ",
            "file:///etc/hosts",
            "javascript:alert(1)",
            "ssh://example.test",
            "http://",
            "https://example.test/\nfoo",
        ] {
            assert!(
                parse_external_http_url(invalid).is_err(),
                "expected invalid URL to be rejected: {invalid:?}"
            );
        }
        let oversized = format!("https://example.test/{}", "a".repeat(2048));
        assert!(parse_external_http_url(&oversized).is_err());
    }
}
