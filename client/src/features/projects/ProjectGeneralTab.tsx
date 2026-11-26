import {
  type ColumnDef,
  IqujiBadge,
  IqujiButtonLink,
  IqujiDataTable,
  IqujiEmptyState,
  IqujiInput,
} from "@iquji-community/web-component";
import type { Application, ProjectWorkspace } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { ApplicationInputStatus } from "./ApplicationInputStatus";
import { stateLabel, stateTone } from "./model";

export function ProjectGeneralTab({
  workspace,
  onSelectApplication,
  search,
  onSearchChange,
}: {
  workspace: ProjectWorkspace;
  onSelectApplication: (id: string) => void;
  search: string;
  onSearchChange: (value: string) => void;
}) {
  const { t, locale } = useI18n();
  const applications = workspace.applications;
  const filtered = applications.filter((item) =>
    item.name.toLowerCase().includes(search.trim().toLowerCase()),
  );
  const configFor = (id: string) =>
    workspace.applicationDeploymentConfigs?.find((item) => item.applicationId === id);

  const columns: ColumnDef<Application>[] = [
    {
      accessorKey: "name",
      header: t("apps.title"),
      cell: ({ row }) => {
        const config = configFor(row.original.id);
        const missing = [
          !config?.sourceConfig ? t("apps.tabSource") : null,
          config?.sourceConfig?.provider === "git" && !config.buildServerId
            ? t("servers.title")
            : null,
          !config?.swarmRuntime?.services?.length ? t("apps.tabRuntime") : null,
          !config?.deploymentManagerServerId ? t("projects.deploymentManager") : null,
        ].filter(Boolean);
        return (
          <div className="ops-table-cell-stack">
            <span className="ops-row">
              <strong>{row.original.name}</strong>
              <ApplicationInputStatus status={row.original.inputStatus} />
              <OperationActivityDot scope={{ applicationId: row.original.id }} />
            </span>
            {missing.length ? (
              <span className="ops-muted">
                {t("state.idle")}: {missing[0]}
                {missing.length > 1 ? "…" : ""}
              </span>
            ) : null}
          </div>
        );
      },
    },
    {
      id: "runtime",
      header: t("common.status"),
      cell: ({ row }) => {
        const snapshot = row.original.runtimeSnapshot;
        const sampled = Boolean(
          snapshot?.checkedAt && !Number.isNaN(new Date(snapshot.checkedAt).getTime()),
        );
        return (
          <div className="ops-table-cell-stack">
            <IqujiBadge tone={sampled ? stateTone(snapshot?.status) : "neutral"}>
              {sampled ? stateLabel(snapshot?.status, t) : t("common.unknown")}
            </IqujiBadge>
            <span className="ops-muted" title={snapshot?.checkedAt}>
              {sampled
                ? new Date(snapshot?.checkedAt ?? "").toLocaleString(locale, {
                    month: "2-digit",
                    day: "2-digit",
                    hour: "2-digit",
                    minute: "2-digit",
                  })
                : t("monitoring.notRefreshed")}
            </span>
          </div>
        );
      },
    },
    {
      id: "source",
      header: t("projects.provider"),
      cell: ({ row }) => {
        const source = configFor(row.original.id)?.sourceConfig;
        if (!source) return <span className="ops-muted">{t("state.idle")}</span>;
        if (source.provider === "image") return source.image;
        if (source.provider !== "git")
          return `${source.provider} · ${source.packageName}@${source.version}`;
        const repository =
          source.repositoryUrl
            .replace(/\/$/, "")
            .split("/")
            .pop()
            ?.replace(/\.git$/, "") || source.repositoryUrl;
        return (
          <span title={source.repositoryUrl}>
            {repository} · {source.ref}
          </span>
        );
      },
    },
    {
      id: "build",
      header: t("servers.title"),
      cell: ({ row }) => {
        const id = configFor(row.original.id)?.buildServerId;
        return id ? (
          (workspace.servers?.find((item) => item.id === id)?.name ?? t("common.unknown"))
        ) : (
          <span className="ops-muted">{t("state.idle")}</span>
        );
      },
    },
    {
      id: "entry",
      header: t("apps.tabRoutes"),
      cell: ({ row }) => {
        const routes = workspace.routes.filter((item) => item.applicationId === row.original.id);
        const applied = routes.filter((item) => item.status === "active" && item.lastApplied);
        const urls = [...new Set(applied.flatMap((item) => item.accessibleUrls ?? []))].filter(
          (url) => /^https?:\/\//.test(url),
        );
        const pending = routes.some((item) => item.status === "pending");
        return (
          <div className="ops-table-cell-stack">
            {urls.length ? (
              <div className="ops-row">
                <IqujiButtonLink
                  compact
                  variant="text"
                  href={urls[0]}
                  target="_blank"
                  rel="noreferrer"
                  onClick={(event) => event.stopPropagation()}
                >
                  {urls[0]}
                </IqujiButtonLink>
                {urls.length > 1 ? (
                  <span className="ops-muted" title={urls.join("\n")}>
                    +{urls.length - 1}
                  </span>
                ) : null}
              </div>
            ) : (
              <span className="ops-muted">
                {applied.length
                  ? t("state.ready")
                  : pending
                    ? t("state.pending")
                    : routes.some((item) => item.status === "failed")
                      ? t("state.failed")
                      : routes.some((item) => item.status === "disabled")
                        ? t("state.stopped")
                        : t("state.idle")}
              </span>
            )}
            {pending && urls.length ? (
              <span className="ops-muted">{t("state.pending")}</span>
            ) : null}
          </div>
        );
      },
    },
  ];

  return (
    <section className="ops-project-list">
      <div className="ops-section-heading">
        <div className="ops-project-search">
          <IqujiInput
            aria-label={t("projects.findApp")}
            placeholder={t("projects.findApp")}
            value={search}
            onChange={(event) => onSearchChange(event.currentTarget.value)}
          />
        </div>
        <span className="ops-muted">{filtered.length}</span>
      </div>
      {applications.length === 0 ? (
        <IqujiEmptyState title={t("projects.noApps")} body={t("projects.createFromTree")} />
      ) : (
        <IqujiDataTable
          label={t("apps.title")}
          data={filtered}
          columns={columns}
          onRowClick={(item) => onSelectApplication(item.id)}
          rowKey={(item) => item.id}
          emptyTitle={t("projects.noMatchingApps")}
          emptyBody={t("projects.emptySearchBody")}
        />
      )}
    </section>
  );
}
