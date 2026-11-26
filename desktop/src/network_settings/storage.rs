use super::{Credential, NetworkSettingsError, StoredSettings};
#[cfg(test)]
use std::{collections::HashMap, sync::Mutex};
use std::{
    fs::{self, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
};

pub(super) trait CredentialStore: Send + Sync {
    fn load(&self, account: &str) -> Result<Credential, NetworkSettingsError>;
    fn save(&self, account: &str, credential: &Credential) -> Result<(), NetworkSettingsError>;
    fn delete(&self, account: &str) -> Result<(), NetworkSettingsError>;
}

pub(super) struct SystemCredentialStore;

impl CredentialStore for SystemCredentialStore {
    fn load(&self, account: &str) -> Result<Credential, NetworkSettingsError> {
        let value = keyring::Entry::new(super::KEYRING_SERVICE, account)
            .map_err(|_| NetworkSettingsError::storage())?
            .get_password()
            .map_err(|_| NetworkSettingsError::storage())?;
        serde_json::from_str(&value).map_err(|_| NetworkSettingsError::storage())
    }

    fn save(&self, account: &str, credential: &Credential) -> Result<(), NetworkSettingsError> {
        let value =
            serde_json::json!({"username": credential.username, "password": credential.password})
                .to_string();
        keyring::Entry::new(super::KEYRING_SERVICE, account)
            .map_err(|_| NetworkSettingsError::storage())?
            .set_password(&value)
            .map_err(|_| NetworkSettingsError::storage())
    }

    fn delete(&self, account: &str) -> Result<(), NetworkSettingsError> {
        match keyring::Entry::new(super::KEYRING_SERVICE, account)
            .map_err(|_| NetworkSettingsError::storage())?
            .delete_credential()
        {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(NetworkSettingsError::storage()),
        }
    }
}

#[cfg(test)]
#[derive(Default)]
pub(super) struct MemoryCredentialStore(pub(super) Mutex<HashMap<String, Credential>>);

#[cfg(test)]
impl CredentialStore for MemoryCredentialStore {
    fn load(&self, account: &str) -> Result<Credential, NetworkSettingsError> {
        self.0
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?
            .get(account)
            .cloned()
            .ok_or_else(NetworkSettingsError::storage)
    }

    fn save(&self, account: &str, credential: &Credential) -> Result<(), NetworkSettingsError> {
        self.0
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?
            .insert(account.to_owned(), credential.clone());
        Ok(())
    }

    fn delete(&self, account: &str) -> Result<(), NetworkSettingsError> {
        self.0
            .lock()
            .map_err(|_| NetworkSettingsError::storage())?
            .remove(account);
        Ok(())
    }
}

pub(super) fn random_account() -> Result<String, NetworkSettingsError> {
    let mut bytes = [0_u8; 24];
    getrandom::fill(&mut bytes).map_err(|_| NetworkSettingsError::storage())?;
    Ok(hex::encode(bytes))
}

pub(super) fn configuration_path(state_directory: &Path) -> Result<PathBuf, NetworkSettingsError> {
    checked_directory(state_directory)?;
    let directory = state_directory.join("network");
    checked_directory(&directory)?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&directory, fs::Permissions::from_mode(0o700))
            .map_err(|_| NetworkSettingsError::storage())?;
    }
    Ok(directory.join("proxy-v1.json"))
}

fn checked_directory(path: &Path) -> Result<(), NetworkSettingsError> {
    if !path.is_absolute() {
        return Err(NetworkSettingsError::storage());
    }
    for ancestor in path.ancestors() {
        match fs::symlink_metadata(ancestor) {
            Ok(metadata) if metadata.file_type().is_symlink() || !metadata.is_dir() => {
                return Err(NetworkSettingsError::storage());
            }
            Ok(_) => {}
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return Err(NetworkSettingsError::storage()),
        }
    }
    fs::create_dir_all(path).map_err(|_| NetworkSettingsError::storage())
}

pub(super) fn load(path: &Path) -> Result<StoredSettings, NetworkSettingsError> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if !metadata.is_file() || metadata.file_type().is_symlink() => {
            return Err(NetworkSettingsError::storage());
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            return Ok(StoredSettings::default());
        }
        Err(_) => return Err(NetworkSettingsError::storage()),
        _ => {}
    }
    let mut options = OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.custom_flags(libc::O_NOFOLLOW);
    }
    let file = options
        .open(path)
        .map_err(|_| NetworkSettingsError::storage())?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if file
            .metadata()
            .map_err(|_| NetworkSettingsError::storage())?
            .permissions()
            .mode()
            & 0o077
            != 0
        {
            return Err(NetworkSettingsError::storage());
        }
    }
    let mut bytes = Vec::new();
    file.take(16_385)
        .read_to_end(&mut bytes)
        .map_err(|_| NetworkSettingsError::storage())?;
    if bytes.len() > 16_384 {
        return Err(NetworkSettingsError::storage());
    }
    serde_json::from_slice(&bytes).map_err(|_| NetworkSettingsError::storage())
}

pub(super) fn save(path: &Path, settings: &StoredSettings) -> Result<(), NetworkSettingsError> {
    checked_directory(path.parent().ok_or_else(NetworkSettingsError::storage)?)?;
    let temporary = path.with_file_name(format!(".proxy-{}.tmp", random_account()?));
    let result = (|| {
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        let mut file = options
            .open(&temporary)
            .map_err(|_| NetworkSettingsError::storage())?;
        let bytes = serde_json::to_vec(settings).map_err(|_| NetworkSettingsError::storage())?;
        file.write_all(&bytes)
            .and_then(|_| file.sync_all())
            .map_err(|_| NetworkSettingsError::storage())?;
        fs::rename(&temporary, path).map_err(|_| NetworkSettingsError::storage())
    })();
    if result.is_err() {
        let _ = fs::remove_file(temporary);
    }
    result
}
