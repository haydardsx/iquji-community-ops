import { IqujiBadge, useIqujiNotification } from "@iquji-community/web-component";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createContext, type ReactNode, useCallback, useContext, useEffect, useRef } from "react";
import type { OperationRun } from "@/contracts/types";
import { errorMessage, fetchJson } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useOperationError } from "@/shared/ui/use-operation-feedback";
import { operationActivityChanges } from "./activity-state";

export type OperationScope = {
  section?: "server-runtime" | "application-routes";
  action?: string;
  targetKind?: string;
  targetId?: string;
  projectId?: string;
  applicationId?: string;
  serverId?: string;
  tunnelId?: string;
  category?: string;
  navigation?: "projects" | "servers" | "tunnels" | "settings";
};

export type OperationActivityItem = OperationRun & {
  active: boolean;
  unread: boolean;
  readVersion: string;
  scope: Omit<OperationScope, "serverId"> & { serverIds: string[]; reachable?: boolean };
};
export type OperationActivity = {
  items: OperationActivityItem[];
  activeCount: number;
  unreadCount: number;
};

const EMPTY_ITEMS: OperationActivityItem[] = [];
type ActivityValue = {
  items: OperationActivityItem[];
  revision: number;
  markRead: (id: string, updatedAt: string, status: string) => Promise<void>;
};
const ActivityContext = createContext<ActivityValue>({
  items: EMPTY_ITEMS,
  revision: 0,
  markRead: async () => {},
});
export const OPERATION_ACTIVITY_QUERY_KEY = ["operation-activity"] as const;

export function matchesOperationScope(item: OperationActivityItem, scope: OperationScope) {
  // 服务器入口只通往自有运行操作日志，关联构建仍由项目入口引导。
  if ((scope.serverId || scope.navigation === "servers") && item.scope.section !== "server-runtime")
    return false;
  return Object.entries(scope).every(([key, value]) => {
    if (!value) return true;
    if (key === "action") return item.action.toLowerCase().includes(value.toLowerCase());
    if (key === "serverId") return item.scope.serverIds.includes(value);
    return item.scope[key as keyof typeof item.scope] === value;
  });
}

export function operationActivityTone(item: OperationActivityItem) {
  if (item.scope.reachable === false) return undefined;
  if (item.status === "failed" && item.unread) return "danger" as const;
  if (
    item.active &&
    ["queued", "running"].includes(item.status) &&
    item.queue?.state !== "pending_confirmation"
  )
    return "info" as const;
  if (item.status === "succeeded" && item.unread) return "success" as const;
  return undefined;
}

/** 同一任务只汇总一次；取消、已读及未确认动作不参与父级提示。 */
export function summarizeOperationActivity(
  items: OperationActivityItem[],
  scopes: OperationScope[] = [{}],
  operationId?: string,
) {
  const matching = [
    ...new Map(
      items
        .filter(
          (item) =>
            (!operationId || item.id === operationId) &&
            scopes.some((scope) => matchesOperationScope(item, scope)),
        )
        .map((item) => [item.id, item]),
    ).values(),
  ];
  const tones = matching.map(operationActivityTone);
  const active = tones.filter((tone) => tone === "info").length;
  const failed = tones.filter((tone) => tone === "danger").length;
  const succeeded = tones.filter((tone) => tone === "success").length;
  const tone = failed
    ? ("danger" as const)
    : active
      ? ("info" as const)
      : succeeded
        ? ("success" as const)
        : undefined;
  return {
    active,
    failed,
    succeeded,
    unread: failed + succeeded,
    total: active + failed + succeeded,
    tone,
  };
}

export const SERVER_MONITORING_QUERY_FAMILIES = [
  "monitoring-swarm",
  "monitoring-servers",
  "monitoring-agents",
] as const;

export function operationQueryFamilies(item: OperationActivityItem): string[] {
  const families = ["operation-runs", "operation-run", "operation-logs"];
  const scope = item.scope;
  if (scope.projectId || scope.applicationId)
    families.push(
      "projects",
      "project-workspace",
      "project-applications",
      "application-runtime",
      "application-builds",
      "deployment-eligibility",
    );
  if (scope.serverIds.length)
    families.push(
      "servers",
      "server-health",
      "server-resources",
      "server-operation-logs",
      "server-jenkins-access",
      ...SERVER_MONITORING_QUERY_FAMILIES,
      "deployment-eligibility",
    );
  if (scope.tunnelId) families.push("tunnels", "tunnel-detail", "tunnel-runs", "tunnel-probes");
  if (scope.category === "secrets") families.push("secrets");
  if (scope.category === "domain-certificates")
    families.push("domains", "certificates", "resolvers", "ingresses");
  return families;
}

/** 应用仅维护一个持久活动查询，各级导航从同一个快照计算红点。 */
export function OperationActivityProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient();
  const { t } = useI18n();
  const notification = useIqujiNotification();
  const previous = useRef<OperationActivityItem[] | null>(null);
  const query = useQuery({
    queryKey: OPERATION_ACTIVITY_QUERY_KEY,
    queryFn: () => fetchJson<OperationActivity>("operations/activity"),
    refetchInterval: (query) => (query.state.data?.activeCount ? 2000 : 10000),
    refetchOnWindowFocus: "always",
    staleTime: 1000,
  });
  useOperationError(query.error, t("activity.readFailed"));
  const items = query.data?.items ?? EMPTY_ITEMS;

  useEffect(() => {
    if (!query.data) return;
    if (previous.current) {
      const keys = new Set(
        operationActivityChanges(previous.current, items).flatMap(operationQueryFamilies),
      );
      for (const key of keys) void client.invalidateQueries({ queryKey: [key] });
    }
    previous.current = items;
  }, [items, client, query.data]);

  const markRead = useCallback(
    async (id: string, updatedAt: string, status: string) => {
      if (!["succeeded", "failed", "cancelled"].includes(status)) return;
      try {
        await fetchJson(`operations/runs/${encodeURIComponent(id)}/read`, {
          method: "POST",
          body: { updatedAt, status },
        });
      } catch (error) {
        notification.open({
          title: t("activity.markReadFailed"),
          description: errorMessage(error),
          tone: "danger",
        });
        throw error;
      }
      client.setQueryData<OperationActivity>(OPERATION_ACTIVITY_QUERY_KEY, (current) => {
        if (!current) return current;
        const items = current.items.filter(
          (item) => item.id !== id || item.readVersion !== `${status}:${updatedAt}`,
        );
        return { ...current, items, unreadCount: items.filter((item) => item.unread).length };
      });
      await client.invalidateQueries({ queryKey: OPERATION_ACTIVITY_QUERY_KEY });
    },
    [client, notification, t],
  );

  return (
    <ActivityContext.Provider value={{ items, markRead, revision: query.dataUpdatedAt }}>
      {children}
    </ActivityContext.Provider>
  );
}

export const useOperationActivity = () => useContext(ActivityContext);

export function OperationActivityDot({
  scope = {},
  scopes,
  operationId,
}: {
  scope?: OperationScope;
  scopes?: OperationScope[];
  operationId?: string;
}) {
  const { items } = useOperationActivity();
  const { t } = useI18n();
  const summary = summarizeOperationActivity(items, scopes ?? [scope], operationId);
  if (!summary.tone) return null;
  const label = t("activity.description", summary);
  return <IqujiBadge variant="dot" tone={summary.tone} aria-label={label} title={label} />;
}
