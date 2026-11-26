use std::future::Future;
use tauri::{WebviewWindow, webview::PlatformWebview};
use tokio::sync::oneshot;

const FAILED: &str = "WEBVIEW_RELOAD_FAILED";
type ReloadResult = Result<(), &'static str>;

#[derive(Debug, PartialEq, Eq)]
enum ReloadMode {
    Normal,
    FromOrigin,
}

impl ReloadMode {
    fn for_window(label: &str, ignore_cache: bool) -> Result<Self, &'static str> {
        if label != "main" {
            return Err("WEBVIEW_RELOAD_UNAVAILABLE");
        }
        Ok(if ignore_cache {
            Self::FromOrigin
        } else {
            Self::Normal
        })
    }
}

#[tauri::command]
pub async fn reload_webview(window: WebviewWindow, ignore_cache: bool) -> ReloadResult {
    match ReloadMode::for_window(window.label(), ignore_cache)? {
        ReloadMode::Normal => window.reload().map_err(|_| FAILED),
        ReloadMode::FromOrigin => {
            let (started, result) = oneshot::channel();
            window
                .with_webview(move |view| {
                    let _ = started.send(reload_from_origin(view));
                })
                .map_err(|_| FAILED)?;
            result.await.map_err(|_| FAILED)?.await
        }
    }
}

#[cfg(target_os = "macos")]
fn reload_from_origin(view: PlatformWebview) -> impl Future<Output = ReloadResult> {
    use objc2_web_kit::WKWebView;
    // Tauri 在主线程回调中提供当前 WKWebView，其指针在本次回调期间有效。
    let navigation = unsafe {
        view.inner()
            .cast::<WKWebView>()
            .as_ref()
            .and_then(|webview| webview.reloadFromOrigin())
    };
    std::future::ready(navigation.map(|_| ()).ok_or(FAILED))
}

#[cfg(target_os = "linux")]
fn reload_from_origin(view: PlatformWebview) -> impl Future<Output = ReloadResult> {
    use webkit2gtk::WebViewExt;
    view.inner().reload_bypass_cache();
    std::future::ready(Ok(()))
}

#[cfg(windows)]
fn reload_from_origin(view: PlatformWebview) -> impl Future<Output = ReloadResult> {
    use webview2_com::CallDevToolsProtocolMethodCompletedHandler;
    use windows_core::w;

    let (completed, result) = oneshot::channel();
    let handler = CallDevToolsProtocolMethodCompletedHandler::create(Box::new(move |status, _| {
        let _ = completed.send(status.map_err(|_| FAILED));
        Ok(())
    }));
    // 仅在 WebView2 所属的 UI 线程调用 COM，协议参数固定，完成处理器由 WebView2 持有。
    let started = unsafe {
        view.controller().CoreWebView2().and_then(|webview| {
            webview.CallDevToolsProtocolMethod(
                w!("Page.reload"),
                w!(r#"{"ignoreCache":true}"#),
                &handler,
            )
        })
    }
    .map_err(|_| FAILED);
    async move {
        started?;
        result.await.map_err(|_| FAILED)?
    }
}

#[cfg(test)]
mod tests {
    use super::ReloadMode;

    #[test]
    fn main_window_supports_both_reload_modes() {
        assert_eq!(
            ReloadMode::for_window("main", false),
            Ok(ReloadMode::Normal)
        );
        assert_eq!(
            ReloadMode::for_window("main", true),
            Ok(ReloadMode::FromOrigin)
        );
    }

    #[test]
    fn other_windows_cannot_reload() {
        for ignore_cache in [false, true] {
            assert_eq!(
                ReloadMode::for_window("secondary", ignore_cache),
                Err("WEBVIEW_RELOAD_UNAVAILABLE")
            );
        }
    }
}
