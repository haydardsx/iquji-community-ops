import {
  type ColumnDef,
  IconButton,
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDataTable,
  IqujiDropdownMenu,
  IqujiLoadingState,
  IqujiSettingsContent,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import type { Certificate, Domain } from "@/contracts/types";
import {
  deleteCertificate,
  deleteDomain,
  fetchCertificates,
  fetchDomainServers,
  fetchDomains,
  updateDomain,
  verifyCertificate,
} from "@/features/domains/api";
import { DomainRedirectDrawer } from "@/features/domains/DomainRedirectDrawer";
import {
  buildDomainTree,
  certificateExpiryLabel,
  certificateStatusLabel,
  certificateStatusTone,
  type DomainTreeNode,
  flattenDomainTree,
  traefikGateways,
} from "@/features/domains/domain-model";
import { CertificateDialog, DomainDialog } from "@/features/domains/EntityDialog";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { OperationHistoryButton } from "@/shared/ui/operation-history";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";

type PendingDeleteTarget = {
  kind: "domain" | "certificate";
  id: string;
  name: string;
};

export function DomainCertificatesSection() {
  const queryClient = useQueryClient();
  const [domainDialog, setDomainDialog] = useState<{ entity: Domain | null } | null>(null);
  const [redirectId, setRedirectId] = useState<string | null>(null);
  const [certificateDialog, setCertificateDialog] = useState<{
    entity: Certificate | null;
    initialDomainId?: string;
  } | null>(null);
  const [pendingDelete, setPendingDelete] = useState<PendingDeleteTarget | null>(null);
  const [collapsedIds, setCollapsedIds] = useState<Set<string>>(() => new Set());
  const notify = useOperationNotice();
  const operation = useConfirmedOperation();
  const { t, locale } = useI18n();

  const domains = useQuery({ queryKey: ["domains"], queryFn: fetchDomains });
  const certificates = useQuery({ queryKey: ["certificates"], queryFn: fetchCertificates });
  const servers = useQuery({ queryKey: ["servers"], queryFn: fetchDomainServers });

  const refresh = () =>
    Promise.all(
      ["domains", "certificates", "resolvers", "ingresses", "project-workspace"].map((key) =>
        queryClient.invalidateQueries({ queryKey: [key] }),
      ),
    );

  const remove = useMutation({
    mutationFn: () => {
      if (!pendingDelete) throw new Error("Missing delete target");
      return pendingDelete.kind === "domain"
        ? deleteDomain(pendingDelete.id)
        : deleteCertificate(pendingDelete.id);
    },
    onSuccess: async () => {
      setPendingDelete(null);
      notify(t("domains.assetDeleted"));
      await refresh();
    },
  });
  useOperationError(remove.error, t("domains.deleteFailed"));

  const unbind = useMutation({
    mutationFn: (domain: Domain) =>
      updateDomain(domain.id, {
        name: domain.name,
        certificateId: "",
        parentDomainId: domain.parentDomainId,
      }),
    onSuccess: async () => {
      notify(t("domains.certUnbound"));
      await refresh();
    },
  });
  useOperationError(unbind.error, t("domains.unbindFailed"));

  const issue = useMutation({
    mutationFn: (certificate: Certificate) =>
      operation.submit({
        action: "verify_certificate",
        targetKind: "certificate",
        targetId: certificate.id,
        label: t("domains.issueCertFor", { name: certificate.name }),
        create: (requestKey) => verifyCertificate(certificate.id, requestKey),
      }),
    onSuccess: () => void refresh(),
  });
  useOperationError(issue.error, t("domains.issueFailed"));

  const domainList = domains.data?.items ?? [];
  const redirectSource = domainList.find((item) => item.id === redirectId);
  const certificateList = certificates.data?.items ?? [];
  // 只有已安装 Traefik 的主机能作为 HTTP-01 验证入口，避免用户选到不可用目标。
  const serverList = traefikGateways(servers.data?.items ?? []);
  const certificateById = useMemo(
    () => new Map(certificateList.map((item) => [item.id, item])),
    [certificateList],
  );

  // 构建域名层级树
  const domainTree = useMemo(() => buildDomainTree(domainList), [domainList]);
  const displayRows = useMemo(
    () => flattenDomainTree(domainTree, collapsedIds),
    [domainTree, collapsedIds],
  );

  // 筛选未关联任何域名的独立证书
  const unassociatedCertificates = useMemo(() => {
    const boundCertIds = new Set(domainList.map((d) => d.certificateId).filter(Boolean));
    return certificateList.filter((cert) => !boundCertIds.has(cert.id));
  }, [domainList, certificateList]);

  const toggleCollapsed = useCallback((id: string) => {
    setCollapsedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }, []);

  const domainColumns = useMemo<ColumnDef<DomainTreeNode>[]>(
    () => [
      {
        id: "name",
        header: t("domains.colDomain"),
        cell: ({ row }) => {
          const item = row.original;
          const isCollapsed = collapsedIds.has(item.domain.id);
          const indent = item.depth * 22;

          return (
            <div
              className="ops-domain-tree-cell"
              style={{ paddingLeft: indent > 0 ? `${indent}px` : undefined }}
            >
              {item.hasChildren ? (
                <IconButton
                  compact
                  icon={
                    <IqujiIcon decorative name={isCollapsed ? "chevron-right" : "chevron-down"} />
                  }
                  label={
                    isCollapsed ? t("domains.expandSubdomains") : t("domains.collapseSubdomains")
                  }
                  onClick={(event) => {
                    event.stopPropagation();
                    toggleCollapsed(item.domain.id);
                  }}
                  variant="ghost"
                />
              ) : item.depth > 0 ? (
                <span aria-hidden="true" className="ops-domain-tree-branch">
                  {item.isLastChild ? "└─" : "├─"}
                </span>
              ) : (
                <span aria-hidden="true" className="ops-domain-tree-leaf-spacer" />
              )}
              <span className="ops-domain-tree-icon">
                <IqujiIcon decorative name={item.depth === 0 ? "globe" : "link"} />
              </span>
              <span className="ops-domain-tree-name">
                {item.depth > 0 && item.parent ? (
                  <>
                    <span className="ops-domain-tree-subpart">{item.subdomainPart}</span>
                    <span className="ops-muted">.{item.parent.name}</span>
                  </>
                ) : (
                  <span className="ops-table-primary-cell">{item.domain.name}</span>
                )}
                {item.domain.certificateId ? (
                  <OperationActivityDot
                    scope={{ targetKind: "certificate", targetId: item.domain.certificateId }}
                  />
                ) : null}
              </span>
              {item.depth === 0 && item.childCount > 0 ? (
                <IqujiBadge tone="neutral">
                  {t("domains.subdomainCount", { count: item.childCount })}
                </IqujiBadge>
              ) : null}
            </div>
          );
        },
      },
      {
        id: "certificate",
        header: t("domains.colCertAndHttps"),
        cell: ({ row }) => {
          const cert = row.original.domain.certificateId
            ? certificateById.get(row.original.domain.certificateId)
            : undefined;

          if (!cert) {
            return <IqujiBadge tone="neutral">{t("domains.httpNoCert")}</IqujiBadge>;
          }

          return (
            <div className="ops-domain-cert-cell">
              <div
                className="ops-row"
                style={{ gap: "var(--iquji-space-2)", justifyContent: "flex-start" }}
              >
                <IqujiBadge tone={certificateStatusTone(cert)}>
                  {certificateStatusLabel(cert, t)}
                </IqujiBadge>
                <span className="ops-muted">
                  {certificateExpiryLabel(cert.expiresAt, undefined, t, locale)}
                </span>
              </div>
              {cert.name && cert.name !== row.original.domain.name ? (
                <span className="ops-muted ops-text-caption">
                  {t("domains.certPrefix", { name: cert.name })}
                </span>
              ) : null}
            </div>
          );
        },
      },
      {
        id: "redirect",
        header: t("domains.redirect"),
        cell: ({ row }) => {
          const asset = row.original.domain;
          if (!asset.redirect) return <span className="ops-muted">—</span>;
          const target = domainList.find((item) => item.id === asset.redirect?.targetDomainId);
          return (
            <div className="ops-stack-xs">
              <span>
                {asset.redirect.scheme}://{target?.name}
              </span>
              <span className="ops-muted">
                {t(
                  asset.redirectRuntime?.status === "active"
                    ? "domains.redirectActive"
                    : asset.redirectRuntime?.status === "failed"
                      ? "domains.redirectFailed"
                      : "domains.redirectPending",
                )}
              </span>
            </div>
          );
        },
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => {
          const domain = row.original.domain;
          const cert = domain.certificateId ? certificateById.get(domain.certificateId) : undefined;

          return (
            <IqujiDropdownMenu
              ariaLabel={t("domains.manageDomainAria", { name: domain.name })}
              items={[
                {
                  key: "edit",
                  label: t("domains.editDomain"),
                  onSelect: () => setDomainDialog({ entity: domain }),
                },
                {
                  key: "redirect",
                  label: t("domains.redirect"),
                  onSelect: () => setRedirectId(domain.id),
                },
                ...(cert
                  ? [
                      {
                        key: "issue",
                        label:
                          cert.status === "valid"
                            ? t("domains.reissueCert")
                            : t("domains.issueCert"),
                        disabled: issue.isPending,
                        onSelect: () => issue.mutate(cert),
                      },
                      {
                        key: "edit_cert",
                        label: t("domains.editCert"),
                        onSelect: () =>
                          setCertificateDialog({ entity: cert, initialDomainId: undefined }),
                      },
                      {
                        key: "unbind_cert",
                        label: t("domains.unbindCert"),
                        disabled: unbind.isPending,
                        onSelect: () => unbind.mutate(domain),
                      },
                    ]
                  : [
                      {
                        key: "apply_cert",
                        label: t("domains.applyCertForDomain"),
                        onSelect: () =>
                          setCertificateDialog({ entity: null, initialDomainId: domain.id }),
                      },
                    ]),
                {
                  key: "delete",
                  label: t("domains.deleteDomainAction"),
                  tone: "danger",
                  onSelect: () =>
                    setPendingDelete({
                      kind: "domain",
                      id: domain.id,
                      name: domain.name,
                    }),
                },
              ]}
              trigger={
                <IconButton
                  compact
                  icon={<IqujiIcon decorative name="more" />}
                  label={t("domains.domainCertManagement")}
                  variant="ghost"
                />
              }
            />
          );
        },
      },
    ],
    [certificateById, collapsedIds, domainList, issue, unbind, toggleCollapsed, t, locale],
  );

  const orphanCertificateColumns = useMemo<ColumnDef<Certificate>[]>(
    () => [
      {
        accessorKey: "name",
        header: t("domains.colCertName"),
        cell: ({ row }) => (
          <span className="ops-row">
            {row.original.name}
            <OperationActivityDot
              scope={{ targetKind: "certificate", targetId: row.original.id }}
            />
          </span>
        ),
      },
      {
        id: "domains",
        header: t("domains.colCoveredDomains"),
        cell: ({ row }) => (
          <span className="ops-muted">{(row.original.domains ?? []).join("、") || "—"}</span>
        ),
      },
      {
        id: "status",
        header: t("domains.colStatus"),
        cell: ({ row }) => (
          <div className="ops-stack-xs">
            <IqujiBadge tone={certificateStatusTone(row.original)}>
              {certificateStatusLabel(row.original, t)}
            </IqujiBadge>
            <span className="ops-muted">
              {certificateExpiryLabel(row.original.expiresAt, undefined, t, locale)}
            </span>
          </div>
        ),
      },
      {
        id: "actions",
        header: "",
        cell: ({ row }) => (
          <IqujiDropdownMenu
            ariaLabel={t("domains.manageCertAria", { name: row.original.name })}
            items={[
              {
                key: "issue",
                label: row.original.status === "valid" ? t("domains.reissue") : t("domains.issue"),
                disabled: issue.isPending,
                onSelect: () => issue.mutate(row.original),
              },
              {
                key: "edit",
                label: t("common.edit"),
                onSelect: () =>
                  setCertificateDialog({ entity: row.original, initialDomainId: undefined }),
              },
              {
                key: "delete",
                label: t("common.delete"),
                tone: "danger",
                onSelect: () =>
                  setPendingDelete({
                    kind: "certificate",
                    id: row.original.id,
                    name: row.original.name,
                  }),
              },
            ]}
            trigger={
              <IconButton
                compact
                icon={<IqujiIcon decorative name="more" />}
                label={t("domains.certManagement")}
                variant="ghost"
              />
            }
          />
        ),
      },
    ],
    [issue, t, locale],
  );

  const hasLoading = domains.isLoading || certificates.isLoading;
  const hasError = domains.isError || certificates.isError;
  const queryError = domains.error || certificates.error;

  return (
    <IqujiSettingsContent
      actions={
        <div className="ops-row" style={{ gap: "var(--iquji-space-2)" }}>
          <OperationHistoryButton scope={{ category: "domain-certificates" }} />
          <IqujiButton
            compact
            onClick={() => setCertificateDialog({ entity: null, initialDomainId: undefined })}
            variant="secondary"
          >
            {t("domains.applyCert")}
          </IqujiButton>
          <IqujiButton compact onClick={() => setDomainDialog({ entity: null })} variant="primary">
            {t("domains.createDomain")}
          </IqujiButton>
        </div>
      }
      description={t("domains.sectionDesc")}
      title={t("domains.title")}
    >
      <div className="ops-stack" id="domain-settings-panel">
        {hasError ? (
          <IqujiAlert title={t("domains.assetsLoadError")} tone="danger">
            {errorMessage(queryError)}
          </IqujiAlert>
        ) : null}
        {hasLoading ? (
          <IqujiLoadingState label={t("domains.loadingAssets")} />
        ) : (
          <IqujiDataTable
            columns={domainColumns}
            data={displayRows}
            emptyBody={t("domains.emptyBody")}
            emptyTitle={t("domains.emptyTitle")}
            label={t("domains.domainsListLabel")}
            rowKey={(row) => row.domain.id}
          />
        )}

        {unassociatedCertificates.length > 0 ? (
          <div className="ops-domain-orphan-section">
            <div className="ops-domain-orphan-header">
              <span className="ops-domain-orphan-title">
                {t("domains.unassociatedCertsTitle", { count: unassociatedCertificates.length })}
              </span>
              <span className="ops-muted ops-text-caption">
                {t("domains.unassociatedCertsDesc")}
              </span>
            </div>
            <IqujiDataTable
              columns={orphanCertificateColumns}
              data={unassociatedCertificates}
              emptyTitle={t("domains.emptyUnassociatedCerts")}
              label={t("domains.unassociatedCertsListLabel")}
              rowKey={(row) => row.id}
            />
          </div>
        ) : null}
      </div>

      <DomainDialog
        certificates={certificateList}
        domain={domainDialog?.entity}
        domains={domainList}
        onOpenChange={(next) => {
          if (!next) setDomainDialog(null);
        }}
        onSaved={() => void refresh()}
        open={domainDialog !== null}
      />
      {redirectSource ? (
        <DomainRedirectDrawer
          key={redirectId}
          source={redirectSource}
          domains={domainList}
          servers={serverList}
          onClose={() => setRedirectId(null)}
          onSaved={refresh}
        />
      ) : null}
      <CertificateDialog
        certificate={certificateDialog?.entity}
        domains={domainList}
        initialDomainId={certificateDialog?.initialDomainId}
        onOpenChange={(next) => {
          if (!next) setCertificateDialog(null);
        }}
        onSaved={() => void refresh()}
        open={certificateDialog !== null}
        servers={serverList}
      />
      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("domains.closeDeleteConfirm")}
        confirmLabel={t("common.delete")}
        confirmLoading={remove.isPending}
        description={
          pendingDelete
            ? pendingDelete.kind === "domain"
              ? t("domains.deleteDomainConfirm", { name: pendingDelete.name })
              : t("domains.deleteCertConfirm", { name: pendingDelete.name })
            : undefined
        }
        onConfirm={() => remove.mutate()}
        onOpenChange={(next) => {
          if (!next) setPendingDelete(null);
        }}
        open={pendingDelete !== null}
        title={t("domains.confirmDeleteTitle")}
      />
    </IqujiSettingsContent>
  );
}
