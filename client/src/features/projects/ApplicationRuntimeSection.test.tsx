import { IqujiOverlayProvider } from "@iquji-community/web-component";
import { describe, expect, it, vi } from "vitest";
import { fetchJson } from "@/shared/api/client";
import { renderToStaticMarkup } from "@/test/render";
import { ApplicationRuntimeSection } from "./ApplicationRuntimeSection";
import { fetchApplicationRuntime } from "./api";

vi.mock("@/shared/api/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/shared/api/client")>()),
  fetchJson: vi.fn(),
}));
vi.mock("@/shared/operations/use-confirmed-operation", () => ({
  useConfirmedOperation: () => ({ isPending: false }),
}));

describe("运行服务端口", () => {
  it("保留运行态里已发布和仅在集群内可见的端口", async () => {
    const ports = [
      {
        name: "web",
        protocol: "http",
        containerPort: 3000,
        publishedPort: 8080,
        publishedHost: "example.test",
        publishedVerified: true,
      },
      { name: "ssh", protocol: "tcp", containerPort: 22, internalVerified: true },
    ];
    vi.mocked(fetchJson).mockResolvedValueOnce({
      status: "running",
      availability: "available",
      checkedAt: "2026-01-01T00:00:00Z",
      services: [
        {
          serviceSlotKey: "main",
          serviceId: "svc_demo",
          serviceName: "demo",
          serverId: "host_demo",
          expectedReplicas: 1,
          desiredReplicas: 1,
          runningReplicas: 1,
          tasks: [],
          ports,
        },
      ],
    });
    const runtime = await fetchApplicationRuntime("app_example");
    expect(runtime.services[0].ports).toEqual(ports);
    const markup = renderToStaticMarkup(
      <IqujiOverlayProvider>
        <ApplicationRuntimeSection
          applicationId="app_example"
          runtime={runtime}
          loading={false}
          error={null}
          onChanged={() => {}}
          dirty={false}
          hasActiveRoutes={false}
        />
      </IqujiOverlayProvider>,
    );
    for (const text of ["main", "host_demo"]) expect(markup).toContain(text);
    expect(markup).not.toContain("<table");
    for (const text of ["web", "HTTP", "容器 3000", "发布 8080", "ssh", "TCP", "容器 22", "未发布"])
      expect(markup).toContain(text);
  });
});
