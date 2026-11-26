use serde::{Deserialize, Serialize};
use std::{
    path::Path,
    sync::{Arc, Mutex},
};
use tokio::process::Command as TokioCommand;

#[path = "network_settings/storage.rs"]
mod storage;

#[path = "network_settings/system_proxy.rs"]
mod system_proxy;

pub(super) const KEYRING_SERVICE: &str = "cloud.iquji.ops.network-proxy.v1";
const LOOPBACK_BYPASS: &str = "localhost,.localhost,127.0.0.0/8,::1,::ffff:127.0.0.0/104";

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum NetworkProxyMode {
    Direct,
    System,
    Manual,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct NetworkProxySettings {
    pub mode: NetworkProxyMode,
    pub url: Option<String>,
    pub has_credentials: bool,
}

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum NetworkCredentialAction {
    Keep,
    Replace,
    Clear,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct NetworkProxyCredentialsInput {
    pub action: NetworkCredentialAction,
    pub username: Option<String>,
    pub password: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct SaveNetworkProxySettings {
    pub mode: NetworkProxyMode,
    pub url: Option<String>,
    pub credentials: NetworkProxyCredentialsInput,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct NetworkSettingsError {
    pub code: &'static str,
    pub message: &'static str,
}

impl NetworkSettingsError {
    fn invalid() -> Self {
        Self {
            code: "network_proxy_invalid",
            message: "网络代理设置格式无效",
        }
    }
    fn storage() -> Self {
        Self {
            code: "network_proxy_storage",
            message: "系统凭据存储或代理配置保存失败",
        }
    }
    fn unavailable() -> Self {
        Self {
            code: "network_proxy_unavailable",
            message: "网络代理设置暂不可用",
        }
    }
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct StoredSettings {
    version: u16,
    mode: NetworkProxyMode,
    url: Option<String>,
    credential_account: Option<String>,
}

impl Default for StoredSettings {
    fn default() -> Self {
        Self {
            version: 1,
            mode: NetworkProxyMode::Direct,
            url: None,
            credential_account: None,
        }
    }
}

impl StoredSettings {
    fn projection(&self) -> NetworkProxySettings {
        NetworkProxySettings {
            mode: self.mode,
            url: self.url.clone(),
            has_credentials: self.credential_account.is_some(),
        }
    }

    fn validate(&self) -> Result<(), NetworkSettingsError> {
        if self.version != 1
            || self.credential_account.as_ref().is_some_and(|account| {
                account.len() != 48 || !account.bytes().all(|byte| byte.is_ascii_hexdigit())
            })
            || (self.credential_account.is_some() && self.url.is_none())
        {
            return Err(NetworkSettingsError::invalid());
        }
        if let Some(url) = self.url.as_deref() {
            validate_endpoint(Some(url))?;
        }
        if self.mode == NetworkProxyMode::Manual && self.url.is_none() {
            return Err(NetworkSettingsError::invalid());
        }
        Ok(())
    }
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct Credential {
    username: String,
    password: String,
}

#[derive(Default)]
struct State {
    path: Option<std::path::PathBuf>,
    settings: Option<StoredSettings>,
}

pub struct NetworkProxy {
    state: Mutex<State>,
    credentials: Arc<dyn storage::CredentialStore>,
}

impl Default for NetworkProxy {
    fn default() -> Self {
        Self {
            state: Mutex::new(State::default()),
            credentials: Arc::new(storage::SystemCredentialStore),
        }
    }
}

impl NetworkProxy {
    #[cfg(test)]
    fn with_credentials(credentials: Arc<dyn storage::CredentialStore>) -> Self {
        Self {
            state: Mutex::new(State::default()),
            credentials,
        }
    }

    pub fn initialize(&self, state_directory: &Path) -> Result<(), NetworkSettingsError> {
        let path = storage::configuration_path(state_directory)?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?;
        if state.settings.is_some() {
            return Err(NetworkSettingsError::storage());
        }
        let settings = storage::load(&path)?;
        settings.validate()?;
        if settings.mode == NetworkProxyMode::Manual
            && let Some(account) = settings.credential_account.as_deref()
        {
            let _ = self.credentials.load(account)?;
        }
        state.path = Some(path);
        state.settings = Some(settings);
        Ok(())
    }

    pub fn settings(&self) -> Result<NetworkProxySettings, NetworkSettingsError> {
        self.state
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?
            .settings
            .as_ref()
            .map(StoredSettings::projection)
            .ok_or_else(NetworkSettingsError::unavailable)
    }

    pub fn save(
        &self,
        input: SaveNetworkProxySettings,
    ) -> Result<NetworkProxySettings, NetworkSettingsError> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?;
        let path = state
            .path
            .as_ref()
            .ok_or_else(NetworkSettingsError::unavailable)?;
        let previous = state
            .settings
            .clone()
            .ok_or_else(NetworkSettingsError::unavailable)?;
        if input.mode == NetworkProxyMode::Manual {
            validate_endpoint(input.url.as_deref())?;
        } else if input.url.is_some() {
            return Err(NetworkSettingsError::invalid());
        }
        let supplied = input.credentials;
        if supplied.action != NetworkCredentialAction::Replace
            && (supplied.username.is_some() || supplied.password.is_some())
        {
            return Err(NetworkSettingsError::invalid());
        }
        let credential = match supplied.action {
            NetworkCredentialAction::Keep => {
                if input.mode == NetworkProxyMode::Manual
                    && previous.credential_account.is_some()
                    && !same_endpoint(previous.url.as_deref(), input.url.as_deref())
                {
                    return Err(NetworkSettingsError::invalid());
                }
                if input.mode == NetworkProxyMode::Manual {
                    previous
                        .credential_account
                        .as_deref()
                        .map(|account| self.credentials.load(account))
                        .transpose()?
                } else {
                    None
                }
            }
            NetworkCredentialAction::Clear => None,
            NetworkCredentialAction::Replace => {
                if input.mode != NetworkProxyMode::Manual {
                    return Err(NetworkSettingsError::invalid());
                }
                let username = supplied
                    .username
                    .ok_or_else(NetworkSettingsError::invalid)?;
                let password = supplied
                    .password
                    .ok_or_else(NetworkSettingsError::invalid)?;
                if username.is_empty()
                    || username.contains(':')
                    || username.len() > 1024
                    || password.len() > 4096
                    || username.chars().any(char::is_control)
                    || password.chars().any(char::is_control)
                {
                    return Err(NetworkSettingsError::invalid());
                }
                Some(Credential { username, password })
            }
        };
        let account = match supplied.action {
            NetworkCredentialAction::Keep => previous.credential_account.clone(),
            NetworkCredentialAction::Clear => None,
            NetworkCredentialAction::Replace => Some(storage::random_account()?),
        };
        let next = StoredSettings {
            version: 1,
            mode: input.mode,
            url: if input.mode == NetworkProxyMode::Manual {
                input.url
            } else {
                previous.url.clone()
            },
            credential_account: account.clone(),
        };
        next.validate()?;
        if let (Some(account), Some(credential)) = (account.as_deref(), credential.as_ref()) {
            self.credentials.save(account, credential)?;
        }
        if let Err(error) = storage::save(path, &next) {
            if supplied.action == NetworkCredentialAction::Replace
                && let Some(account) = account.as_deref()
            {
                let _ = self.credentials.delete(account);
            }
            return Err(error);
        }
        state.settings = Some(next.clone());
        if previous.credential_account != account
            && let Some(account) = previous.credential_account.as_deref()
        {
            let _ = self.credentials.delete(account);
        }
        Ok(next.projection())
    }

    fn process_config(&self) -> Result<ProcessProxyConfig, NetworkSettingsError> {
        let state = self
            .state
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?;
        let settings = state
            .settings
            .as_ref()
            .ok_or_else(NetworkSettingsError::unavailable)?;
        match settings.mode {
            NetworkProxyMode::Direct => Ok(ProcessProxyConfig::Direct),
            NetworkProxyMode::System => Ok(ProcessProxyConfig::System(system_proxy::environment())),
            NetworkProxyMode::Manual => {
                let endpoint = settings
                    .url
                    .as_deref()
                    .ok_or_else(NetworkSettingsError::invalid)?;
                let credential = settings
                    .credential_account
                    .as_deref()
                    .map(|account| self.credentials.load(account))
                    .transpose()?;
                Ok(ProcessProxyConfig::Manual(proxy_url(
                    endpoint,
                    credential.as_ref(),
                )?))
            }
        }
    }
}

enum ProcessProxyConfig {
    Direct,
    System(Vec<(String, String)>),
    Manual(String),
}

pub fn configure_process(
    command: &mut TokioCommand,
    proxy: &NetworkProxy,
) -> Result<(), NetworkSettingsError> {
    match proxy.process_config()? {
        ProcessProxyConfig::Direct => clear_proxy_environment(command),
        ProcessProxyConfig::System(values) => {
            #[cfg(target_os = "macos")]
            clear_proxy_environment(command);
            let bypass = system_proxy::loopback_bypass(&values);
            command.env("NO_PROXY", &bypass).env("no_proxy", &bypass);
            for (key, value) in values {
                if !key.eq_ignore_ascii_case("NO_PROXY") && !key.eq_ignore_ascii_case("no_proxy") {
                    command.env(key, value);
                }
            }
        }
        ProcessProxyConfig::Manual(url) => {
            clear_proxy_environment(command);
            command
                .env("HTTP_PROXY", &url)
                .env("http_proxy", &url)
                .env("HTTPS_PROXY", &url)
                .env("https_proxy", &url)
                .env("NO_PROXY", LOOPBACK_BYPASS)
                .env("no_proxy", LOOPBACK_BYPASS);
        }
    }
    Ok(())
}

fn clear_proxy_environment(command: &mut TokioCommand) {
    for key in [
        "HTTP_PROXY",
        "http_proxy",
        "HTTPS_PROXY",
        "https_proxy",
        "ALL_PROXY",
        "all_proxy",
        "NO_PROXY",
        "no_proxy",
    ] {
        command.env_remove(key);
    }
}

#[tauri::command]
pub async fn network_proxy_settings(
    window: tauri::WebviewWindow,
    state: tauri::State<'_, std::sync::Arc<NetworkProxy>>,
) -> Result<NetworkProxySettings, NetworkSettingsError> {
    if window.label() != "main" {
        return Err(NetworkSettingsError::unavailable());
    }
    let proxy = state.inner().clone();
    tauri::async_runtime::spawn_blocking(move || proxy.settings())
        .await
        .map_err(|_| NetworkSettingsError::unavailable())?
}

#[tauri::command]
pub async fn save_network_proxy_settings(
    app: tauri::AppHandle,
    window: tauri::WebviewWindow,
    state: tauri::State<'_, std::sync::Arc<NetworkProxy>>,
    input: SaveNetworkProxySettings,
) -> Result<NetworkProxySettings, NetworkSettingsError> {
    if window.label() != "main" {
        return Err(NetworkSettingsError::unavailable());
    }
    let proxy = state.inner().clone();
    let saved = tauri::async_runtime::spawn_blocking(move || proxy.save(input))
        .await
        .map_err(|_| NetworkSettingsError::unavailable())??;
    crate::restart_runtime(&app)
        .await
        .map_err(|_| NetworkSettingsError::unavailable())?;
    Ok(saved)
}

fn validate_endpoint(value: Option<&str>) -> Result<(), NetworkSettingsError> {
    let Some(value) = value else { return Ok(()) };
    if value.len() > 2048 || value != value.trim() || value.chars().any(char::is_whitespace) {
        return Err(NetworkSettingsError::invalid());
    }
    let url = url::Url::parse(value).map_err(|_| NetworkSettingsError::invalid())?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || !matches!(url.path(), "" | "/")
        || url.query().is_some()
        || url.fragment().is_some()
        || url.port() == Some(0)
    {
        return Err(NetworkSettingsError::invalid());
    }
    Ok(())
}

fn same_endpoint(left: Option<&str>, right: Option<&str>) -> bool {
    left.zip(right)
        .and_then(|(a, b)| {
            Some(url::Url::parse(a).ok()?.origin() == url::Url::parse(b).ok()?.origin())
        })
        .unwrap_or(false)
}

fn proxy_url(
    endpoint: &str,
    credential: Option<&Credential>,
) -> Result<String, NetworkSettingsError> {
    let mut url = url::Url::parse(endpoint).map_err(|_| NetworkSettingsError::invalid())?;
    if let Some(credential) = credential {
        url.set_username(&credential.username)
            .map_err(|_| NetworkSettingsError::invalid())?;
        url.set_password(Some(&credential.password))
            .map_err(|_| NetworkSettingsError::invalid())?;
    }
    Ok(url.to_string())
}

#[cfg(test)]
#[path = "network_settings/tests.rs"]
mod tests;
