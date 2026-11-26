import {
  type ColumnDef,
  IqujiBadge,
  IqujiButton,
  IqujiDataTable,
  IqujiEmptyState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";
import { workspacePages } from "@/contracts/pages";
import type { MonitoringServer } from "@/features/monitoring/api";
import { formatSampleTime } from "@/features/monitoring/monitoring-utils";
import { getSwarmConnectionFacts } from "@/features/servers/swarm-connection";
import { useI18n } from "@/shared/i18n";

export type SwarmNodeRecord = {
  id?: string;
  serverId?: string;
  serverName?: string;
  publicHost?: string;
  role?: string;
  hostname?: string;
  status?: string;
  availability?: string;
  managerStatus?: string;
  engineVersion?: string;
  clusterId?: string;
  sampledAt?: string;
  sampleStatus?: string;
  connectionMode?: "public" | "private" | "unknown";
  advertiseAddr?: string;
  managerAddress?: string;
};

export function SwarmNodesTable({
  nodes = [],
  servers = [],
  searchKeyword = "",
}: {
  nodes: SwarmNodeRecord[];
  servers: MonitoringServer[];
  searchKeyword: string;
}) {
  const { t, locale } = useI18n();
  const navigate = useNavigate();
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const serverMap = useMemo(() => {
    const map = new Map<string, MonitoringServer>();
    for (const s of servers) {
      map.set(s.serverId, s);
    }
    return map;
  }, [servers]);

  const handleCopy = useCallback(async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedId(text);
      setTimeout(() => setCopiedId(null), 2000);
    } catch {
      // 忽略剪贴板错误
    }
  }, []);

  const filteredNodes = useMemo(() => {
    if (!searchKeyword.trim()) return nodes;
    const kw = searchKeyword.trim().toLowerCase();
    return nodes.filter((n) => {
      const hMatch = n.hostname?.toLowerCase().includes(kw);
      const idMatch = n.id?.toLowerCase().includes(kw);
      const rMatch = n.role?.toLowerCase().includes(kw);
      const sMatch = n.serverId?.toLowerCase().includes(kw);
      const aMatch = `${n.advertiseAddr ?? ""} ${n.managerAddress ?? ""}`
        .toLowerCase()
        .includes(kw);
      return hMatch || idMatch || rMatch || sMatch || aMatch;
    });
  }, [nodes, searchKeyword]);

  const columns = useMemo<ColumnDef<SwarmNodeRecord>[]>(
    () => [
      {
        accessorKey: "hostname",
        header: t("swarm.colNode"),
        cell: ({ row }) => {
          const node = row.original;
          const host = node.hostname || t("swarm.unknownNode");
          const id = node.id || "";
          return (
            <div className="ops-primary-title-cell">
              <span className="ops-swarm-node-title">{host}</span>
              {id ? (
                <div className="ops-swarm-mono-sub">
                  <span>{id.slice(0, 12)}</span>
                  <IqujiButton
                    variant="ghost"
                    type="button"
                    className="ops-copy-host-btn"
                    onClick={() => void handleCopy(id)}
                    aria-label={t("monitoring.copyNodeId")}
                  >
                    <IqujiIcon decorative name={copiedId === id ? "check" : "copy"} size={12} />
                  </IqujiButton>
                </div>
              ) : null}
            </div>
          );
        },
      },
      {
        accessorKey: "role",
        header: t("swarm.colRole"),
        cell: ({ row }) => {
          const node = row.original;
          const isManager = node.role === "manager";
          const isLeader =
            typeof node.managerStatus === "string" &&
            node.managerStatus.toLowerCase().includes("leader");

          return (
            <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <IqujiBadge tone={isManager ? "info" : "neutral"}>
                {isManager ? t("swarm.manager") : t("swarm.worker")}
              </IqujiBadge>
              {isLeader ? (
                <span className="ops-swarm-leader-badge" title={t("swarm.leaderDesc")}>
                  ★ {t("swarm.leader")}
                </span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: "connection",
        header: t("swarm.colConnection"),
        cell: ({ row }) => {
          const node = row.original;
          const facts = getSwarmConnectionFacts(
            {
              swarmRole: node.role,
              swarmConnectionMode: node.connectionMode,
              swarmAdvertiseAddr: node.advertiseAddr,
              swarmManagerAddress: node.managerAddress,
            },
            t,
          );
          return facts.mode === "unknown" ? (
            <span className="ops-muted">{t("swarm.connectionUnknown")}</span>
          ) : (
            <div className="ops-primary-title-cell">
              <span>
                {t(facts.mode === "public" ? "swarm.connectionPublic" : "swarm.connectionPrivate")}
              </span>
              <span className="ops-swarm-mono-text">{facts.endpoint}</span>
            </div>
          );
        },
      },
      {
        accessorKey: "status",
        header: t("swarm.colStatus"),
        cell: ({ row }) => {
          const status = row.original.status?.toLowerCase() ?? "unknown";
          const isReady = status === "ready";
          const isDown = status === "down";
          const tone = isReady ? "success" : isDown ? "danger" : "neutral";

          return (
            <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <span
                className={`ops-status-dot ${isReady ? "ops-status-dot--pulse" : ""}`}
                data-tone={tone}
              />
              <span style={{ fontSize: 13, textTransform: "capitalize" }}>{status}</span>
            </div>
          );
        },
      },
      {
        accessorKey: "availability",
        header: t("swarm.colAvailability"),
        cell: ({ row }) => {
          const avail = row.original.availability?.toLowerCase() ?? "active";
          const tone = avail === "active" ? "success" : avail === "drain" ? "warning" : "neutral";

          return <IqujiBadge tone={tone}>{avail.toUpperCase()}</IqujiBadge>;
        },
      },
      {
        accessorKey: "engineVersion",
        header: t("swarm.colEngine"),
        cell: ({ row }) => {
          const ver = row.original.engineVersion;
          return ver ? (
            <span className="ops-swarm-mono-text">{ver}</span>
          ) : (
            <span style={{ color: "var(--iquji-color-muted)" }}>-</span>
          );
        },
      },
      {
        accessorKey: "serverId",
        header: t("swarm.colServer"),
        cell: ({ row }) => {
          const serverId = row.original.serverId;
          if (!serverId) {
            return <span style={{ color: "var(--iquji-color-muted)" }}>-</span>;
          }
          const server = serverMap.get(serverId);
          const serverName = server?.serverName || serverId;

          return (
            <IqujiButton
              variant="ghost"
              type="button"
              className="ops-table-link-btn"
              onClick={() =>
                navigate({
                  to: workspacePages.servers.path,
                  search: { serverId },
                })
              }
            >
              <span>{serverName}</span>
              <IqujiIcon decorative name="arrow-right" size={12} />
            </IqujiButton>
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
    [t, locale, copiedId, serverMap, navigate, handleCopy],
  );

  if (filteredNodes.length === 0) {
    return (
      <div style={{ padding: 24 }}>
        <IqujiEmptyState
          title={searchKeyword ? t("swarm.emptySearch") : t("common.noData")}
          body={searchKeyword ? t("swarm.emptySearchBody") : t("common.empty")}
          icon={<IqujiIcon decorative name="server" />}
        />
      </div>
    );
  }

  return (
    <IqujiDataTable
      columns={columns}
      data={filteredNodes}
      label={t("swarm.tabNodes")}
      rowKey={(row) => row.id || row.hostname || ""}
    />
  );
}
