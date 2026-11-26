use super::*;
use std::sync::Arc;

#[test]
fn rejects_embedded_credentials_and_unsupported_proxy_schemes() {
    let mut with_userinfo = url::Url::parse("http://proxy.example.test").unwrap();
    with_userinfo.set_username("example").unwrap();
    with_userinfo.set_password(Some("sample")).unwrap();
    for endpoint in [
        "socks5://proxy:1080",
        with_userinfo.as_str(),
        "https://proxy/path",
        "http://proxy?x=1",
    ] {
        assert!(validate_endpoint(Some(endpoint)).is_err());
    }
    assert!(validate_endpoint(Some("http://127.0.0.1:8080")).is_ok());
}

#[test]
fn credentials_are_encoded_only_into_ephemeral_child_configuration() {
    let credential = Credential {
        username: "a@b".into(),
        password: "secret:/p".into(),
    };
    let proxy = proxy_url("http://proxy:8080", Some(&credential)).unwrap();
    let proxy = url::Url::parse(&proxy).unwrap();
    assert!(proxy.username() == "a%40b");
    assert!(proxy.password() == Some("secret%3A%2Fp"));
    assert_eq!(proxy.host_str(), Some("proxy"));
    assert_eq!(proxy.port(), Some(8080));
    let settings = StoredSettings {
        version: 1,
        mode: NetworkProxyMode::Manual,
        url: Some("http://proxy:8080".into()),
        credential_account: Some("a".repeat(48)),
    };
    let serialized = serde_json::to_string(&settings).unwrap();
    assert!(!serialized.contains("secret"));
    assert!(
        !serde_json::to_string(&settings.projection())
            .unwrap()
            .contains("secret")
    );
}

#[test]
fn save_input_rejects_unknown_properties() {
    let value = r#"{"mode":"manual","url":"http://proxy:80","credentials":{"action":"keep"},"password":"secret"}"#;
    assert!(serde_json::from_str::<SaveNetworkProxySettings>(value).is_err());
}

#[test]
fn manual_credentials_are_replaced_kept_and_cleared_without_file_secrets() {
    let directory = std::env::temp_dir().canonicalize().unwrap().join(format!(
        "iquops-proxy-test-{}",
        storage::random_account().unwrap()
    ));
    let vault = Arc::new(storage::MemoryCredentialStore::default());
    let proxy = NetworkProxy::with_credentials(vault.clone());
    proxy.initialize(&directory).unwrap();
    let save = |action, username: Option<&str>, password: Option<&str>| SaveNetworkProxySettings {
        mode: NetworkProxyMode::Manual,
        url: Some("http://proxy.example.test:8080".into()),
        credentials: NetworkProxyCredentialsInput {
            action,
            username: username.map(str::to_owned),
            password: password.map(str::to_owned),
        },
    };

    let with_credentials = proxy
        .save(save(
            NetworkCredentialAction::Replace,
            Some("user"),
            Some("secret"),
        ))
        .unwrap();
    assert!(with_credentials.has_credentials);
    let config = std::fs::read_to_string(directory.join("network/proxy-v1.json")).unwrap();
    assert!(!config.contains("user"));
    assert!(!config.contains("secret"));
    assert_eq!(vault.0.lock().unwrap().len(), 1);
    let mut child = tokio::process::Command::new("iquops-proxy-test");
    configure_process(&mut child, &proxy).unwrap();
    let proxy_environment = child
        .as_std()
        .get_envs()
        .find(|(key, _)| *key == "HTTP_PROXY")
        .and_then(|(_, value)| value)
        .unwrap()
        .to_string_lossy();
    let endpoint = url::Url::parse(&proxy_environment).unwrap();
    assert!(endpoint.username() == "user" && endpoint.password() == Some("secret"));
    assert_eq!(endpoint.host_str(), Some("proxy.example.test"));
    assert_eq!(endpoint.port(), Some(8080));
    assert!(child.as_std().get_envs().any(|(key, value)| {
        key == "NO_PROXY"
            && value.is_some_and(|value| value.to_string_lossy().contains("localhost"))
    }));

    let kept = proxy
        .save(save(NetworkCredentialAction::Keep, None, None))
        .unwrap();
    assert!(kept.has_credentials);
    let mut changed_endpoint = save(NetworkCredentialAction::Keep, None, None);
    changed_endpoint.url = Some("http://other.example.test:8080".into());
    assert!(proxy.save(changed_endpoint).is_err());
    let cleared = proxy
        .save(save(NetworkCredentialAction::Clear, None, None))
        .unwrap();
    assert!(!cleared.has_credentials);
    assert!(vault.0.lock().unwrap().is_empty());
    assert_eq!(proxy.settings().unwrap(), cleared);
    let restored = NetworkProxy::with_credentials(vault);
    restored.initialize(&directory).unwrap();
    assert_eq!(restored.settings().unwrap(), cleared);
    let _ = std::fs::remove_dir_all(directory);
}
