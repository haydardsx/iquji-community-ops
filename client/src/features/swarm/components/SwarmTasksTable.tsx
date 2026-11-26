import {
  type ColumnDef,
  IqujiBadge,
  IqujiDataTable,
  IqujiEmptyState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMemo } from "react";
import { formatSampleTime } from "@/features/monitoring/monitoring-utils";
import { useI18n } from "@/shared/i18n";

export type SwarmTaskRecord = {
  id?: string;
  serviceId?: string;
  serviceName?: string;
  nodeId?: string;
  desiredState?: string;
  currentState?: string;
  image?: string;
  error?: string;
  clusterId?: string;
  sampledAt?: string;
  sampleStatus?: string;
};

export function SwarmTasksTable({
  tasks = [],
  searchKeyword = "",
}: {
  tasks: SwarmTaskRecord[];
  searchKeyword: string;
}) {
  const { t, locale } = useI18n();

  const filteredTasks = useMemo(() => {
    if (!searchKeyword.trim()) return tasks;
    const kw = searchKeyword.trim().toLowerCase();
    return tasks.filter((task) => {
      const sMatch = task.serviceName?.toLowerCase().includes(kw);
      const idMatch = task.id?.toLowerCase().includes(kw);
      const nMatch = task.nodeId?.toLowerCase().includes(kw);
      const cMatch = task.currentState?.toLowerCase().includes(kw);
      return sMatch || idMatch || nMatch || cMatch;
    });
  }, [tasks, searchKeyword]);

  const columns = useMemo<ColumnDef<SwarmTaskRecord>[]>(
    () => [
      {
        accessorKey: "id",
        header: t("swarm.colTaskId"),
        cell: ({ row }) => {
          const id = row.original.id || "-";
          return <span className="ops-swarm-mono-text">{id.slice(0, 12)}</span>;
        },
      },
      {
        accessorKey: "serviceName",
        header: t("swarm.colTaskService"),
        cell: ({ row }) => {
          const name = row.original.serviceName || row.original.serviceId || "-";
          return <span style={{ fontWeight: 600, color: "var(--iquji-color-text)" }}>{name}</span>;
        },
      },
      {
        accessorKey: "nodeId",
        header: t("swarm.colTaskNode"),
        cell: ({ row }) => {
          const nid = row.original.nodeId || "-";
          return <span className="ops-swarm-mono-text">{nid.slice(0, 12)}</span>;
        },
      },
      {
        accessorKey: "desiredState",
        header: t("swarm.colDesiredState"),
        cell: ({ row }) => {
          const ds = row.original.desiredState || "Running";
          return <IqujiBadge tone="info">{ds}</IqujiBadge>;
        },
      },
      {
        accessorKey: "currentState",
        header: t("swarm.colCurrentState"),
        cell: ({ row }) => {
          const cs = row.original.currentState || "Unknown";
          const isRunning = cs.toLowerCase().includes("running");
          const isFailed = ["failed", "rejected", "shutdown", "error"].some((w) =>
            cs.toLowerCase().includes(w),
          );
          const tone = isRunning ? "success" : isFailed ? "danger" : "neutral";

          return (
            <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <span
                className={`ops-status-dot ${isRunning ? "ops-status-dot--pulse" : ""}`}
                data-tone={tone}
              />
              <span style={{ fontSize: 12, color: "var(--iquji-color-text)" }}>{cs}</span>
            </div>
          );
        },
      },
      {
        accessorKey: "error",
        header: t("swarm.colError"),
        cell: ({ row }) => {
          const err = row.original.error;
          if (!err) {
            return <span style={{ color: "var(--iquji-color-muted)", fontSize: 12 }}>-</span>;
          }
          return (
            <span
              style={{
                color: "#ef4444",
                fontSize: 11,
                fontFamily: "var(--iquji-font-mono)",
                maxWidth: 240,
                display: "inline-block",
                overflow: "hidden",
                textOverflow: "ellipsis",
                whiteSpace: "nowrap",
              }}
              title={err}
            >
              {err}
            </span>
          );
        },
      },
      {
        accessorKey: "sampledAt",
        header: t("swarm.colSampledAt"),
        cell: ({ row }) => {
          const time = row.original.sampledAt;
          return <span className="ops-swarm-mono-text">{formatSampleTime(time, locale)}</span>;
        },
      },
    ],
    [t, locale],
  );

  if (filteredTasks.length === 0) {
    return (
      <div style={{ padding: 24 }}>
        <IqujiEmptyState
          title={searchKeyword ? t("swarm.emptySearch") : t("common.noData")}
          body={searchKeyword ? t("swarm.emptySearchBody") : t("common.empty")}
          icon={<IqujiIcon decorative name="radar" />}
        />
      </div>
    );
  }

  return (
    <IqujiDataTable
      columns={columns}
      data={filteredTasks}
      label={t("swarm.tabTasks")}
      rowKey={(row) => row.id || `${row.serviceName}-${row.nodeId}`}
    />
  );
}
