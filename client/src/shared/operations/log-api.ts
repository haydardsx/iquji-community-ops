import type { IqujiLogLevel } from "@iquji-community/web-component";
import type { OperationRun, Page } from "@/contracts/types";
import { fetchJson } from "@/shared/api/client";

import type { OperationScope } from "./activity";

export type { OperationScope } from "./activity";

export type OperationLogEntry = {
  id: string;
  operationRunId?: string;
  source: string;
  phase: string;
  level: IqujiLogLevel;
  event: string;
  errorCode?: string;
  message: string;
  timestamp: string;
  errorSummary?: string;
};

// 日志保留纳秒；补齐小数秒后字典序与 UTC 时间顺序一致。
function sortableTimestamp(value: string) {
  return value.replace(
    /(?:\.(\d+))?Z$/,
    (_, fraction: string | undefined) => `.${(fraction ?? "").padEnd(9, "0")}Z`,
  );
}

export function orderedLogEntries(pages: Array<Page<OperationLogEntry>> = []) {
  const entries = new Map(pages.flatMap((page) => page.items).map((entry) => [entry.id, entry]));
  return [...entries.values()].sort(
    (left, right) =>
      sortableTimestamp(left.timestamp).localeCompare(sortableTimestamp(right.timestamp)) ||
      left.id.localeCompare(right.id),
  );
}

export function operationQuery(scope: OperationScope, page: number, status = "") {
  const query = new URLSearchParams({ page: String(page), pageSize: "20" });
  for (const [key, value] of Object.entries(scope)) if (value) query.set(key, value);
  if (status) query.set("status", status);
  return query.toString();
}

export function fetchOperationHistory(scope: OperationScope, page: number, status = "") {
  return fetchJson<Page<OperationRun>>(`operations/runs?${operationQuery(scope, page, status)}`);
}

export function fetchOperationLogPage(operationId: string, page: number) {
  const query = new URLSearchParams({
    operationRunId: operationId,
    page: String(page),
    pageSize: "100",
    order: "desc",
  });
  return fetchJson<Page<OperationLogEntry>>(`logs/runtime?${query}`);
}

export function isFinishedOperation(status: string) {
  return ["succeeded", "failed", "cancelled"].includes(status);
}

export function operationTone(status: string) {
  switch (status) {
    case "succeeded":
      return "success" as const;
    case "failed":
      return "danger" as const;
    case "cancelled":
      return "warning" as const;
    case "running":
      return "info" as const;
    default:
      return "neutral" as const;
  }
}
