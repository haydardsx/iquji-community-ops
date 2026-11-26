use std::process::Stdio;
use std::time::Duration;

use crate::diagnostic::emit;
use tokio::process::{Child, Command};

use crate::network_settings::{self, NetworkProxy};
use crate::paths::Paths;

pub struct Started {
    pub api: Child,
    pub worker: Child,
    pub port: u16,
}

pub async fn start(
    paths: &Paths,
    network_proxy: &NetworkProxy,
    preferred_port: Option<u16>,
    window_key: &str,
) -> Result<Started, String> {
    let api_binary = paths.binary("iquops-server");
    let worker_binary = paths.binary("iquops-worker");
    ensure_binary(&api_binary)?;
    ensure_binary(&worker_binary)?;
    std::fs::create_dir_all(&paths.state_dir).map_err(|error| error.to_string())?;

    let port = preferred_port.unwrap_or(if paths.dev { 3001 } else { free_port()? });
    let base_url = format!("http://127.0.0.1:{port}");

    let mut api_command = Command::new(&api_binary);
    configure(&mut api_command, paths, port, &base_url, network_proxy)?;
    api_command
        .env("IQUOPS_AUTOMATION_WINDOW", window_key)
        .env("IQUOPS_MCP_BINARY", paths.binary("iquops-mcp"));
    let mut api = api_command
        .spawn()
        .map_err(|error| format!("Go API 启动失败：{error}"))?;

    wait_for_health(port).await.map_err(|error| {
        let _ = api.start_kill();
        format!("Go API 未就绪：{error}")
    })?;
    emit("info", "api_ready", "桌面 API 已就绪", None);

    let mut worker_command = Command::new(&worker_binary);
    if let Err(error) = configure(&mut worker_command, paths, port, &base_url, network_proxy) {
        let _ = api.start_kill();
        return Err(error);
    }
    let worker = match worker_command.spawn() {
        Ok(worker) => worker,
        Err(error) => {
            let _ = api.start_kill();
            return Err(format!("Go Worker 启动失败：{error}"));
        }
    };

    Ok(Started { api, worker, port })
}

fn configure(
    command: &mut Command,
    paths: &Paths,
    port: u16,
    base_url: &str,
    network_proxy: &NetworkProxy,
) -> Result<(), String> {
    let database = paths.state_dir.join("state.sqlite");
    command
        .env("IQUOPS_REPOSITORY_ROOT", &paths.resource_root)
        .env("IQUOPS_STATE_DIR", &paths.state_dir)
        .env("IQUOPS_SQLITE_PATH", database)
        .env("IQUOPS_API_HOST", "127.0.0.1")
        .env("IQUOPS_API_PORT", port.to_string())
        .env("IQUOPS_PUBLIC_BASE_URL", base_url)
        .env("IQUOPS_FRONTEND_DIR", &paths.frontend_dir)
        .env("IQUOPS_JENKINS_BUNDLE_DIR", &paths.jenkins_dir)
        .env("IQUOPS_AGENT_BUNDLE_DIR", &paths.agent_dir)
        .stdin(Stdio::null())
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    if std::env::var_os("IQUOPS_FRPC_PATH").is_none_or(|path| path.is_empty()) {
        command.env("IQUOPS_FRPC_PATH", paths.binary("frpc"));
    }
    network_settings::configure_process(command, network_proxy)
        .map_err(|error| error.message.to_string())
}

fn ensure_binary(path: &std::path::Path) -> Result<(), String> {
    if !path.is_file() {
        return Err(format!("缺少运行时二进制：{}", path.display()));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if let Ok(metadata) = std::fs::metadata(path) {
            let mut permissions = metadata.permissions();
            permissions.set_mode(0o755);
            let _ = std::fs::set_permissions(path, permissions);
        }
    }
    Ok(())
}

fn free_port() -> Result<u16, String> {
    let listener = std::net::TcpListener::bind("127.0.0.1:0")
        .map_err(|error| format!("无法分配回环端口：{error}"))?;
    let port = listener
        .local_addr()
        .map_err(|error| error.to_string())?
        .port();
    drop(listener);
    Ok(port)
}

async fn wait_for_health(port: u16) -> Result<(), String> {
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(2))
        .no_proxy()
        .build()
        .map_err(|error| error.to_string())?;
    let url = format!("http://127.0.0.1:{port}/health");
    let mut last_error = String::new();
    for _ in 0..120 {
        match client.get(&url).send().await {
            Ok(response) if response.status().is_success() => return Ok(()),
            Ok(response) => last_error = format!("HTTP {}", response.status()),
            Err(error) => last_error = error.to_string(),
        }
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    Err(last_error)
}

pub async fn stop(child: &mut Option<Child>, label: &str, grace: Duration) {
    let Some(mut process) = child.take() else {
        return;
    };
    #[cfg(unix)]
    {
        if let Some(pid) = process.id() {
            // 只向本进程启动的子进程发送 SIGTERM，不按端口结束未知实例。
            unsafe {
                libc::kill(pid as libc::pid_t, libc::SIGTERM);
            }
        }
    }
    let deadline = tokio::time::Instant::now() + grace;
    loop {
        match process.try_wait() {
            Ok(Some(status)) => {
                emit(
                    "info",
                    "sidecar_stopped",
                    &format!("{label} 已退出（{status}）"),
                    None,
                );
                return;
            }
            Ok(None) => {
                if tokio::time::Instant::now() >= deadline {
                    emit(
                        "warn",
                        "sidecar_stop_timeout",
                        &format!("{label} 停止超时，正在结束进程"),
                        Some("SIDECAR_STOP_TIMEOUT"),
                    );
                    let _ = process.start_kill();
                    let _ = process.wait().await;
                    return;
                }
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
            Err(_) => {
                emit(
                    "error",
                    "sidecar_wait_failed",
                    &format!("无法等候 {label} 退出"),
                    Some("SIDECAR_WAIT_FAILED"),
                );
                let _ = process.start_kill();
                return;
            }
        }
    }
}
