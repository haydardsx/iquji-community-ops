import { describe, expect, it, vi } from "vitest";
import { fetchJson } from "@/shared/api/client";
import { fetchOperationHistory, fetchOperationLogPage, operationQuery } from "./log-api";

vi.mock("@/shared/api/client", () => ({ fetchJson: vi.fn() }));

describe("操作日志查询合同", () => {
  it("默认历史包含所有状态并保留关联资源作用域", async () => {
    await fetchOperationHistory({ serverId: "server/a&b", category: "secrets" }, 2);
    const path = vi.mocked(fetchJson).mock.lastCall?.[0] ?? "";
    const query = new URL(path, "https://ops.example.test").searchParams;
    expect(path.startsWith("operations/runs?")).toBe(true);
    expect(query.get("serverId")).toBe("server/a&b");
    expect(query.get("category")).toBe("secrets");
    expect(query.get("page")).toBe("2");
    expect(query.has("status")).toBe(false);
  });

  it("状态筛选与分页通过服务端查询，不截断为固定最近记录", () => {
    const query = new URLSearchParams(
      operationQuery({ projectId: "project_example" }, 3, "succeeded"),
    );
    expect(query.get("status")).toBe("succeeded");
    expect(query.get("page")).toBe("3");
    expect(query.get("pageSize")).toBe("20");
  });

  it("日志按操作编号定位并遵守单页上限", async () => {
    await fetchOperationLogPage("operation/a&b", 4);
    const path = vi.mocked(fetchJson).mock.lastCall?.[0] ?? "";
    const query = new URL(path, "https://ops.example.test").searchParams;
    expect(query.get("operationRunId")).toBe("operation/a&b");
    expect(query.get("page")).toBe("4");
    expect(query.get("pageSize")).toBe("100");
    expect(query.get("order")).toBe("desc");
  });
});
