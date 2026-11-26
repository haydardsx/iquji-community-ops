"""用刚安装的宿主、Worker 与授权 MCP 客户端验收同一份临时状态。"""

from contextlib import contextmanager
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import time
import urllib.request


LOOPBACK = urllib.request.build_opener(urllib.request.ProxyHandler({}))
PUBLIC_TEMPLATES = {"demo-backend", "demo-frontend", "pmail", "gitea"}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


@contextmanager
def running(executable, env, log_path):
    with log_path.open("wb") as log:
        process = subprocess.Popen([executable], env=env, stdout=log, stderr=log)
        try:
            yield process
        finally:
            if process.poll() is None:
                if os.name == "nt":
                    subprocess.run(["taskkill", "/pid", str(process.pid), "/t", "/f"],
                                   check=True, stdout=subprocess.DEVNULL)
                else:
                    process.terminate()
                try:
                    process.wait(25)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(5)


def request(port, endpoint, token=None, data=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    body = None if data is None else json.dumps(data).encode()
    query = urllib.request.Request(f"http://127.0.0.1:{port}{endpoint}", body, headers)
    with LOOPBACK.open(query, timeout=4) as response:
        return response.read()


def wait_ready(process, state, message):
    deadline = time.monotonic() + 90
    while time.monotonic() < deadline:
        require(process.poll() is None, message)
        try:
            port = int((state / "automation.port").read_text())
            if json.loads(request(port, "/health")).get("ok") is True:
                return port
        except (OSError, ValueError):
            pass
        time.sleep(0.3)
    raise RuntimeError(message)


def smoke(host, resources, work):
    state = work / "state"
    state.mkdir()
    suffix = ".exe" if os.name == "nt" else ""
    server = resources / "runtime" / ("iquops-server" + suffix)
    mcp = resources / "runtime" / ("iquops-mcp" + suffix)
    env = {key: value for key, value in os.environ.items() if not key.startswith("IQUOPS_")}
    env["IQUOPS_STATE_DIR"] = str(state)
    owner_key = secrets.token_hex(32)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        provision_port = listener.getsockname()[1]
    # 用随包服务的窗口授权接口创建一次性客户端，再从正式桌面进程读回数据。
    fixture_env = env | {
        "IQUOPS_API_HOST": "127.0.0.1",
        "IQUOPS_API_PORT": str(provision_port),
        "IQUOPS_AUTOMATION_WINDOW": owner_key,
        "IQUOPS_SQLITE_PATH": str(state / "state.sqlite"),
        "IQUOPS_MCP_BINARY": str(mcp),
        "IQUOPS_REPOSITORY_ROOT": str(resources),
        "IQUOPS_FRONTEND_DIR": str(resources / "client"),
    }
    with running(server, fixture_env, work / "provision.log") as fixture:
        port = wait_ready(fixture, state, "随包授权服务启动失败")
        grant = {"name": "Release verification", "permissions": [
            {"command": "templates_list", "mode": "direct", "targets": []}
        ]}
        result = json.loads(request(port, "/api/automation/window/grant", owner_key, grant))
        client = result["config"]["mcpServers"]["iquops"]
        require(Path(client["command"]) == mcp, "MCP 授权返回的程序与安装包不符")
        require(Path(client["env"]["IQUOPS_STATE_DIR"]) == state, "MCP 配置指向错误的状态目录")
    (state / "automation.port").unlink(missing_ok=True)
    host_log = work / "desktop.log"
    with running(host, env, host_log) as desktop:
        port = wait_ready(desktop, state, "安装后的桌面宿主未启动 API")
        require(b"iQuji Ops" in request(port, "/"), "桌面入口没有提供产品页面")
        expected = {"worker.started", "desktop.window_ready"}
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            observed = set()
            for line in host_log.read_bytes().splitlines():
                try:
                    observed.add(json.loads(line).get("event"))
                except (ValueError, AttributeError):
                    continue
            if expected <= observed:
                break
            require(desktop.poll() is None, "桌面进程提前退出")
            time.sleep(0.25)
        require(expected <= observed, "窗口或 Worker 未就绪")
        client_env = env | client["env"]
        def call(command):
            return subprocess.run([mcp, "call", command], env=client_env,
                                  capture_output=True, timeout=15)
        templates = call("templates_list")
        require(templates.returncode == 0, "随包 MCP 未能读回已授权模板")
        items = json.loads(templates.stdout)["items"]
        require(len(items) == 4 and {item["id"] for item in items} == PUBLIC_TEMPLATES,
                "安装后的默认模板清单不符")
        denied = call("projects_list")
        require(denied.returncode != 0 and
                json.loads(denied.stdout)["errorCode"] == "AUTOMATION_PERMISSION_DENIED",
                "MCP 没有拒绝未授权的项目查询")
