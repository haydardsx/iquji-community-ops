import {
  IqujiAlert,
  IqujiBadge,
  IqujiButton,
  IqujiDropdownMenu,
  IqujiTabs,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { Server, ServerRuntimeMetrics } from "@/contracts/types";
import { MONITORING_REFRESH_MS } from "@/features/monitoring/api";
import { fetchSecretAssets, SECRET_ASSETS_QUERY_KEY } from "@/features/secrets/api";
import { errorMessage } from "@/shared/api/client";
import { useAutomationScope } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import {
  OperationActivityDot,
  SERVER_MONITORING_QUERY_FAMILIES,
} from "@/shared/operations/activity";
import { noteGuideFormOpen } from "@/shared/tutorial/action-guide-state";
import { TerminalDrawer } from "@/shared/ui/terminal-drawer";
import { fetchServerHealth, fetchServerResources, probeServerRuntime } from "./api";
import { runtimeCheckOutcome, runtimeComponentPresentation } from "./runtime-presentation";
import { ServerConnectionDrawer } from "./ServerConnectionDrawer";
import { ServerFrpsIdleDot } from "./ServerFrpsIdleDot";
import { sshConnectCommand } from "./ssh-command";
import { ServerOverviewTab } from "./tabs/ServerOverviewTab";
import { ServerRuntimeTab } from "./tabs/ServerRuntimeTab";

export type ServerDetailTab = "overview" | "runtime";

export function ServerDetail({
  onDelete,
  onEdit,
  server,
}: {
  server: Server;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const queryClient = useQueryClient();
  const { t } = useI18n();
  const [tab, setTab] = useState<ServerDetailTab>("overview");
  useAutomationScope("servers-detail", server.id, tab, (value) => {
    if (value === "overview" || value === "runtime") setTab(value);
  });
  const [copied, setCopied] = useState(false);
  const [showConnection, setShowConnection] = useState(false);
  const [showTerminal, setShowTerminal] = useState(false);
  const notify = useIqujiNotification();

  const health = useQuery({
    queryFn: () => fetchServerHealth(server.id),
    queryKey: ["server-health", server.id],
    refetchInterval: MONITORING_REFRESH_MS,
    refetchOnMount: "always",
  });

  const resources = useQuery({
    queryFn: () => fetchServerResources(server.id),
    queryKey: ["server-resources", server.id],
  });

  const secrets = useQuery({
    queryFn: fetchSecretAssets,
    queryKey: SECRET_ASSETS_QUERY_KEY,
  });

  const probe = useMutation({
    mutationFn: () => probeServerRuntime(server.id),
    onSuccess: (result) => {
      const outcomes = (result.process?.checks ?? []).map(runtimeCheckOutcome);
      const failed = outcomes.includes("failed");
      const warned = outcomes.includes("warning");
      notify.open({
        title:
          failed || warned ? t("servers.probeCompleteWarn") : t("servers.probeCompleteSuccess"),
        description: result.serverRuntime?.summary ?? t("servers.recentProbeUpdated"),
        tone: failed || warned ? "warning" : "success",
      });
      refresh();
    },
    onError: (error) =>
      notify.open({
        title: t("servers.probeFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      }),
  });

  const metrics = (health.data?.serverHealth?.metrics ?? {}) as ServerRuntimeMetrics;
  const ssh = runtimeComponentPresentation(metrics, "ssh", t);
  const relatedSecrets = (secrets.data?.items ?? []).filter((secret) =>
    server.secretAssetIds?.includes(secret.id),
  );

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["server-health", server.id] });
    void queryClient.invalidateQueries({ queryKey: ["server-resources", server.id] });
    for (const key of ["servers", ...SERVER_MONITORING_QUERY_FAMILIES]) {
      void queryClient.invalidateQueries({ queryKey: [key] });
    }
    void queryClient.invalidateQueries({ queryKey: ["project-workspace"] });
    void queryClient.invalidateQueries({ queryKey: ["deployment-eligibility"] });
    void queryClient.invalidateQueries({ queryKey: ["application-runtime"] });
    void queryClient.invalidateQueries({ queryKey: SECRET_ASSETS_QUERY_KEY });
  };

  const connectCommand = sshConnectCommand(server);

  const handleCopyConnect = async () => {
    try {
      await navigator.clipboard.writeText(connectCommand);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (error) {
      notify.open({
        title: t("servers.copyCommand"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      });
    }
  };

  const tabsConfig = [
    { label: t("servers.tabOverview"), value: "overview" as const },
    {
      label: (
        <span className="ops-row">
          {t("servers.tabRuntime")}
          <OperationActivityDot scopes={[{ serverId: server.id, section: "server-runtime" }]} />
          {metrics.frpsIdle ? <ServerFrpsIdleDot /> : null}
        </span>
      ),
      value: "runtime" as const,
    },
  ];

  return (
    <div className="ops-stack">
      {/* 顶部主机信息与唯一操作入口 */}
      <div className="ops-server-header">
        <div className="ops-server-header__main">
          <div className="ops-server-header__title-group">
            <div className="ops-server-header__title-row">
              <h1 data-ops-guide-object={server.id} className="ops-server-header__title">
                {server.name}
              </h1>
              <IqujiBadge tone={server.status === "online" ? "success" : "neutral"}>
                {server.status
                  ? t(`state.${server.status}` as any) || server.status
                  : t("state.idle")}
              </IqujiBadge>
              <IqujiBadge tone={ssh.tone}>
                {metrics.ssh === false ? ssh.label : `SSH ${ssh.label}`}
              </IqujiBadge>
              {health.isLoading || resources.isLoading ? (
                <IqujiBadge
                  aria-label={t("servers.syncingResources")}
                  className="ops-server-sync-badge"
                  title={t("servers.syncingResources")}
                  tone="info"
                >
                  <IqujiIcon
                    className="ops-server-sync-spinner"
                    decorative
                    name="loader"
                    size={12}
                  />
                  <span>{t("servers.syncing")}</span>
                </IqujiBadge>
              ) : null}
            </div>
          </div>

          <div className="ops-server-header__actions">
            <IqujiDropdownMenu
              ariaLabel={t("servers.manageServer")}
              trigger={
                <IqujiButton
                  compact
                  variant="secondary"
                  data-ops-guide-menu="server.edit"
                  iconStart={<IqujiIcon decorative name="more" size={14} />}
                >
                  {t("common.actions")}
                </IqujiButton>
              }
              items={[
                {
                  key: "terminal",
                  label: t("terminal.server"),
                  icon: <IqujiIcon decorative name="terminal" size={16} />,
                  onSelect: () => setShowTerminal(true),
                },
                {
                  key: "connection",
                  label: t("servers.connectionProxy"),
                  icon: <IqujiIcon decorative name="network" size={16} />,
                  onSelect: () => setShowConnection(true),
                },
                {
                  key: "reprobe",
                  label: t("servers.reprobe"),
                  icon: <IqujiIcon decorative name="radar" size={16} />,
                  disabled: probe.isPending,
                  onSelect: () => probe.mutate(),
                },
                {
                  key: "edit",
                  label: t("servers.editServer"),
                  icon: <IqujiIcon decorative name="pencil" size={16} />,
                  onSelect: () => {
                    noteGuideFormOpen("server.edit");
                    onEdit();
                  },
                },
                { kind: "separator", key: "danger-sep" },
                {
                  key: "delete",
                  label: t("servers.deleteServer"),
                  icon: <IqujiIcon decorative name="trash" size={16} />,
                  tone: "danger",
                  onSelect: onDelete,
                },
              ]}
            />
          </div>
        </div>

        {/* SSH 快捷连接通道与 IP 复制条 */}
        <div className="ops-server-connect-bar">
          <div className="ops-server-connect-snippet">
            <span className="ops-muted ops-text-semibold">SSH: </span>
            <code className="ops-server-connect-code">{connectCommand}</code>
            {server.privateHost ? <span className="ops-muted">({server.privateHost})</span> : null}
          </div>

          <IqujiButton compact onClick={handleCopyConnect} variant="secondary">
            <IqujiIcon decorative name={copied ? "check" : "copy"} />
            {copied ? t("common.copied") : t("servers.copyCommand")}
          </IqujiButton>
        </div>
      </div>

      {/* 终端与连接代理抽屉 */}
      <TerminalDrawer
        open={showTerminal}
        onOpenChange={setShowTerminal}
        kind="server"
        id={server.id}
        name={server.name}
      />
      {showConnection && (
        <ServerConnectionDrawer
          key={server.id}
          server={server}
          onClose={() => setShowConnection(false)}
          onSaved={refresh}
        />
      )}
      {health.isError || resources.isError || secrets.isError ? (
        <IqujiAlert title={t("servers.loadDataFailed")} tone="danger">
          {errorMessage(health.error ?? resources.error ?? secrets.error)}
        </IqujiAlert>
      ) : null}

      {/* 顶层双标签导航 */}
      <div className="ops-server-tabs">
        <IqujiTabs
          label={t("servers.viewAria")}
          onChange={setTab}
          panelId="server-detail-panel"
          tabs={tabsConfig}
          value={tab}
        />
      </div>

      {/* Tab 内容视窗 */}
      <div id="server-detail-panel" role="tabpanel">
        {tab === "overview" ? (
          <ServerOverviewTab
            onChanged={refresh}
            health={health.data}
            resources={resources.data}
            secrets={relatedSecrets}
            server={server}
          />
        ) : null}

        {tab === "runtime" ? (
          <ServerRuntimeTab metrics={metrics} onRefresh={refresh} server={server} />
        ) : null}
      </div>
    </div>
  );
}
