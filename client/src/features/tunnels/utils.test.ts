import { describe, expect, it } from "vitest";

import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { tunnelServerBlockReason } from "./mappings";
import type { FrpTunnelDetail } from "./types";
import {
  canStartTunnel,
  canStopTunnel,
  generateFrpcConfig,
  isTunnelActive,
  tunnelBlockReasonLabel,
} from "./utils";

const tunnel: FrpTunnelDetail = {
  id: "tun_1",
  name: "开发回调",
  serverId: "srv_1",
  frpsBindPort: 7000,
  probeHitCount: 0,
  runCount: 0,
  createdAt: "2026-10-30T00:00:00Z",
  updatedAt: "2026-10-30T00:00:00Z",
  mappings: [
    {
      name: "web",
      targetType: "local_service",
      localHost: "127.0.0.1",
      localPort: 3000,
      frpsProxyPort: 18080,
      publicDomain: "console.example.com",
      publicPathPrefix: "/",
      entrypoint: "websecure",
      publicScheme: "https",
    },
    {
      name: "api",
      targetType: "local_service",
      localHost: "localhost",
      localPort: 8080,
      frpsProxyPort: 18081,
      publicPathPrefix: "/api",
      entrypoint: "web",
      publicScheme: "http",
    },
  ],
};

describe("服务器 FRPS 选择条件", () => {
  const server: Server = {
    id: "srv_example",
    name: "示例服务器",
    publicHost: "192.0.2.10",
    createdAt: "2026-10-30T00:00:00Z",
    updatedAt: "2026-10-30T00:00:00Z",
  };

  it.each<[ServerRuntimeMetrics, string | undefined]>([
    [{ frpsStatus: "installed", frps: true }, undefined],
    [{ frpsStatus: "installed", frps: false }, "tunnels.serverFrpsUnavailable"],
    [{ frpsStatus: "missing", frps: false }, "tunnels.serverFrpsMissing"],
    [{ frpsStatus: "unavailable", frps: false }, "tunnels.serverFrpsUnavailable"],
    [{ frpsStatus: "unknown", frps: false }, "tunnels.serverFrpsUnknown"],
    [{}, "tunnels.serverFrpsUnknown"],
    [{ frpsStatus: "running", frps: true }, "tunnels.serverFrpsInUse"],
    [{ frpsStatus: "installed", frps: true, frpsInUse: true }, "tunnels.serverFrpsInUse"],
  ])("根据运行状态禁用服务器选项 %j", (metrics, reason) => {
    expect(tunnelServerBlockReason({ ...server, health: { metrics } }, new Set())).toBe(reason);
  });

  it("服务器已有活动隧道时，即使安装快照仍显示就绪也不可选", () => {
    const ready = { ...server, health: { metrics: { frpsStatus: "installed", frps: true } } };
    expect(tunnelServerBlockReason(ready, new Set([server.id]))).toBe("tunnels.serverFrpsInUse");
  });
});

describe("隧道状态门禁", () => {
  it("活动运行期间不可启动、不可维护", () => {
    for (const status of [
      "pending_confirmation",
      "starting",
      "running",
      "stopping",
      "cleanup_required",
    ]) {
      expect(isTunnelActive(status)).toBe(true);
      expect(canStartTunnel(status)).toBe(false);
    }
    expect(isTunnelActive("stopped")).toBe(false);
    expect(isTunnelActive(undefined)).toBe(false);
    expect(canStartTunnel("stopped")).toBe(true);
    expect(canStartTunnel("failed")).toBe(true);
  });

  it("允许停止启动中、运行中以及需要重试停止的隧道", () => {
    expect(canStopTunnel("running")).toBe(true);
    expect(canStopTunnel("starting")).toBe(true);
    expect(canStopTunnel("stopped")).toBe(false);
    expect(canStopTunnel("cleanup_required")).toBe(true);
    expect(canStopTunnel("stopping")).toBe(false);
  });
});

describe("阻断原因", () => {
  it("把稳定错误码翻成可执行中文", () => {
    expect(tunnelBlockReasonLabel("SERVER_SSH_SECRET_MATERIAL_REQUIRED")).toContain("SSH");
    expect(tunnelBlockReasonLabel("LOCAL_TCP_PORT_UNREACHABLE:web")).toContain("web");
    expect(tunnelBlockReasonLabel("FRP_TUNNEL_MAPPING_NAME_DUPLICATE")).toContain("映射名称重复");
  });

  it("未知错误码保留原值", () => {
    expect(tunnelBlockReasonLabel("SOMETHING_NEW")).toBe("SOMETHING_NEW");
  });
});

describe("frpc 参考配置", () => {
  it("与 Worker 字段同构：域名、端口换算、传输协议与映射排序", () => {
    const config = generateFrpcConfig(
      { ...tunnel, mappings: [...tunnel.mappings].reverse() },
      { serverHost: "198.51.100.7", httpsPort: 8443 },
    );
    expect(config).toContain('serverAddr = "console.example.com"');
    expect(config).toContain("serverPort = 8443");
    expect(config).toContain('auth.method = "token"');
    expect(config).toContain('auth.token = "{{ .Envs.FRP_TOKEN }}"');
    expect(config).toContain('transport.protocol = "wss"');
    expect(config).toContain("transport.tls.enable = true");
    expect(config).toContain('log.to = "console"');
    expect(config.indexOf('name = "api"')).toBeLessThan(config.indexOf('name = "web"'));
    expect(config).not.toContain("frpc.toml - ");
  });

  it("没有公网域名时通过带 TLS 的 TCP 连接服务器控制端口", () => {
    const httpOnly = tunnel.mappings[1];
    if (!httpOnly) throw new Error("测试映射缺失");
    const config = generateFrpcConfig(
      { ...tunnel, mappings: [{ ...httpOnly, publicDomain: "" }] },
      { serverHost: "198.51.100.7", httpPort: 8080 },
    );
    expect(config).toContain('serverAddr = "198.51.100.7"');
    expect(config).toContain("serverPort = 7000");
    expect(config).toContain('transport.protocol = "tcp"');
    expect(config).toContain("transport.tls.enable = true");
  });

  it("为探针生成 HTTP 转发插件与当前运行的归属头", () => {
    const detail: FrpTunnelDetail = {
      ...tunnel,
      latestRun: {
        id: "tunrun_example",
        tunnelId: tunnel.id,
        status: "running",
        createdAt: tunnel.createdAt,
        updatedAt: tunnel.updatedAt,
      },
      mappings: [
        ...tunnel.mappings,
        {
          name: "probe",
          targetType: "http_probe",
          localHost: "::1",
          localPort: 17990,
          frpsProxyPort: 18082,
          publicPathPrefix: "/probe",
          entrypoint: "web",
          publicScheme: "http",
        },
      ],
    };
    const config = generateFrpcConfig(detail, { serverHost: "198.51.100.7" });
    expect(config).toContain('[proxies.plugin]\ntype = "http2http"\nlocalAddr = "[::1]:17990"');
    expect(config).toContain('[proxies.plugin.requestHeaders.set]\nX-IQuOps-Tunnel-ID = "tun_1"');
    expect(config).toContain('X-IQuOps-Tunnel-Run-ID = "tunrun_example"');
    expect(config).toContain('X-IQuOps-Tunnel-Mapping = "probe"');

    const draftConfig = generateFrpcConfig(
      { ...detail, latestRun: undefined },
      { serverHost: "198.51.100.7" },
    );
    expect(draftConfig).toContain('X-IQuOps-Tunnel-Run-ID = "{{ .Envs.FRP_TUNNEL_RUN_ID }}"');
  });

  it("仍运行的旧隧道沿用原有 Traefik 控制入口", () => {
    const httpOnly = tunnel.mappings[1];
    if (!httpOnly) throw new Error("测试映射缺失");
    const config = generateFrpcConfig(
      {
        ...tunnel,
        mappings: [{ ...httpOnly, publicDomain: "" }],
        latestRun: {
          id: "tunrun_legacy_example",
          tunnelId: tunnel.id,
          legacyIngress: true,
          status: "running",
          createdAt: tunnel.createdAt,
          updatedAt: tunnel.updatedAt,
        },
      },
      { serverHost: "198.51.100.7", httpPort: 8080 },
    );
    expect(config).toContain("serverPort = 8080");
    expect(config).toContain('transport.protocol = "websocket"');
  });
});
