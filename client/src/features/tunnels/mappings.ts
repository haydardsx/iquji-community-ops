import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import type { FrpTunnelMapping, TunnelMappingInput } from "./types";

export const MAPPING_LIMIT = 20;
export const PROBE_DEFAULT_PORT = 17990;
const PROXY_PORT_BASE = 18080;

export function defaultMappingName(index: number): string {
  return index === 0 ? "local-service" : `local-service-${index + 1}`;
}

/** 新建隧道时的第一条映射：本机 3000 端口，代理端口沿用历史默认值。 */
export function createDefaultMapping(existing: TunnelMappingInput[] = []): TunnelMappingInput {
  const names = new Set(existing.map((item) => item.name));
  const ports = new Set(existing.map((item) => item.frpsProxyPort));
  let index = existing.length;
  let name = defaultMappingName(index);
  while (names.has(name)) {
    index += 1;
    name = defaultMappingName(index);
  }
  let frpsProxyPort = PROXY_PORT_BASE + index;
  while (ports.has(frpsProxyPort) && frpsProxyPort < 65535) frpsProxyPort += 1;
  return {
    entrypoint: "web",
    frpsProxyPort,
    localHost: "127.0.0.1",
    localPort: 3000,
    name,
    publicDomain: "",
    publicPathPrefix: index === 0 ? "/" : `/local-${index + 1}`,
    publicScheme: "http",
    targetType: "local_service",
  };
}

/** 内置 HTTP 探针映射：本地地址与端口由服务器端填充，这里只保存入口。 */
export function createProbeMapping(existing: TunnelMappingInput[] = []): TunnelMappingInput {
  const names = new Set(existing.map((item) => item.name));
  const ports = new Set(existing.map((item) => item.frpsProxyPort));
  let name = "probe";
  let suffix = 2;
  while (names.has(name)) {
    name = `probe-${suffix}`;
    suffix += 1;
  }
  let frpsProxyPort = PROBE_DEFAULT_PORT;
  while (ports.has(frpsProxyPort) && frpsProxyPort < 65535) frpsProxyPort += 1;
  // 探针默认使用 /probe，绑定同一域名时仍可与其他映射区分。
  let publicPathPrefix = "/probe";
  const paths = new Set(existing.map((item) => item.publicPathPrefix));
  let pathIndex = 1;
  while (paths.has(publicPathPrefix)) {
    pathIndex += 1;
    publicPathPrefix = `/probe-${pathIndex}`;
  }
  return {
    entrypoint: "web",
    frpsProxyPort,
    localHost: "127.0.0.1",
    localPort: PROBE_DEFAULT_PORT,
    name,
    publicDomain: "",
    publicPathPrefix,
    publicScheme: "http",
    targetType: "http_probe",
  };
}

export function mappingToInput(mapping: FrpTunnelMapping): TunnelMappingInput {
  return {
    entrypoint: mapping.entrypoint || "web",
    frpsProxyPort: mapping.frpsProxyPort,
    localHost: mapping.localHost || "127.0.0.1",
    localPort: mapping.localPort || 0,
    name: mapping.name,
    publicDomain: mapping.publicDomain ?? "",
    publicPathPrefix: mapping.publicPathPrefix || "/",
    publicScheme: mapping.publicScheme || "http",
    targetType: mapping.targetType === "http_probe" ? "http_probe" : "local_service",
  };
}

/** 入口决定协议：web 只能 http，websecure 只能 https。 */
export function applyEntrypoint(
  mapping: TunnelMappingInput,
  entrypoint: string,
): TunnelMappingInput {
  const next = entrypoint === "websecure" ? "websecure" : "web";
  return { ...mapping, entrypoint: next, publicScheme: next === "websecure" ? "https" : "http" };
}

export function tunnelServerLabel(server: Server, t?: (key: string) => string): string {
  const host =
    server.publicHost || server.privateHost || (t ? t("tunnels.unfilledAddress") : "未填写地址");
  return `${server.name} · ${host}`;
}

export function tunnelServerBlockReason(server: Server, activeServerIds: ReadonlySet<string>) {
  const metrics = server.health?.metrics as ServerRuntimeMetrics | undefined;
  if (activeServerIds.has(server.id) || metrics?.frpsInUse || metrics?.frpsStatus === "running") {
    return "tunnels.serverFrpsInUse";
  }
  switch (metrics?.frpsStatus) {
    case "missing":
      return "tunnels.serverFrpsMissing";
    case "unavailable":
      return "tunnels.serverFrpsUnavailable";
    case "installed":
      return metrics.frps === true ? undefined : "tunnels.serverFrpsUnavailable";
    default:
      return "tunnels.serverFrpsUnknown";
  }
}

export function serverTraefikPorts(server?: Server): { httpPort: number; httpsPort: number } {
  const metrics = (server?.health?.metrics ?? {}) as ServerRuntimeMetrics;
  const httpPort = Number(metrics.traefikHttpPort);
  const httpsPort = Number(metrics.traefikHttpsPort);
  return {
    httpPort: Number.isFinite(httpPort) && httpPort > 0 ? httpPort : 80,
    httpsPort: Number.isFinite(httpsPort) && httpsPort > 0 ? httpsPort : 443,
  };
}
