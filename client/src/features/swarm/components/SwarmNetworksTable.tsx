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

export type SwarmNetworkRecord = {
  id?: string;
  name?: string;
  driver?: string;
  scope?: string;
  clusterId?: string;
  sampledAt?: string;
  sampleStatus?: string;
};

export function SwarmNetworksTable({
  networks = [],
  searchKeyword = "",
}: {
  networks: SwarmNetworkRecord[];
  searchKeyword: string;
}) {
  const { t, locale } = useI18n();

  const filteredNetworks = useMemo(() => {
    if (!searchKeyword.trim()) return networks;
    const kw = searchKeyword.trim().toLowerCase();
    return networks.filter((net) => {
      const nMatch = net.name?.toLowerCase().includes(kw);
      const idMatch = net.id?.toLowerCase().includes(kw);
      const dMatch = net.driver?.toLowerCase().includes(kw);
      const sMatch = net.scope?.toLowerCase().includes(kw);
      return nMatch || idMatch || dMatch || sMatch;
    });
  }, [networks, searchKeyword]);

  const columns = useMemo<ColumnDef<SwarmNetworkRecord>[]>(
    () => [
      {
        accessorKey: "name",
        header: t("swarm.colNetworkName"),
        cell: ({ row }) => {
          const name = row.original.name || "-";
          return <span style={{ fontWeight: 600, color: "var(--iquji-color-text)" }}>{name}</span>;
        },
      },
      {
        accessorKey: "id",
        header: t("swarm.colNetworkId"),
        cell: ({ row }) => {
          const id = row.original.id || "-";
          return <span className="ops-swarm-mono-text">{id.slice(0, 12)}</span>;
        },
      },
      {
        accessorKey: "driver",
        header: t("swarm.colDriver"),
        cell: ({ row }) => {
          const driver = row.original.driver || "overlay";
          return <IqujiBadge tone="info">{driver}</IqujiBadge>;
        },
      },
      {
        accessorKey: "scope",
        header: t("swarm.colScope"),
        cell: ({ row }) => {
          const scope = row.original.scope || "swarm";
          return <IqujiBadge tone="neutral">{scope}</IqujiBadge>;
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
    [t, locale],
  );

  if (filteredNetworks.length === 0) {
    return (
      <div style={{ padding: 24 }}>
        <IqujiEmptyState
          title={searchKeyword ? t("swarm.emptySearch") : t("common.noData")}
          body={searchKeyword ? t("swarm.emptySearchBody") : t("common.empty")}
          icon={<IqujiIcon decorative name="network" />}
        />
      </div>
    );
  }

  return (
    <IqujiDataTable
      columns={columns}
      data={filteredNetworks}
      label={t("swarm.tabNetworks")}
      rowKey={(row) => row.id || row.name || ""}
    />
  );
}
