use std::path::PathBuf;

use tauri::Manager;

#[derive(Debug, Clone)]
pub struct Paths {
    pub dev: bool,
    pub resource_root: PathBuf,
    pub state_dir: PathBuf,
    pub frontend_dir: PathBuf,
    pub runtime_dir: PathBuf,
    pub agent_dir: PathBuf,
    pub jenkins_dir: PathBuf,
}

impl Paths {
    pub fn resolve(app: &tauri::AppHandle) -> Result<Self, String> {
        let dev = tauri::is_dev();
        let state_dir = std::env::var("IQUOPS_STATE_DIR")
            .ok()
            .map(PathBuf::from)
            .or_else(|| dirs::config_dir().map(|directory| directory.join("IQuOps").join("state")))
            .ok_or_else(|| "无法确定本机状态目录".to_string())?;
        let resource_root = if dev {
            development_root()?
        } else {
            app.path()
                .resource_dir()
                .map_err(|error| error.to_string())?
        };
        let runtime_dir = std::env::var("IQUOPS_RUNTIME_DIR")
            .ok()
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                if dev {
                    resource_root.join("desktop/runtime")
                } else {
                    resource_root.join("runtime")
                }
            });
        let frontend_dir = std::env::var("IQUOPS_FRONTEND_DIR")
            .ok()
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                if dev {
                    resource_root.join("client/dist")
                } else {
                    resource_root.join("client")
                }
            });
        let agent_dir = std::env::var("IQUOPS_AGENT_BUNDLE_DIR")
            .ok()
            .map(PathBuf::from)
            .unwrap_or_else(|| agent_directory(&resource_root, dev));
        let jenkins_dir = std::env::var("IQUOPS_JENKINS_BUNDLE_DIR")
            .ok()
            .map(PathBuf::from)
            .unwrap_or_else(|| jenkins_directory(&resource_root, dev));
        Ok(Self {
            dev,
            resource_root,
            state_dir,
            frontend_dir,
            runtime_dir,
            agent_dir,
            jenkins_dir,
        })
    }

    #[must_use]
    pub fn binary(&self, name: &str) -> PathBuf {
        self.runtime_dir.join(executable_name(name))
    }
}

fn development_root() -> Result<PathBuf, String> {
    #[cfg(dev)]
    {
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .parent()
            .map(std::path::Path::to_path_buf)
            .ok_or_else(|| "无法确定 Community 开发目录".to_string())
    }
    #[cfg(not(dev))]
    {
        Err("发行构建不能使用开发目录".to_string())
    }
}

fn agent_directory(root: &std::path::Path, dev: bool) -> PathBuf {
    root.join(if dev { "agent/dist" } else { "agent" })
}

fn jenkins_directory(root: &std::path::Path, dev: bool) -> PathBuf {
    root.join(if dev {
        "server/resources/runtime/managed/jenkins"
    } else {
        "resources/runtime/managed/jenkins"
    })
}

fn executable_name(name: &str) -> String {
    if cfg!(windows) {
        format!("{name}.exe")
    } else {
        name.to_string()
    }
}

#[cfg(test)]
mod tests {
    use super::{agent_directory, jenkins_directory};
    use std::path::Path;

    #[test]
    fn resources_follow_development_and_packaged_layouts() {
        let root = Path::new("community");
        assert_eq!(agent_directory(root, true), root.join("agent/dist"));
        assert_eq!(agent_directory(root, false), root.join("agent"));
        assert_eq!(
            jenkins_directory(root, true),
            root.join("server/resources/runtime/managed/jenkins")
        );
        assert_eq!(
            jenkins_directory(root, false),
            root.join("resources/runtime/managed/jenkins")
        );
    }
}
