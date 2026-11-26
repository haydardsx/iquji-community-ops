import {
  type ColumnDef,
  IconButton,
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDataTable,
  IqujiDropdownMenu,
  IqujiEmptyState,
  IqujiInput,
  IqujiLoadingState,
  IqujiSettingsContent,
  IqujiTabs,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useState } from "react";
import type { SecretAsset } from "@/contracts/types";
import {
  deleteSecretAsset,
  fetchSecretAssets,
  fetchSecretDeletePreview,
  SECRET_ASSETS_QUERY_KEY,
} from "@/features/secrets/api";
import { SecretCopySnippet } from "@/features/secrets/components/SecretCopySnippet";
import { GitTokenFormDrawer } from "@/features/secrets/GitTokenFormDrawer";
import { SecretDetailDrawer } from "@/features/secrets/SecretDetailDrawer";
import { SecretRotationDrawer } from "@/features/secrets/SecretRotationDrawer";
import { SshKeyFormDrawer } from "@/features/secrets/SshKeyFormDrawer";
import {
  assetTypeLabel,
  assetTypeTone,
  countByTab,
  filterByQuery,
  filterByTab,
  linkedServers,
  materialLabel,
  materialTone,
  type SecretTab,
} from "@/features/secrets/secrets-model";
import { fetchServers } from "@/features/servers/api";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { OperationHistoryButton } from "@/shared/ui/operation-history";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";

export function SecretsSettingsSection() {
  const { t, locale } = useI18n();
  const queryClient = useQueryClient();
  const [tab, setTab] = useState<SecretTab>("all");
  const [query, setQuery] = useState("");
  const [sshDrawerOpen, setSshDrawerOpen] = useState(false);
  const [gitDrawerOpen, setGitDrawerOpen] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [rotationTarget, setRotationTarget] = useState<SecretAsset | null>(null);
  const [pendingDelete, setPendingDelete] = useState<SecretAsset | null>(null);
  const notify = useOperationNotice();

  const assets = useQuery({ queryKey: SECRET_ASSETS_QUERY_KEY, queryFn: fetchSecretAssets });
  const servers = useQuery({ queryKey: ["servers"], queryFn: fetchServers });
  const serverList = useMemo(() => servers.data?.items ?? [], [servers.data]);
  const allItems = useMemo(() => assets.data?.items ?? [], [assets.data]);
  const [appOpen, setAppOpen] = useState(false);
  const [registryOpen, setRegistryOpen] = useState(false);
  const preview = useQuery({
    queryFn: () => fetchSecretDeletePreview(pendingDelete?.id ?? ""),
    queryKey: ["secret-delete-preview", pendingDelete?.id],
    enabled: Boolean(pendingDelete?.id),
  });
  useOperationError(preview.error, t("secrets.deleteInfluenceFailed"));

  const remove = useMutation({
    mutationFn: async () => {
      if (!pendingDelete) throw new Error(t("secrets.missingDeleteTarget"));
      return deleteSecretAsset(pendingDelete.id);
    },
    onSuccess: async () => {
      const name = pendingDelete?.name ?? t("secrets.newSecret");
      setPendingDelete(null);
      setSelectedId(null);
      setRotationTarget(null);
      notify(t("secrets.deleteSuccessNotif", { name }));
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: SECRET_ASSETS_QUERY_KEY }),
        queryClient.invalidateQueries({ queryKey: ["servers"] }),
      ]);
    },
  });
  useOperationError(remove.error, t("common.opFailed"));

  const counts = countByTab(allItems);
  const visible = useMemo(
    () => filterByQuery(filterByTab(allItems, tab), query, serverList),
    [allItems, tab, query, serverList],
  );
  const selected = allItems.find((item) => item.id === selectedId) ?? null;

  const openDetail = useCallback((id: string) => {
    setSelectedId(id);
  }, []);

  const columns = useMemo<ColumnDef<SecretAsset>[]>(
    () => [
      {
        accessorKey: "name",
        header: t("secrets.colName"),
        cell: ({ row }) => {
          const icon =
            row.original.assetType === "ssh_key"
              ? "key-round"
              : row.original.assetType === "git_token"
                ? "code"
                : "server-cog";
          return (
            <div className="ops-table-title-cell">
              <span className="ops-table-title-cell__icon">
                <IqujiIcon decorative name={icon} size={16} />
              </span>
              <div className="ops-stack-xs">
                <strong className="ops-table-primary-cell">
                  {row.original.name}
                  <OperationActivityDot
                    scope={{ targetKind: "secret_asset", targetId: row.original.id }}
                  />
                </strong>
                <span className="ops-muted ops-text-micro">
                  {row.original.description || t("secrets.noRemark")}
                </span>
              </div>
            </div>
          );
        },
      },
      {
        cell: ({ row }) => (
          <IqujiBadge tone={assetTypeTone(row.original.assetType)}>
            {assetTypeLabel(row.original.assetType, t)}
          </IqujiBadge>
        ),
        header: t("secrets.colType"),
        id: "type",
      },
      {
        cell: ({ row }) => (
          <SecretCopySnippet
            displayValue={row.original.maskedSummary ?? "••••••••"}
            label={t("secrets.colMasked")}
            value={row.original.maskedSummary ?? ""}
          />
        ),
        header: t("secrets.colMasked"),
        id: "masked",
      },
      {
        cell: ({ row }) => {
          if (row.original.assetType === "registry_secret")
            return <span>{t("secrets.registryPurpose")}</span>;
          if (row.original.assetType === "application_secret")
            return <span>{t("secrets.purposeApplicationSecret")}</span>;
          if (row.original.assetType === "git_token") {
            return <span className="ops-muted">{t("secrets.repoAuth")}</span>;
          }
          const names = linkedServers(row.original.id, serverList);
          return (
            <span className="ops-muted">
              {names.length > 0 ? names.join("、") : t("secrets.unboundStatus")}
            </span>
          );
        },
        header: t("secrets.colLinked"),
        id: "linked",
      },
      {
        cell: ({ row }) => (
          <IqujiBadge tone={materialTone(row.original.materialStatus)}>
            {materialLabel(row.original.materialStatus, t)}
          </IqujiBadge>
        ),
        header: t("secrets.colMaterial"),
        id: "material",
      },
      {
        cell: ({ row }) => (
          <span className="ops-muted">
            {row.original.rotatedAt
              ? new Date(row.original.rotatedAt).toLocaleDateString(locale)
              : "—"}
          </span>
        ),
        header: t("secrets.colRotated"),
        id: "rotated",
      },
      {
        id: "actions",
        header: t("secrets.colActions"),
        cell: ({ row }) => (
          <div className="ops-table-actions-cell" data-no-row-click>
            <IqujiDropdownMenu
              ariaLabel={t("secrets.actionMenuAria", { name: row.original.name })}
              items={[
                {
                  key: "detail",
                  label: t("secrets.actionDetail"),
                  onSelect: () => openDetail(row.original.id),
                },
                ...[
                  {
                    key: "rotate",
                    label: t("secrets.actionRotate"),
                    disabled: row.original.materialStatus === "pending",
                    onSelect: () => {
                      setSelectedId(null);
                      setRotationTarget(row.original);
                    },
                  },
                  {
                    key: "delete",
                    label: t("secrets.actionDelete"),
                    tone: "danger" as const,
                    onSelect: () => {
                      setSelectedId(null);
                      setPendingDelete(row.original);
                    },
                  },
                ],
              ]}
              trigger={
                <IconButton
                  compact
                  icon={<IqujiIcon decorative name="more" />}
                  label={t("secrets.actionMore")}
                  onClick={(e) => e.stopPropagation()}
                  variant="ghost"
                />
              }
            />
          </div>
        ),
      },
    ],
    [serverList, openDetail, t, locale],
  );

  const renderHeaderActions = () => {
    if (tab === "ssh") {
      return (
        <IqujiButton onClick={() => setSshDrawerOpen(true)} variant="primary">
          <IqujiIcon decorative name="plus" size={14} /> {t("secrets.newSshKey")}
        </IqujiButton>
      );
    }
    if (tab === "git") {
      return (
        <IqujiButton onClick={() => setGitDrawerOpen(true)} variant="primary">
          <IqujiIcon decorative name="plus" size={14} /> {t("secrets.newGitToken")}
        </IqujiButton>
      );
    }
    if (tab === "registry")
      return (
        <IqujiButton onClick={() => setRegistryOpen(true)}>
          {t("secrets.newRegistrySecret")}
        </IqujiButton>
      );
    if (tab === "application") {
      return (
        <IqujiButton onClick={() => setAppOpen(true)}>
          {t("secrets.newApplicationSecret")}
        </IqujiButton>
      );
    }
    return (
      <IqujiDropdownMenu
        ariaLabel={t("secrets.newSecretMenuAria")}
        items={[
          {
            key: "registry",
            label: t("secrets.newRegistrySecret"),
            onSelect: () => setRegistryOpen(true),
          },
          {
            key: "new-app",
            label: t("secrets.newApplicationSecret"),
            onSelect: () => setAppOpen(true),
          },
          {
            key: "new-ssh",
            label: t("secrets.newSshKey"),
            onSelect: () => setSshDrawerOpen(true),
          },
          {
            key: "new-git",
            label: t("secrets.newGitToken"),
            onSelect: () => setGitDrawerOpen(true),
          },
        ]}
        trigger={
          <IqujiButton variant="primary">
            <IqujiIcon decorative name="plus" size={14} /> {t("secrets.newSecret")}
            <IqujiIcon decorative name="chevron-down" size={14} />
          </IqujiButton>
        }
      />
    );
  };

  return (
    <IqujiSettingsContent
      actions={
        <div className="ops-row">
          <OperationHistoryButton scope={{ category: "secrets" }} />
          {renderHeaderActions()}
        </div>
      }
      description={t("secrets.desc")}
      title={t("secrets.title")}
    >
      {assets.isError ? (
        <IqujiAlert title={t("secrets.readSecretsFailed")} tone="danger">
          {errorMessage(assets.error)}
        </IqujiAlert>
      ) : null}

      <IqujiTabs
        label={t("secrets.tabAriaLabel")}
        onChange={setTab}
        tabs={[
          { label: t("secrets.tabAll", { count: counts.all }), value: "all" },
          { label: t("secrets.tabGit", { count: counts.git }), value: "git" },
          { label: t("secrets.tabRegistry", { count: counts.registry }), value: "registry" },
          { label: t("secrets.tabSsh", { count: counts.ssh }), value: "ssh" },
          {
            label: t("secrets.tabApplication", { count: counts.application }),
            value: "application",
          },
        ]}
        value={tab}
      />

      <div className="ops-secret-toolbar">
        <IqujiInput
          aria-label={t("secrets.searchAria")}
          onChange={(event) => setQuery(event.currentTarget.value)}
          placeholder={t("secrets.searchPlaceholder")}
          value={query}
        />
        <span className="ops-muted">{t("secrets.totalCount", { count: visible.length })}</span>
      </div>

      {assets.isLoading ? (
        <IqujiLoadingState label={t("secrets.readingSecrets")} />
      ) : visible.length === 0 ? (
        <IqujiEmptyState
          action={
            query.trim() ? null : tab === "ssh" ? (
              <IqujiButton onClick={() => setSshDrawerOpen(true)} variant="primary">
                {t("secrets.newSshKey")}
              </IqujiButton>
            ) : tab === "git" ? (
              <IqujiButton onClick={() => setGitDrawerOpen(true)} variant="primary">
                {t("secrets.newGitToken")}
              </IqujiButton>
            ) : tab === "registry" ? (
              <IqujiButton onClick={() => setRegistryOpen(true)} variant="primary">
                {t("secrets.newRegistrySecret")}
              </IqujiButton>
            ) : tab === "all" ? (
              <div className="ops-cluster-actions">
                <IqujiButton onClick={() => setSshDrawerOpen(true)} variant="primary">
                  {t("secrets.newSshKey")}
                </IqujiButton>
                <IqujiButton onClick={() => setGitDrawerOpen(true)} variant="secondary">
                  {t("secrets.newGitToken")}
                </IqujiButton>
              </div>
            ) : null
          }
          body={
            query.trim()
              ? t("secrets.emptySearchDesc")
              : tab === "ssh"
                ? t("secrets.emptySshDesc")
                : tab === "git"
                  ? t("secrets.emptyGitDesc")
                  : tab === "application"
                    ? t("secrets.emptyApplicationDesc")
                    : tab === "registry"
                      ? t("secrets.emptyRegistryDesc")
                      : t("secrets.emptyAllDesc")
          }
          title={query.trim() ? t("secrets.emptySearchTitle") : t("secrets.emptyTitle")}
        />
      ) : (
        <IqujiDataTable
          columns={columns}
          data={visible}
          label={t("secrets.tableLabel")}
          onRowClick={(row) => openDetail(row.id)}
          pinLastColumn
          rowKey={(row) => row.id}
        />
      )}

      <SshKeyFormDrawer onOpenChange={setSshDrawerOpen} open={sshDrawerOpen} servers={serverList} />

      <GitTokenFormDrawer registrySecret open={registryOpen} onOpenChange={setRegistryOpen} />
      <GitTokenFormDrawer applicationSecret open={appOpen} onOpenChange={setAppOpen} />
      <GitTokenFormDrawer onOpenChange={setGitDrawerOpen} open={gitDrawerOpen} />

      <SecretDetailDrawer
        asset={selected}
        assets={allItems}
        onChanged={() => undefined}
        onOpenChange={(next) => {
          if (!next) setSelectedId(null);
        }}
        servers={serverList}
      />

      <SecretRotationDrawer
        asset={rotationTarget}
        onOpenChange={(open) => {
          if (!open) setRotationTarget(null);
        }}
        open={rotationTarget !== null}
      />

      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("secrets.confirmDeleteClose")}
        confirmDisabled={preview.data?.blocked === true}
        confirmLabel={
          preview.data?.blocked ? t("secrets.hasReferences") : t("secrets.confirmDeleteAction")
        }
        confirmLoading={remove.isPending}
        description={
          pendingDelete
            ? preview.isLoading
              ? t("secrets.readingImpact", { name: pendingDelete.name })
              : preview.data?.blocked
                ? t("secrets.blockedImpact", { name: pendingDelete.name })
                : t("secrets.unrecoverableImpact", { name: pendingDelete.name })
            : undefined
        }
        onConfirm={() => remove.mutate()}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
        open={pendingDelete !== null}
        title={
          preview.data?.blocked ? t("secrets.blockedDeleteTitle") : t("secrets.confirmDeleteTitle")
        }
      >
        {pendingDelete && !preview.isLoading ? (
          <div className="ops-confirm-delete-body">
            {preview.data?.blocked ? (
              <IqujiAlert title={t("secrets.blockedRefTitle")} tone="danger">
                <ul className="ops-confirm-impact-list">
                  {preview.data.usage.registryApplications?.length ? (
                    <li>
                      {t("secrets.registryUsage", {
                        apps: preview.data.usage.registryApplications.join("、"),
                      })}
                    </li>
                  ) : null}
                  {preview.data.usage.applicationVariables.length > 0 ? (
                    <li>
                      {t("secrets.appVarPrefix")}
                      {preview.data.usage.applicationVariables.join("、")}
                    </li>
                  ) : null}
                </ul>
              </IqujiAlert>
            ) : (preview.data?.usage.servers.length ?? 0) > 0 ||
              (preview.data?.usage.gitApplications.length ?? 0) > 0 ? (
              <IqujiAlert title={t("secrets.cascadeImpactTitle")} tone="warning">
                <ul className="ops-confirm-impact-list">
                  {(preview.data?.usage.servers.length ?? 0) > 0 ? (
                    <li>
                      {t("secrets.unbindServerPrefix", {
                        count: preview.data?.usage.servers.length,
                      })}
                      {preview.data?.usage.servers.join("、")}
                    </li>
                  ) : null}
                  {(preview.data?.usage.gitApplications.length ?? 0) > 0 ? (
                    <li>
                      {t("secrets.clearGitAuthPrefix", {
                        count: preview.data?.usage.gitApplications.length,
                      })}
                      {preview.data?.usage.gitApplications.join("、")}
                    </li>
                  ) : null}
                </ul>
              </IqujiAlert>
            ) : null}
          </div>
        ) : null}
      </IqujiConfirmDialog>
    </IqujiSettingsContent>
  );
}
