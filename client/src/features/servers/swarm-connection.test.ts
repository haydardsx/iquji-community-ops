import { describe, expect, it } from "vitest";
import type { ServerRuntimeMetrics } from "@/contracts/types";
import { hostRange, privateIPv4Boundary, privateIPv6Boundary } from "@/test/swarm-addresses";
import { getSwarmConnectionFacts, swarmAddressMode } from "./swarm-connection";

describe("Swarm 实际连接地址", () => {
  it("识别私网与共享地址", () => {
    const [first, last] = hostRange("172.16.0.0/12");
    const [mapped] = hostRange("192.168.0.0/16");
    for (const address of [
      ...hostRange("10.0.0.0/8"),
      `${first}:2377`,
      last,
      ...hostRange("192.168.0.0/16"),
      ...hostRange("100.64.0.0/10"),
      privateIPv6Boundary,
      `::ffff:${mapped}`,
    ]) {
      expect(swarmAddressMode(address), address).toBe("private");
    }
  });

  it("公网示例与 IPv6 端点使用公网语义", () => {
    for (const address of [
      "192.0.2.10:3377",
      "192.0.2.13",
      "2001:db8::10",
      "[2001:db8::10]:3377",
    ]) {
      expect(swarmAddressMode(address), address).toBe("public");
    }
  });

  it("没有可用探测地址时保持待核验", () => {
    const [linkLocalIPv6] = "fe80::/10".split("/");
    for (const address of [
      "",
      "example.test",
      "0.0.0.0",
      "::",
      "127.0.0.1",
      ...hostRange("169.254.0.0/16"),
      `[${linkLocalIPv6}]:2377`,
      "::1",
      "300.1.1.1",
      "010.0.0.1",
      "192.0.2.1:70000",
    ]) {
      expect(swarmAddressMode(address), address).toBe("unknown");
    }
    expect(getSwarmConnectionFacts({ swarmConnectionMode: "public" })).toEqual({
      mode: "unknown",
      address: "",
      endpoint: "",
      label: "连接地址待核验",
    });
  });

  it("旧版 Worker 本机地址与缺少来源依据的 Manager 地址等待重新采样", () => {
    for (const metrics of [
      { swarmRole: "worker", swarmAdvertiseAddr: "192.0.2.10" },
      { swarmRole: "manager", swarmAdvertiseAddr: privateIPv4Boundary },
      {
        swarmRole: "worker",
        swarmAdvertiseAddr: privateIPv4Boundary,
        swarmConnectionMode: "public",
      },
    ] satisfies ServerRuntimeMetrics[]) {
      expect(getSwarmConnectionFacts(metrics)).toEqual({
        mode: "unknown",
        address: "",
        endpoint: "",
        label: "连接地址待核验",
      });
    }
  });

  it("服务器列表与详情不把旧 manager 控制端点当作当前地址", () => {
    const old = {
      swarm: true,
      swarmRole: "manager",
      swarmControlAvailable: true,
      swarmManagerAddress: "192.0.2.24:2377",
    };
    for (const state of [
      { swarmVerificationStatus: "stale" as const },
      { swarmVerificationStatus: "unverified" as const },
      {
        swarmVerificationStatus: "verified" as const,
        swarmVerificationError: "SWARM_PROBE_FAILED",
      },
      { swarmVerificationStatus: "verified" as const, ssh: false },
      { swarmVerificationStatus: "not_joined" as const, swarm: false },
      {},
    ]) {
      const result = getSwarmConnectionFacts({ ...old, ...state });
      expect(result).toEqual({
        mode: "unknown",
        address: "",
        endpoint: "",
        label: "连接地址待核验",
      });
    }
  });

  it("Manager 使用实际控制端点，Worker 使用自己的通告地址", () => {
    const metrics: ServerRuntimeMetrics = {
      swarm: true,
      swarmVerificationStatus: "verified",
      swarmRole: "manager",
      swarmManagerAddress: "192.0.2.10:3377",
      swarmAdvertiseAddr: privateIPv4Boundary,
      swarmConnectionMode: "private",
    };
    expect(getSwarmConnectionFacts(metrics)).toEqual({
      mode: "public",
      address: "192.0.2.10",
      endpoint: "192.0.2.10:3377",
      label: "公网 · 192.0.2.10:3377",
    });
    expect(getSwarmConnectionFacts({ ...metrics, swarmRole: "worker" }).label).toBe(
      `私网 · ${privateIPv4Boundary}`,
    );
  });
});
