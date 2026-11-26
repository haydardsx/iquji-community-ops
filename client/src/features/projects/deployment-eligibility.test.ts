import { describe, expect, it } from "vitest";
import type { DeploymentEligibility, Server } from "@/contracts/types";
import { deploymentServerOptions, managerOptions } from "./deployment-eligibility";

const servers: Server[] = ["manager", "worker", "other"].map((id) => ({
  id,
  name: id,
  publicHost: id,
  createdAt: "",
  updatedAt: "",
  health: { metrics: { swarm: true } },
}));
const eligibility: DeploymentEligibility = {
  deploymentManagerServerId: "manager",
  deploymentClusterId: "cluster-a",
  servers: servers.map((server) => ({
    serverId: server.id,
    eligible: server.id !== "other",
    clusterId: server.id === "other" ? "cluster-b" : "cluster-a",
    role: server.id === "manager" ? "manager" : "worker",
    reasons:
      server.id === "other"
        ? [{ code: "SWARM_CLUSTER_MISMATCH", message: "属于其他 Swarm 集群" }]
        : [],
  })),
};

describe("应用部署服务器资格", () => {
  it("部署管理节点独立于 Traefik，HTTP 网关还需要属于同一集群", () => {
    const managers = ["a", "b", "c"].map((id, index) => ({
      ...servers[index],
      id,
      health: {
        metrics: {
          swarm: true,
          ssh: true,
          docker: true,
          swarmVerificationStatus: "verified",
          swarmControlAvailable: true,
          swarmRole: "manager",
          swarmClusterId: index === 2 ? "other" : "same",
          traefik: index !== 0,
        },
      },
    }));
    expect(
      managerOptions(managers, String)
        .slice(1)
        .map((option) => option.disabled),
    ).toEqual([false, false, false]);
    expect(
      managerOptions(managers, String, "same")
        .slice(1)
        .map((option) => option.disabled),
    ).toEqual([true, false, true]);
  });
  it("同集群 manager 和 worker 可选，异群服务器展示具体原因", () => {
    const options = deploymentServerOptions(servers, eligibility, []);
    expect(options.map((option) => option.disabled)).toEqual([false, false, true]);
    expect(options[2].label).toContain("属于其他 Swarm 集群");
  });

  it("保留已选但失效的服务器及已移出项目的旧引用", () => {
    const selected = ["other", "removed"];
    const options = deploymentServerOptions(servers, eligibility, selected);
    expect(options.find((option) => option.value === "other")?.label).toContain("已选但不可部署");
    expect(options.find((option) => option.value === "removed")).toMatchObject({ disabled: true });
    expect(selected).toEqual(["other", "removed"]);
  });

  it("部署范围变化后保留服务目标，并明确标记范围外引用", () => {
    const options = deploymentServerOptions(servers, eligibility, ["worker"], ["manager"]);
    expect(options.find((option) => option.value === "worker")).toMatchObject({
      disabled: true,
      reason: "不在允许部署范围",
    });
  });

  it("资格未返回时不会仅凭旧 Swarm 标志开放选择", () => {
    expect(deploymentServerOptions(servers, undefined, []).every((option) => option.disabled)).toBe(
      true,
    );
  });
});
