mod diagnostic;
mod external_url;
mod network_settings;
mod paths;
mod runtime;
mod webview_reload;

use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};
use std::time::Duration;

use diagnostic::emit;
use tauri::{Manager, RunEvent, WebviewUrl, WebviewWindowBuilder};
use tokio::process::Child;
use tokio::sync::Mutex;

struct Children {
    api: Mutex<Option<Child>>,
    worker: Mutex<Option<Child>>,
    restart: Mutex<()>,
    shutting_down: AtomicBool,
    paths: paths::Paths,
    port: u16,
    window_key: String,
}

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(window) = app.get_webview_window("main") {
                let _ = window.show();
                let _ = window.set_focus();
            }
        }))
        .invoke_handler(tauri::generate_handler![
            external_url::open_external_url,
            automation_window_key,
            network_settings::network_proxy_settings,
            network_settings::save_network_proxy_settings,
            webview_reload::reload_webview,
        ])
        .setup(|app| {
            let handle = app.handle().clone();
            let paths = paths::Paths::resolve(&handle)
                .map_err(|error| std::io::Error::other(format!("路径解析失败：{error}")))?;
            let network_proxy = Arc::new(network_settings::NetworkProxy::default());
            network_proxy
                .initialize(&paths.state_dir)
                .map_err(|error| {
                    std::io::Error::other(format!("代理设置初始化失败：{}", error.message))
                })?;
            let mut key = [0u8; 32];
            getrandom::fill(&mut key).map_err(|_| std::io::Error::other("无法初始化本地自动化"))?;
            let window_key = hex::encode(key);
            let started = tauri::async_runtime::block_on(runtime::start(
                &paths,
                &network_proxy,
                None,
                &window_key,
            ))
            .map_err(|error| std::io::Error::other(format!("运行时启动失败：{error}")))?;
            let port = started.port;
            app.manage(network_proxy);
            app.manage(Children {
                api: Mutex::new(Some(started.api)),
                worker: Mutex::new(Some(started.worker)),
                restart: Mutex::new(()),
                shutting_down: AtomicBool::new(false),
                paths: paths.clone(),
                port,
                window_key,
            });
            let url = if paths.dev {
                "http://127.0.0.1:5174".to_string()
            } else {
                format!("http://127.0.0.1:{port}")
            };
            #[allow(unused_mut)]
            let mut window_builder = WebviewWindowBuilder::new(
                app,
                "main",
                WebviewUrl::External(
                    url.parse::<tauri::Url>()
                        .map_err(|error| std::io::Error::other(error.to_string()))?,
                ),
            )
            .title("iQuji Ops")
            .inner_size(1440.0, 960.0)
            .min_inner_size(1100.0, 760.0)
            .resizable(true)
            .on_new_window(|url, _features| {
                // 拦截 target="_blank" 与 window.open，仅将合法 http/https 地址交给系统浏览器。
                if let Ok(parsed) = external_url::parse_external_http_url(url.as_str())
                    && external_url::spawn_system_browser(&parsed).is_err()
                {
                    emit(
                        "error",
                        "external_url_failed",
                        "外部链接打开失败",
                        Some("EXTERNAL_URL_OPEN_FAILED"),
                    );
                }
                tauri::webview::NewWindowResponse::Deny
            });

            #[cfg(target_os = "macos")]
            {
                window_builder = window_builder
                    .decorations(true)
                    .title_bar_style(tauri::TitleBarStyle::Overlay)
                    .hidden_title(true)
                    .traffic_light_position(tauri::LogicalPosition::new(18.0, 24.0));
            }

            let window = window_builder.build()?;
            window.show()?;
            emit("info", "window_ready", "桌面窗口已就绪", None);
            monitor(handle.clone());
            install_signal_shutdown(handle);
            Ok(())
        })
        .build(tauri::generate_context!());
    let app = app.unwrap_or_else(|_| {
        emit(
            "error",
            "startup_failed",
            "桌面宿主启动失败",
            Some("DESKTOP_STARTUP_FAILED"),
        );
        std::process::exit(1);
    });

    app.run(|app, event| {
        if let RunEvent::ExitRequested { api, .. } = event {
            let handle = app.clone();
            let state = handle.state::<Children>();
            if state.shutting_down.swap(true, Ordering::SeqCst) {
                return;
            }
            api.prevent_exit();
            tauri::async_runtime::spawn(async move {
                stop_runtime(&handle).await;
                handle.exit(0);
            });
        }
    });
}

fn monitor(handle: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        loop {
            tokio::time::sleep(Duration::from_secs(2)).await;
            let state = handle.state::<Children>();
            if state.shutting_down.load(Ordering::SeqCst) {
                return;
            }
            let exited = {
                let mut api = state.api.lock().await;
                match api.as_mut().map(tokio::process::Child::try_wait) {
                    Some(Ok(Some(status))) => Some(("api", status.to_string())),
                    _ => None,
                }
            };
            if let Some((label, status)) = exited {
                emit(
                    "error",
                    "sidecar_exited",
                    &format!("{label} 意外退出（{status}）"),
                    Some("SIDECAR_EXITED"),
                );
                handle.exit(1);
                return;
            }
            let exited = {
                let mut worker = state.worker.lock().await;
                match worker.as_mut().map(tokio::process::Child::try_wait) {
                    Some(Ok(Some(status))) => Some(("worker", status.to_string())),
                    _ => None,
                }
            };
            if let Some((label, status)) = exited {
                emit(
                    "error",
                    "sidecar_exited",
                    &format!("{label} 意外退出（{status}）"),
                    Some("SIDECAR_EXITED"),
                );
                handle.exit(1);
                return;
            }
        }
    });
}

async fn stop_runtime(handle: &tauri::AppHandle) {
    let state = handle.state::<Children>();
    let _restart = state.restart.lock().await;
    {
        let mut worker = state.worker.lock().await;
        runtime::stop(&mut worker, "worker", Duration::from_secs(30)).await;
    }
    {
        let mut api = state.api.lock().await;
        runtime::stop(&mut api, "api", Duration::from_secs(5)).await;
    }
    emit("info", "stopped", "桌面运行服务已停止", None);
}

pub(crate) async fn restart_runtime(handle: &tauri::AppHandle) -> Result<(), String> {
    let state = handle.state::<Children>();
    let _restart = state.restart.lock().await;
    if state.shutting_down.load(Ordering::SeqCst) {
        return Err("IQUOPS_RUNTIME_SHUTTING_DOWN".to_string());
    }
    {
        let mut worker = state.worker.lock().await;
        runtime::stop(&mut worker, "worker", Duration::from_secs(30)).await;
    }
    {
        let mut api = state.api.lock().await;
        runtime::stop(&mut api, "api", Duration::from_secs(5)).await;
    }
    let network_proxy = handle.state::<Arc<network_settings::NetworkProxy>>();
    let started = runtime::start(
        &state.paths,
        &network_proxy,
        Some(state.port),
        &state.window_key,
    )
    .await?;
    *state.api.lock().await = Some(started.api);
    *state.worker.lock().await = Some(started.worker);
    Ok(())
}

fn install_signal_shutdown(handle: tauri::AppHandle) {
    tauri::async_runtime::spawn(async move {
        #[cfg(unix)]
        {
            use tokio::signal::unix::{SignalKind, signal};
            let mut terminate = signal(SignalKind::terminate()).ok();
            let mut interrupt = signal(SignalKind::interrupt()).ok();
            tokio::select! {
                () = async {
                    if let Some(signal) = terminate.as_mut() {
                        signal.recv().await;
                    } else {
                        std::future::pending::<()>().await;
                    }
                } => {}
                () = async {
                    if let Some(signal) = interrupt.as_mut() {
                        signal.recv().await;
                    } else {
                        std::future::pending::<()>().await;
                    }
                } => {}
            }
        }
        #[cfg(not(unix))]
        {
            let _ = tokio::signal::ctrl_c().await;
        }
        let state = handle.state::<Children>();
        if state.shutting_down.swap(true, Ordering::SeqCst) {
            return;
        }
        emit("info", "shutdown", "已收到关闭信号", None);
        stop_runtime(&handle).await;
        handle.exit(0);
    });
}

#[tauri::command]
fn automation_window_key(
    window: tauri::WebviewWindow,
    state: tauri::State<'_, Children>,
) -> Result<String, String> {
    let url = window.url().map_err(|_| "无法识别窗口地址")?;
    let port = if state.paths.dev { 5174 } else { state.port };
    if window.label() != "main"
        || url.scheme() != "http"
        || url.host_str() != Some("127.0.0.1")
        || url.port_or_known_default() != Some(port)
    {
        return Err("本窗口无权管理自动化".into());
    }
    Ok(state.window_key.clone())
}
