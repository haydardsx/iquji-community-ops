import { IqujiAlert, IqujiEmptyState, IqujiLoadingState } from "@iquji-community/web-component";
import type { ProjectWorkspace } from "@/contracts/types";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { ApplicationDetail, type ApplicationTab } from "./ApplicationDetail";
import { ProjectGeneralTab } from "./ProjectGeneralTab";
import { ProjectHeader } from "./ProjectHeader";

export function ProjectDetail({
  workspace,
  loading,
  error,
  onChanged,
  applicationId,
  onSelectApplication,
  applicationTab,
  onApplicationTabChange,
  search,
  onSearchChange,
}: {
  workspace?: ProjectWorkspace;
  loading: boolean;
  error: unknown;
  onChanged: () => void;
  applicationId?: string;
  onSelectApplication: (id?: string) => void;
  applicationTab: ApplicationTab;
  onApplicationTabChange: (tab: ApplicationTab) => void;
  search: string;
  onSearchChange: (value: string) => void;
}) {
  const { t } = useI18n();
  if (loading) return <IqujiLoadingState label={t("projects.loading")} />;
  if (error)
    return (
      <IqujiAlert title={t("projects.workspaceLoadError")} tone="danger">
        {errorMessage(error)}
      </IqujiAlert>
    );
  if (!workspace?.project)
    return (
      <IqujiEmptyState
        title={t("projects.emptySelectionTitle")}
        body={t("projects.emptySelectionBody")}
      />
    );
  const application = workspace.applications.find((item) => item.id === applicationId);

  if (application) {
    return (
      <ApplicationDetail
        key={application.id}
        application={application}
        workspace={workspace}
        onChanged={onChanged}
        onClose={() => onSelectApplication(undefined)}
        onDeleted={() => onSelectApplication(undefined)}
        tab={applicationTab}
        onTabChange={onApplicationTabChange}
      />
    );
  }

  return (
    <div className="ops-project-workspace">
      <ProjectHeader project={workspace.project} />
      <div className="ops-project-content">
        <ProjectGeneralTab
          workspace={workspace}
          onSelectApplication={onSelectApplication}
          search={search}
          onSearchChange={onSearchChange}
        />
      </div>
    </div>
  );
}
