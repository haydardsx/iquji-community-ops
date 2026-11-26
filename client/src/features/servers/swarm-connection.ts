import type { ServerRuntimeMetrics } from "@/contracts/types";

export type SwarmConnectionMode = "public" | "private";
export type SwarmAddressMode = SwarmConnectionMode | "unknown";

function parseSwarmAddress(value?: string) {
  const input = value?.trim() ?? "";
  const bracketed = input.match(/^\[([^\]]+)\](?::(\d+))?$/);
  const ipv4 = input.match(/^(\d+\.\d+\.\d+\.\d+)(?::(\d+))?$/);
  const host = bracketed?.[1] ?? ipv4?.[1] ?? input;
  const port = bracketed?.[2] ?? ipv4?.[2];
  if (port && (Number(port) < 1 || Number(port) > 65535)) return null;
  if (ipv4) {
    const parts = host.split(".");
    if (parts.some((part) => part.length > 1 && part.startsWith("0"))) return null;
    const octets = parts.map(Number);
    if (octets.some((octet) => octet > 255)) return null;
    const address = octets.join(".");
    return { address, endpoint: port ? `${address}:${Number(port)}` : address };
  }
  if (!host.includes(":")) return null;
  try {
    const parsed = new URL(`http://[${host}]`);
    const address = parsed.hostname.slice(1, -1);
    return { address, endpoint: port ? `[${address}]:${Number(port)}` : address };
  } catch {
    return null;
  }
}

function ipv4Mode(address: string): SwarmAddressMode {
  const [first, second] = address.split(".").map(Number);
  if (first === 0 || first === 127 || first >= 224 || (first === 169 && second === 254)) {
    return "unknown";
  }
  if (
    first === 10 ||
    (first === 172 && second >= 16 && second <= 31) ||
    (first === 192 && second === 168) ||
    (first === 100 && second >= 64 && second <= 127)
  ) {
    return "private";
  }
  return "public";
}

export function swarmAddressMode(value?: string): SwarmAddressMode {
  const parsed = parseSwarmAddress(value);
  if (!parsed) return "unknown";
  const address = parsed.address;
  if (address.includes(".")) return ipv4Mode(address);
  if (address === "::" || address === "::1") return "unknown";
  const mapped = address.match(/^::ffff:([\da-f]+):([\da-f]+)$/);
  if (mapped) {
    const high = Number.parseInt(mapped[1], 16);
    const low = Number.parseInt(mapped[2], 16);
    return ipv4Mode(`${high >> 8}.${high & 255}.${low >> 8}.${low & 255}`);
  }
  const prefix = Number.parseInt(address.split(":")[0], 16);
  if ((prefix & 0xff00) === 0xff00 || (prefix & 0xffc0) === 0xfe80) return "unknown";
  return (prefix & 0xfe00) === 0xfc00 ? "private" : "public";
}

export function swarmConnectionModeLabel(mode: SwarmAddressMode, t?: (key: string) => string) {
  if (t) {
    const keys: Record<string, string> = {
      public: "swarm.connectionPublic",
      private: "swarm.connectionPrivate",
      unknown: "swarm.statusUnverified",
    };
    return t(keys[mode] ?? "swarm.statusUnverified");
  }
  return { public: "公网", private: "私网", unknown: "待核验" }[mode];
}

export function swarmIdentityVerified(metrics: ServerRuntimeMetrics) {
  return (
    metrics.swarm === true &&
    metrics.ssh !== false &&
    metrics.docker !== false &&
    metrics.swarmVerificationStatus === "verified" &&
    !metrics.swarmVerificationError
  );
}

export function getSwarmConnectionFacts(
  metrics: ServerRuntimeMetrics,
  t?: (key: string) => string,
) {
  return swarmSampleConnectionFacts(swarmIdentityVerified(metrics) ? metrics : {}, t);
}

// 旧版 NodeAddr 可能仅代表本机地址，实际控制端点或新采样模式才能确认连接事实。
export function swarmSampleConnectionFacts(
  metrics: ServerRuntimeMetrics,
  t?: (key: string) => string,
) {
  const managerEndpoint = metrics.swarmRole === "manager" ? metrics.swarmManagerAddress : undefined;
  const source = managerEndpoint || metrics.swarmAdvertiseAddr;
  const parsed = parseSwarmAddress(source);
  const classified = swarmAddressMode(source);
  const mode =
    managerEndpoint || metrics.swarmConnectionMode === classified ? classified : "unknown";
  const address = mode === "unknown" ? "" : (parsed?.address ?? "");
  const endpoint = mode === "unknown" ? "" : (parsed?.endpoint ?? "");
  const unverified = t ? t("swarm.connectionUnknown") : "连接地址待核验";
  return {
    mode,
    address,
    endpoint,
    label: mode === "unknown" ? unverified : `${swarmConnectionModeLabel(mode, t)} · ${endpoint}`,
  };
}
