"""限制发行来源为当前工作流的 GitHub 托管运行，并检查新的构建目录。"""

import os
from pathlib import Path
import re
import subprocess
import sys


WORKFLOW = ".github/workflows/desktop.yml"
STAGING = {"dist-release", "installers"}
PACKAGES = {".dmg", ".deb", ".exe", ".msi", ".appimage", ".pkg", ".rpm"}


def context(stage):
    env = os.environ
    expected_job = {"build": "package", "release": "release"}[stage]
    if not (env.get("GITHUB_ACTIONS") == "true" and
            env.get("RUNNER_ENVIRONMENT") == "github-hosted" and
            env.get("GITHUB_SERVER_URL") == "https://github.com" and
            env.get("GITHUB_JOB") == expected_job and
            Path.cwd().resolve() == Path(env["GITHUB_WORKSPACE"]).resolve()):
        raise RuntimeError("仅允许在当前工作流指定的 GitHub 托管作业中执行")
    repo, ref, sha = (env[k] for k in ("GITHUB_REPOSITORY", "GITHUB_REF", "GITHUB_SHA"))
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    if head != sha or env["GITHUB_WORKFLOW_REF"] != f"{repo}/{WORKFLOW}@{ref}":
        raise RuntimeError("检出修订与 GitHub 工作流身份不符")
    if stage == "release" and (env["GITHUB_EVENT_NAME"] != "push" or
                               not re.fullmatch(r"refs/tags/v\d+\.\d+\.\d+", ref)):
        raise RuntimeError("发行必须由版本标签推送触发")
    run, attempt = (env[k] for k in ("GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT"))
    if not (re.fullmatch(r"[1-9]\d*", run) and re.fullmatch(r"[1-9]\d*", attempt)):
        raise RuntimeError("缺少有效的运行或重试编号")
    return run, attempt


def prepare(stage):
    context(stage)
    tracked = subprocess.check_output(["git", "ls-files", "-z"]).decode().split("\0")
    for name in filter(None, tracked):
        path = Path(name)
        if (path.suffix.lower() in PACKAGES or set(path.parts) & STAGING or
                any(p.lower().endswith(".app") for p in path.parts)):
            raise RuntimeError("源码检出包含预制安装包或发行暂存内容")
    locations = [Path("dist-release"), Path("installers")]
    if stage == "build":
        locations += [Path(p) for p in ["desktop/target", "desktop/runtime", "agent/dist", "client/dist"]]
    if any(p.exists() or p.is_symlink() for p in locations):
        raise RuntimeError("要求全新、空白的构建和发行目录")
    Path("dist-release" if stage == "build" else "installers").mkdir(mode=0o700)


if __name__ == "__main__":
    prepare(sys.argv[1])
