import {
  IconButton,
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiTabs,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { lazy, Suspense, useState } from "react";
import type { FrpTunnel, OperationRun, Server } from "@/contracts/types";
import { errorMessage, fetchJson } from "@/shared/api/client";
import { useAutomationScope } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { toOperationRun } from "@/shared/operations/task-response";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { OperationHistoryButton } from "@/shared/ui/operation-history";
import { fetchTunnel, fetchTunnelProbeHits, previewTunnel, startTunnel, stopTunnel } from "./api";
import { mappingToInput, serverTraefikPorts } from "./mappings";
import { TunnelHistoryTab } from "./TunnelHistoryTab";
import { TunnelOverviewTab } from "./TunnelOverviewTab";
import { TunnelProbeTab } from "./TunnelProbeTab";
import type { FrpTunnelDetail, FrpTunnelMapping } from "./types";
import {
  canStopTunnel,
  formatRelativeTime,
  getTunnelStatusMeta,
  isTunnelActive,
  tunnelBlockReasonLabel,
} from "./utils";

type Tab = "overview" | "probe" | "history" | "config";

/* 源码编辑器只在打开配置页时加载，避免 CodeMirror 进入隧道主包。 */
const TunnelConfigTab = lazy(() =>
  import("./TunnelConfigTab").then((module) => ({ default: module.TunnelConfigTab })),
);

export function TunnelDetail({
  onChanged,
  onDelete,
  onEdit,
  server,
  tunnel,
}: {
  tunnel: FrpTunnel;
  server?: Server;
  onChanged: () => void;
  onDelete: () => void;
  onEdit: () => void;
}) {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<Tab>("overview");
  useAutomationScope("tunnels-detail", tunnel.id, tab, (value) => {
    if (value === "overview" || value === "probe" || value === "config" || value === "history")
      setTab(value);
  });
  const operation = useConfirmedOperation();
  const notify = useIqujiNotification();

  const detailQuery = useQuery({
    queryFn: () => fetchTunnel(tunnel.id),
    queryKey: ["tunnel-detail", tunnel.id],
    refetchInterval: (query) => {
      const status = query.state.data?.status ?? tunnel.status;
      return status === "pending_confirmation" || status === "starting" || status === "stopping"
        ? 2000
        : false;
    },
  });
  const currentStatus = detailQuery.data?.status ?? tunnel.status ?? "stopped";
  const probeHits = useQuery({
    enabled: tab === "probe",
    queryFn: () => fetchTunnelProbeHits(tunnel.id, 1, 100),
    queryKey: ["tunnel-probes", tunnel.id],
    refetchInterval: tab === "probe" && currentStatus === "running" ? 2000 : false,
  });
  const probeHitCount =
    probeHits.dataUpdatedAt >= detailQuery.dataUpdatedAt
      ? probeHits.data?.hitCount
      : detailQuery.data?.probeHitCount;

  const detail: FrpTunnelDetail = {
    ...tunnel,
    mappings: (detailQuery.data?.mappings ??
      (tunnel.mappings as unknown as FrpTunnelMapping[]) ??
      []) as FrpTunnelMapping[],
    probeHitCount: probeHitCount ?? tunnel.probeHitCount ?? 0,
    runCount: detailQuery.data?.runCount ?? tunnel.runCount ?? 0,
    status: currentStatus,
    lastRunAt: detailQuery.data?.lastRunAt ?? tunnel.lastRunAt,
    server: detailQuery.data?.server,
    latestRun: detailQuery.data?.latestRun,
  };

  const { t } = useI18n();
  const statusMeta = getTunnelStatusMeta(detail.status, t);
  const running = canStopTunnel(detail.status);
  const pending = detail.status === "pending_confirmation";
  const pendingTaskId = pending ? detail.latestRun?.startOperationRunId : undefined;
  const active = isTunnelActive(detail.status);
  const serverName = server?.name || detail.server?.name || t("tunnels.noAssociatedServer");
  const serverHost =
    server?.publicHost || detail.server?.publicHost || t("tunnels.unfilledAddress");
  const ports = serverTraefikPorts(server);

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["tunnel-detail", tunnel.id] }),
      queryClient.invalidateQueries({ queryKey: ["tunnel-runs", tunnel.id] }),
      queryClient.invalidateQueries({ queryKey: ["tunnel-probes", tunnel.id] }),
      queryClient.invalidateQueries({ queryKey: ["tunnel-probe-status"] }),
      queryClient.invalidateQueries({ queryKey: ["tunnels"] }),
      queryClient.invalidateQueries({ queryKey: ["servers"] }),
      queryClient.invalidateQueries({ queryKey: ["server-health", detail.serverId] }),
    ]);
    onChanged();
  };

  // 启动与停止都由 Worker 执行；启动前先预检，把阻断原因直接翻译给用户。
  const toggle = async () => {
    if (!running && !pending) {
      try {
        const preview = await previewTunnel(
          {
            mappings: detail.mappings.map(mappingToInput),
            name: detail.name,
            serverId: detail.serverId,
            frpsBindPort: detail.frpsBindPort,
          },
          detail.id,
        );
        const blockers = preview.process?.decision?.blockers ?? [];
        if (preview.process?.decision?.allowed === false) {
          notify.open({
            title: t("tunnels.preflightCheckFailed"),
            description: blockers.map(tunnelBlockReasonLabel).join("；"),
            tone: "danger",
            duration: 0,
          });
          return;
        }
      } catch {
        // 预检请求失败时仍继续提交，服务端会再次校验并返回明确错误。
      }
    }
    void operation
      .submit({
        action: running ? "stop_frp_tunnel" : "start_frp_tunnel",
        targetKind: "frp_tunnel_run",
        // 上一次确认中断时沿用原运行 ID，确认链路可继续确认同一操作。
        targetId: pending ? detail.latestRun?.id : undefined,
        label: running
          ? t("tunnels.stopTunnelAction")
          : pending
            ? t("tunnels.resumeStartAction")
            : t("tunnels.startTunnelAction"),
        create: async (requestKey) => {
          if (running) return toOperationRun(await stopTunnel(tunnel.id, requestKey));
          // 上次确认中断时直接续确认原操作，不再新建运行。
          if (pendingTaskId) return fetchJson<OperationRun>(`operations/runs/${pendingTaskId}`);
          const started = await startTunnel(tunnel.id, requestKey);
          return toOperationRun(started.task);
        },
      })
      .then(() => refresh())
      .catch(() => undefined);
  };

  return (
    <div className="ops-stack">
      <header className="ops-tunnel-header">
        <div className="ops-tunnel-header__main">
          <div className="ops-tunnel-header__title-row">
            <h1 data-ops-guide-object={tunnel.id} className="ops-tunnel-header__title">
              {detail.name}
            </h1>
            <IqujiBadge tone={statusMeta.tone}>{statusMeta.label}</IqujiBadge>
          </div>
          <div className="ops-tunnel-header__actions">
            <OperationHistoryButton scope={{ tunnelId: tunnel.id }} />
            <IqujiButton
              compact
              disabled={((active && !running) || operation.isPending) && !pending}
              loading={operation.isPending}
              onClick={() => void toggle()}
              variant={running ? "secondary" : "primary"}
            >
              <IqujiIcon decorative name={running ? "square" : "play"} />
              {running
                ? t("tunnels.btnStop")
                : pending
                  ? t("tunnels.btnResume")
                  : t("tunnels.btnStart")}
            </IqujiButton>
            <IqujiButton
              data-ops-guide-open="tunnel.edit"
              compact
              disabled={active}
              onClick={onEdit}
              variant="secondary"
            >
              <IqujiIcon decorative name="pencil" />
              {t("common.edit")}
            </IqujiButton>
            <IqujiButton compact disabled={active} onClick={onDelete} variant="ghost">
              <IqujiIcon decorative name="trash" />
              {t("common.delete")}
            </IqujiButton>
            <IconButton
              compact
              disabled={detailQuery.isFetching}
              icon={<IqujiIcon decorative name="rotate-ccw" />}
              label={t("tunnels.refreshAria")}
              onClick={() => void refresh()}
              variant="ghost"
            />
          </div>
        </div>
        <p className="ops-tunnel-meta">
          <span>
            {serverName}（{serverHost}）
          </span>
          <span className="ops-tunnel-meta__divider">
            {t("tunnels.metaFrpsPort", { port: detail.frpsBindPort })}
          </span>
          <span className="ops-tunnel-meta__divider">
            {t("tunnels.metaMappingsCount", { count: detail.mappings.length })}
          </span>
          <span className="ops-tunnel-meta__divider">
            {detail.lastRunAt
              ? t("tunnels.metaLastRun", { time: formatRelativeTime(detail.lastRunAt, t) })
              : t("tunnels.metaNeverRun")}
          </span>
        </p>
        {detail.status === "cleanup_required" ? (
          <IqujiAlert title={t("tunnels.statusCleanupRequired")} tone="warning">
            {t("tunnels.cleanupRequiredNotice")}
          </IqujiAlert>
        ) : active ? (
          <p className="ops-tunnel-config-note">{t("tunnels.runningLockNotice")}</p>
        ) : null}
        {detailQuery.isError ? (
          <IqujiAlert title={t("tunnels.statusLoadFailed")} tone="danger">
            {errorMessage(detailQuery.error)}
          </IqujiAlert>
        ) : null}
      </header>

      <IqujiTabs
        label={t("tunnels.tunnelView")}
        onChange={setTab}
        panelId="tunnel-tab-panel"
        tabs={[
          { label: t("tunnels.tabOverviewLabel"), value: "overview" },
          {
            label: t("tunnels.tabProbeWithCount", { count: detail.probeHitCount ?? 0 }),
            value: "probe",
          },
          {
            label: t("tunnels.tabHistoryWithCount", { count: detail.runCount ?? 0 }),
            value: "history",
          },
          { label: t("tunnels.tabClientConfig"), value: "config" },
        ]}
        value={tab}
      />

      <div id="tunnel-tab-panel" role="tabpanel">
        {tab === "overview" ? (
          <TunnelOverviewTab disabled={active} onEdit={onEdit} tunnel={detail} />
        ) : null}
        {tab === "probe" ? (
          <TunnelProbeTab
            onHitsCleared={onChanged}
            probeHits={probeHits}
            probeMappings={
              currentStatus === "running"
                ? detail.mappings.filter((item) => item.targetType === "http_probe")
                : []
            }
            tunnelId={tunnel.id}
            tunnelName={tunnel.name}
          />
        ) : null}
        {tab === "history" ? <TunnelHistoryTab tunnelId={tunnel.id} /> : null}
        {tab === "config" ? (
          <Suspense fallback={<p className="ops-muted">{t("tunnels.loadingConfigEditor")}</p>}>
            <TunnelConfigTab
              httpPort={ports.httpPort}
              httpsPort={ports.httpsPort}
              serverHost={serverHost}
              tunnel={detail}
            />
          </Suspense>
        ) : null}
      </div>
    </div>
  );
}
