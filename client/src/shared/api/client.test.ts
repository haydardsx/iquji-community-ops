import { describe, expect, it } from "vitest";
import { ApiError, errorMessage, operationErrorMessage } from "./client";

describe("结构化操作阻断原因", () => {
  it("展示受影响的跳转规则并用 ID 标识缺失的名称", () => {
    const message = errorMessage(
      new ApiError({
        status: 409,
        fallbackMessage: "请求失败",
        body: {
          errorCode: "DOMAIN_REDIRECT_TARGET_IN_USE",
          details: {
            affectedRedirects: [
              {
                sourceId: "a",
                sourceName: "a.example.test",
                targetId: "b",
                targetName: "b.example.test",
              },
              { sourceId: "c", targetId: "b", targetName: "b.example.test" },
            ],
          },
        },
      }),
    );
    expect(message).toContain("a.example.test → b.example.test");
    expect(message).toContain("c → b.example.test");
  });
  it("输出引用阻断同时显示受影响应用及其 ID", () => {
    const message = operationErrorMessage("APPLICATION_OUTPUT_IN_USE", {
      affectedApplications: [{ kind: "application", id: "app_web", name: "示例前端" }],
    });
    expect(message).toContain("示例前端（app_web）");
  });
  it("把发布预检中的检查目标和 Swarm 集群原因翻译成中文", () => {
    const message = operationErrorMessage("RELEASE_PLAN_PREVIEW_BLOCKED", {
      blockers: ["SWARM_CLUSTER_MISMATCH"],
      checks: [
        {
          code: "SWARM_CLUSTER_MISMATCH",
          target: { kind: "server", id: "srv_app", name: "应用节点" },
          details: { deploymentManagerServerName: "控制节点" },
        },
      ],
    });
    expect(message).toContain("应用节点");
    expect(message).toContain("控制节点");
    expect(message).toContain("Swarm 集群");
  });

  it("把 Swarm 卸载影响面中的应用和服务器引用展示出来", () => {
    const message = operationErrorMessage("SWARM_UNINSTALL_BLOCKED_APPLICATION_RUNTIME", {
      blocked: true,
      reasons: [
        {
          code: "SERVER_HAS_APPLICATION_INSTANCE",
          resourceKind: "applicationInstance",
          resourceId: "instance_one",
          resourceName: "示例应用",
          field: "serverId",
        },
      ],
    });
    expect(message).toContain("示例应用");
    expect(message).toContain("应用实例");
    expect(message).not.toContain("serverId");
    expect(message).not.toContain("instance_one");
  });

  it("错误对象沿用统一错误出口读取结构化详情", () => {
    const message = errorMessage(
      new ApiError({
        status: 409,
        body: {
          errorCode: "SWARM_UNINSTALL_BLOCKED_TUNNEL",
          details: {
            impact: {
              reasons: [{ code: "SERVER_HAS_ACTIVE_TUNNEL_RUN", resourceName: "网关服务器" }],
            },
          },
        },
        fallbackMessage: "failed",
      }),
    );
    expect(message).toContain("网关服务器");
    expect(message).toContain("内网穿透");
  });

  it("兼容已落库的复合发布阻断码", () => {
    expect(operationErrorMessage("RELEASE_PLAN_PREVIEW_BLOCKED:SWARM_CLUSTER_MISMATCH")).toContain(
      "Swarm 集群",
    );
  });

  it("提示隧道编辑时必须从域名资产选择域名", () => {
    expect(
      operationErrorMessage("FRP_TUNNEL_INVALID:FRP_TUNNEL_PUBLIC_DOMAIN_NOT_IN_ASSETS"),
    ).toContain("请从域名资产中选择公网域名");
  });

  it("根据发布来源解释预检与容器部署错误", () => {
    expect(operationErrorMessage("APPLICATION_PACKAGE_VERSION_INVALID")).toContain("精确版本");
    expect(operationErrorMessage("APPLICATION_RELEASE_HEALTH_TIMEOUT")).toContain("健康状态");
    expect(operationErrorMessage("BUILD_SERVER_JENKINS_UNAVAILABLE")).toContain("Jenkins 不可用");
  });
  it("按 Jenkins 安装阶段解释错误并隐藏第三方输出", () => {
    for (const [code, message] of [
      ["JENKINS_PLUGIN_INSTALL_FAILED", "在线安装插件失败"],
      ["JENKINS_PLUGIN_BUNDLE_INVALID", "完整性校验"],
      ["JENKINS_BROKER_FIREWALL_UNAVAILABLE", "DOCKER-USER"],
      ["JENKINS_BROKER_DOCKER_SOCKET_UNAVAILABLE", "/var/run/docker.sock"],
      ["JENKINS_CONTAINER_NAME_CONFLICT", "同名容器"],
      ["JENKINS_BROKER_HOST_ACCESS_DENIED", "容器权限策略"],
      ["JENKINS_BROKER_START_FAILED", "Docker 服务日志"],
    ]) {
      const summary = operationErrorMessage(code);
      expect(summary).toContain(message);
      expect(operationErrorMessage(`${code}: example build output`)).toBe(summary);
    }
  });
  it("Swarm 加入失败提供中文原因和诊断入口提示", () => {
    for (const code of [
      "SWARM_MANAGER_ENDPOINT_UNAVAILABLE",
      "SWARM_MANAGER_ENDPOINT_INVALID",
      "SWARM_UNFINISHED_JOIN_EXIT_BLOCKED",
      "SWARM_JOIN_PENDING",
      "SWARM_NODE_ERROR",
      "SWARM_NODE_LOCKED",
      "SWARM_JOIN_TOKEN_REJECTED",
      "SWARM_JOIN_CERTIFICATE_FAILED",
      "SWARM_JOIN_STATE_INVALID",
      "SWARM_WORKER_JOIN_FAILED",
      "SWARM_WORKER_LEAVE_FAILED",
      "SWARM_WORKER_TASK_PROBE_FAILED",
      "SWARM_WORKER_MANAGER_NOT_ASSOCIATED",
      "SWARM_WORKER_NODE_REMOVE_FAILED",
      "SWARM_WORKER_NODE_STILL_ACTIVE",
      "SWARM_WORKER_IDENTITY_REQUIRED",
      "SWARM_WORKER_NODE_PROBE_FAILED",
      "SWARM_WORKER_NODE_OWNERSHIP_MISMATCH",
      "SWARM_DEPARTURE_RECORD_SAVE_FAILED",
      "SWARM_POST_UNINSTALL_PROBE_FAILED",
      "SWARM_CONNECTION_MODE_INVALID",
      "SWARM_ADVERTISE_ADDRESS_REQUIRED",
      "SWARM_CONNECTION_ADDRESS_MODE_MISMATCH",
      "SWARM_MANAGER_CONNECTION_MODE_MISMATCH",
      "SWARM_CONNECTION_RECONFIGURE_REQUIRED",
      "SWARM_CONNECTION_MANAGER_REQUIRED",
      "SWARM_CONNECTION_MULTIPLE_MANAGERS",
      "SWARM_CONNECTION_OPERATION_ACTIVE",
      "SWARM_CONNECTION_MIGRATION_REQUIRED",
      "SWARM_CONNECTION_RECONFIGURE_FAILED",
      "SWARM_CONNECTION_VERIFY_FAILED",
      "SWARM_CONNECTION_ROLLBACK_FAILED",
      "SWARM_ENGINE_API_UNAVAILABLE",
      "SWARM_CONNECTION_STATE_UNAVAILABLE",
      "SWARM_CONNECTION_AUTOLOCK_UNSUPPORTED",
      "SWARM_NODE_ADDRESS_UNVERIFIED",
      "SWARM_CONNECTION_CHECKPOINT_FAILED",
      "SWARM_CONNECTION_RECOVERY_REQUIRED",
      "OPERATION_CANCELLED",
      "OPERATION_TIMEOUT",
    ]) {
      expect(operationErrorMessage(code)).not.toBe(code);
    }
    expect(operationErrorMessage("SWARM_MANAGER_ENDPOINT_UNAVAILABLE")).toContain("节点间路由");
    expect(operationErrorMessage("SWARM_WORKER_JOIN_FAILED")).toContain("诊断日志");
  });

  it("Swarm 重建失败区分备份、核验和恢复状态", () => {
    for (const code of [
      "SWARM_REBUILD_SCOPE_UNSUPPORTED",
      "SWARM_REBUILD_BACKUP_FAILED",
      "SWARM_REBUILD_STATE_UNAVAILABLE",
      "SWARM_REBUILD_CHECKPOINT_FAILED",
      "SWARM_REBUILD_CHECKPOINT_INVALID",
      "SWARM_REBUILD_VERIFY_FAILED",
      "SWARM_REBUILD_RECOVERY_REQUIRED",
      "SWARM_REBUILD_RECOVERY_INVALID",
      "SWARM_REBUILD_RECOVERY_NOT_REQUIRED",
      "SWARM_REBUILD_ROLLBACK_FAILED",
      "SWARM_REBUILD_INTERRUPTED_RECOVERED",
    ]) {
      expect(operationErrorMessage(code)).not.toBe(code);
    }
    expect(operationErrorMessage("SWARM_REBUILD_SCOPE_UNSUPPORTED")).toContain("单节点");
    expect(operationErrorMessage("SWARM_REBUILD_BACKUP_FAILED")).toContain("实际阶段与恢复结果");
    expect(operationErrorMessage("SWARM_REBUILD_RECOVERY_REQUIRED")).toContain("停止自动修改");
    expect(operationErrorMessage("SWARM_REBUILD_ROLLBACK_FAILED")).toContain("尚未确认恢复");
    expect(operationErrorMessage("SWARM_REBUILD_INTERRUPTED_RECOVERED")).toContain("ID 可能已变化");
  });

  it("备份、恢复与数据维护诊断码提供可读中文说明", () => {
    for (const code of [
      "BACKUP_PASSWORD_REQUIRED",
      "BACKUP_PASSWORD_TOO_SHORT",
      "BACKUP_PASSPHRASE_NOT_ALLOWED",
      "LEGACY_BUNDLE_UNSUPPORTED",
      "RESTORE_CONFLICTS_PRESENT",
      "RESTORE_SECRET_MATERIAL_MISSING",
      "DATA_MAINTENANCE_ACTIVE_TASK",
      "DATA_MAINTENANCE_PHRASE_INVALID",
    ]) {
      expect(operationErrorMessage(code)).not.toBe(code);
    }
    expect(operationErrorMessage("BACKUP_PASSPHRASE_NOT_ALLOWED")).toContain("明文备份");
    expect(operationErrorMessage("RESTORE_CONFLICTS_PRESENT")).toContain("冲突");
    expect(operationErrorMessage("DATA_MAINTENANCE_ACTIVE_TASK")).toContain("操作任务");
  });
});
