"""安装当前平台的候选包，验证资源、签名状态和实际运行结果，再暂存发行附件。"""

import argparse
from contextlib import contextmanager
import hashlib
import json
import mmap
import os
from pathlib import Path
import platform
import plistlib
import shutil
import subprocess
import tarfile
import tempfile

from smoke_desktop import require, smoke
from hosted_source import context


TARGETS = {
    "macos-arm64": ("Darwin", "arm64", "dmg"),
    "macos-x64": ("Darwin", "x86_64", "dmg"),
    "windows-x64": ("Windows", "AMD64", "nsis"),
    "linux-amd64": ("Linux", "x86_64", "deb"),
}


def output(*args):
    return subprocess.check_output(args, text=True).strip()


def exactly_one(candidates, label):
    files = list(candidates)
    require(len(files) == 1, label)
    return files[0]


def sha256(file):
    with file.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def pe_x64(file):
    with file.open("rb") as stream:
        if stream.read(2) != b"MZ":
            return False
        stream.seek(0x3C)
        offset = int.from_bytes(stream.read(4), "little")
        stream.seek(offset)
        return stream.read(6) == b"PE\x00\x00\x64\x86"


def linux_arch(file):
    with file.open("rb") as stream:
        header = stream.read(20)
    require(header[:6] == b"\x7fELF\x02\x01", "无效的 Linux 可执行格式")
    return int.from_bytes(header[18:20], "little")


@contextmanager
def installed(package, work, config):
    system = platform.system()
    if system == "Darwin":
        mount = work / "volume"
        mount.mkdir()
        subprocess.run(["hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount,
                        package], check=True, stdout=subprocess.DEVNULL)
        try:
            source = exactly_one(mount.glob("*.app"), "DMG 应当包含一个桌面应用")
            app = work / source.name
            subprocess.run(["ditto", source, app], check=True)
        finally:
            subprocess.run(["hdiutil", "detach", mount], check=True, stdout=subprocess.DEVNULL)
        contents = app / "Contents"
        bundle = plistlib.loads((contents / "Info.plist").read_bytes())
        require(bundle["CFBundleIdentifier"] == config["identifier"] and
                bundle["CFBundleShortVersionString"] == config["version"], "macOS 应用标识或版本错误")
        subprocess.run(["codesign", "--verify", "--deep", "--strict", app], check=True)
        info = subprocess.check_output(["codesign", "-dvv", app], stderr=subprocess.STDOUT)
        require(b"Signature=adhoc" in info, "签名状态与发布说明不符")
        yield contents / "MacOS" / bundle["CFBundleExecutable"], contents / "Resources"
    elif system == "Windows":
        install_dir = work / "installed"
        subprocess.run([package, "/S", "/D=" + str(install_dir)], check=True, timeout=180)
        try:
            signature = subprocess.run([
                "powershell", "-NoProfile", "-NonInteractive", "-Command",
                "(Get-AuthenticodeSignature -LiteralPath $env:VERIFY_INSTALLER).Status.ToString()",
            ], env=os.environ | {"VERIFY_INSTALLER": str(package)}, capture_output=True,
                text=True, check=True).stdout.strip()
            require(signature == "NotSigned", "Windows 签名状态与发布说明不符")
            host = exactly_one((p for p in install_dir.glob("*.exe")
                                if "uninstall" not in p.name.lower()), "无法唯一确定 Windows 桌面宿主")
            yield host, install_dir
        finally:
            uninstaller = install_dir / "uninstall.exe"
            if uninstaller.is_file():
                subprocess.run([uninstaller, "/S", "_?=" + str(install_dir)], timeout=120, check=True)
    else:
        package_name = output("dpkg-deb", "--field", package, "Package")
        require(output("dpkg-deb", "--field", package, "Architecture") == "amd64" and
                output("dpkg-deb", "--field", package, "Version").split("-")[0] == config["version"],
                "Debian 包元数据不符")
        subprocess.run(["sudo", "apt-get", "install", "--yes", package], check=True)
        entries = [Path(s) for s in output("dpkg-query", "-L", package_name).splitlines()]
        host = exactly_one((p for p in entries if p.parent == Path("/usr/bin") and p.is_file()),
                           "无法唯一确定 Linux 桌面宿主")
        contract = exactly_one((p for p in entries if p.parts[-3:] == ("config", "app", "app.json")),
                               "无法确定 Linux 资源目录")
        yield host, contract.parents[2]


def verify_plugins(resources, notices):
    plugins = resources / "resources/runtime/managed/jenkins"
    expected = {}
    for line in (plugins / "plugins.lock").read_text(encoding="utf8").splitlines():
        plugin, version, digest = line.split(":")
        refs = notices["components"]["jenkins:" + plugin + "@" + version]
        require(refs and all(ref in notices["notices"] for ref in refs), "Jenkins 插件许可缺失")
        expected[plugin + ".jpi"] = digest
    with tarfile.open(plugins / "jenkins-plugins.tar", "r:") as archive:
        require(sorted(archive.getnames()) == sorted(expected), "包内 Jenkins 插件集合不符")
        for member in archive.getmembers():
            require(member.isfile(), "插件归档包含非普通文件")
            with archive.extractfile(member) as stream:
                require(hashlib.file_digest(stream, "sha256").hexdigest() == expected[member.name],
                        "Jenkins 插件内容与锁文件不符")


def check_build_paths(files):
    # 找出本次构建的真实目录，即使二进制以 UTF-16 保存路径也须检出。
    paths = {"workspace": Path.cwd(), "profile": Path.home()}
    for key in ["RUNNER_TEMP", "CARGO_HOME", "RUSTUP_HOME"]:
        if os.environ.get(key):
            paths[key] = Path(os.environ[key])
    needles = {value.encode(encoding): (label, encoding) for label, path in paths.items()
               for value in {str(path), path.as_posix()} for encoding in ("utf8", "utf-16-le")}
    for file in files:
        if file.is_file() and file.stat().st_size:
            with file.open("rb") as stream, mmap.mmap(stream.fileno(), 0, access=mmap.ACCESS_READ) as data:
                for needle, (label, encoding) in needles.items():
                    offset = data.find(needle)
                    if offset >= 0:
                        section = pe_section(data, offset) if file.suffix.lower() == ".exe" else "data"
                        raise RuntimeError(f"Package contains a build path ({file.name}, {label}, {section}, {encoding})")


def pe_section(data, offset):
    # 诊断仅报告预定义的段名或 PDB 记录类型，不输出匹配路径或二进制原文。
    if offset >= 24 and data[offset - 24:offset - 20] == b"RSDS":
        return "CodeView-PDB"
    if data[:2] == b"MZ":
        pe = int.from_bytes(data[60:64], "little")
        count = int.from_bytes(data[pe + 6:pe + 8], "little")
        first = pe + 24 + int.from_bytes(data[pe + 20:pe + 22], "little")
        for i in range(count):
            row = data[first + 40 * i:first + 40 * (i + 1)]
            if len(row) != 40:
                break
            size = int.from_bytes(row[16:20], "little")
            start = int.from_bytes(row[20:24], "little")
            name = row[:8].rstrip(b"\x00")
            if start <= offset < start + size and name in (b".rdata", b".text", b".data", b".rsrc"):
                return name.decode("ascii")
    return "other"


def verify_resources(host, resources, config):
    app = json.loads((resources / "config/app/app.json").read_bytes())
    require(app["appId"] == config["identifier"] and app["version"] == config["version"] and
            app["productName"] == config["productName"], "包内应用标识、名称或版本不一致")
    for file in ["licenses/APP-LICENSE", "licenses/THIRD_PARTY_NOTICES.md",
                 "client/index.html", "runtime/frpc-LICENSE"]:
        require((resources / file).stat().st_size > 0, "安装包缺少所需资源")
    for ext in ["js", "css"]:
        require(any((resources / "client/assets").glob("*." + ext)), "客户端静态资源缺失")
    for component in ["runtime", "client"]:
        notices = json.loads((resources / component / "third-party-notices.json").read_bytes())
        require(notices["components"] and notices["notices"], "缺少第三方许可")
        if component == "runtime":
            verify_plugins(resources, notices)
    for arch, machine in [("amd64", 62), ("arm64", 183)]:
        agent = resources / "agent" / ("iquops-monitoring-agent-linux-" + arch)
        require(linux_arch(agent) == machine, "随包监控 Agent 架构不符")
    binaries = [host]
    for name in ["frpc", "iquops-server", "iquops-worker", "iquops-mcp"]:
        binaries.append(resources / "runtime" / (name + (".exe" if os.name == "nt" else "")))
    for file in binaries:
        require(file.is_file() and os.access(file, os.X_OK), "桌面可执行文件缺失")
        if platform.system() == "Darwin":
            require(platform.machine() in output("lipo", "-archs", file).split(), "macOS 程序架构不符")
        elif platform.system() == "Linux":
            require(linux_arch(file) == 62, "Linux 程序架构不符")
        else:
            require(pe_x64(file), "Windows 程序架构不符")
    check_build_paths([host, *resources.rglob("*")])


def main():
    context("build")
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("target", choices=TARGETS)
    args = parser.parse_args()
    system, arch, bundle = TARGETS[args.target]
    require(platform.system() == system and platform.machine().lower() == arch.lower(),
            "验收机器与安装包目标架构不一致")
    config = json.loads(Path("desktop/tauri.conf.json").read_bytes())
    suffix = ".exe" if bundle == "nsis" else "." + bundle
    package = exactly_one(
        (Path("desktop/target/release/bundle") / bundle).glob("*" + suffix), "应当仅有一个目标安装包")
    require(not package.is_symlink() and package.is_file(), "构建输出必须是普通安装包文件")
    package = package.resolve(strict=True)
    with tempfile.TemporaryDirectory() as directory:
        work = Path(directory).resolve()
        with installed(package, work, config) as (host, resources):
            verify_resources(host, resources, config)
            smoke(host, resources, work)
    destination = Path("dist-release")
    require(not destination.is_symlink() and destination.is_dir() and not any(destination.iterdir()),
            "暂存目录必须是本次作业准备的空目录")
    artifact = destination / f"iQuji-Ops-{config['version']}-{args.target}{suffix}"
    shutil.copyfile(package, artifact)
    artifact.with_name(artifact.name + ".sha256").write_text(
        f"{sha256(artifact)}  {artifact.name}\n", encoding="utf8")
    print("已验收安装包 " + artifact.name)


if __name__ == "__main__":
    main()
