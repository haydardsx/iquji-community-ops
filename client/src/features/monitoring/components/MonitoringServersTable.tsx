import {
  type ColumnDef,
  IqujiBadge,
  IqujiButton,
  IqujiDataTable,
  IqujiEmptyState,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo, useState } from "react";
import { workspacePages } from "@/contracts/pages";
import { getSwarmConnectionFacts } from "@/features/servers/swarm-connection";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import type { MonitoringServer } from "../api";
import {
  agentStatusLabel,
  formatPercent,
  formatSampleTime,
  formatTimeAgo,
  getResourceTone,
} from "../monitoring-utils";

export function MonitoringServersTable({
  isLoading,
  items,
  onlyAbnormal,
  searchKeyword,
}: {
  items: MonitoringServer[];
  isLoading: boolean;
  searchKeyword: string;
  onlyAbnormal: boolean;
}) {
  const navigate = useNavigate();
  const { t, locale } = useI18n();
  const [copiedHost, setCopiedHost] = useState<string | null>(null);

  const handleCopyHost = useCallback(async (host: string) => {
    try {
      await navigator.clipboard.writeText(host);
      setCopiedHost(host);
      setTimeout(() => setCopiedHost(null), 2000);
    } catch {
      // 忽略剪贴板错误
    }
  }, []);

  const filteredData = useMemo(() => {
    return items.filter((item) => {
      if (onlyAbnormal) {
        const isOffline =
          item.connectionStatus === "offline" || ["offline", "stale"].includes(item.agentStatus);
        const hasAlerts = (item.alerts && item.alerts.length > 0) || item.sampleStatus === "stale";
        if (!isOffline && !hasAlerts) return false;
      }
      if (searchKeyword.trim()) {
        const kw = searchKeyword.trim().toLowerCase();
        const nMatch = item.serverName?.toLowerCase().includes(kw);
        const hMatch = item.publicHost?.toLowerCase().includes(kw);
        const sMatch = item.serverId?.toLowerCase().includes(kw);
        return nMatch || hMatch || sMatch;
      }
      return true;
    });
  }, [items, onlyAbnormal, searchKeyword]);

  const columns = useMemo<ColumnDef<MonitoringServer>[]>(
    () => [
      {
        accessorKey: "serverName",
        header: t("monitoring.colServerHost"),
        cell: ({ row }) => {
          const publicHost = row.original.publicHost;
          const swarmConnection = getSwarmConnectionFacts(row.original, t);
          return (
            <div className="ops-primary-title-cell">
              <IqujiButton
                variant="ghost"
                type="button"
                className="ops-table-link-btn"
                onClick={() =>
                  navigate({
                    to: workspacePages.servers.path,
                    search: { serverId: row.original.serverId },
                  })
                }
              >
                {row.original.serverName}
                <OperationActivityDot scope={{ serverId: row.original.serverId }} />
              </IqujiButton>
              {publicHost ? (
                <div style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
                  <span className="ops-primary-title-cell__sub">{publicHost}</span>
                  <IqujiButton
                    variant="ghost"
                    type="button"
                    aria-label={t("monitoring.copyHostAddress")}
                    className="ops-copy-host-btn"
                    onClick={() => handleCopyHost(publicHost)}
                    style={{
                      border: "none",
                      background: "transparent",
                      cursor: "pointer",
                      padding: 0,
                      color: copiedHost === publicHost ? "#10b981" : "var(--iquji-color-muted)",
                      display: "inline-flex",
                      alignItems: "center",
                    }}
                  >
                    <IqujiIcon decorative name={copiedHost === publicHost ? "check" : "copy"} />
                  </IqujiButton>
                </div>
              ) : (
                <span className="ops-primary-title-cell__sub">
                  {t("monitoring.unconfiguredExtAddress")}
                </span>
              )}
              {row.original.swarmRole === "manager" || row.original.swarmRole === "worker" ? (
                <span className="ops-primary-title-cell__sub">Swarm {swarmConnection.label}</span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: "resources",
        header: t("monitoring.colResourceWatermark"),
        cell: ({ row }) => {
          const cpu = row.original.cpuPercent;
          const mem = row.original.memoryPercent;
          const disk = row.original.diskPercent;

          if (row.original.sampleStatus !== "fresh") {
            return (
              <span style={{ color: "var(--iquji-color-muted)", fontSize: 12 }}>
                {row.original.sampleStatus === "stale"
                  ? t("monitoring.resourceSampleStale")
                  : t("monitoring.resourceNotSampled")}
              </span>
            );
          }

          return (
            <div className="ops-watermark-row">
              {/* CPU 水位 */}
              <div className="ops-watermark-item">
                <span className="ops-watermark-item__label">CPU</span>
                <div className="ops-watermark-item__bar">
                  <span
                    className="ops-watermark-item__fill"
                    data-tone={getResourceTone(cpu)}
                    style={{ width: `${Math.min(Math.max(cpu ?? 0, 0), 100)}%` }}
                  />
                </div>
                <span className="ops-watermark-item__val">{formatPercent(cpu)}</span>
              </div>

              {/* 内存水位 */}
              <div className="ops-watermark-item">
                <span className="ops-watermark-item__label">{t("monitoring.memoryLabel")}</span>
                <div className="ops-watermark-item__bar">
                  <span
                    className="ops-watermark-item__fill"
                    data-tone={getResourceTone(mem)}
                    style={{ width: `${Math.min(Math.max(mem ?? 0, 0), 100)}%` }}
                  />
                </div>
                <span className="ops-watermark-item__val">{formatPercent(mem)}</span>
              </div>

              {/* 磁盘水位 */}
              <div className="ops-watermark-item">
                <span className="ops-watermark-item__label">{t("monitoring.diskLabel")}</span>
                <div className="ops-watermark-item__bar">
                  <span
                    className="ops-watermark-item__fill"
                    data-tone={getResourceTone(disk)}
                    style={{ width: `${Math.min(Math.max(disk ?? 0, 0), 100)}%` }}
                  />
                </div>
                <span className="ops-watermark-item__val">{formatPercent(disk)}</span>
              </div>
            </div>
          );
        },
      },
      {
        id: "agent",
        header: t("monitoring.colAgentStatus"),
        cell: ({ row }) => {
          const isOnline = row.original.agentStatus === "online";
          const tone = isOnline
            ? "success"
            : ["offline", "stale"].includes(row.original.agentStatus)
              ? "warning"
              : "neutral";
          const version = row.original.agentVersion;
          return (
            <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
              <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
                <span
                  className={`ops-status-dot ${isOnline ? "ops-status-dot--pulse" : ""}`}
                  data-tone={tone}
                />
                <IqujiBadge tone={tone}>{agentStatusLabel(row.original.agentStatus, t)}</IqujiBadge>
              </div>
              {version ? (
                <span
                  style={{
                    fontFamily: "var(--iquji-font-mono, monospace)",
                    fontSize: 11,
                    color: "var(--iquji-color-muted)",
                  }}
                >
                  {version}
                </span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: "heartbeat",
        header: t("monitoring.colLastHeartbeat"),
        cell: ({ row }) => {
          const time = row.original.lastHeartbeatAt;
          const sampledAt = row.original.lastSampleAt;
          return (
            <div style={{ display: "flex", flexDirection: "column" }}>
              <span style={{ fontSize: 13, color: "var(--iquji-color-text)" }}>
                {t("monitoring.heartbeatPrefix", { time: formatTimeAgo(time, t) })}
              </span>
              {time ? (
                <span
                  style={{
                    fontSize: 11,
                    color: "var(--iquji-color-muted)",
                    fontFamily: "var(--iquji-font-mono)",
                  }}
                >
                  {formatSampleTime(time, locale)}
                </span>
              ) : null}
              <span className="ops-primary-title-cell__sub">
                {t("monitoring.samplePrefix", { time: formatSampleTime(sampledAt, locale) })}
              </span>
            </div>
          );
        },
      },
      {
        id: "actions",
        header: t("common.actions"),
        cell: ({ row }) => {
          return (
            <IqujiButton
              compact
              iconEnd={<IqujiIcon decorative name="arrow-right" size={13} />}
              onClick={() =>
                navigate({
                  to: workspacePages.servers.path,
                  search: { serverId: row.original.serverId },
                })
              }
              variant="ghost"
            >
              {t("monitoring.manageServer")}
            </IqujiButton>
          );
        },
      },
    ],
    [copiedHost, handleCopyHost, navigate, locale, t],
  );

  if (isLoading) {
    return <IqujiLoadingState label={t("monitoring.loadingServers")} />;
  }

  if (filteredData.length === 0) {
    return (
      <div style={{ padding: 24 }}>
        <IqujiEmptyState
          body={
            searchKeyword || onlyAbnormal
              ? t("monitoring.noMatchingHostsBody")
              : t("monitoring.noServersBody")
          }
          icon={<IqujiIcon decorative name="server" />}
          title={
            searchKeyword || onlyAbnormal
              ? t("monitoring.noMatchingHostsTitle")
              : t("monitoring.noServersTitle")
          }
        />
      </div>
    );
  }

  return (
    <div className="ops-servers-view-container">
      <IqujiDataTable
        columns={columns}
        data={filteredData}
        label={t("monitoring.serversTableAria")}
        rowKey={(row) => row.serverId}
      />
    </div>
  );
}
