import type { IqujiConfirmDialogProps } from "@iquji-community/web-component";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Server } from "@/contracts/types";
import type { ConfirmedOperationInput } from "@/shared/operations/confirmed-operation";
import { renderToStaticMarkup } from "@/test/render";
import { privateIPv4Boundary } from "@/test/swarm-addresses";
import { ServerSwarmRebuild } from "./ServerSwarmRebuild";
import { SwarmInstallFields } from "./SwarmInstallFields";

const mocks = vi.hoisted(() => ({
  dialog: null as IqujiConfirmDialogProps | null,
  invalidateQueries: vi.fn(),
  onChanged: vi.fn(),
  fetchJson: vi.fn(),
  operation: {
    submit: vi.fn(),
    reset: vi.fn(),
    cancelPending: vi.fn(),
    isPending: false,
    operationId: null as string | null,
  },
}));

vi.mock("@iquji-community/web-component", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@iquji-community/web-component")>()),
  IqujiConfirmDialog: (props: IqujiConfirmDialogProps) => {
    mocks.dialog = props;
    return <div>{props.children}</div>;
  },
}));
vi.mock("@tanstack/react-query", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-query")>()),
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));
vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => mocks.operation,
}));
vi.mock("@/shared/api/client", () => ({ fetchJson: mocks.fetchJson }));

const server: Server = {
  id: "server_manager",
  name: "示例管理节点",
  publicHost: "192.0.2.20",
  privateHost: privateIPv4Boundary,
  health: {
    metrics: {
      swarm: true,
      swarmVerificationStatus: "verified",
      swarmRole: "manager",
      swarmManagerAddress: `${privateIPv4Boundary}:2377`,
    },
  },
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
};

beforeEach(() => {
  vi.clearAllMocks();
  mocks.dialog = null;
  mocks.operation.operationId = null;
  mocks.operation.isPending = false;
  mocks.invalidateQueries.mockResolvedValue(undefined);
  mocks.fetchJson.mockResolvedValue({ id: "operation_rebuild" });
  mocks.operation.submit.mockResolvedValue({ id: "operation_rebuild" });
});

describe("Swarm 备份重建确认", () => {
  it("明确维护范围和中断影响，公网草稿与当前私网事实分别显示", () => {
    const markup = renderToStaticMarkup(
      <ServerSwarmRebuild
        server={server}
        initialConnectionMode="public"
        onChanged={mocks.onChanged}
      />,
    );
    expect(markup).toContain(`当前实际连接 私网 · ${privateIPv4Boundary}:2377`);
    expect(markup).toContain('value="192.0.2.20"');
    expect(markup).toContain("维护期间网关会中断");
    expect(markup).toContain("证书和持久化数据");
    expect(markup).toContain("单 manager、单节点");
    expect(markup).toContain("不能包含业务 Swarm 服务、secret 或 config");
    expect(markup).toContain("新集群、节点和网络的 ID 会变化");
    expect(markup).not.toContain("核验当前连接");
    expect(mocks.dialog?.confirmLabel).toBe("确认备份并重建");
    expect(mocks.dialog?.confirmDisabled).toBe(false);
    expect(mocks.fetchJson).not.toHaveBeenCalled();
  });

  it("显式确认后通过正式操作确认链提交重建参数", async () => {
    renderToStaticMarkup(
      <ServerSwarmRebuild
        server={server}
        initialConnectionMode="public"
        onChanged={mocks.onChanged}
      />,
    );
    mocks.dialog?.onConfirm();
    expect(mocks.operation.submit).toHaveBeenCalledWith(
      expect.objectContaining({
        action: "rebuild_docker_swarm",
        targetKind: "server",
        targetId: server.id,
      }),
    );
    const input = mocks.operation.submit.mock.calls[0]?.[0] as ConfirmedOperationInput;
    await input.create("request_example");
    expect(mocks.fetchJson).toHaveBeenCalledWith(`servers/${server.id}/swarm/rebuild`, {
      method: "POST",
      requestKey: "request_example",
      body: { connectionMode: "public", advertiseAddr: "192.0.2.20" },
    });
  });

  it("目标地址不符合所选网络时禁止创建操作", () => {
    renderToStaticMarkup(
      <ServerSwarmRebuild
        server={{ ...server, publicHost: privateIPv4Boundary }}
        initialConnectionMode="public"
        onChanged={mocks.onChanged}
      />,
    );
    expect(mocks.dialog?.confirmDisabled).toBe(true);
    mocks.dialog?.onConfirm();
    expect(mocks.operation.submit).not.toHaveBeenCalled();
    expect(mocks.fetchJson).not.toHaveBeenCalled();
  });

  it("实际地址待核验时保留未知事实，不将目标公网 IP 标为实际地址", () => {
    const markup = renderToStaticMarkup(
      <ServerSwarmRebuild
        server={{ ...server, health: { metrics: {} } }}
        onChanged={mocks.onChanged}
      />,
    );
    expect(markup).toContain("当前实际连接 连接地址待核验");
    expect(markup).toContain('value="192.0.2.20"');
    expect(markup).not.toContain("当前实际连接 公网");
    expect(mocks.dialog?.confirmDisabled).toBe(false);
  });

  it("保留待确认操作以便重试，锁定地址并阻止关闭", () => {
    mocks.operation.operationId = "operation_rebuild";
    const markup = renderToStaticMarkup(
      <ServerSwarmRebuild server={server} onChanged={mocks.onChanged} />,
    );
    expect(mocks.dialog?.mandatory).toBe(true);
    expect(mocks.dialog?.confirmLabel).toBe("重试确认");
    expect(markup.match(/<input[^>]*disabled=""[^>]*>/g)).toEqual(
      expect.arrayContaining([expect.stringContaining(`value="${privateIPv4Boundary}"`)]),
    );
    expect(markup).toContain("取消原操作");
  });

  it.each([true, false])("加入表单仅在 manager 网络不匹配=%s 时提供重建引导", (mismatch) => {
    const role = "worker";
    const markup = renderToStaticMarkup(
      <SwarmInstallFields
        server={{ ...server, id: "server_worker" }}
        role={role}
        managers={[
          {
            ...server,
            health: {
              metrics: {
                swarm: true,
                swarmVerificationStatus: "verified",
                swarmRole: "manager",
                swarmManagerAddress: mismatch ? `${privateIPv4Boundary}:2377` : "192.0.2.20:2377",
              },
            },
          },
        ]}
        managerId={server.id}
        connectionMode="public"
        advertiseAddr="192.0.2.30"
        disabled={false}
        keepExisting={false}
        onManagerChange={() => {}}
        onConnectionModeChange={() => {}}
        onAdvertiseAddrChange={() => {}}
        onManagerChanged={mocks.onChanged}
      />,
    );
    expect(markup.includes("备份并重建 Swarm")).toBe(mismatch);
    expect(markup.includes("请先重建目标 manager 的通信网络")).toBe(mismatch);
    if (mismatch) expect(mocks.dialog?.description).toBe(server.name);
  });
});
