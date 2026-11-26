import {
  IconButton,
  IqujiAlert,
  IqujiConfirmDialog,
  IqujiEmptyState,
  IqujiInput,
  IqujiLoadingState,
  IqujiWorkbenchLayout,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useRouter, useSearch } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useShell } from "@/app/shell";
import { workspacePages } from "@/contracts/pages";
import type { Project } from "@/contracts/types";
import { ApplicationDialog } from "@/features/projects/ApplicationDialog";
import { deleteProject, fetchProjects, fetchWorkspace } from "@/features/projects/api";
import { ProjectDetail } from "@/features/projects/ProjectDetail";
import { ProjectDialog } from "@/features/projects/ProjectDialog";
import { ProjectTree } from "@/features/projects/ProjectTree";
import { errorMessage } from "@/shared/api/client";
import { useAutomationOpener, useAutomationScope } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { markTutorialProjectOpen } from "@/shared/tutorial/controller";
import { ScopeMasterPanel } from "@/shared/ui/ScopeMasterPanel";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";

export function ProjectsPage() {
  const queryClient = useQueryClient();
  const { sidebarCollapsed } = useShell();
  const search = useSearch({ from: workspacePages.projects.path });
  const route = useRouter();
  const navigate = useNavigate({ from: workspacePages.projects.path });
  const { t } = useI18n();

  const [dialog, setDialog] = useState<"create" | "edit" | null>(null);
  const [actionProject, setActionProject] = useState<Project | null>(null);
  const [newApplication, setNewApplication] = useState(false);
  const [targetProjectId, setTargetProjectId] = useState<string | null>(null);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const projects = useQuery({ queryKey: ["projects"], queryFn: fetchProjects });
  const selected = projects.data?.items.find((item) => item.id === search.projectId) ?? null;
  const workspace = useQuery({
    queryKey: ["project-workspace", search.projectId],
    queryFn: () => fetchWorkspace(search.projectId ?? ""),
    enabled: Boolean(search.projectId),
    refetchInterval: 5000,
  });

  // 默认定位到第一个项目
  useEffect(() => {
    if (
      route.latestLocation.pathname !== workspacePages.projects.path ||
      projects.isLoading ||
      !projects.data?.items.length
    )
      return;
    const hasValidSelected = projects.data.items.some((item) => item.id === search.projectId);
    if (!hasValidSelected) {
      const firstProject = projects.data.items[0];
      void navigate({
        replace: true,
        search: (old) => ({
          ...old,
          projectId: firstProject.id,
          applicationId: undefined,
          projectTab: "applications",
        }),
      });
    }
  }, [projects.isLoading, projects.data?.items, search.projectId, navigate, route]);

  // 选定项目后，如果尚未选定应用，默认选中该项目下的第一个应用
  useEffect(() => {
    if (
      route.latestLocation.pathname !== workspacePages.projects.path ||
      workspace.isLoading ||
      !workspace.data?.applications.length ||
      (search.projectView === "overview" && !search.applicationId)
    )
      return;
    const currentApps = workspace.data.applications;
    const hasValidApp = currentApps.some((app) => app.id === search.applicationId);
    if (!hasValidApp) {
      const firstApp = currentApps[0];
      void navigate({
        replace: true,
        search: (old) => ({
          ...old,
          applicationId: firstApp.id,
          applicationTab: "source",
        }),
      });
    }
  }, [
    workspace.isLoading,
    workspace.data?.applications,
    search.applicationId,
    search.projectView,
    navigate,
    route,
  ]);

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["projects"] }),
      queryClient.invalidateQueries({ queryKey: ["project-workspace"] }),
      queryClient.invalidateQueries({ queryKey: ["project-applications"] }),
    ]);
  };

  const notice = useOperationNotice();
  const remove = useMutation({
    mutationFn: () => {
      const projectToDelete = actionProject ?? selected;
      if (!projectToDelete) throw new Error(t("projects.selectProjectPrompt"));
      return deleteProject(projectToDelete.id);
    },
    onSuccess: async () => {
      const deletedId = (actionProject ?? selected)?.id;
      setDeleteOpen(false);
      setActionProject(null);
      notice(t("projects.deleteSuccess"));
      if (search.projectId === deletedId) {
        await navigate({
          search: (old) => ({ ...old, projectId: undefined, applicationId: undefined }),
        });
      }
      await refresh();
    },
  });
  useOperationError(remove.error, t("projects.deleteError"));

  const filtered = (projects.data?.items ?? []).filter((item) =>
    `${item.name} ${item.description ?? ""}`
      .toLowerCase()
      .includes(search.search.trim().toLowerCase()),
  );

  useAutomationScope(
    "projects",
    selected?.id ?? "",
    search.applicationId ? search.applicationTab : "applications",
  );
  useAutomationOpener("project.create", () => {
    setActionProject(null);
    setDialog("create");
  });
  useAutomationOpener(
    "project.edit",
    () => {
      setActionProject(selected);
      setDialog("edit");
    },
    Boolean(selected),
  );
  useAutomationOpener(
    "application.create",
    () => {
      setTargetProjectId(selected?.id ?? null);
      setNewApplication(true);
    },
    Boolean(selected),
  );
  const activeProjectId = targetProjectId ?? selected?.id ?? search.projectId;

  return (
    <div className="ops-workbench-fill">
      {projects.isError ? (
        <IqujiAlert title={t("projects.loadError")} tone="danger">
          {errorMessage(projects.error)}
        </IqujiAlert>
      ) : null}
      <IqujiWorkbenchLayout
        className="ops-inner-workbench"
        centerClassName="ops-workbench__center"
        leftClassName="ops-inner-workbench__scope"
        defaultLeftWidth={260}
        leftMinWidth={220}
        leftMaxWidth={360}
        leftCollapsed={sidebarCollapsed}
        leftResizeLabel={t("projects.resizeLabel")}
        left={
          <ScopeMasterPanel
            title={t("projects.title")}
            count={filtered.length}
            action={
              <IconButton
                compact
                data-ops-guide-open="project.create"
                data-ops-tutorial="project-create.open"
                label={t("projects.newProject")}
                icon={<IqujiIcon decorative name="plus" />}
                onClick={() => {
                  markTutorialProjectOpen();
                  setActionProject(null);
                  setDialog("create");
                }}
              />
            }
            search={
              <IqujiInput
                aria-label={t("projects.searchAriaLabel")}
                placeholder={t("projects.searchPlaceholder")}
                value={search.search}
                onChange={(event) =>
                  void navigate({
                    replace: true,
                    search: (old) => ({ ...old, search: event.currentTarget.value }),
                  })
                }
              />
            }
          >
            {projects.isLoading ? <IqujiLoadingState label={t("projects.loading")} /> : null}
            {projects.data && filtered.length === 0 ? (
              <IqujiEmptyState
                title={search.search ? t("projects.emptySearchTitle") : t("projects.emptyTitle")}
                body={search.search ? t("projects.emptySearchBody") : t("projects.emptyBody")}
              />
            ) : null}
            {filtered.length > 0 ? (
              <ProjectTree
                projects={filtered}
                selectedProjectId={search.projectId}
                selectedApplicationId={search.applicationId}
                onSelectProject={(projectId) =>
                  void navigate({
                    search: (old) => ({
                      ...old,
                      projectId,
                      applicationId: undefined,
                      projectTab: "applications",
                      appSearch: "",
                    }),
                  })
                }
                onSelectApplication={(projectId, applicationId) =>
                  void navigate({
                    search: (old) => ({
                      ...old,
                      projectId,
                      applicationId,
                      applicationTab: "source",
                      projectTab: "applications",
                    }),
                  })
                }
                onCreateApplication={(projectId) => {
                  setTargetProjectId(projectId);
                  setNewApplication(true);
                }}
                onEditProject={(project) => {
                  setActionProject(project);
                  setDialog("edit");
                }}
                onDeleteProject={(project) => {
                  setActionProject(project);
                  setDeleteOpen(true);
                }}
              />
            ) : null}
          </ScopeMasterPanel>
        }
        center={
          search.projectId ? (
            <ProjectDetail
              workspace={workspace.data}
              loading={workspace.isLoading}
              error={workspace.error}
              onChanged={() => void refresh()}
              applicationId={search.applicationId}
              onSelectApplication={(id) =>
                void navigate({
                  search: (old) => ({
                    ...old,
                    applicationId: id,
                    projectView: id ? undefined : "overview",
                    applicationTab: "source",
                  }),
                })
              }
              applicationTab={search.applicationTab}
              onApplicationTabChange={(applicationTab) =>
                void navigate({ replace: true, search: (old) => ({ ...old, applicationTab }) })
              }
              search={search.appSearch}
              onSearchChange={(appSearch) =>
                void navigate({ replace: true, search: (old) => ({ ...old, appSearch }) })
              }
            />
          ) : (
            <div className="ops-empty-surface">
              <IqujiEmptyState
                title={t("projects.emptySelectionTitle")}
                body={t("projects.emptySelectionBody")}
              />
            </div>
          )
        }
      />
      <ProjectDialog
        open={dialog !== null}
        project={dialog === "edit" ? (actionProject ?? selected) : null}
        onOpenChange={(open) => {
          if (!open) {
            setDialog(null);
            setActionProject(null);
          }
        }}
        onSaved={async (project) => {
          await refresh();
          await navigate({
            search: (old) => ({
              ...old,
              projectId: project.id,
              applicationId: undefined,
              projectTab: "applications",
            }),
          });
        }}
      />
      {activeProjectId ? (
        <ApplicationDialog
          open={newApplication}
          projectId={activeProjectId}
          onOpenChange={(open) => {
            setNewApplication(open);
            if (!open) setTargetProjectId(null);
          }}
          onSaved={async (application) => {
            await refresh();
            await navigate({
              search: (old) => ({
                ...old,
                projectId: activeProjectId,
                applicationId: application.id,
                applicationTab: "source",
                projectTab: "applications",
              }),
            });
          }}
        />
      ) : null}
      <IqujiConfirmDialog
        open={deleteOpen}
        title={t("projects.deleteDialogTitle")}
        description={t("projects.deleteDialogDesc", {
          name: (actionProject ?? selected)?.name ?? "",
        })}
        closeLabel={t("projects.deleteCloseLabel")}
        cancelLabel={t("common.cancel")}
        confirmLabel={t("projects.deleteConfirmLabel")}
        confirmLoading={remove.isPending}
        onConfirm={() => remove.mutate()}
        onOpenChange={(open) => {
          setDeleteOpen(open);
          if (!open) setActionProject(null);
        }}
      />
    </div>
  );
}
