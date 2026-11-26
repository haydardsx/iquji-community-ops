use std::collections::{BTreeSet, HashMap};

#[cfg(any(target_os = "macos", target_os = "windows"))]
use std::process::Command;

pub(super) fn environment() -> Vec<(String, String)> {
    #[cfg(target_os = "macos")]
    {
        macos_environment()
    }
    #[cfg(target_os = "windows")]
    {
        windows_environment()
    }
    #[cfg(target_os = "linux")]
    {
        Vec::new()
    }
    #[cfg(not(any(target_os = "macos", target_os = "windows", target_os = "linux")))]
    {
        Vec::new()
    }
}

pub(super) fn loopback_bypass(values: &[(String, String)]) -> String {
    let inherited = [
        std::env::var("NO_PROXY").unwrap_or_default(),
        std::env::var("no_proxy").unwrap_or_default(),
    ];
    compose_loopback_bypass(values, &inherited)
}

fn compose_loopback_bypass(values: &[(String, String)], inherited: &[String]) -> String {
    let system = values
        .iter()
        .filter(|(key, _)| key.eq_ignore_ascii_case("NO_PROXY"))
        .map(|(_, value)| value.as_str())
        .collect::<Vec<_>>();
    let mut entries = BTreeSet::new();
    for item in [
        "localhost",
        ".localhost",
        "127.0.0.0/8",
        "::1",
        "::ffff:127.0.0.0/104",
    ]
    .into_iter()
    .chain(inherited.iter().map(String::as_str))
    .chain(system)
    .flat_map(|value| value.split([',', ';']))
    .map(str::trim)
    .filter(|value| !value.is_empty() && !value.eq_ignore_ascii_case("<local>"))
    {
        entries.insert(item.to_string());
    }
    entries.into_iter().collect::<Vec<_>>().join(",")
}

#[cfg(target_os = "macos")]
fn macos_environment() -> Vec<(String, String)> {
    let Ok(output) = Command::new("/usr/sbin/scutil").arg("--proxy").output() else {
        return Vec::new();
    };
    let text = String::from_utf8_lossy(&output.stdout);
    let (values, exceptions) = parse_macos_proxy_output(&text);
    let mut result = Vec::new();
    for (enabled, host, port, upper, lower) in [
        (
            "HTTPEnable",
            "HTTPProxy",
            "HTTPPort",
            "HTTP_PROXY",
            "http_proxy",
        ),
        (
            "HTTPSEnable",
            "HTTPSProxy",
            "HTTPSPort",
            "HTTPS_PROXY",
            "https_proxy",
        ),
    ] {
        if values.get(enabled).is_some_and(|value| value == "1")
            && let (Some(host), Some(port)) = (values.get(host), values.get(port))
            && let Ok(port) = port.parse::<u16>()
        {
            let host = host.trim_matches(['[', ']']);
            let host = if host.contains(':') {
                format!("[{host}]")
            } else {
                host.to_string()
            };
            let address = format!("http://{host}:{port}");
            result.push((upper.to_string(), address.clone()));
            result.push((lower.to_string(), address));
        }
    }
    if !exceptions.is_empty() {
        let exceptions = exceptions.join(",");
        result.push(("NO_PROXY".to_string(), exceptions));
    }
    result
}

fn parse_macos_proxy_output(output: &str) -> (HashMap<String, String>, Vec<String>) {
    let mut values = HashMap::new();
    let mut exceptions = Vec::new();
    let mut in_exceptions = false;
    for line in output.lines() {
        let line = line.trim();
        if in_exceptions {
            if line == "}" {
                in_exceptions = false;
            } else if let Some((_, value)) = line.split_once(": ") {
                let value = value.trim();
                if !value.is_empty() {
                    exceptions.push(value.to_string());
                }
            }
            continue;
        }
        if let Some((key, value)) = line.split_once(": ") {
            let key = key.trim();
            let value = value.trim();
            if key == "ExceptionsList" {
                in_exceptions = value.starts_with("<array>");
            } else {
                values.insert(key.to_string(), value.to_string());
            }
        }
    }
    (values, exceptions)
}

#[cfg(target_os = "windows")]
fn windows_environment() -> Vec<(String, String)> {
    let Ok(output) = Command::new("reg.exe")
        .args([
            "query",
            r"HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings",
        ])
        .output()
    else {
        return Vec::new();
    };
    if !output.status.success() {
        return Vec::new();
    }
    let values = parse_windows_registry_values(&String::from_utf8_lossy(&output.stdout));
    if !values
        .get("ProxyEnable")
        .is_some_and(|value| proxy_enabled(value))
    {
        return Vec::new();
    }
    let mut result = [
        "HTTP_PROXY",
        "http_proxy",
        "HTTPS_PROXY",
        "https_proxy",
        "ALL_PROXY",
        "all_proxy",
    ]
    .into_iter()
    .map(|key| (key.to_string(), String::new()))
    .collect::<Vec<_>>();
    result.extend(windows_proxy_server_values(
        values.get("ProxyServer").map(String::as_str),
    ));
    if let Some(overrides) = values.get("ProxyOverride") {
        result.push(("NO_PROXY".to_string(), overrides.clone()));
    }
    result
}

#[cfg(any(target_os = "windows", test))]
fn proxy_enabled(value: &str) -> bool {
    let value = value.trim();
    value == "1"
        || value
            .strip_prefix("0x")
            .or_else(|| value.strip_prefix("0X"))
            .and_then(|value| u32::from_str_radix(value, 16).ok())
            == Some(1)
}

#[cfg(any(target_os = "windows", test))]
fn parse_windows_registry_values(output: &str) -> HashMap<String, String> {
    let mut values = HashMap::new();
    for line in output.lines() {
        let line = line.trim();
        let Some(name_end) = line.find(char::is_whitespace) else {
            continue;
        };
        let name = &line[..name_end];
        let rest = line[name_end..].trim_start();
        let Some(kind_end) = rest.find(char::is_whitespace) else {
            continue;
        };
        let kind = &rest[..kind_end];
        let value = rest[kind_end..].trim_start();
        if matches!(kind, "REG_DWORD" | "REG_SZ" | "REG_EXPAND_SZ") {
            values.insert(name.to_string(), value.to_string());
        }
    }
    values
}

#[cfg(any(target_os = "windows", test))]
fn windows_proxy_server_values(server: Option<&str>) -> Vec<(String, String)> {
    let Some(server) = server.map(str::trim).filter(|value| !value.is_empty()) else {
        return Vec::new();
    };
    let mut protocols = HashMap::new();
    let mut fallback = None;
    for item in server
        .split(';')
        .map(str::trim)
        .filter(|item| !item.is_empty())
    {
        if let Some((protocol, value)) = item.split_once('=') {
            protocols.insert(protocol.trim().to_ascii_lowercase(), value.trim());
        } else {
            fallback = Some(item);
        }
    }
    let http = protocols.get("http").copied().or(fallback);
    let https = protocols.get("https").copied().or(http).or(fallback);
    let mut result = Vec::new();
    if let Some(proxy) = http.and_then(normalize_system_proxy_url) {
        result.push(("HTTP_PROXY".to_string(), proxy.clone()));
        result.push(("http_proxy".to_string(), proxy));
    }
    if let Some(proxy) = https.and_then(normalize_system_proxy_url) {
        result.push(("HTTPS_PROXY".to_string(), proxy.clone()));
        result.push(("https_proxy".to_string(), proxy));
    }
    result
}

#[cfg(any(target_os = "windows", test))]
fn normalize_system_proxy_url(value: &str) -> Option<String> {
    let value = if value.contains("://") {
        value.to_string()
    } else {
        format!("http://{value}")
    };
    let url = url::Url::parse(&value).ok()?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || !matches!(url.path(), "" | "/")
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return None;
    }
    Some(url.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_windows_internet_settings_and_preserves_bypass_rules() {
        assert!(proxy_enabled("0x1"));
        assert!(proxy_enabled("0x00000001"));
        assert!(!proxy_enabled("0x0"));
        let values = parse_windows_registry_values(
            "ProxyEnable    REG_DWORD    0x1\nProxyServer    REG_SZ    http=proxy.test:8080;https=secure.test:8443\nProxyOverride    REG_SZ    *.internal.test;<local>",
        );
        assert!(values["ProxyEnable"].eq_ignore_ascii_case("0x1"));
        let mut environment =
            windows_proxy_server_values(values.get("ProxyServer").map(String::as_str));
        environment.push(("NO_PROXY".into(), values["ProxyOverride"].clone()));
        let http = environment
            .iter()
            .find(|(key, _)| key == "HTTP_PROXY")
            .unwrap();
        let https = environment
            .iter()
            .find(|(key, _)| key == "HTTPS_PROXY")
            .unwrap();
        let bypass = loopback_bypass(&environment);
        assert_eq!(http.1, "http://proxy.test:8080/");
        assert_eq!(https.1, "http://secure.test:8443/");
        assert!(bypass.contains("*.internal.test"));
        assert!(bypass.contains("127.0.0.0/8"));
        assert!(!bypass.contains("<local>"));
    }

    #[test]
    fn accepts_a_single_windows_proxy_for_both_schemes() {
        let values = windows_proxy_server_values(Some("proxy.test:3128"));
        assert!(
            values
                .iter()
                .any(|(key, value)| key == "HTTP_PROXY" && value.contains("proxy.test:3128"))
        );
        assert!(
            values
                .iter()
                .any(|(key, value)| key == "HTTPS_PROXY" && value.contains("proxy.test:3128"))
        );
    }

    #[test]
    fn retains_inherited_proxy_bypasses_and_loopback() {
        let bypass = compose_loopback_bypass(&[], &["private.test;*.internal.test".into()]);
        assert!(bypass.contains("private.test"));
        assert!(bypass.contains("*.internal.test"));
        assert!(bypass.contains("localhost"));
        assert!(bypass.contains("127.0.0.0/8"));
    }

    #[test]
    fn reads_macos_exceptions_list_for_bypass() {
        let (values, exceptions) = parse_macos_proxy_output(
            "HTTPEnable : 1\nHTTPProxy : proxy.test\nHTTPPort : 8080\nExceptionsList : <array> {\n  0 : *.local\n  1 : 169.254/16\n}\n",
        );
        assert_eq!(values["HTTPEnable"], "1");
        assert_eq!(exceptions, ["*.local", "169.254/16"]);
        let bypass = compose_loopback_bypass(&[("NO_PROXY".into(), exceptions.join(","))], &[]);
        assert!(bypass.contains("*.local"));
        assert!(bypass.contains("169.254/16"));
        assert!(bypass.contains("::1"));
    }
}
