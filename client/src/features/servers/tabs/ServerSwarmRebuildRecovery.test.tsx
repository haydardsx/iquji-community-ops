import type { IqujiConfirmDialogProps } from "@iquji-community/web-component";
import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OperationRun } from "@/contracts/types";
import type { ConfirmedOperationInput } from "@/shared/operations/confirmed-operation";
import { renderToStaticMarkup } from "@/test/render";
import { ServerOperationHistory } from "./ServerOperationHistory";
import { ServerSwarmRebuildRecovery } from "./ServerSwarmRebuildRecovery";

const mocks = vi.hoisted(() => ({
  dialogs: [] as IqujiConfirmDialogProps[],
  fetchJson: vi.fn(),
  onClose: vi.fn(),
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
    mocks.dialogs.push(props);
    return <div>{props.children}</div>;
  },
}));
vi.mock("@/shared/ui/inspector-drawer", () => ({
  InspectorDrawer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));
vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => mocks.operation,
}));
vi.mock("@/shared/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/shared/api/client")>()),
  fetchJson: mocks.fetchJson,
}));

const original: OperationRun = {
  id: "operation_original",
  action: "rebuild_docker_swarm",
  targetKind: "server",
  targetId: "server_manager",
  targetName: "示例管理节点",
  status: "failed",
  errorCode: "SWARM_REBUILD_ROLLBACK_FAILED",
  details: { swarmRebuildRecovery: { needsRecovery: true, operationRunId: "operation_original" } },
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:01:00Z",
};

function renderRecovery(run = original) {
  return renderToStaticMarkup(
    <QueryClientProvider client={new QueryClient()}>
      <ServerSwarmRebuildRecovery run={run} onClose={mocks.onClose} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.dialogs = [];
  mocks.operation.operationId = null;
  mocks.operation.isPending = false;
  mocks.operation.submit.mockResolvedValue({ id: "operation_recovery_new" });
  mocks.fetchJson.mockResolvedValue({ id: "operation_recovery_new" });
});

describe("Swarm 继续恢复入口", () => {
  it("确认说明复用原备份和基线，不在展示时直接发起恢复", () => {
    const markup = renderRecovery();
    expect(markup).toContain("继续恢复");
    expect(markup).toContain("不会创建新备份或覆盖原始基线");
    expect(markup).toContain("请先处理上次诊断日志中的外部条件");
    expect(markup).toContain("只有原连接与受管 Traefik 通过实际核验才会完成");
    expect(mocks.dialogs[0]?.confirmLabel).toBe("确认继续恢复");
    expect(mocks.operation.submit).not.toHaveBeenCalled();
    expect(mocks.fetchJson).not.toHaveBeenCalled();
  });

  it("正式确认创建新恢复任务，参数指向原始备份任务", async () => {
    renderRecovery({
      ...original,
      id: "operation_previous_recovery",
      action: "recover_docker_swarm_rebuild",
    });
    mocks.dialogs[0]?.onConfirm();
    expect(mocks.operation.submit).toHaveBeenCalledWith(
      expect.objectContaining({
        action: "recover_docker_swarm_rebuild",
        targetKind: "server",
        targetId: "server_manager",
      }),
    );
    const input = mocks.operation.submit.mock.calls[0]?.[0] as ConfirmedOperationInput;
    await input.create("request_example");
    expect(mocks.fetchJson).toHaveBeenCalledWith("servers/server_manager/swarm/rebuild/recover", {
      method: "POST",
      requestKey: "request_example",
      body: { operationRunId: original.id },
    });
  });

  it("仅有错误码或服务端标记不需恢复时不展示继续恢复", () => {
    expect(renderRecovery({ ...original, details: undefined })).toBe("");
    expect(
      renderRecovery({
        ...original,
        details: { swarmRebuildRecovery: { needsRecovery: false, operationRunId: original.id } },
      }),
    ).toBe("");
    expect(mocks.dialogs).toHaveLength(0);
  });

  it("失败历史保留原失败和恢复失败，每份原备份仅提供一个继续恢复入口", () => {
    const client = new QueryClient();
    client.setQueryData(
      [
        "operation-runs",
        "history",
        { serverId: original.targetId, section: "server-runtime" },
        1,
        "",
      ],
      {
        items: [
          {
            ...original,
            id: "operation_previous_recovery",
            action: "recover_docker_swarm_rebuild",
          },
          original,
        ],
        page: 1,
        pageSize: 20,
        total: 2,
      },
    );
    const markup = renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <ServerOperationHistory serverId={original.targetId} />
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(markup).toContain("备份并重建 Swarm");
    expect(markup).toContain("继续恢复 Swarm");
    expect(markup.match(/>继续恢复</g)).toHaveLength(1);
    expect(mocks.dialogs).toHaveLength(0);
  });
});
