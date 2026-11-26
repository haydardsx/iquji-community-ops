import {
  type ColumnDef,
  IconButton,
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDataTable,
  IqujiInput,
  IqujiLoadingState,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { type UseQueryResult, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { openExternalUrl } from "@/shared/desktop/external-url";
import { useI18n } from "@/shared/i18n";
import { clearTunnelProbeHits, type fetchTunnelProbeHits, fetchTunnelProbeStatus } from "./api";
import { TunnelProbeDetailDrawer } from "./TunnelProbeDetailDrawer";
import type { FrpProbeHit, FrpTunnelMapping } from "./types";
import { copyToClipboard, formatFriendlyTime, formatRelativeTime, getMethodTone } from "./utils";

const METHOD_FILTERS = ["ALL", "GET", "POST", "PUT", "DELETE"] as const;
type MethodFilter = (typeof METHOD_FILTERS)[number];

export function TunnelProbeTab({
  tunnelId,
  tunnelName,
  probeHits: probes,
  probeMappings = [],
  onHitsCleared,
}: {
  tunnelId: string;
  tunnelName: string;
  probeHits: UseQueryResult<Awaited<ReturnType<typeof fetchTunnelProbeHits>>>;
  probeMappings?: FrpTunnelMapping[];
  onHitsCleared?: () => void;
}) {
  const queryClient = useQueryClient();
  const notify = useIqujiNotification();
  const [search, setSearch] = useState("");
  const [methodFilter, setMethodFilter] = useState<MethodFilter>("ALL");
  const [selected, setSelected] = useState<FrpProbeHit | null>(null);
  const [confirmClear, setConfirmClear] = useState(false);
  const [copiedEndpoint, setCopiedEndpoint] = useState(false);
  const [copiedUrl, setCopiedUrl] = useState<string | null>(null);
  const { t } = useI18n();

  const status = useQuery({
    queryFn: fetchTunnelProbeStatus,
    queryKey: ["tunnel-probe-status"],
  });

  const clear = useMutation({
    mutationFn: () => clearTunnelProbeHits(tunnelId),
    onSuccess: async () => {
      setConfirmClear(false);
      await queryClient.invalidateQueries({ queryKey: ["tunnel-probes", tunnelId] });
      await queryClient.invalidateQueries({ queryKey: ["tunnels"] });
      onHitsCleared?.();
    },
  });

  const allHits = probes.data?.items ?? [];
  const hasActiveFilter = methodFilter !== "ALL" || search.trim().length > 0;

  const filtered = useMemo(() => {
    const keyword = search.trim().toLowerCase();
    return allHits.filter((item) => {
      if (methodFilter !== "ALL" && item.method.toUpperCase() !== methodFilter) return false;
      if (!keyword) return true;
      return [item.method, item.mappingName, item.path, item.rawQuery ?? "", item.remoteAddr]
        .join(" ")
        .toLowerCase()
        .includes(keyword);
    });
  }, [allHits, methodFilter, search]);

  const activeProbeEntries = useMemo(
    () => probeMappings.filter((item) => item.targetType === "http_probe" && item.publicUrl),
    [probeMappings],
  );

  const localEndpoint = status.data?.localEndpoint ?? "127.0.0.1:17990";
  const listenerEnabled = Boolean(status.data?.enabled);

  const handleRefresh = () => {
    void probes.refetch();
    void status.refetch();
  };

  const handleCopyEndpoint = async () => {
    if (!(await copyToClipboard(localEndpoint))) return;
    setCopiedEndpoint(true);
    setTimeout(() => setCopiedEndpoint(false), 2000);
  };

  const handleCopyQuickUrl = async (url: string) => {
    if (!(await copyToClipboard(url))) return;
    setCopiedUrl(url);
    setTimeout(() => setCopiedUrl(null), 2000);
  };

  const handleOpenQuickUrl = async (url: string) => {
    if (await openExternalUrl(url)) return;
    notify.open({ title: t("tunnels.openFailed"), description: url, tone: "danger" });
  };

  const columns = useMemo<ColumnDef<FrpProbeHit>[]>(
    () => [
      {
        accessorKey: "method",
        cell: ({ row }) => (
          <div className="ops-probe-method-cell">
            <IqujiBadge tone={getMethodTone(row.original.method)}>{row.original.method}</IqujiBadge>
          </div>
        ),
        header: t("tunnels.probeColMethod"),
      },
      {
        cell: ({ row }) => {
          const full = `${row.original.path}${row.original.rawQuery ? `?${row.original.rawQuery}` : ""}`;
          return (
            <div className="ops-probe-path-cell" title={full}>
              <code className="ops-probe-path-cell__path">{row.original.path}</code>
              {row.original.rawQuery ? (
                <code className="ops-probe-path-cell__query">?{row.original.rawQuery}</code>
              ) : null}
            </div>
          );
        },
        header: t("tunnels.probeColPath"),
        id: "path",
      },
      {
        accessorKey: "mappingName",
        cell: ({ row }) => (
          <span className="ops-probe-mapping-tag">{row.original.mappingName}</span>
        ),
        header: t("tunnels.probeColMapping"),
      },
      {
        accessorKey: "remoteAddr",
        cell: ({ row }) => <code className="ops-probe-source">{row.original.remoteAddr}</code>,
        header: t("tunnels.probeColSource"),
      },
      {
        accessorKey: "receivedAt",
        cell: ({ row }) => (
          <div className="ops-probe-time-cell">
            <span className="ops-probe-time-cell__rel">
              {formatRelativeTime(row.original.receivedAt, t)}
            </span>
            <span className="ops-probe-time-cell__abs">
              {formatFriendlyTime(row.original.receivedAt)}
            </span>
          </div>
        ),
        header: t("tunnels.probeColTime"),
      },
      {
        cell: ({ row }) => (
          <IqujiButton
            compact
            onClick={(event) => {
              event.stopPropagation();
              setSelected(row.original);
            }}
            variant="ghost"
          >
            {t("tunnels.probeDetails")}
            <IqujiIcon decorative name="chevron-right" size={13} />
          </IqujiButton>
        ),
        header: "",
        id: "actions",
      },
    ],
    [t],
  );

  return (
    <div className="ops-stack">
      <section className="ops-probe-workbench">
        <header className="ops-probe-header">
          <div className="ops-probe-header__lead">
            <span className="ops-probe-header__icon" data-enabled={listenerEnabled}>
              <IqujiIcon decorative name="radar" size={18} />
            </span>
            <div className="ops-probe-header__copy">
              <div className="ops-probe-header__title-row">
                <h2 className="ops-probe-header__title">{t("tunnels.probePanelTitle")}</h2>
                <span className="ops-probe-header__count">
                  {t("tunnels.probeCountBadge", { count: allHits.length })}
                </span>
              </div>
              <p className="ops-probe-header__desc">{t("tunnels.probePanelDesc")}</p>
            </div>
          </div>

          <div className="ops-probe-header__actions">
            <div className="ops-probe-listener" data-enabled={listenerEnabled}>
              <span
                className={`ops-status-dot ${listenerEnabled ? "ops-status-dot--pulse" : ""}`}
                data-tone={listenerEnabled ? "success" : "warning"}
              />
              <span className="ops-probe-listener__label">{t("tunnels.probeLocalListen")}</span>
              <code className="ops-probe-listener__endpoint">{localEndpoint}</code>
              <IconButton
                compact
                icon={<IqujiIcon decorative name={copiedEndpoint ? "check" : "copy"} size={12} />}
                label={t("tunnels.probeCopyEndpoint")}
                onClick={() => void handleCopyEndpoint()}
                variant="ghost"
              />
              <IqujiBadge tone={listenerEnabled ? "success" : "warning"}>
                {listenerEnabled ? t("tunnels.probeListening") : t("tunnels.probeNotEnabled")}
              </IqujiBadge>
            </div>

            <IqujiButton
              compact
              disabled={probes.isFetching}
              iconStart={<IqujiIcon decorative name="refresh" size={13} />}
              loading={probes.isFetching}
              onClick={handleRefresh}
              variant="secondary"
            >
              {t("common.refresh")}
            </IqujiButton>

            <IqujiButton
              compact
              disabled={allHits.length === 0 || clear.isPending}
              iconStart={<IqujiIcon decorative name="trash" size={13} />}
              onClick={() => setConfirmClear(true)}
              variant="ghost"
            >
              {t("tunnels.probeClear")}
            </IqujiButton>
          </div>
        </header>

        <div className="ops-probe-toolbar">
          <div className="ops-filter-segmented">
            {METHOD_FILTERS.map((method) => (
              <IqujiButton
                className={`ops-filter-segmented__btn ${
                  methodFilter === method ? "ops-filter-segmented__btn--active" : ""
                }`}
                key={method}
                onClick={() => setMethodFilter(method)}
                type="button"
                variant="ghost"
              >
                {method === "ALL" ? t("tunnels.probeMethodAll") : method}
              </IqujiButton>
            ))}
          </div>

          <div className="ops-probe-toolbar__right">
            {hasActiveFilter && allHits.length > 0 ? (
              <span className="ops-probe-toolbar__match">
                {t("tunnels.probeMatchCount", {
                  matched: filtered.length,
                  total: allHits.length,
                })}
              </span>
            ) : null}
            <div className="ops-search-input-wrap ops-probe-search">
              <span className="ops-search-input-wrap__icon">
                <IqujiIcon decorative name="search" size={14} />
              </span>
              <IqujiInput
                aria-label={t("tunnels.probeSearchAria")}
                onChange={(event) => setSearch(event.currentTarget.value)}
                placeholder={t("tunnels.probeSearchPlaceholder")}
                value={search}
              />
            </div>
          </div>
        </div>

        {status.data?.errorSummary || probes.isError || clear.isError ? (
          <div className="ops-probe-alerts">
            {status.data?.errorSummary ? (
              <IqujiAlert title={t("tunnels.probeListenerError")} tone="warning">
                {operationErrorMessage(status.data.errorSummary)}
              </IqujiAlert>
            ) : null}
            {probes.isError || clear.isError ? (
              <IqujiAlert title={t("tunnels.probeLoadFailed")} tone="danger">
                {errorMessage(probes.error ?? clear.error)}
              </IqujiAlert>
            ) : null}
          </div>
        ) : null}

        <div className="ops-probe-body">
          {probes.isLoading ? (
            <div className="ops-probe-loading">
              <IqujiLoadingState label={t("tunnels.probeLoading")} />
            </div>
          ) : filtered.length === 0 ? (
            <div className="ops-probe-empty">
              <span className="ops-probe-empty__icon">
                <IqujiIcon decorative name={hasActiveFilter ? "search" : "radar"} size={22} />
              </span>
              <h3 className="ops-probe-empty__title">
                {hasActiveFilter
                  ? t("tunnels.probeEmptyTitleSearch")
                  : t("tunnels.probeEmptyTitle")}
              </h3>
              <p className="ops-probe-empty__desc">
                {hasActiveFilter ? t("tunnels.probeEmptyBodySearch") : t("tunnels.probeEmptyBody")}
              </p>

              {hasActiveFilter ? (
                <IqujiButton
                  compact
                  onClick={() => {
                    setSearch("");
                    setMethodFilter("ALL");
                  }}
                  variant="secondary"
                >
                  {t("tunnels.probeResetFilter")}
                </IqujiButton>
              ) : activeProbeEntries.length > 0 ? (
                <div className="ops-probe-quick-entries">
                  <span className="ops-probe-quick-entries__label">
                    {t("tunnels.probeQuickTestTitle")}
                  </span>
                  <div className="ops-probe-quick-entries__list">
                    {activeProbeEntries.map((mapping) => {
                      const url = mapping.publicUrl ?? "";
                      return (
                        <div className="ops-probe-quick-entry" key={mapping.name}>
                          <span className="ops-probe-mapping-tag">{mapping.name}</span>
                          <code className="ops-probe-quick-entry__url">{url}</code>
                          <div className="ops-probe-quick-entry__actions">
                            <IqujiButton
                              compact
                              onClick={() => void handleCopyQuickUrl(url)}
                              variant="secondary"
                            >
                              <IqujiIcon
                                decorative
                                name={copiedUrl === url ? "check" : "copy"}
                                size={12}
                              />
                              {copiedUrl === url ? t("common.copied") : t("common.copy")}
                            </IqujiButton>
                            <IqujiButton
                              compact
                              onClick={() => void handleOpenQuickUrl(url)}
                              variant="ghost"
                            >
                              <IqujiIcon decorative name="external-link" size={12} />
                              {t("tunnels.open")}
                            </IqujiButton>
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              ) : null}
            </div>
          ) : (
            <IqujiDataTable
              columns={columns}
              data={filtered}
              label={t("tunnels.probeTableLabel")}
              onRowClick={(row) => setSelected(row)}
              rowKey={(row) => row.id}
            />
          )}
        </div>
      </section>

      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("tunnels.probeClearClose")}
        confirmLabel={t("tunnels.probeClearConfirm")}
        confirmLoading={clear.isPending}
        description={t("tunnels.probeClearConfirmDesc", { name: tunnelName })}
        onConfirm={() => clear.mutate()}
        onOpenChange={(open) => {
          if (!open) setConfirmClear(false);
        }}
        open={confirmClear}
        title={t("tunnels.probeClearTitle")}
      />

      <TunnelProbeDetailDrawer hit={selected} onClose={() => setSelected(null)} />
    </div>
  );
}
