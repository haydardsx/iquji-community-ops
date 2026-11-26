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
import { useSearch } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useShell } from "@/app/shell";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { MONITORING_REFRESH_MS } from "@/features/monitoring/api";
import { deleteServer, fetchServers } from "@/features/servers/api";
import { swarmMembership } from "@/features/servers/runtime-presentation";
import { ServerDetail } from "@/features/servers/ServerDetail";
import { ServerDialog } from "@/features/servers/ServerDialog";
import { ServerFrpsIdleDot } from "@/features/servers/ServerFrpsIdleDot";
import { getSwarmConnectionFacts } from "@/features/servers/swarm-connection";
import { errorMessage } from "@/shared/api/client";
import { useAutomationOpener, useAutomationScope } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { ScopeMasterPanel } from "@/shared/ui/ScopeMasterPanel";
import { SelectionCard } from "@/shared/ui/selection-card";

export function ServersPage() {
  const queryClient = useQueryClient();
  const notify = useIqujiNotification();
  const { sidebarCollapsed } = useShell();
  const { t } = useI18n();
  const searchParams = useSearch({ strict: false }) as { serverId?: string };
  const [selectedId, setSelectedId] = useState<string | null>(searchParams?.serverId ?? null);
  const [dialog, setDialog] = useState<"create" | "edit" | null>(null);
  const [search, setSearch] = useState("");
  const [pendingDelete, setPendingDelete] = useState<Server | null>(null);

  useEffect(() => {
    if (searchParams?.serverId) {
      setSelectedId(searchParams.serverId);
    }
  }, [searchParams?.serverId]);

  const servers = useQuery({
    queryKey: ["servers"],
    queryFn: fetchServers,
    refetchOnMount: "always",
    refetchInterval: MONITORING_REFRESH_MS,
  });
  const selected = servers.data?.items.find((server) => server.id === selectedId) ?? null;

  useAutomationScope("servers", selected?.id ?? "", "");
  useAutomationOpener("server.create", () => setDialog("create"));
  useAutomationOpener("server.edit", () => setDialog("edit"), Boolean(selected));
  const remove = useMutation({
    mutationFn: async () => {
      if (!pendingDelete) throw new Error(t("servers.deleteMissing"));
      return deleteServer(pendingDelete.id);
    },
    onSuccess: async () => {
      setPendingDelete(null);
      setSelectedId(null);
      await queryClient.invalidateQueries({ queryKey: ["servers"] });
      notify.open({ title: t("servers.deleteSuccess"), tone: "success" });
    },
    onError: (error) =>
      notify.open({
        title: t("servers.deleteFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      }),
  });

  const filteredServers = (servers.data?.items ?? []).filter((item) => {
    const keyword = search.trim().toLowerCase();
    if (!keyword) return true;
    return `${item.name} ${item.publicHost} ${item.privateHost ?? ""}`
      .toLowerCase()
      .includes(keyword);
  });

  return (
    <div className="ops-workbench-fill">
      {servers.isError ? (
        <IqujiAlert title={t("servers.loadError")} tone="danger">
          {errorMessage(servers.error)}
        </IqujiAlert>
      ) : null}

      <IqujiWorkbenchLayout
        center={
          selected ? (
            <ServerDetail
              key={selected.id}
              onDelete={() => setPendingDelete(selected)}
              onEdit={() => setDialog("edit")}
              server={selected}
            />
          ) : (
            <div className="ops-empty-surface">
              <IqujiEmptyState
                body={t("servers.emptySelectionBody")}
                title={t("servers.emptySelectionTitle")}
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
                data-ops-guide-open="server.create"
                data-ops-tutorial="workspace.servers"
                label={t("servers.newServer")}
                onClick={() => setDialog("create")}
                variant="ghost"
              />
            }
            count={filteredServers.length}
            search={
              <IqujiInput
                aria-label={t("servers.searchPlaceholder")}
                onChange={(event) => setSearch(event.currentTarget.value)}
                placeholder={t("servers.searchPlaceholder")}
                value={search}
              />
            }
            title={t("servers.title")}
          >
            {servers.isLoading ? <IqujiLoadingState label={t("common.loading")} /> : null}
            {servers.data && filteredServers.length === 0 ? (
              <IqujiEmptyState body={t("servers.emptyBody")} title={t("servers.emptyTitle")} />
            ) : null}
            {filteredServers.map((server) => {
              const metrics = (server.health?.metrics ?? {}) as ServerRuntimeMetrics;
              const membership = swarmMembership(metrics);
              const connection = getSwarmConnectionFacts(metrics, t);
              return (
                <SelectionCard
                  active={server.id === selectedId}
                  description={
                    membership === "manager" || membership === "worker"
                      ? `Swarm ${connection.label}`
                      : undefined
                  }
                  icon="server"
                  key={server.id}
                  meta={`${server.publicHost}${server.privateHost ? ` · ${server.privateHost}` : ""}`}
                  onClick={() => setSelectedId(server.id)}
                  status={server.status ?? t("state.ready")}
                  statusTone={
                    server.status === "running" || server.status === "online"
                      ? "success"
                      : "neutral"
                  }
                  title={server.name}
                  trailing={
                    <span className="ops-row">
                      <OperationActivityDot scope={{ serverId: server.id }} />
                      {metrics.frpsIdle ? <ServerFrpsIdleDot /> : null}
                    </span>
                  }
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

      <ServerDialog
        onOpenChange={(open) => {
          if (!open) setDialog(null);
        }}
        onSaved={async (server) => {
          setSelectedId(server.id);
          await queryClient.invalidateQueries({ queryKey: ["servers"] });
        }}
        open={dialog !== null}
        server={dialog === "edit" ? selected : null}
      />

      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("common.close")}
        confirmLabel={t("common.delete")}
        confirmLoading={remove.isPending}
        description={
          pendingDelete ? t("servers.deleteDesc", { name: pendingDelete.name }) : undefined
        }
        onConfirm={() => remove.mutate()}
        onOpenChange={(open) => {
          if (!open) setPendingDelete(null);
        }}
        open={pendingDelete !== null}
        title={t("servers.deleteTitle")}
      />
    </div>
  );
}
