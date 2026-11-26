import {
  IconButton,
  IqujiButton,
  IqujiDropdownMenu,
  IqujiLoadingState,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import type { Project } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { noteGuideFormOpen } from "@/shared/tutorial/action-guide-state";
import { ApplicationInputStatus } from "./ApplicationInputStatus";
import { fetchProjectApplications } from "./api";

export function ProjectTree({
  projects,
  selectedProjectId,
  selectedApplicationId,
  onSelectProject,
  onSelectApplication,
  onCreateApplication,
  onEditProject,
  onDeleteProject,
}: {
  projects: Project[];
  selectedProjectId?: string;
  selectedApplicationId?: string;
  onSelectProject: (projectId: string) => void;
  onSelectApplication: (projectId: string, applicationId: string) => void;
  onCreateApplication: (projectId: string) => void;
  onEditProject: (project: Project) => void;
  onDeleteProject: (project: Project) => void;
}) {
  // 记录主动折叠的项目 ID，默认所有项目均为展开状态
  const [collapsedIds, setCollapsedIds] = useState<Set<string>>(() => new Set());

  const toggleCollapse = (projectId: string) => {
    setCollapsedIds((prev) => {
      const next = new Set(prev);
      if (next.has(projectId)) {
        next.delete(projectId);
      } else {
        next.add(projectId);
      }
      return next;
    });
  };

  return (
    <div className="ops-tree-list">
      {projects.map((project) => (
        <ProjectBranch
          key={project.id}
          project={project}
          isExpanded={!collapsedIds.has(project.id)}
          isSelected={project.id === selectedProjectId}
          selectedApplicationId={
            project.id === selectedProjectId ? selectedApplicationId : undefined
          }
          onToggleExpand={() => toggleCollapse(project.id)}
          onSelectProject={() => {
            onSelectProject(project.id);
            // 若之前被收起，点击项目时顺畅重新展开
            if (collapsedIds.has(project.id)) {
              toggleCollapse(project.id);
            }
          }}
          onSelectApplication={(appId) => onSelectApplication(project.id, appId)}
          onCreateApplication={() => onCreateApplication(project.id)}
          onEditProject={() => onEditProject(project)}
          onDeleteProject={() => onDeleteProject(project)}
        />
      ))}
    </div>
  );
}

function ProjectBranch({
  project,
  isExpanded,
  isSelected,
  selectedApplicationId,
  onToggleExpand,
  onSelectProject,
  onSelectApplication,
  onCreateApplication,
  onEditProject,
  onDeleteProject,
}: {
  project: Project;
  isExpanded: boolean;
  isSelected: boolean;
  selectedApplicationId?: string;
  onToggleExpand: () => void;
  onSelectProject: () => void;
  onSelectApplication: (applicationId: string) => void;
  onCreateApplication: () => void;
  onEditProject: () => void;
  onDeleteProject: () => void;
}) {
  const { t } = useI18n();
  const applicationsQuery = useQuery({
    queryKey: ["project-applications", project.id],
    queryFn: () => fetchProjectApplications(project.id),
    enabled: isExpanded,
    refetchInterval: 5000,
  });

  const pending = applicationsQuery.data?.items.map((app) => app.inputStatus);
  const projectStatus = pending?.includes("unavailable")
    ? "unavailable"
    : pending?.includes("rebuild")
      ? "rebuild"
      : pending?.includes("restart")
        ? "restart"
        : undefined;
  const appCount = applicationsQuery.data?.items.length ?? project.applicationIds?.length ?? 0;
  const hasChildSelected = isSelected && Boolean(selectedApplicationId);

  return (
    <div className="ops-tree-branch">
      {/* 项目主行 */}
      <div
        className="ops-tree-row ops-tree-row--project"
        data-selected={(isSelected && !selectedApplicationId) || undefined}
        data-has-child-selected={hasChildSelected || undefined}
      >
        <IconButton
          compact
          className="ops-tree-arrow-btn"
          label={isExpanded ? t("shell.collapseSidebar") : t("shell.expandSidebar")}
          icon={
            <IqujiIcon decorative name={isExpanded ? "chevron-down" : "chevron-right"} size={13} />
          }
          onClick={onToggleExpand}
        />

        <IqujiButton variant="ghost" className="ops-tree-label-btn" onClick={onSelectProject}>
          <IqujiIcon decorative name="folder" size={15} />
          <span className="ops-tree-text" title={project.name}>
            {project.name}
          </span>
          <span className="ops-tree-count-pill" title={`${appCount}`}>
            {appCount}
          </span>
          <ApplicationInputStatus status={projectStatus} />
          <OperationActivityDot scope={{ projectId: project.id }} />
        </IqujiButton>

        {!project.builtin && (
          <div className="ops-tree-actions">
            <IqujiDropdownMenu
              ariaLabel={t("common.actions")}
              trigger={
                <IconButton
                  compact
                  data-ops-guide-menu={isSelected ? "project.edit" : undefined}
                  className="ops-tree-more-btn"
                  label={t("common.actions")}
                  icon={<IqujiIcon decorative name="more" size={13} />}
                />
              }
              items={[
                {
                  key: "edit",
                  label: t("projects.editProject"),
                  onSelect: () => {
                    if (isSelected) noteGuideFormOpen("project.edit");
                    onEditProject();
                  },
                },
                {
                  key: "delete",
                  label: t("projects.deleteConfirmLabel"),
                  tone: "danger",
                  onSelect: onDeleteProject,
                },
              ]}
            />
          </div>
        )}
      </div>

      {/* 展开的子应用列表 */}
      {isExpanded ? (
        <div className="ops-tree-sublist">
          {applicationsQuery.isLoading ? (
            <div className="ops-tree-loading-row">
              <IqujiLoadingState label={t("common.loading")} />
            </div>
          ) : null}

          {applicationsQuery.data?.items.map((app) => (
            <div
              key={app.id}
              className="ops-tree-row ops-tree-row--app"
              data-selected={selectedApplicationId === app.id || undefined}
            >
              <IqujiButton
                variant="ghost"
                className="ops-tree-app-btn"
                onClick={() => onSelectApplication(app.id)}
              >
                <IqujiIcon decorative name="boxes" size={14} />
                <span className="ops-tree-text" title={app.name}>
                  {app.name}
                </span>
                <ApplicationInputStatus status={app.inputStatus} />
                <OperationActivityDot scope={{ applicationId: app.id }} />
              </IqujiButton>
            </div>
          ))}

          {/* 新建应用 */}
          <div className="ops-tree-row ops-tree-row--action">
            <IqujiButton
              variant="ghost"
              data-ops-guide-open={isSelected ? "application.create" : undefined}
              className="ops-tree-action-btn"
              onClick={onCreateApplication}
              iconStart={<IqujiIcon decorative name="plus" size={12} />}
            >
              {t("projects.addApp")}
            </IqujiButton>
          </div>
        </div>
      ) : null}
    </div>
  );
}
