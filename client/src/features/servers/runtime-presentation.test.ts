import { describe, expect, it } from "vitest";
import { type OpsMessageKey, opsCatalogs } from "@/shared/i18n";
import {
  agentDescription,
  runtimeCheckOutcome,
  runtimeComponentPresentation,
  swarmControlPresentation,
  swarmJoinIncomplete,
  swarmLeavePresentation,
  swarmMembership,
  swarmMembershipDescription,
} from "./runtime-presentation";

describe("服务器运行时展示", () => {
  it("只有本次确认未加入才显示未启用，未知角色或失联不会沿用旧控制权", () => {
    const t = (key: string) => key;
    for (const metrics of [
      {},
      { ssh: false, swarmRole: "manager", swarmControlAvailable: true },
      { ssh: true, docker: false, swarmRole: "none", swarmVerificationStatus: "stale" as const },
      { ssh: true, docker: true, swarm: false, swarmRole: "manager", swarmControlAvailable: true },
    ]) {
      expect(swarmMembership(metrics)).toBe("unknown");
      expect(swarmControlPresentation(metrics, t).label).toBe("swarm.statusUnverified");
    }
    const removed = { ssh: true, docker: false, swarmVerificationStatus: "not_joined" as const };
    expect(swarmMembership(removed)).toBe("inactive");
    expect(swarmControlPresentation(removed, t).label).toBe("servers.notEnabled");
    expect(
      swarmControlPresentation(
        {
          swarm: true,
          swarmVerificationStatus: "verified",
          swarmRole: "manager",
          swarmControlAvailable: false,
        },
        t,
      ).label,
    ).toBe("servers.runtimeUnavailable");
    expect(
      swarmControlPresentation(
        {
          swarm: true,
          swarmVerificationStatus: "verified",
          swarmRole: "worker",
          swarmControlAvailable: false,
        },
        t,
      ).label,
    ).toBe("servers.managedByManager");
  });

  it("区分 SSH 失败、引擎暂不可用、Agent 未运行与已确认缺失", () => {
    expect(runtimeComponentPresentation({ ssh: false, docker: false }, "docker").label).toBe(
      "状态待核验",
    );
    expect(runtimeComponentPresentation({ ssh: false }, "ssh").label).toBe("SSH 无法连接");
    expect(
      runtimeComponentPresentation(
        { ssh: true, docker: false, dockerStatus: "unavailable" },
        "docker",
      ).label,
    ).toBe("暂不可用");
    expect(
      runtimeComponentPresentation({ ssh: true, docker: false, dockerStatus: "missing" }, "docker")
        .label,
    ).toBe("未安装");
    expect(agentDescription({ ssh: false, agent: false })).toBe("状态待核验");
    expect(agentDescription({ ssh: true, agent: false })).toBe("未运行");
    expect(agentDescription({ ssh: true, agent: false, agentStatus: "not_installed" })).toBe(
      "未安装",
    );
  });

  it("未入群节点显示未加入", () => {
    expect(swarmMembershipDescription({ swarm: false, swarmRole: "none" })).toBe("未加入集群");
    expect(runtimeComponentPresentation({ swarm: false, swarmRole: "none" }, "swarm")).toEqual({
      label: "未加入",
      tone: "neutral",
    });
  });

  it("未完成加入提供显式退出入口，并保留错误状态", () => {
    for (const swarmLocalNodeState of ["pending", "error"]) {
      const metrics = { swarm: false, swarmRole: "none", swarmLocalNodeState };
      expect(swarmJoinIncomplete(metrics)).toBe(true);
      expect(swarmMembershipDescription(metrics)).toContain("加入尚未完成");
    }
    expect(runtimeComponentPresentation({ swarmLocalNodeState: "error" }, "swarm")).toEqual({
      label: "加入异常",
      tone: "warning",
    });
    expect(swarmJoinIncomplete({ swarmLocalNodeState: "inactive" })).toBe(false);
    expect(swarmJoinIncomplete({ swarmLocalNodeState: "active" })).toBe(false);
  });

  it("已加入节点展示真实集群和核验状态", () => {
    expect(
      swarmMembershipDescription({
        swarm: true,
        swarmRole: "worker",
        swarmClusterId: "cluster-a",
        swarmVerificationStatus: "verified",
      }),
    ).toBe("worker · cluster-a · 已核验");
    expect(swarmMembershipDescription({ swarm: true, swarmRole: "none" })).toContain("角色待核验");
  });

  it("worker 脱离说明区分 Swarm 调度与保留的独立环境，manager 保留成员保护", () => {
    const zh = (key: string) => opsCatalogs["zh-CN"][key as OpsMessageKey];
    const worker = swarmLeavePresentation({ swarm: true, swarmRole: "worker" }, zh);
    expect(worker.label).toBe("脱离集群");
    expect(worker.confirmLabel).toBe("确认脱离集群");
    expect(worker.description).toContain("停止接收 Swarm 调度");
    expect(worker.description).toContain("不要求原 manager 在线或已纳管");
    expect(worker.description).toContain("否则提示管理员清理旧记录");
    expect(worker.description).toContain("Docker、Jenkins 和独立 Compose 环境保留");
    const manager = swarmLeavePresentation({ swarm: true, swarmRole: "manager" }, zh);
    expect(manager.label).toBe("解散集群");
    expect(manager.confirmLabel).toBe("确认解散集群");
    expect(manager.description).toContain("唯一节点");
    expect(manager.description).toContain("其他管理节点、工作节点或服务");
    const en = (key: string) => opsCatalogs["en-US"][key as OpsMessageKey];
    expect(swarmLeavePresentation({ swarm: true, swarmRole: "manager" }, en).label).toBe(
      "Dissolve cluster",
    );
    expect(swarmLeavePresentation({ swarmLocalNodeState: "pending" }, zh).confirmLabel).toBe(
      "确认退出未完成加入",
    );
  });

  it("可选组件未安装使用中性状态，探测失败保留告警", () => {
    for (const component of ["jenkins", "traefik"] as const) {
      expect(
        runtimeComponentPresentation(
          { [component]: false, [`${component}Status`]: "missing" },
          component,
        ),
      ).toEqual({ label: "未安装", tone: "neutral" });
      expect(
        runtimeComponentPresentation(
          { [component]: false, [`${component}Status`]: "unavailable" },
          component,
        ),
      ).toEqual({ label: "运行异常", tone: "warning" });
    }
  });

  it("Agent 无版本信息但运行正常时展示采集正常", () => {
    expect(agentDescription({ agent: true })).toBe("监控采集正常");
    expect(agentDescription({ agent: true, agentVersion: "1.0" })).toBe("已运行 · v1.0");
  });
  it("已加入但归属未知或过期的 Swarm 显示待核验", () => {
    for (const swarmVerificationStatus of [undefined, "stale", "unverified"] as const) {
      expect(
        runtimeComponentPresentation({ swarm: true, swarmVerificationStatus }, "swarm"),
      ).toEqual({ label: "待核验", tone: "neutral" });
    }
    expect(
      runtimeComponentPresentation(
        { swarm: true, swarmVerificationError: "SWARM_MANAGER_SSH_UNAVAILABLE" },
        "swarm",
      ),
    ).toEqual({ label: "核验失败", tone: "warning" });
  });

  it("已有组件停止或故障时继续显示异常", () => {
    for (const status of ["error", "failed", "stopped", "exited"]) {
      expect(
        runtimeComponentPresentation({ jenkins: true, jenkinsStatus: status }, "jenkins"),
      ).toEqual({ label: "运行异常", tone: "warning" });
    }
  });

  it("旧快照可选组件缺失检查使用未启用状态", () => {
    expect(runtimeCheckOutcome({ outcome: "failed", summary: "SWARM_NOT_INITIALIZED" })).toBe(
      "skipped",
    );
    expect(runtimeCheckOutcome({ outcome: "failed", summary: "JENKINS_HEALTHCHECK_FAILED" })).toBe(
      "failed",
    );
    expect(runtimeCheckOutcome({ status: "skipped" })).toBe("skipped");
  });
  it("明确卸载状态覆盖旧的就绪标志", () => {
    for (const status of ["missing", "not_installed", "uninstalled"]) {
      expect(
        runtimeComponentPresentation({ jenkins: true, jenkinsStatus: status }, "jenkins"),
      ).toEqual({ label: "未安装", tone: "neutral" });
    }
  });
});
