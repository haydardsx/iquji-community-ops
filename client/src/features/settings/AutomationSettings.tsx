import {
  type ColumnDef,
  IconButton,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDataTable,
  IqujiDiagnosticBanner,
  IqujiEmptyState,
  IqujiLoadingState,
  IqujiSettingsContent,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useCallback, useEffect, useMemo, useState } from "react";
import { errorMessage } from "@/shared/api/client";
import { windowRequest } from "@/shared/desktop/automation-window-api";
import { isTauriDesktop } from "@/shared/desktop/external-url";
import { useI18n } from "@/shared/i18n";
import { RecordSnippet } from "@/shared/ui/record-snippet";
import {
  type Client,
  type Permission,
  policyDraft,
  type Rule,
  type Summary,
  summarizeDraft,
} from "./automation-permissions";
import { McpClientDrawer } from "./McpClientDrawer";
import { McpConfigDialog } from "./McpConfigDialog";
import "./automation-settings.css";

/**
 * 设置 → MCP 设置：客户端列表主视图（配置在抽屉、令牌与撤销在弹窗中完成）
 */
export function AutomationSettings() {
  const { t } = useI18n();
  const notification = useIqujiNotification();
  const desktop = isTauriDesktop();
  const [data, setData] = useState<Summary>();
  const [loading, setLoading] = useState(desktop);
  const [loadError, setLoadError] = useState("");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [editingClient, setEditingClient] = useState<Client | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [createdConfig, setCreatedConfig] = useState<{ name: string; config: unknown } | null>(
    null,
  );
  const [pendingRevoke, setPendingRevoke] = useState<Client | null>(null);
  const [revoking, setRevoking] = useState(false);

  const loadClients = useCallback((signal?: AbortSignal) => {
    setLoading(true);
    setLoadError("");
    return windowRequest<Summary>("clients", {}, signal)
      .then((summary) => {
        setData(summary);
      })
      .catch((e) => {
        if (!signal?.aborted) setLoadError(errorMessage(e));
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false);
      });
  }, []);

  useEffect(() => {
    if (!desktop) return;
    const controller = new AbortController();
    void loadClients(controller.signal);
    return () => controller.abort();
  }, [desktop, loadClients]);

  const openCreate = () => {
    setEditingClient(null);
    setSaveError("");
    setDrawerOpen(true);
  };

  const openEdit = useCallback((client: Client) => {
    setEditingClient(client);
    setSaveError("");
    setDrawerOpen(true);
  }, []);

  const handleSave = async (payload: {
    id?: string;
    name: string;
    permissions: Permission[];
    rules: Rule[];
  }) => {
    if (!data) return;
    setSaving(true);
    setSaveError("");
    try {
      const clients = [...data.clients];
      if (payload.id) {
        const response = await windowRequest<{ client: Client }>("update", payload);
        const index = clients.findIndex((item) => item.id === payload.id);
        if (index >= 0) clients[index] = response.client;
        setData({ ...data, clients });
        setDrawerOpen(false);
        notification.open({ title: t("common.allSaved"), tone: "success" });
      } else {
        const response = await windowRequest<{ client: Client; config: unknown }>("grant", {
          name: payload.name,
          permissions: payload.permissions,
          rules: payload.rules,
        });
        clients.push(response.client);
        setData({ ...data, clients });
        setDrawerOpen(false);
        setCreatedConfig({ name: response.client.name, config: response.config });
      }
    } catch (e) {
      setSaveError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  const handleRevoke = async () => {
    if (!pendingRevoke || !data) return;
    setRevoking(true);
    try {
      await windowRequest("revoke", { id: pendingRevoke.id });
      const clients = data.clients.filter((item) => item.id !== pendingRevoke.id);
      setData({ ...data, clients });
      if (editingClient?.id === pendingRevoke.id) {
        setDrawerOpen(false);
        setEditingClient(null);
      }
      setPendingRevoke(null);
      notification.open({ title: t("common.opCompleted"), tone: "success" });
    } catch (e) {
      notification.open({
        title: t("common.opFailed"),
        description: errorMessage(e),
        tone: "danger",
        duration: 0,
      });
    } finally {
      setRevoking(false);
    }
  };

  const commands = data?.commands ?? [];
  const clients = data?.clients ?? [];

  const columns = useMemo<ColumnDef<Client>[]>(
    () => [
      {
        accessorKey: "name",
        header: t("automation.colClient"),
        cell: ({ row }) => (
          <div className="ops-mcp-client-cell">
            <span className="ops-table-primary-cell">{row.original.name}</span>
            <RecordSnippet label="ID" value={row.original.id} />
          </div>
        ),
      },
      {
        id: "permissions",
        header: t("automation.colPermissions"),
        cell: ({ row }) => {
          const summary = summarizeDraft(policyDraft(row.original), commands);
          return (
            <div className="ops-mcp-perm-summary">
              <IqujiBadge tone={summary.granted > 0 ? "info" : "neutral"}>
                {t("automation.grantedRatio", {
                  granted: summary.granted,
                  total: summary.total,
                })}
              </IqujiBadge>
              {summary.granted > 0 ? (
                <span className="ops-muted ops-text-caption">
                  {t("automation.summaryBreakdown", {
                    direct: summary.direct,
                    guide: summary.guide,
                  })}
                </span>
              ) : (
                <span className="ops-muted ops-text-caption">{t("automation.noPermissions")}</span>
              )}
            </div>
          );
        },
      },
      {
        id: "actions",
        header: t("automation.colActions"),
        cell: ({ row }) => (
          <div className="ops-mcp-table-actions" data-no-row-click>
            <IconButton
              compact
              icon={<IqujiIcon decorative name="pencil" size={14} />}
              label={t("common.edit")}
              onClick={() => openEdit(row.original)}
              variant="ghost"
            />
            <IconButton
              compact
              icon={<IqujiIcon decorative name="trash" size={14} />}
              label={t("automation.revoke")}
              onClick={() => setPendingRevoke(row.original)}
              variant="ghost"
            />
          </div>
        ),
      },
    ],
    [commands, openEdit, t],
  );

  if (!desktop) {
    return (
      <IqujiSettingsContent description={t("automation.intro")} title={t("automation.title")}>
        <IqujiEmptyState
          body={t("automation.desktop")}
          icon={<IqujiIcon decorative name="monitor" size={28} />}
          title={t("automation.desktopTitle")}
        />
      </IqujiSettingsContent>
    );
  }

  return (
    <IqujiSettingsContent
      actions={
        <IqujiButton
          compact
          disabled={!data || loading}
          iconStart={<IqujiIcon decorative name="plus" size={14} />}
          onClick={openCreate}
          variant="primary"
        >
          {t("automation.newClient")}
        </IqujiButton>
      }
      description={t("automation.intro")}
      title={t("automation.title")}
    >
      {loadError ? (
        <IqujiDiagnosticBanner
          action={
            <IqujiButton compact onClick={() => void loadClients()} variant="secondary">
              {t("common.retry")}
            </IqujiButton>
          }
          title={loadError}
          tone="danger"
        />
      ) : null}

      {loading && !data ? (
        <IqujiLoadingState label={t("common.loading")} />
      ) : clients.length === 0 && !loadError ? (
        <IqujiEmptyState
          action={
            <IqujiButton
              iconStart={<IqujiIcon decorative name="plus" size={14} />}
              onClick={openCreate}
              variant="primary"
            >
              {t("automation.newClient")}
            </IqujiButton>
          }
          body={t("automation.emptyBody")}
          icon={<IqujiIcon decorative name="sliders-horizontal" size={28} />}
          title={t("automation.emptyTitle")}
        />
      ) : (
        <IqujiDataTable
          columns={columns}
          data={clients}
          label={t("automation.externalAccess")}
          onRowClick={openEdit}
          pinLastColumn
          rowKey={(row) => row.id}
        />
      )}

      <McpClientDrawer
        busy={saving}
        client={editingClient}
        commands={commands}
        error={saveError}
        key={editingClient?.id ?? (drawerOpen ? "new" : "closed")}
        onClose={() => {
          if (!saving) setDrawerOpen(false);
        }}
        onSave={handleSave}
        open={drawerOpen}
      />

      <McpConfigDialog
        clientName={createdConfig?.name ?? ""}
        config={createdConfig?.config ?? null}
        onClose={() => setCreatedConfig(null)}
      />

      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("common.close")}
        confirmLabel={t("automation.revoke")}
        confirmLoading={revoking}
        description={t("automation.revokeConfirmDesc", { name: pendingRevoke?.name ?? "" })}
        onConfirm={() => void handleRevoke()}
        onOpenChange={(open) => {
          if (!open && !revoking) setPendingRevoke(null);
        }}
        open={pendingRevoke !== null}
        title={t("automation.revokeConfirmTitle")}
      />
    </IqujiSettingsContent>
  );
}
