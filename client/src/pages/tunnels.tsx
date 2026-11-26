import {
  IconButton,
  IqujiAlert,
  IqujiConfirmDialog,
  IqujiEmptyState,
  IqujiInput,
  IqujiLoadingState,
  IqujiWorkbenchLayout,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useShell } from "@/app/shell";
import { workspacePages } from "@/contracts/pages";
import type { FrpTunnel, Server } from "@/contracts/types";
import { deleteTunnel, fetchTunnelServers, fetchTunnels } from "@/features/tunnels/api";
import { TunnelDetail } from "@/features/tunnels/TunnelDetail";
import { TunnelDialog } from "@/features/tunnels/TunnelDialog";
import { getTunnelStatusMeta, isTunnelActive } from "@/features/tunnels/utils";
import { errorMessage } from "@/shared/api/client";
import { useAutomationOpener, useAutomationScope } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { toOperationRun } from "@/shared/operations/task-response";
import { useConfirmedOperation } from "@/shared/operations/use-confirmed-operation";
import { ScopeMasterPanel } from "@/shared/ui/ScopeMasterPanel";
import { SelectionCard } from "@/shared/ui/selection-card";

export function TunnelsPage() {
  const queryClient = useQueryClient();
  const notify = useIqujiNotification();
  const { sidebarCollapsed } = useShell();
  const { t } = useI18n();
  const [dialogOpen, setDialogOpen] = useState(false);
  const [editingTunnel, setEditingTunnel] = useState<FrpTunnel | null>(null);
  const routeSearch = useSearch({ from: workspacePages.tunnels.path });
  const navigate = useNavigate({ from: workspacePages.tunnels.path });
  const [selectedId, setSelectedId] = useState<string | null>(routeSearch.tunnelId ?? null);
  useEffect(() => {
    setSelectedId(routeSearch.tunnelId ?? null);
  }, [routeSearch.tunnelId]);
  const [search, setSearch] = useState("");
  const [pendingDelete, setPendingDelete] = useState<FrpTunnel | null>(null);
  const operation = useConfirmedOperation();

  const tunnels = useQuery({ queryKey: ["tunnels"], queryFn: fetchTunnels });
  const servers = useQuery({ queryKey: ["servers"], queryFn: fetchTunnelServers });
  const selected = tunnels.data?.items.find((tunnel) => tunnel.id === selectedId) ?? null;

  useAutomationScope("tunnels", selected?.id ?? "", "");
  useAutomationOpener("tunnel.create", () => {
    setEditingTunnel(null);
    setDialogOpen(true);
  });
  useAutomationOpener("tunnel.edit", () => setEditingTunnel(selected), Boolean(selected));
  const serverMap = useMemo(() => {
    const map = new Map<string, Server>();
    for (const server of servers.data?.items ?? []) map.set(server.id, server);
    return map;
  }, [servers.data]);

  // 同一服务器只允许一个活动穿透，新建时必须排除已有活动隧道的服务器。
  const activeServerIds = useMemo(
    () =>
      new Set(
        (tunnels.data?.items ?? [])
          .filter((tunnel) => isTunnelActive(tunnel.status))
          .map((tunnel) => tunnel.serverId),
      ),
    [tunnels.data],
  );

  // 删除是危险动作：创建后必须确认，Worker 才会真正清理隧道、运行历史与探针记录。
  const remove = useMutation({
    mutationFn: async () => {
      if (!pendingDelete) throw new Error(t("servers.deleteMissing"));
      const target = pendingDelete;
      await operation.submit({
        action: "delete_frp_tunnel",
        targetKind: "frp_tunnel",
        targetId: target.id,
        label: t("tunnels.deleteActionLabel", { name: target.name }),
        create: async (requestKey) => toOperationRun(await deleteTunnel(target.id, requestKey)),
      });
    },
    onSuccess: async () => {
      setPendingDelete(null);
      setSelectedId(null);
      await queryClient.invalidateQueries({ queryKey: ["tunnels"] });
      notify.open({ title: t("tunnels.deleteSuccess"), tone: "success" });
    },
  });

  const filtered = (tunnels.data?.items ?? []).filter((tunnel) => {
    const keyword = search.trim().toLowerCase();
    if (!keyword) return true;
    const serverName = serverMap.get(tunnel.serverId)?.name ?? tunnel.serverId;
    return `${tunnel.name} ${serverName}`.toLowerCase().includes(keyword);
  });

  const invalidate = async () => {
    await queryClient.invalidateQueries({ queryKey: ["tunnels"] });
    if (selectedId) {
      await queryClient.invalidateQueries({ queryKey: ["tunnel-detail", selectedId] });
      await queryClient.invalidateQueries({ queryKey: ["tunnel-runs", selectedId] });
    }
  };

  return (
    <div className="ops-workbench-fill">
      {tunnels.isError ? (
        <IqujiAlert title={t("tunnels.deleteFailed")} tone="danger">
          {errorMessage(tunnels.error)}
        </IqujiAlert>
      ) : null}

      <IqujiWorkbenchLayout
        center={
          selected ? (
            <TunnelDetail
              onChanged={() => void invalidate()}
              onDelete={() => setPendingDelete(selected)}
              onEdit={() => setEditingTunnel(selected)}
              server={serverMap.get(selected.serverId)}
              tunnel={selected}
            />
          ) : (
            <div className="ops-empty-surface">
              <IqujiEmptyState
                body={t("tunnels.emptySelectionBody")}
                title={t("tunnels.emptySelectionTitle")}
              />
            </div>
          )
        }
        centerClassName="ops-workbench__center"
        className="ops-inner-workbench"
        defaultLeftWidth={280}
        left={
          <ScopeMasterPanel
            action={
              <IconButton
                compact
                icon={<IqujiIcon decorative name="plus" />}
                data-ops-guide-open="tunnel.create"
                data-ops-tutorial="workspace.tunnels"
                label={t("tunnels.newTunnel")}
                onClick={() => {
                  setEditingTunnel(null);
                  setDialogOpen(true);
                }}
                variant="ghost"
              />
            }
            count={filtered.length}
            search={
              <IqujiInput
                aria-label={t("tunnels.searchPlaceholder")}
                onChange={(event) => setSearch(event.currentTarget.value)}
                placeholder={t("tunnels.searchPlaceholder")}
                value={search}
              />
            }
            title={t("tunnels.title")}
          >
            {tunnels.isLoading ? <IqujiLoadingState label={t("common.loading")} /> : null}
            {tunnels.data && filtered.length === 0 ? (
              <IqujiEmptyState
                body={search ? t("common.noData") : t("tunnels.emptyBody")}
                title={search ? t("common.noData") : t("tunnels.emptyTitle")}
              />
            ) : null}
            {filtered.map((tunnel) => {
              const meta = getTunnelStatusMeta(tunnel.status, t);
              return (
                <SelectionCard
                  active={tunnel.id === selectedId}
                  description={serverMap.get(tunnel.serverId)?.name ?? t("common.unnamed")}
                  icon="network"
                  key={tunnel.id}
                  meta={t("tunnels.mappingsCount", { count: tunnel.mappings.length })}
                  onClick={() => {
                    setSelectedId(tunnel.id);
                    void navigate({ search: (current) => ({ ...current, tunnelId: tunnel.id }) });
                  }}
                  status={meta.label}
                  statusTone={meta.tone}
                  title={tunnel.name}
                  trailing={<OperationActivityDot scope={{ tunnelId: tunnel.id }} />}
                />
              );
            })}
          </ScopeMasterPanel>
        }
        leftClassName="ops-inner-workbench__scope"
        leftCollapsed={sidebarCollapsed}
        leftMaxWidth={380}
        leftMinWidth={240}
        leftResizeLabel={t("projects.resizeLabel")}
      />

      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("common.close")}
        confirmLabel={t("common.delete")}
        confirmLoading={remove.isPending}
        description={
          pendingDelete ? t("tunnels.deleteDesc", { name: pendingDelete.name }) : undefined
        }
        onConfirm={() => remove.mutate()}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
        open={pendingDelete !== null}
        title={t("tunnels.deleteTitle")}
      />

      <TunnelDialog
        activeServerIds={activeServerIds}
        onOpenChange={(open) => {
          if (!open) {
            setDialogOpen(false);
            setEditingTunnel(null);
          }
        }}
        onSaved={(tunnelId) => {
          if (tunnelId) setSelectedId(tunnelId);
          void invalidate();
          void queryClient.invalidateQueries({ queryKey: ["tunnels"] });
        }}
        open={dialogOpen || editingTunnel !== null}
        servers={servers.data?.items ?? []}
        tunnel={editingTunnel}
      />
    </div>
  );
}
