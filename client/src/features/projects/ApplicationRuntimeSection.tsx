import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDropdownMenu,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMemo, useState } from "react";
import type { ApplicationRuntime, Server } from "@/contracts/types";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { NoticeCarousel, type NoticeItem } from "@/shared/ui/notice-carousel";
import { RecordSnippet } from "@/shared/ui/record-snippet";
import { SectionPanel } from "@/shared/ui/section-panel";
import { runApplicationAction } from "./api";
import { stateLabel, stateTone } from "./model";
import "./ApplicationRuntimeSection.css";

type Action = "start" | "stop" | "restart" | "uninstall";

function computeServiceStatus(
  service: ApplicationRuntime["services"][0],
  t: (key: string) => string,
): {
  label: string;
  tone: "success" | "warning" | "danger" | "neutral" | "info";
} {
  if (service.desiredReplicas === 0) {
    return { label: t("state.stopped") || "已停止", tone: "neutral" };
  }
  if (service.runningReplicas >= service.desiredReplicas) {
    return { label: t("state.running") || "运行中", tone: "success" };
  }
  if (service.runningReplicas > 0) {
    return { label: t("state.degraded") || "部分就绪", tone: "warning" };
  }
  if (service.tasks.some((t) => t.status === "failed" || t.status === "rejected")) {
    return { label: t("state.failed") || "执行失败", tone: "danger" };
  }
  return { label: t("state.starting") || "启动中", tone: "info" };
}

export function ApplicationRuntimeSection({
  applicationId,
  runtime,
  servers = [],
  loading,
  error,
  onChanged,
  dirty,
  hasActiveRoutes,
}: {
  applicationId: string;
  runtime?: ApplicationRuntime;
  servers?: Server[];
  loading: boolean;
  error: unknown;
  onChanged: () => void;
  dirty: boolean;
  hasActiveRoutes: boolean;
}) {
  const { t, locale } = useI18n();
  const [action, setAction] = useState<Action | null>(null);
  const [retryAction, setRetryAction] = useState<Action | null>(null);
  const operation = useConfirmedOperation();
  const available = runtime?.availability === "available";
  const hasStopped = runtime?.services.some((service) => service.desiredReplicas === 0);
  const hasRunning = runtime?.services.some((service) => service.desiredReplicas > 0);

  const labels: Record<Action, string> = useMemo(
    () => ({
      start: t("projects.actionStart"),
      stop: t("projects.actionStop"),
      restart: t("projects.actionRestart"),
      uninstall: t("projects.actionUninstall"),
    }),
    [t],
  );

  const choose = (name: Action) => {
    operation.reset();
    setRetryAction(name);
    setAction(name);
  };
  const submit = async () => {
    if (!action) return;
    try {
      await operation.submit({
        action: action === "uninstall" ? "uninstall_application" : `${action}_application`,
        targetKind: "application",
        targetId: applicationId,
        label: labels[action],
        create: (requestKey) => runApplicationAction(applicationId, action, requestKey),
      });
      setAction(null);
      onChanged();
    } catch {
      /* 由统一操作反馈保留重试入口。 */
    }
  };
  return (
    <SectionPanel
      icon="activity"
      title={t("projects.actualRuntimeState")}
      description={
        t("projects.actualRuntimeStateDesc") || "查看应用的集群实例运行状态、健康状况与副本调度。"
      }
      actions={
        <IqujiDropdownMenu
          ariaLabel={t("projects.runOperations")}
          trigger={
            <IqujiButton
              compact
              variant="secondary"
              data-ops-guide-action={
                hasRunning && !operation.operationId ? "application_restart" : undefined
              }
              disabled={operation.isPending || (!operation.operationId && (!available || dirty))}
            >
              {t("projects.runOperations")}
            </IqujiButton>
          }
          items={
            operation.operationId
              ? [
                  {
                    key: "retry",
                    label: t("projects.retryLastRunOp"),
                    onSelect: () => setAction(retryAction),
                  },
                ]
              : [
                  ...(hasStopped
                    ? [
                        {
                          key: "start",
                          label: t("projects.actionStart"),
                          onSelect: () => choose("start"),
                        },
                      ]
                    : []),
                  ...(hasRunning
                    ? [
                        {
                          key: "stop",
                          label: t("projects.actionStop"),
                          onSelect: () => choose("stop"),
                        },
                      ]
                    : []),
                  {
                    key: "restart",
                    label: t("projects.actionRestart"),
                    disabled: !hasRunning,
                    onSelect: () => choose("restart"),
                  },
                  { kind: "separator", key: "separator" },
                  {
                    key: "uninstall",
                    label: hasActiveRoutes
                      ? t("projects.uninstallServiceDisableRoutesFirst")
                      : t("projects.actionUninstall"),
                    disabled: hasActiveRoutes,
                    tone: "danger",
                    onSelect: () => choose("uninstall"),
                  },
                ]
          }
        />
      }
    >
      {loading ? <IqujiLoadingState label={t("projects.checkingRemoteSwarm")} /> : null}
      {error ? (
        <IqujiAlert title={t("projects.readRuntimeStateFailed")} tone="danger">
          {errorMessage(error)}
        </IqujiAlert>
      ) : null}
      {runtime && !available ? (
        <IqujiAlert
          title={
            runtime.availability === "not_deployed"
              ? t("projects.serviceNotDeployed")
              : t("projects.runtimeUnavailable")
          }
          tone="info"
        >
          {runtime.unavailableReason
            ? operationErrorMessage(runtime.unavailableReason)
            : t("projects.checkConfigPrompt")}
        </IqujiAlert>
      ) : null}
      {available ? (
        <>
          <section className="ops-runtime-slots" aria-label={t("projects.actualServices")}>
            {runtime.services.map((service) => {
              const status = computeServiceStatus(service, t);
              const serverName =
                servers.find((server) => server.id === service.serverId)?.name || service.serverId;

              return (
                <section className="ops-runtime-slot" key={service.serviceSlotKey}>
                  <div className="ops-runtime-slot__header">
                    <div className="ops-runtime-slot__identity">
                      <div className="ops-runtime-slot__icon">
                        <IqujiIcon decorative name="boxes" size={16} />
                      </div>
                      <div className="ops-runtime-slot__title-col">
                        <div className="ops-runtime-slot__headline">
                          <h4 className="ops-runtime-slot__title">
                            {t("projects.serviceSlot")} · {service.serviceSlotKey}
                          </h4>
                          <RecordSnippet
                            value={service.serviceName}
                            label={service.serviceName}
                            truncateLength={24}
                          />
                        </div>
                      </div>
                    </div>
                    <div className="ops-runtime-slot__status">
                      <IqujiBadge tone={status.tone}>{status.label}</IqujiBadge>
                    </div>
                  </div>

                  <div className="ops-runtime-meta-grid">
                    <div className="ops-runtime-meta-item">
                      <span className="ops-runtime-meta-label">
                        <IqujiIcon decorative name="server" size={13} />
                        {t("servers.title")}
                      </span>
                      <div className="ops-runtime-meta-value">
                        <strong>{serverName}</strong>
                      </div>
                    </div>

                    <div className="ops-runtime-meta-item">
                      <span className="ops-runtime-meta-label">
                        <IqujiIcon decorative name="boxes" size={13} />
                        {t("projects.runningReplicasHeader")}
                      </span>
                      <div className="ops-runtime-meta-value">
                        <span className="ops-runtime-replicas-count">
                          <strong>{service.runningReplicas}</strong>
                          <span className="ops-muted"> / </span>
                          <span>{service.desiredReplicas}</span>
                        </span>
                      </div>
                    </div>

                    <div className="ops-runtime-meta-item">
                      <span className="ops-runtime-meta-label">
                        <IqujiIcon decorative name="activity" size={13} />
                        {t("projects.taskStatusHeader")}
                      </span>
                      <div className="ops-runtime-meta-value">
                        {service.tasks.length ? (
                          <div className="ops-runtime-tasks">
                            {service.tasks.map((task) => (
                              <span key={task.id} className="ops-runtime-task-pill">
                                <span
                                  className="ops-runtime-task-dot"
                                  data-tone={stateTone(task.status)}
                                />
                                <span>{stateLabel(task.status, t)}</span>
                                {task.error ? (
                                  <span className="ops-runtime-task-err"> · {task.error}</span>
                                ) : null}
                              </span>
                            ))}
                          </div>
                        ) : (
                          <span className="ops-muted">{t("projects.noTasks")}</span>
                        )}
                      </div>
                    </div>
                  </div>

                  {service.ports?.length ? (
                    <div className="ops-runtime-ports-section">
                      <div className="ops-runtime-ports-section__head">
                        <IqujiIcon decorative name="network" size={13} />
                        <span className="ops-runtime-ports-section__title">
                          {t("projects.ports")}
                        </span>
                      </div>
                      <div className="ops-runtime-ports-grid">
                        {service.ports.map((port) => (
                          <div key={port.name} className="ops-runtime-port-chip">
                            <strong className="ops-runtime-port__name">{port.name}</strong>
                            <span className="ops-runtime-port__protocol">
                              {port.protocol.toUpperCase()}
                            </span>
                            <span className="ops-runtime-port__divider">·</span>
                            <span className="ops-runtime-port__container">
                              {t("projects.runtimeContainerPort", { port: port.containerPort })}
                            </span>
                            <IqujiIcon
                              decorative
                              name="arrow-right"
                              size={12}
                              className="ops-runtime-port__arrow"
                            />
                            <span className="ops-runtime-port__published">
                              {port.publishedPort
                                ? t("projects.runtimePublishedPort", { port: port.publishedPort })
                                : t("projects.runtimePortInternal")}
                            </span>
                          </div>
                        ))}
                      </div>
                    </div>
                  ) : null}
                </section>
              );
            })}
          </section>
          {(() => {
            const noticeItems: NoticeItem[] = [
              {
                id: "check-time",
                icon: "clock",
                tone: "neutral",
                content: t("projects.checkedAt", {
                  time: new Date(runtime.checkedAt).toLocaleString(locale),
                }),
              },
              ...(hasActiveRoutes
                ? [
                    {
                      id: "active-routes",
                      icon: "circle-info" as const,
                      tone: "info" as const,
                      content: t("projects.disableRoutesFirstPrompt"),
                    },
                  ]
                : []),
              ...(dirty
                ? [
                    {
                      id: "dirty-config",
                      icon: "circle-alert" as const,
                      tone: "warning" as const,
                      content: t("projects.saveFirstRunOpPrompt"),
                    },
                  ]
                : []),
            ];
            return (
              <div className="ops-runtime-footer">
                <NoticeCarousel items={noticeItems} />
              </div>
            );
          })()}
        </>
      ) : null}
      <IqujiConfirmDialog
        open={action !== null}
        closeLabel={t("projects.closeRunOpConfirm")}
        title={
          action ? t("projects.confirmOpTitle", { action: labels[action] }) : t("common.confirm")
        }
        description={
          action === "uninstall"
            ? t("projects.confirmUninstallDesc")
            : t("projects.confirmOpDesc", { action: action ? labels[action] : "" })
        }
        cancelLabel={t("common.cancel")}
        confirmLabel={
          operation.operationId ? t("common.retry") : action ? labels[action] : t("common.confirm")
        }
        confirmLoading={operation.isPending}
        additionalActions={
          operation.operationId ? (
            <IqujiButton
              compact
              variant="secondary"
              onClick={() =>
                void operation
                  .cancelPending()
                  .then(() => setAction(null))
                  .catch(() => {})
              }
            >
              {t("projects.cancelOldOperation")}
            </IqujiButton>
          ) : undefined
        }
        onConfirm={() => void submit()}
        onOpenChange={(open) => {
          if (!open) setAction(null);
        }}
      />
    </SectionPanel>
  );
}
