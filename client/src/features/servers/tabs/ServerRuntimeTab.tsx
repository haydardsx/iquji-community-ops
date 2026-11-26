import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDialog,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useRef, useState } from "react";
import type { OperationRun, Page, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { errorMessage, fetchJson, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { operationActionLabel } from "@/shared/ui/operation-history";
import "./ServerRuntimeDialog.css";
import { SurfacePanel } from "@/shared/ui/surface-panel";
import {
  fetchServers,
  fetchSwarmUninstallImpact,
  installMonitoringAgent,
  installServerComponent,
  type ServerRuntimeComponent,
  type SwarmInstallInput,
  uninstallMonitoringAgent,
  uninstallServerComponent,
} from "../api";
import {
  getRuntimeComponents,
  runtimeComponentPresentation,
  swarmLeavePresentation,
  swarmMembership,
} from "../runtime-presentation";
import {
  getSwarmConnectionFacts,
  type SwarmConnectionMode,
  swarmIdentityVerified,
} from "../swarm-connection";
import { swarmUninstallWaitsForTask } from "../swarm-uninstall-queue";
import { ServerActiveRunsBanner } from "./ServerActiveRunsBanner";
import { ServerGatewaySection } from "./ServerGatewaySection";
import { ServerJenkinsAccess } from "./ServerJenkinsAccess";
import { ServerOperationHistory } from "./ServerOperationHistory";
import { ServerSwarmDepartureNotice } from "./ServerSwarmDepartureNotice";
import { ServerSwarmRebuild } from "./ServerSwarmRebuild";
import { swarmAddressError } from "./SwarmAddressFields";
import { SwarmDepartureImpact } from "./SwarmDepartureImpact";
import { SwarmInstallFields } from "./SwarmInstallFields";

type Component = ServerRuntimeComponent | "agent";
type Action = {
  component: Component;
  kind: "install" | "uninstall";
  title?: string;
};
const actionName: Record<Component, string> = {
  docker: "docker_engine",
  swarm: "docker_swarm",
  traefik: "traefik_gateway",
  frps: "frps",
  jenkins: "jenkins",
  agent: "monitoring_agent",
};

export function ServerRuntimeTab({
  metrics,
  onRefresh,
  server,
}: {
  server: Server;
  metrics: ServerRuntimeMetrics;
  onRefresh: () => void;
}) {
  const { t } = useI18n();
  const [pending, setPending] = useState<Action | null>(null);
  const [jenkinsAccessOpen, setJenkinsAccessOpen] = useState(false);
  const [cancelRun, setCancelRun] = useState<OperationRun | null>(null);
  const notify = useIqujiNotification();
  const [swarmMode, setSwarmMode] = useState<"manager" | "worker">("manager");
  const [managerId, setManagerId] = useState("");
  const [connectionMode, setConnectionMode] = useState<SwarmConnectionMode>("public");
  const [advertiseAddr, setAdvertiseAddr] = useState(server.publicHost || "");
  const connection = getSwarmConnectionFacts(metrics, t);
  const operation = useConfirmedOperation();
  const cancelButton = useRef<HTMLButtonElement>(null);
  const servers = useQuery({
    queryKey: ["servers"],
    queryFn: fetchServers,
  });
  const swarmImpact = useQuery({
    queryKey: ["swarm-uninstall-impact", server.id],
    queryFn: () => fetchSwarmUninstallImpact(server.id),
    enabled: pending?.component === "swarm" && pending.kind === "uninstall",
    staleTime: 0,
  });
  const runs = useQuery({
    queryKey: ["operation-runs", "server", server.id],
    queryFn: () =>
      fetchJson<Page<OperationRun>>(
        `operations/runs?targetKind=server&targetId=${encodeURIComponent(server.id)}`,
      ),
    refetchInterval: (query) =>
      query.state.data?.items.some((run) => run.status === "queued" || run.status === "running")
        ? 2000
        : false,
  });
  const activeRuns = (runs.data?.items ?? []).filter(
    (run) => run.status === "queued" || run.status === "running",
  );
  const cancel = useMutation({
    mutationFn: async () => {
      if (!cancelRun) throw new Error(t("servers.cancelMissingOp"));
      return fetchJson<OperationRun>(`operations/runs/${cancelRun.id}/cancel`, { method: "POST" });
    },
    onSuccess: (run) => {
      setCancelRun(null);
      notify.open({
        title:
          run.status === "cancelled"
            ? t("servers.oldOpCancelled")
            : t("servers.cancelReqSubmitted"),
        tone: "info",
      });
      void runs.refetch();
      onRefresh();
    },
    onError: (error) =>
      notify.open({
        title: t("servers.cancelOldOpFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      }),
  });
  const managers = (servers.data?.items ?? []).filter((item) => {
    const state = (item.health?.metrics ?? {}) as ServerRuntimeMetrics;
    return (
      item.id !== server.id &&
      state.ssh === true &&
      state.docker === true &&
      swarmIdentityVerified(state) &&
      state.swarmRole === "manager" &&
      state.swarmControlAvailable === true
    );
  });
  const components = getRuntimeComponents(metrics, t);

  const reason = (component: Component, uninstall: boolean) => {
    if (metrics.ssh !== true)
      return metrics.ssh === false ? t("servers.sshUnreachable") : t("servers.notProbed");
    if (!uninstall && component !== "docker" && component !== "agent" && metrics.docker !== true)
      return metrics.dockerStatus === "missing"
        ? t("servers.installDockerFirst")
        : t("servers.dockerRequired");
    if (
      !uninstall &&
      (component === "traefik" || component === "frps") &&
      (!swarmIdentityVerified(metrics) ||
        metrics.swarmRole !== "manager" ||
        metrics.swarmControlAvailable !== true)
    ) {
      return t("servers.manageableSwarmManagerRequired");
    }
    if (
      uninstall &&
      component === "docker" &&
      (metrics.swarm || metrics.traefik || metrics.frpsManagedPresent || metrics.jenkins)
    ) {
      return t("servers.uninstallDependenciesFirst");
    }
    if (component === "frps" && uninstall && metrics.frpsInUse) return t("servers.frpsStopFirst");
    if (component === "frps" && metrics.frpsProbeError)
      return operationErrorMessage(metrics.frpsProbeError);
    return "";
  };

  const submit = async () => {
    if (!pending) return;
    const input: SwarmInstallInput =
      swarmMode === "manager"
        ? { mode: "manager", connectionMode, advertiseAddr: advertiseAddr.trim() || undefined }
        : {
            mode: "worker",
            connectionMode: metrics.swarm === true ? undefined : connectionMode,
            managerServerId: managerId,
            advertiseAddr: advertiseAddr.trim() || undefined,
          };
    try {
      await operation.submit({
        action: `${pending.kind}_${actionName[pending.component]}`,
        targetKind: "server",
        targetId: server.id,
        label: pendingTitle,
        create: (requestKey) =>
          pending.component === "agent"
            ? pending.kind === "install"
              ? installMonitoringAgent(server.id, requestKey)
              : uninstallMonitoringAgent(server.id, requestKey)
            : pending.kind === "install"
              ? installServerComponent(server.id, pending.component, input, requestKey)
              : uninstallServerComponent(server.id, pending.component, requestKey),
      });
      setPending(null);
      onRefresh();
    } catch {
      /* 统一操作链路已呈现失败。 */
    }
  };

  const isVerified = swarmIdentityVerified(metrics);
  const openSwarm = (mode: "manager" | "worker") => {
    operation.reset();
    setSwarmMode(mode);
    const currentManager = managers.find((item) => item.id === metrics.swarmManagerServerId);
    setManagerId(currentManager?.id ?? (managers.length === 1 ? managers[0].id : ""));
    const modeChoice =
      metrics.swarm === true && connection.mode === "private" ? "private" : "public";
    setConnectionMode(modeChoice);
    setAdvertiseAddr(metrics.swarm === true ? connection.address : server.publicHost || "");
    const title =
      mode === "manager"
        ? t("servers.createSwarmCluster")
        : metrics.swarm === true
          ? isVerified
            ? t("servers.reverifySwarmCluster")
            : t("servers.associateAndVerifySwarm")
          : t("servers.joinExistingSwarm");
    setPending({ component: "swarm", kind: "install", title });
  };
  const selectedManager = managers.find((item) => item.id === managerId);
  const managerConnection = getSwarmConnectionFacts(
    (selectedManager?.health?.metrics ?? {}) as ServerRuntimeMetrics,
    t,
  );
  const managerModeMismatch =
    metrics.swarm !== true &&
    managerConnection.mode !== "unknown" &&
    managerConnection.mode !== connectionMode;
  const membership = swarmMembership(metrics);
  const swarmLeave = swarmLeavePresentation(metrics, t);
  const canClose = !operation.isPending && !operation.operationId;
  const pendingTitle = pending
    ? (pending.title ?? operationActionLabel(`${pending.kind}_${actionName[pending.component]}`, t))
    : "";
  const pendingState = pending ? runtimeComponentPresentation(metrics, pending.component, t) : null;
  const leavingSwarm = pending?.component === "swarm" && pending.kind === "uninstall";
  const waitsForQueue = swarmUninstallWaitsForTask(swarmImpact.data);
  const impactBlocked = Boolean(swarmImpact.data?.blocked && !waitsForQueue);
  const isConfirmDisabled =
    (leavingSwarm &&
      (swarmImpact.isFetching || swarmImpact.isError || !swarmImpact.data || impactBlocked)) ||
    (pending?.component === "swarm" &&
      pending.kind === "install" &&
      swarmMode === "worker" &&
      (!managers.some((item) => item.id === managerId) || managerModeMismatch)) ||
    (pending?.component === "swarm" &&
      pending.kind === "install" &&
      metrics.swarm !== true &&
      Boolean(swarmAddressError(connectionMode, advertiseAddr, t)));
  const confirmText = operation.operationId
    ? t("servers.retrySubmit")
    : leavingSwarm
      ? swarmLeave.confirmLabel
      : pending?.component === "swarm"
        ? swarmMode === "manager"
          ? t("servers.createCluster")
          : metrics.swarm === true
            ? isVerified
              ? t("servers.reverify")
              : t("servers.associateAndVerify")
            : t("servers.joinCluster")
        : pending?.kind === "uninstall"
          ? t("servers.confirmUninstall")
          : t("servers.confirmInstall");

  return (
    <div className="ops-stack">
      <SurfacePanel
        title={t("servers.runtimeTitle")}
        actions={<ServerOperationHistory serverId={server.id} />}
      >
        {runs.isError ? (
          <IqujiAlert title={t("servers.opStatusFailed")} tone="danger">
            {errorMessage(runs.error)}
          </IqujiAlert>
        ) : null}
        {activeRuns.length ? (
          <ServerActiveRunsBanner
            runs={activeRuns}
            disabled={cancel.isPending || operation.isPending}
            onCancel={setCancelRun}
          />
        ) : null}

        {/* 组件能力与集群成员关系分别展示操作入口。 */}
        <div className="ops-service-list">
          {components.map((item) => {
            const ready =
              item.key === "frps"
                ? metrics.frpsManagedPresent === true
                : metrics[item.key] === true;
            const presentation = runtimeComponentPresentation(metrics, item.key, t);
            const blocked = item.key === "ssh" ? "" : reason(item.key, ready);
            return (
              <div className="ops-service-row" key={item.key}>
                <div className="ops-service-row__lead">
                  <span className="ops-service-row__icon">
                    <IqujiIcon decorative name={item.icon} size={15} />
                  </span>
                  <span className="ops-service-row__name">{item.label}</span>
                </div>

                <div className="ops-service-row__meta">
                  <div className="ops-stack" style={{ gap: "var(--iquji-space-1)", minWidth: 0 }}>
                    {item.desc !== presentation.label ? (
                      <span className="ops-service-row__desc">{item.desc}</span>
                    ) : null}
                    {item.key === "swarm" && !ready ? (
                      <ServerSwarmDepartureNotice server={server} metrics={metrics} />
                    ) : null}
                    {item.key === "swarm" &&
                    (membership === "manager" || membership === "worker") ? (
                      <span
                        className="ops-muted ops-text-caption"
                        style={{ overflowWrap: "anywhere" }}
                      >
                        {connection.label}
                      </span>
                    ) : null}
                  </div>
                  {blocked ? <span className="ops-service-row__blocked">({blocked})</span> : null}
                </div>

                <div className="ops-service-row__action">
                  <IqujiBadge tone={presentation.tone}>{presentation.label}</IqujiBadge>
                  {item.key === "swarm" ? (
                    <>
                      {membership === "manager" &&
                      isVerified &&
                      metrics.swarmControlAvailable === true ? (
                        <ServerSwarmRebuild
                          server={{ ...server, health: { ...server.health, metrics } }}
                          disabled={Boolean(blocked) || operation.isPending}
                          onChanged={onRefresh}
                        />
                      ) : null}
                      {membership === "inactive" || membership === "worker" ? (
                        <IqujiButton
                          compact
                          variant={
                            ready && isVerified
                              ? "secondary"
                              : managers.length
                                ? "primary"
                                : "secondary"
                          }
                          disabled={Boolean(reason("swarm", false)) || operation.isPending}
                          onClick={() => openSwarm("worker")}
                        >
                          {ready
                            ? isVerified
                              ? t("servers.reverify")
                              : t("servers.associateAndVerify")
                            : t("servers.joinExistingCluster")}
                        </IqujiButton>
                      ) : null}
                      {membership === "inactive" ? (
                        <IqujiButton
                          compact
                          variant={managers.length ? "secondary" : "primary"}
                          disabled={Boolean(blocked) || operation.isPending || servers.isPending}
                          onClick={() => openSwarm("manager")}
                        >
                          {t("servers.createNewCluster")}
                        </IqujiButton>
                      ) : membership === "incomplete" ||
                        membership === "worker" ||
                        membership === "manager" ? (
                        <IqujiButton
                          compact
                          variant="secondary"
                          disabled={Boolean(blocked) || operation.isPending}
                          onClick={() => {
                            operation.reset();
                            setPending({
                              component: "swarm",
                              kind: "uninstall",
                              title: swarmLeave.label,
                            });
                          }}
                        >
                          {swarmLeave.label}
                        </IqujiButton>
                      ) : null}
                    </>
                  ) : item.key === "ssh" || (item.key === "frps" && metrics.frpsExternal) ? null : (
                    <>
                      {item.key === "jenkins" && ready ? (
                        <IqujiButton
                          compact
                          variant="secondary"
                          data-ops-guide-open={`server.jenkins.bind/${server.id}`}
                          disabled={operation.isPending}
                          onClick={() => setJenkinsAccessOpen(true)}
                        >
                          {t("servers.jenkinsAccessButton")}
                        </IqujiButton>
                      ) : null}
                      <IqujiButton
                        compact
                        disabled={Boolean(blocked) || operation.isPending}
                        variant={ready ? "secondary" : "primary"}
                        onClick={() => {
                          operation.reset();
                          setPending({
                            component: item.key as Component,
                            kind: ready ? "uninstall" : "install",
                          });
                        }}
                      >
                        {ready ? t("servers.uninstallComponent") : t("servers.installComponent")}
                      </IqujiButton>
                    </>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </SurfacePanel>

      <IqujiConfirmDialog
        open={cancelRun !== null}
        onOpenChange={(open) => {
          if (!open && !cancel.isPending) setCancelRun(null);
        }}
        closeLabel={t("servers.closeCancelConfirm")}
        cancelLabel={t("common.back")}
        confirmLabel={t("servers.confirmCancel")}
        confirmLoading={cancel.isPending}
        title={t("servers.cancelOldOp")}
        description={
          cancelRun
            ? t("servers.cancelOpDesc", {
                name: server.name,
                action: operationActionLabel(cancelRun.action, t),
              })
            : undefined
        }
        onConfirm={() => cancel.mutate()}
      />

      <ServerJenkinsAccess
        key={server.id}
        serverId={server.id}
        servers={servers.data?.items ?? []}
        open={jenkinsAccessOpen}
        onOpenChange={setJenkinsAccessOpen}
      />
      <ServerGatewaySection server={server} />

      <IqujiDialog
        open={pending !== null}
        onOpenChange={(open) => {
          if (!open && canClose) setPending(null);
        }}
        closeLabel={t("servers.closeComponentDialog")}
        title={pendingTitle}
        description={
          pending?.component === "swarm" && pending.kind === "install"
            ? swarmMode === "manager"
              ? t("servers.initManagerDesc", { name: server.name })
              : metrics.swarm === true
                ? isVerified
                  ? t("servers.reverifyMemberDesc", { name: server.name })
                  : t("servers.associateMemberDesc", { name: server.name })
                : t("servers.joinWorkerDesc", { name: server.name })
            : leavingSwarm
              ? swarmLeave.description
              : pending
                ? t(`servers.componentEffect.${pending.kind}.${pending.component}`)
                : undefined
        }
        className="ops-server-runtime-dialog"
        layout="compact"
        mandatory={!canClose}
        initialFocusRef={cancelButton}
        size="md"
        width={
          pending?.component === "swarm" && pending.kind === "install"
            ? "min(92vw, 720px)"
            : "min(92vw, 540px)"
        }
        actions={
          <>
            {operation.operationId ? (
              <IqujiButton
                disabled={operation.isPending}
                variant="secondary"
                onClick={() =>
                  void operation
                    .cancelPending()
                    .then(() => setPending(null))
                    .catch(() => {})
                }
              >
                {t("servers.cancelOriginalOp")}
              </IqujiButton>
            ) : null}
            <IqujiButton
              ref={cancelButton}
              disabled={!canClose}
              onClick={() => setPending(null)}
              variant="secondary"
            >
              {t("common.cancel")}
            </IqujiButton>
            <IqujiButton
              disabled={isConfirmDisabled}
              loading={operation.isPending}
              onClick={() => void submit()}
              variant={pending?.kind === "uninstall" ? "danger" : "primary"}
            >
              {confirmText}
            </IqujiButton>
          </>
        }
      >
        <div className="ops-form-stack">
          {pending && (pending.component !== "swarm" || leavingSwarm) && pendingState ? (
            <dl className="ops-server-runtime-dialog__facts">
              <div>
                <dt>{t("servers.operationTarget")}</dt>
                <dd>{server.name}</dd>
              </div>
              <div>
                <dt>{t("servers.componentStatus")}</dt>
                <dd>
                  <IqujiBadge tone={pendingState.tone}>{pendingState.label}</IqujiBadge>
                </dd>
              </div>
            </dl>
          ) : null}
          {pending?.component === "swarm" && pending.kind === "install" ? (
            <>
              <SwarmInstallFields
                server={{ ...server, health: { ...server.health, metrics } }}
                role={swarmMode}
                managers={managers}
                managerId={managerId}
                connectionMode={connectionMode}
                advertiseAddr={advertiseAddr}
                disabled={operation.isPending || Boolean(operation.operationId)}
                keepExisting={metrics.swarm === true}
                onManagerChange={setManagerId}
                onConnectionModeChange={setConnectionMode}
                onAdvertiseAddrChange={setAdvertiseAddr}
                onManagerChanged={() => {
                  void servers.refetch();
                  onRefresh();
                }}
              />

              {servers.isError ? (
                <IqujiAlert title={t("servers.loadManagersFailed")} tone="danger">
                  {errorMessage(servers.error)}
                </IqujiAlert>
              ) : null}
            </>
          ) : null}

          {leavingSwarm ? (
            <SwarmDepartureImpact
              query={swarmImpact}
              waitsForQueue={waitsForQueue}
              impactBlocked={impactBlocked}
            />
          ) : null}
        </div>
      </IqujiDialog>
    </div>
  );
}
