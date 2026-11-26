import {
  type ColumnDef,
  IqujiAlert,
  IqujiBadge,
  IqujiDataTable,
} from "@iquji-community/web-component";
import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";

import type { Certificate, Ingress, Resolver, Server } from "@/contracts/types";
import {
  fetchCertificates,
  fetchDomainServers,
  fetchIngresses,
  fetchResolvers,
} from "@/features/domains/api";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { SurfacePanel } from "@/shared/ui/surface-panel";

type VerificationEntry = {
  id: string;
  ingress: Ingress;
  resolver: Resolver;
  serverNames: string;
  certificateCount: number;
};

/**
 * 服务器详情只展示该主机承载的 HTTP-01 验证入口；
 * 网关分组与签发入口由证书页面按网关服务器派生，不在这里重复维护。
 */
export function ServerGatewaySection({ server }: { server: Server }) {
  const { t } = useI18n();
  const ingresses = useQuery({ queryKey: ["ingresses"], queryFn: fetchIngresses });
  const resolvers = useQuery({ queryKey: ["resolvers"], queryFn: fetchResolvers });
  const certificates = useQuery({ queryKey: ["certificates"], queryFn: fetchCertificates });
  const allServers = useQuery({ queryKey: ["servers"], queryFn: fetchDomainServers });

  const serverNames = useMemo(() => {
    const map = new Map<string, string>();
    for (const item of allServers.data?.items ?? []) map.set(item.id, item.name);
    return map;
  }, [allServers.data]);

  const entries = useMemo<VerificationEntry[]>(() => {
    const certificateList: Certificate[] = certificates.data?.items ?? [];
    return (resolvers.data?.items ?? []).flatMap((resolver) => {
      const ingress = (ingresses.data?.items ?? []).find((item) => item.id === resolver.ingressId);
      if (!ingress?.serverIds.includes(server.id)) return [];
      return [
        {
          id: `${ingress.id}:${resolver.id}`,
          ingress,
          resolver,
          serverNames: ingress.serverIds.map((id) => serverNames.get(id) ?? id).join(", "),
          certificateCount: certificateList.filter(
            (certificate) => certificate.resolverId === resolver.id,
          ).length,
        },
      ];
    });
  }, [certificates.data, ingresses.data, resolvers.data, server.id, serverNames]);

  const columns = useMemo<ColumnDef<VerificationEntry>[]>(
    () => [
      {
        id: "ingress",
        header: t("servers.gatewayTitle"),
        cell: ({ row }) => <strong>{row.original.ingress.name}</strong>,
      },
      {
        id: "resolver",
        header: t("domains.newCert"),
        cell: ({ row }) => <span className="ops-muted">{row.original.resolver.name}</span>,
      },
      {
        id: "servers",
        header: t("servers.title"),
        cell: ({ row }) => <span className="ops-muted">{row.original.serverNames}</span>,
      },
      {
        id: "certificates",
        header: t("domains.title"),
        cell: ({ row }) => (
          <IqujiBadge tone={row.original.certificateCount > 0 ? "success" : "neutral"}>
            {row.original.certificateCount}
          </IqujiBadge>
        ),
      },
    ],
    [t],
  );

  return (
    <SurfacePanel title={t("servers.gatewayTitle")}>
      {ingresses.isError || resolvers.isError || certificates.isError || allServers.isError ? (
        <IqujiAlert title={t("common.opFailed")} tone="danger">
          {errorMessage(
            ingresses.error ?? resolvers.error ?? certificates.error ?? allServers.error,
          )}
        </IqujiAlert>
      ) : null}
      {entries.length === 0 ? (
        <div className="ops-compact-empty">{t("servers.gatewayDesc")}</div>
      ) : (
        <IqujiDataTable columns={columns} data={entries} label={t("servers.gatewayTitle")} />
      )}
    </SurfacePanel>
  );
}
