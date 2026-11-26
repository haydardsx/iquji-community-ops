import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useOperationLogRead } from "@/shared/operations/use-operation-log-read";
import { renderToStaticMarkup } from "@/test/render";
import { BuildArtifactDrawer } from "./BuildArtifactDrawer";

vi.mock("@/shared/operations/use-operation-log-read", () => ({ useOperationLogRead: vi.fn() }));
vi.mock("@/shared/ui/inspector-drawer", () => ({
  InspectorDrawer: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

beforeEach(() => vi.clearAllMocks());

describe("构建结果已读", () => {
  it.each(["console", "summary", "stage"] as const)("%s 仅在日志正文展示成功时确认已读", (kind) => {
    const run = {
      id: "operation_example",
      action: "request_jenkins_release",
      status: "succeeded",
      targetKind: "application",
      targetId: "application_example",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:01:00Z",
    };
    const url = `/api/logs/builds/build_example/${kind}`;
    const client = new QueryClient();
    client.setQueryData(["application-build-artifact", url, kind], {
      run,
      content: kind === "console" ? "build completed" : { status: "succeeded" },
    });
    renderToStaticMarkup(
      <QueryClientProvider client={client}>
        <IqujiOverlayProvider>
          <BuildArtifactDrawer
            artifact={{ kind, url, operationId: run.id, label: "构建资料" }}
            onClose={() => {}}
          />
        </IqujiOverlayProvider>
      </QueryClientProvider>,
    );
    expect(useOperationLogRead).toHaveBeenCalledWith(run, kind === "console");
  });
});
