import {
  type ColumnDef,
  IconButton,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDataTable,
  IqujiDropdownMenu,
  IqujiEmptyState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import type { ReactNode } from "react";
import { useMemo, useState } from "react";
import type { ContainerPort, Domain, RouteRow, SwarmServiceConfig } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { OperationHistoryButton } from "@/shared/ui/operation-history";
import { SectionPanel } from "@/shared/ui/section-panel";
import { deleteRoute, disableRoute, publishRoute } from "./api";
import { RouteDialog } from "./RouteDialog";
import "./ApplicationSourceSection.css";

type Action = "publish" | "disable" | "delete";

export function ProjectRoutesTab({
  ports,
  applicationId,
  routes,
  domains,
  services,
  onChanged,
  dirty,
  gatewayReady,
  gatewayField,
}: {
  applicationId: string;
  routes: RouteRow[];
  domains: Domain[];
  services: SwarmServiceConfig[];
  ports?: ContainerPort[];
  onChanged: () => void;
  dirty: boolean;
  gatewayReady: boolean;
  gatewayField?: ReactNode;
}) {
  const { t } = useI18n();
  const [editor, setEditor] = useState<{ route: RouteRow | null } | null>(null);
  const [pending, setPending] = useState<{ action: Action; route: RouteRow } | null>(null);
  const [retryPending, setRetryPending] = useState<{ action: Action; route: RouteRow } | null>(
    null,
  );
  const operation = useConfirmedOperation();
  const unresolved = Boolean(operation.operationId);
  const canAddRoute =
    !dirty &&
    services.length > 0 &&
    (ports === undefined || ports.some((port) => port.protocol === "http"));

  const labels: Record<Action, string> = useMemo(
    () => ({
      publish: t("projects.routesPublishToGateway"),
      disable: t("projects.routesDisable"),
      delete: t("projects.routesDelete"),
    }),
    [t],
  );

  const select = (action: Action, route: RouteRow) => {
    operation.reset();
    setRetryPending({ action, route });
    setPending({ action, route });
  };
  const run = async () => {
    if (!pending) return;
    const { action, route } = pending;
    try {
      await operation.submit({
        label: labels[action],
        action: action === "publish" ? "apply_route" : `${action}_route`,
        targetKind: "route",
        targetId: route.id,
        create: (requestKey) =>
          action === "publish"
            ? publishRoute(applicationId, route.id, requestKey)
            : action === "disable"
              ? disableRoute(applicationId, route.id, requestKey)
              : deleteRoute(applicationId, route.id, requestKey),
      });
      setPending(null);
      onChanged();
    } catch {
      /* 统一确认链路保留原 ID。 */
    }
  };
  const columns: ColumnDef<RouteRow>[] = [
    {
      id: "address",
      header: t("projects.domainLabel"),
      cell: ({ row }) => (
        <IqujiButton
          variant="text"
          compact
          disabled={dirty}
          onClick={() => setEditor({ route: row.original })}
        >
          {row.original.domains.map((item) => item.name).join("、") || t("projects.routesNoDomain")}
        </IqujiButton>
      ),
    },
    {
      id: "path",
      header: t("projects.routesPath"),
      cell: ({ row }) => row.original.rules.map((item) => item.path).join("、"),
    },
    {
      id: "services",
      header: t("projects.targetApp"),
      cell: ({ row }) =>
        row.original.services
          .map((item) =>
            row.original.services.length > 1
              ? `${item.serviceName} (${item.weight})`
              : item.serviceName,
          )
          .join("、"),
    },
    {
      id: "status",
      header: t("projects.routesGatewayStatus"),
      cell: ({ row }) => (
        <IqujiBadge
          tone={
            row.original.status === "active"
              ? "success"
              : row.original.status === "failed"
                ? "danger"
                : "neutral"
          }
          title={row.original.lastPublishSummary}
        >
          {row.original.status === "active"
            ? t("projects.routesStatusActive")
            : row.original.status === "disabled"
              ? t("projects.routesStatusDisabled")
              : row.original.status === "failed"
                ? t("projects.routesStatusFailed")
                : row.original.lastApplied
                  ? t("projects.routesStatusModified")
                  : row.original.pendingRemote
                    ? t("projects.routesStatusPendingRemote")
                    : t("projects.routesStatusPendingPublish")}
        </IqujiBadge>
      ),
    },
    {
      id: "actions",
      header: "",
      cell: ({ row }) => (
        <>
          <IqujiDropdownMenu
            ariaLabel={t("projects.routesActions")}
            trigger={
              <IconButton
                disabled={unresolved}
                label={t("projects.routesActions")}
                icon={<IqujiIcon decorative name="more" />}
              />
            }
            items={[
              {
                key: "publish",
                label: t("projects.routesPublishToGateway"),
                onSelect: () => select("publish", row.original),
                disabled: dirty || !gatewayReady,
              },
              {
                key: "disable",
                label: t("projects.routesDisable"),
                onSelect: () => select("disable", row.original),
                disabled: dirty || (!row.original.lastApplied && !row.original.pendingRemote),
              },
              {
                key: "delete",
                label: t("projects.routesDelete"),
                tone: "danger",
                onSelect: () => select("delete", row.original),
                disabled: dirty,
              },
            ]}
          />
        </>
      ),
    },
  ];
  return (
    <div className="ops-form-stack">
      <SectionPanel
        className="ops-routes-panel"
        icon="network"
        title={t("projects.httpRoutes")}
        description={t("projects.routesDraftPrompt")}
        actions={
          <div className="ops-row">
            <OperationHistoryButton
              scope={{ applicationId, section: "application-routes" }}
              label={t("operations.publishLogs")}
            />
            {unresolved ? (
              <IqujiButton compact variant="secondary" onClick={() => setPending(retryPending)}>
                {t("projects.retryLastRunOp")}
              </IqujiButton>
            ) : (
              <IqujiButton
                compact
                variant="secondary"
                iconStart={<IqujiIcon decorative name="plus" size={14} />}
                disabled={!canAddRoute}
                onClick={() => setEditor({ route: null })}
              >
                {t("projects.routesAddInlet")}
              </IqujiButton>
            )}
          </div>
        }
      >
        <div className="ops-routes-panel__content">
          {gatewayField ? (
            <div className="ops-routes-panel__gateway-bar">
              <div className="ops-routes-panel__gateway-field">{gatewayField}</div>
              <div className="ops-routes-panel__gateway-hint">
                <IqujiIcon
                  decorative
                  name={gatewayReady ? "check-circle" : "circle-info"}
                  size={14}
                  className={gatewayReady ? "ops-routes-icon--ready" : "ops-routes-icon--info"}
                />
                <span>
                  {gatewayReady
                    ? t("projects.gatewayConfiguredPrompt")
                    : t("projects.gatewayUnconfiguredPrompt")}
                </span>
              </div>
            </div>
          ) : null}
          {dirty ? (
            <p className="ops-muted ops-routes-panel__dirty">
              {t("projects.routesSaveAppConfigFirst")}
            </p>
          ) : null}
          <div className="ops-routes-panel__table-wrapper">
            {routes.length ? (
              <IqujiDataTable
                label={t("projects.routesTableLabel")}
                columns={columns}
                data={routes}
              />
            ) : (
              <IqujiEmptyState
                icon={<IqujiIcon decorative name="network" size={32} />}
                title={t("projects.noRoutes")}
                body={
                  services.length
                    ? t("projects.routesEmptyWithServices")
                    : t("projects.routesEmptyNoServices")
                }
                action={
                  !unresolved ? (
                    <IqujiButton
                      compact
                      variant="secondary"
                      iconStart={<IqujiIcon decorative name="plus" size={14} />}
                      disabled={!canAddRoute}
                      onClick={() => setEditor({ route: null })}
                    >
                      {t("projects.routesAddInlet")}
                    </IqujiButton>
                  ) : null
                }
              />
            )}
          </div>
        </div>
      </SectionPanel>
      <RouteDialog
        ports={ports}
        applicationId={applicationId}
        domains={domains}
        services={services}
        route={editor?.route ?? null}
        open={editor !== null}
        onSaved={onChanged}
        onOpenChange={(open) => {
          if (!open) setEditor(null);
        }}
      />
      <IqujiConfirmDialog
        open={pending !== null}
        title={
          pending
            ? t("projects.routesConfirmTitle", { action: labels[pending.action] })
            : t("common.confirm")
        }
        description={
          pending?.action === "delete"
            ? t("projects.routesConfirmDeleteDesc")
            : pending?.action === "disable"
              ? t("projects.routesConfirmDisableDesc")
              : t("projects.routesConfirmPublishDesc")
        }
        closeLabel={t("projects.routesCloseConfirm")}
        cancelLabel={t("common.cancel")}
        confirmLabel={
          operation.operationId
            ? t("common.retry")
            : pending
              ? labels[pending.action]
              : t("common.confirm")
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
                  .then(() => setPending(null))
                  .catch(() => {})
              }
            >
              {t("projects.cancelOldOperation")}
            </IqujiButton>
          ) : undefined
        }
        onConfirm={() => void run()}
        onOpenChange={(open) => {
          if (!open) setPending(null);
        }}
      />
    </div>
  );
}
