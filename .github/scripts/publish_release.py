"""核对同一版本的四个平台附件。草稿上传完整并校验后才公开发行。"""

import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import sys
from hosted_source import WORKFLOW, context


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def verify_provenance(file, digest_hex, run, attempt):
    repo = os.environ["GITHUB_REPOSITORY"]
    sha = os.environ["GITHUB_SHA"]
    run_uri = f"https://github.com/{repo}/actions/runs/{run}/attempts/{attempt}"
    command = [
        "gh", "attestation", "verify", str(file), "--repo", repo,
        "--signer-workflow", f"{repo}/{WORKFLOW}", "--signer-digest", sha,
        "--source-digest", sha, "--source-ref", os.environ["GITHUB_REF"],
        "--deny-self-hosted-runners", "--predicate-type", "https://slsa.dev/provenance/v1",
        "--format", "json",
    ]
    for result in json.loads(subprocess.check_output(command)):
        verified = result["verificationResult"]
        certificate = verified["signature"]["certificate"]
        if certificate.get("runInvocationURI") != run_uri or certificate.get("buildTrigger") != "push":
            continue
        subjects = verified["statement"]["subject"]
        if any(s["name"] == file.name and s["digest"].get("sha256") == digest_hex for s in subjects):
            return
    raise ValueError("安装包缺少来自同一次 GitHub 托管运行的签名证明")


def main():
    if len(sys.argv) != 1:
        raise ValueError("发行作业不接受外部输入")
    run, attempt = context("release")
    tag = os.environ["GITHUB_REF_NAME"]
    version = json.loads(Path("desktop/tauri.conf.json").read_bytes())["version"]
    if tag != "v" + version or not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("发行标签必须匹配桌面版本")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    tag_commit = subprocess.check_output(["git", "rev-list", "-1", tag], text=True).strip()
    if tag_commit != commit or commit != os.environ["GITHUB_SHA"]:
        raise ValueError("发行标签与已验收的提交不符")
    extensions = {"macos-arm64": "dmg", "macos-x64": "dmg", "windows-x64": "exe", "linux-amd64": "deb"}
    artifacts = []
    checksums = []
    prefix = f"installer-{run}-{attempt}-"
    staging = Path("installers")
    if staging.is_symlink() or {p.name for p in staging.iterdir()} != {prefix + t for t in extensions}:
        raise ValueError("发行目录必须仅含当前运行和重试的四个平台附件")
    for target, ext in extensions.items():
        name = f"iQuji-Ops-{version}-{target}.{ext}"
        folder = staging / (prefix + target)
        if folder.is_symlink() or {item.name for item in folder.iterdir()} != {name, name + ".sha256"}:
            raise ValueError("平台上传的文件清单不符")
        if any(p.is_symlink() or not p.is_file() for p in folder.iterdir()):
            raise ValueError("平台附件必须是普通文件")
        file = folder / name
        digest_hex = digest(file)
        line = f"{digest_hex}  {name}\n"
        if file.with_name(name + ".sha256").read_text(encoding="utf8") != line:
            raise ValueError("安装包摘要与验收时不符")
        verify_provenance(file, digest_hex, run, attempt)
        artifacts.append(file)
        checksums.append(line)
    notes = (
        "Download the installer for your operating system and architecture.\n\n"
        "- macOS: choose the arm64 DMG for Apple silicon or the x64 DMG for Intel. "
        "These builds use an ad-hoc signature and have no Apple notarization.\n"
        "- Windows x64: use the NSIS setup executable. This release has no Authenticode signature.\n"
        "- Linux amd64: install the Debian package on a compatible distribution with WebKitGTK 4.1. "
        "The package is built and tested on Ubuntu 24.04.\n\n"
        "Check your download against `SHA256SUMS`. "
        f"Verify GitHub provenance with `gh attestation verify <installer> --repo {os.environ['GITHUB_REPOSITORY']} --deny-self-hosted-runners`.\n"
    )
    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        sums = root / "SHA256SUMS"
        sums.write_text("".join(sorted(checksums, key=lambda line: line.split()[1])), encoding="utf8")
        artifacts.append(sums)
        view = ["gh", "release", "view", tag, "--json", "isDraft,assets,databaseId"]
        existing = subprocess.run(view, text=True, capture_output=True)
        expected = {file.name: "sha256:" + digest(file) for file in artifacts}
        if existing.returncode == 0 or existing.stderr.strip().lower() != "release not found":
            raise ValueError("仅允许创建全新发行：标签已有发行、草稿或无法核实")
        endpoint = f"repos/{os.environ['GITHUB_REPOSITORY']}/releases"
        new_draft = json.loads(subprocess.check_output(
            ["gh", "api", "--method", "POST", endpoint, "--input", "-"],
            input=json.dumps({"tag_name": tag, "target_commitish": commit, "name": "iQuji Ops " + version,
                              "draft": True, "body": notes}).encode()))
        if new_draft["assets"] or not new_draft["draft"]:
            raise ValueError("新建草稿应为空")
        subprocess.run(["gh", "release", "upload", tag, *artifacts], check=True)
        release = json.loads(subprocess.check_output(view))
        remote = {asset["name"]: asset["digest"] for asset in release["assets"]}
        if remote != expected or not release["isDraft"] or release["databaseId"] != new_draft["id"]:
            raise ValueError("草稿附件的名称或摘要不符")
        subprocess.run(["gh", "api", "--method", "PATCH", endpoint + "/" + str(new_draft["id"]),
                        "-F", "draft=false", "-f", "make_latest=true", "--silent"], check=True)


if __name__ == "__main__":
    main()
