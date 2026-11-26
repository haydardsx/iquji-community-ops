import { describe, expect, it } from "vitest";
import {
  matchesOperationScope,
  type OperationActivityItem,
  operationActivityTone,
  operationQueryFamilies,
  summarizeOperationActivity,
} from "./activity";
import { operationActivityChanges } from "./activity-state";

const build: OperationActivityItem = {
  id: "operation_example",
  action: "request_jenkins_release",
  status: "running",
  targetKind: "application",
  targetId: "application_example",
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  readVersion: "running:2026-01-01T00:00:00Z",
  active: true,
  unread: false,
  scope: {
    navigation: "projects",
    projectId: "project_example",
    applicationId: "application_example",
    targetKind: "application",
    targetId: "application_example",
    serverIds: ["server_example"],
  },
};

describe("操作活动归属", () => {
  it("构建由项目和应用引导，不误亮只提供自有日志的服务器入口", () => {
    expect(matchesOperationScope(build, { navigation: "projects" })).toBe(true);
    expect(matchesOperationScope(build, { projectId: "project_example" })).toBe(true);
    expect(
      matchesOperationScope(build, {
        applicationId: "application_example",
        action: "request_jenkins_release",
      }),
    ).toBe(true);
    expect(matchesOperationScope(build, { serverId: "server_example" })).toBe(false);
    expect(matchesOperationScope(build, { navigation: "servers" })).toBe(false);
  });

  it("服务器仅聚合本机运行操作", () => {
    const runtime = {
      ...build,
      scope: { ...build.scope, navigation: "servers" as const, section: "server-runtime" as const },
    };
    expect(
      matchesOperationScope(runtime, { serverId: "server_example", section: "server-runtime" }),
    ).toBe(true);
    expect(matchesOperationScope(runtime, { navigation: "servers" })).toBe(true);
  });

  it("无关目标和其他动作不能误亮红点", () => {
    expect(matchesOperationScope(build, { applicationId: "application_other" })).toBe(false);
    expect(matchesOperationScope(build, { action: "stop_application" })).toBe(false);
    expect(matchesOperationScope(build, { navigation: "settings" })).toBe(false);
  });

  it("Swarm 卡聚合维护动作，不把普通构建误亮为集群维护", () => {
    expect(matchesOperationScope(build, { action: "docker_swarm" })).toBe(false);
    for (const action of [
      "install_docker_swarm",
      "rebuild_docker_swarm",
      "recover_docker_swarm_rebuild",
    ]) {
      expect(matchesOperationScope({ ...build, action }, { action: "docker_swarm" })).toBe(true);
    }
  });

  it("更新只刷新归属资源的查询，不刷新无关业务域", () => {
    const keys = operationQueryFamilies(build);
    expect(keys).toContain("application-builds");
    expect(keys).toContain("servers");
    expect(keys).not.toContain("tunnels");
    expect(keys).not.toContain("secrets");
  });

  it.each(["start_frp_tunnel", "stop_frp_tunnel"])("%s 完成后刷新服务器和隧道状态", (action) => {
    const running: OperationActivityItem = {
      ...build,
      action,
      targetKind: "frp_tunnel_run",
      targetId: "tunnel_run_example",
      scope: {
        navigation: "tunnels",
        targetKind: "frp_tunnel_run",
        targetId: "tunnel_run_example",
        tunnelId: "tunnel_example",
        serverIds: ["server_example"],
      },
    };
    const succeeded = {
      ...running,
      active: false,
      readVersion: "succeeded:2026-01-01T00:00:01Z",
      status: "succeeded",
      unread: true,
    };
    const keys = operationActivityChanges([running], [succeeded]).flatMap(operationQueryFamilies);
    expect(keys).toEqual(
      expect.arrayContaining([
        "servers",
        "server-health",
        "tunnel-detail",
        "monitoring-swarm",
        "monitoring-servers",
        "monitoring-agents",
      ]),
    );
  });
});

describe("操作圆点状态", () => {
  it("不可达目标不计入父层圆点，原始任务状态和未读记录保持不变", () => {
    const deleted = {
      ...build,
      status: "failed",
      active: false,
      unread: true,
      scope: { ...build.scope, reachable: false },
    };
    expect(operationActivityTone(deleted)).toBeUndefined();
    expect(summarizeOperationActivity([deleted], [{ navigation: "projects" }]).total).toBe(0);
    expect(deleted.unread).toBe(true);
    expect(deleted.status).toBe("failed");
    expect(
      summarizeOperationActivity([{ ...deleted, scope: { ...deleted.scope, reachable: true } }])
        .tone,
    ).toBe("danger");
  });

  it.each(["queued", "running"])("活动任务 %s 显示蓝色", (status) => {
    expect(operationActivityTone({ ...build, status })).toBe("info");
  });

  it("成功和失败仅在未读时显示对应颜色，取消及待确认不显示", () => {
    expect(
      operationActivityTone({ ...build, status: "succeeded", active: false, unread: true }),
    ).toBe("success");
    expect(operationActivityTone({ ...build, status: "failed", active: false, unread: true })).toBe(
      "danger",
    );
    for (const status of ["cancelled", "unknown", "succeeded", "failed"]) {
      expect(
        operationActivityTone({ ...build, status, active: false, unread: false }),
      ).toBeUndefined();
    }
    expect(
      operationActivityTone({ ...build, status: "cancelled", active: false, unread: true }),
    ).toBeUndefined();
    expect(
      operationActivityTone({
        ...build,
        status: "queued",
        queue: { state: "pending_confirmation", position: 0, canCancel: true },
      }),
    ).toBeUndefined();
  });

  it("混合任务以失败未读、进行中、成功未读排序，汇总忽略取消且不重复计数", () => {
    const success = { ...build, id: "success", status: "succeeded", active: false, unread: true };
    const failed = { ...build, id: "failed", status: "failed", active: false, unread: true };
    const cancelled = {
      ...build,
      id: "cancelled",
      status: "cancelled",
      active: false,
      unread: true,
    };
    const scopes = [{ projectId: "project_example" }, { applicationId: "application_example" }];
    expect(summarizeOperationActivity([build, success, failed, cancelled], scopes)).toEqual({
      tone: "danger",
      active: 1,
      failed: 1,
      succeeded: 1,
      unread: 2,
      total: 3,
    });
    expect(summarizeOperationActivity([build, success, cancelled], scopes).tone).toBe("info");
    expect(summarizeOperationActivity([success, cancelled], scopes).tone).toBe("success");
    expect(summarizeOperationActivity([cancelled], scopes).total).toBe(0);
    expect(summarizeOperationActivity([build, failed], scopes, build.id).tone).toBe("info");
  });
});
