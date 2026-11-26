import {
  type ColumnDef,
  IqujiBadge,
  IqujiButton,
  IqujiDataTable,
  IqujiEmptyState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useCallback, useMemo, useState } from "react";
import { formatSampleTime } from "@/features/monitoring/monitoring-utils";
import { useI18n } from "@/shared/i18n";

export type SwarmServiceRecord = {
  id?: string;
  name?: string;
  image?: string;
  replicas?: string;
  clusterId?: string;
  sampledAt?: string;
  sampleStatus?: string;
};

export function SwarmServicesTable({
  services = [],
  searchKeyword = "",
}: {
  services: SwarmServiceRecord[];
  searchKeyword: string;
}) {
  const { t, locale } = useI18n();
  const [copiedText, setCopiedText] = useState<string | null>(null);

  const handleCopy = useCallback(async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      setCopiedText(text);
      setTimeout(() => setCopiedText(null), 2000);
    } catch {
      // 忽略剪贴板错误
    }
  }, []);

  const filteredServices = useMemo(() => {
    if (!searchKeyword.trim()) return services;
    const kw = searchKeyword.trim().toLowerCase();
    return services.filter((s) => {
      const nMatch = s.name?.toLowerCase().includes(kw);
      const iMatch = s.image?.toLowerCase().includes(kw);
      const idMatch = s.id?.toLowerCase().includes(kw);
      return nMatch || iMatch || idMatch;
    });
  }, [services, searchKeyword]);

  const columns = useMemo<ColumnDef<SwarmServiceRecord>[]>(
    () => [
      {
        accessorKey: "name",
        header: t("swarm.colServiceName"),
        cell: ({ row }) => {
          const item = row.original;
          const name = item.name || t("swarm.unnamedService");
          const id = item.id || "";
          return (
            <div className="ops-primary-title-cell">
              <span className="ops-swarm-node-title">{name}</span>
              {id ? (
                <span className="ops-primary-title-cell__sub">ID: {id.slice(0, 12)}</span>
              ) : null}
            </div>
          );
        },
      },
      {
        accessorKey: "replicas",
        header: t("swarm.colReplicas"),
        cell: ({ row }) => {
          const replicas = row.original.replicas || "0/0";
          const parts = replicas.split("/");
          const isHealthy = parts.length === 2 && parts[0] === parts[1] && parts[0] !== "0";

          return (
            <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <span
                className={`ops-status-dot ${isHealthy ? "ops-status-dot--pulse" : ""}`}
                data-tone={isHealthy ? "success" : "neutral"}
              />
              <IqujiBadge tone={isHealthy ? "success" : "neutral"}>{replicas}</IqujiBadge>
            </div>
          );
        },
      },
      {
        accessorKey: "image",
        header: t("swarm.colImage"),
        cell: ({ row }) => {
          const img = row.original.image || "-";
          return (
            <div style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
              <span className="ops-swarm-mono-text" title={img}>
                {img}
              </span>
              {img !== "-" ? (
                <IqujiButton
                  variant="ghost"
                  type="button"
                  className="ops-copy-host-btn"
                  onClick={() => void handleCopy(img)}
                  aria-label={t("monitoring.copyImage")}
                >
                  <IqujiIcon decorative name={copiedText === img ? "check" : "copy"} size={12} />
                </IqujiButton>
              ) : null}
            </div>
          );
        },
      },
      {
        accessorKey: "clusterId",
        header: t("swarm.colCluster"),
        cell: ({ row }) => {
          const cid = row.original.clusterId;
          return cid ? (
            <span className="ops-swarm-mono-text">{cid.slice(0, 16)}</span>
          ) : (
            <span style={{ color: "var(--iquji-color-muted)" }}>-</span>
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
    [t, locale, copiedText, handleCopy],
  );

  if (filteredServices.length === 0) {
    return (
      <div style={{ padding: 24 }}>
        <IqujiEmptyState
          title={searchKeyword ? t("swarm.emptySearch") : t("common.noData")}
          body={searchKeyword ? t("swarm.emptySearchBody") : t("common.empty")}
          icon={<IqujiIcon decorative name="boxes" />}
        />
      </div>
    );
  }

  return (
    <IqujiDataTable
      columns={columns}
      data={filteredServices}
      label={t("swarm.tabServices")}
      rowKey={(row) => row.id || row.name || ""}
    />
  );
}
