use std::env;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

fn main() {
    let desktop = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").expect("缺少 Cargo 工作目录"));
    let root = desktop.parent().expect("桌面目录缺少 Community 根目录");
    let target_os = env::var("CARGO_CFG_TARGET_OS").expect("缺少 Cargo 目标系统");
    let target_arch = env::var("CARGO_CFG_TARGET_ARCH").expect("缺少 Cargo 目标架构");
    let goos = match target_os.as_str() {
        "macos" => "darwin",
        "linux" => "linux",
        "windows" => "windows",
        other => panic!("不支持的桌面目标系统：{other}"),
    };
    let goarch = match target_arch.as_str() {
        "x86_64" => "amd64",
        "aarch64" => "arm64",
        other => panic!("不支持的桌面目标架构：{other}"),
    };
    let runtime = desktop.join("runtime");
    let agent_output = root.join("agent/dist");
    fs::create_dir_all(&runtime).expect("无法创建 sidecar 输出目录");
    fs::create_dir_all(&agent_output).expect("无法创建 Agent 输出目录");
    watch_go_sources(&root.join("server"));
    watch_go_sources(&root.join("agent"));
    // Go 的嵌入资源可在源码未修改时变化，目录本身也须监听新增与删除。
    println!("cargo:rerun-if-changed=../server/internal/domain/templates");
    println!("cargo:rerun-if-changed=../server/internal/adapter/sqlite/migrations");
    println!("cargo:rerun-if-env-changed=GOTOOLCHAIN");

    bundle_frpc(&desktop, &runtime, goos, goarch);
    bundle_jenkins_plugins(&desktop, &runtime);
    for (package, name) in [
        ("./cmd/desktop", "iquops-server"),
        ("./cmd/worker", "iquops-worker"),
        ("./cmd/mcp", "iquops-mcp"),
    ] {
        let name = if goos == "windows" {
            format!("{name}.exe")
        } else {
            name.to_string()
        };
        build_go(
            &root.join("server"),
            package,
            &runtime.join(name),
            goos,
            goarch,
        );
    }
    for arch in ["amd64", "arm64"] {
        build_go(
            &root.join("agent"),
            ".",
            &agent_output.join(format!("iquops-monitoring-agent-linux-{arch}")),
            "linux",
            arch,
        );
    }
    tauri_build::build();
}

fn bundle_frpc(desktop: &Path, runtime: &Path, goos: &str, goarch: &str) {
    println!("cargo:rerun-if-changed=build_support/frpc.go");
    println!("cargo:rerun-if-env-changed=IQUOPS_FRP_ARCHIVE");
    if let Some(archive) = env::var_os("IQUOPS_FRP_ARCHIVE") {
        println!("cargo:rerun-if-changed={}", Path::new(&archive).display());
    }
    let cache = PathBuf::from(env::var_os("OUT_DIR").expect("缺少 Cargo 构建输出目录"));
    let status = Command::new("go")
        .current_dir(desktop)
        .env("GO111MODULE", "off")
        .env("CGO_ENABLED", "0")
        .env_remove("GOOS")
        .env_remove("GOARCH")
        .args(["run", "./build_support/frpc.go", goos, goarch])
        .arg(runtime)
        .arg(cache)
        .status()
        .unwrap_or_else(|error| panic!("无法启动 frpc 打包：{error}"));
    assert!(status.success(), "frpc 打包失败：{goos}/{goarch}");
}

fn bundle_jenkins_plugins(desktop: &Path, runtime: &Path) {
    let lock = "../server/resources/runtime/managed/jenkins/plugins.lock";
    println!("cargo:rerun-if-changed={lock}");
    println!("cargo:rerun-if-changed=build_support/jenkins/main.go");
    println!("cargo:rerun-if-env-changed=IQUOPS_JENKINS_PLUGINS_DIR");
    if let Some(directory) = env::var_os("IQUOPS_JENKINS_PLUGINS_DIR") {
        println!("cargo:rerun-if-changed={}", Path::new(&directory).display());
    }
    let cache = PathBuf::from(env::var_os("OUT_DIR").expect("缺少 Cargo 构建输出目录"));
    let status = Command::new("go")
        .current_dir(desktop)
        .env("GO111MODULE", "off")
        .env("CGO_ENABLED", "0")
        .env_remove("GOOS")
        .env_remove("GOARCH")
        .args(["run", "./build_support/jenkins", lock])
        .arg(runtime.join("jenkins-plugins.tar"))
        .arg(cache.join("jenkins-plugins"))
        .status()
        .unwrap_or_else(|error| panic!("无法启动 Jenkins 插件打包：{error}"));
    assert!(status.success(), "Jenkins 插件打包失败");
}

fn build_go(directory: &Path, package: &str, output: &Path, goos: &str, goarch: &str) {
    let status = Command::new("go")
        .current_dir(directory)
        .env("CGO_ENABLED", "0")
        .env("GOOS", goos)
        .env("GOARCH", goarch)
        .args([
            "build",
            "-trimpath",
            "-buildvcs=false",
            "-ldflags",
            "-s -w",
            "-o",
        ])
        .arg(output)
        .arg(package)
        .status()
        .unwrap_or_else(|error| panic!("无法启动 Go 构建：{error}"));
    assert!(status.success(), "Go 构建失败：{package} ({goos}/{goarch})");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(output, fs::Permissions::from_mode(0o755))
            .expect("无法设置 Go 二进制执行权限");
    }
}

fn watch_go_sources(directory: &Path) {
    for entry in fs::read_dir(directory).expect("无法读取 Go 源码目录") {
        let entry = entry.expect("无法读取 Go 源码文件");
        let path = entry.path();
        let name = entry.file_name();
        let name = name.to_string_lossy();
        if entry.file_type().expect("无法读取源码文件类型").is_dir() {
            if !matches!(name.as_ref(), ".git" | ".cache" | ".local" | "bin" | "dist") {
                watch_go_sources(&path);
            }
        } else if path.extension().is_some_and(|extension| extension == "go")
            || matches!(name.as_ref(), "go.mod" | "go.sum")
        {
            println!("cargo:rerun-if-changed={}", path.display());
        }
    }
}
