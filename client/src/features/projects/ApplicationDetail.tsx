import { zodResolver } from "@hookform/resolvers/zod";
import {
  IconButton,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDropdownMenu,
  IqujiTabs,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { useFieldArray, useForm } from "react-hook-form";
import type { Application, ProjectWorkspace, ServerRuntimeMetrics } from "@/contracts/types";
import { fetchDomains } from "@/features/domains/api";
import { fetchSecretAssets, SECRET_ASSETS_QUERY_KEY } from "@/features/secrets/api";
import { useAutomationOpener, useAutomationScope } from "@/shared/desktop/automation-controls";
import { useWorkspaceUnloadGuard } from "@/shared/desktop/workspace-interactions";
import { useI18n } from "@/shared/i18n";
import { OperationActivityDot } from "@/shared/operations/activity";
import { noteGuideFormOpen } from "@/shared/tutorial/action-guide-state";
import { TerminalDrawer } from "@/shared/ui/terminal-drawer";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import { ApplicationBuildSection } from "./ApplicationBuildSection";
import { ApplicationSourceSection } from "./ApplicationConfigSections";
import { ApplicationDialog } from "./ApplicationDialog";
import { ApplicationDirectPorts } from "./ApplicationDirectPorts";
import { FormSelect } from "./ApplicationFields";
import { ApplicationInputStatus } from "./ApplicationInputStatus";
import { ApplicationLogsSection } from "./ApplicationLogsSection";
import { ApplicationOutputsSection } from "./ApplicationOutputsSection";
import { ApplicationRuntimeSection } from "./ApplicationRuntimeSection";
import { ApplicationServicesSection } from "./ApplicationServicesSection";
import { ApplicationVariablesSection } from "./ApplicationVariablesSection";
import { deleteApplication, fetchApplicationRuntime, saveDeploymentConfig } from "./api";
import { useApplicationSourceControls } from "./application-source-controls";
import { ContainerFields } from "./container-fields";
import { managerOptions } from "./deployment-eligibility";
import {
  type ApplicationForm,
  configPayload,
  createApplicationFormSchema,
  formValues,
  stateLabel,
  stateTone,
} from "./model";
import { ProjectRoutesTab } from "./ProjectRoutesTab";

export type ApplicationTab = "source" | "runtime" | "container" | "routes" | "variables" | "logs";

export function ApplicationDetail({
  application,
  workspace,
  onChanged,
  onClose,
  onDeleted,
  tab,
  onTabChange,
}: {
  application: Application;
  workspace: ProjectWorkspace;
  onChanged: () => void;
  onClose: () => void;
  onDeleted: () => void;
  tab: ApplicationTab;
  onTabChange: (tab: ApplicationTab) => void;
}) {
  const { t } = useI18n();
  const schema = useMemo(() => createApplicationFormSchema(t), [t]);
  const config = useMemo(
    () =>
      workspace.applicationDeploymentConfigs?.find(
        (item) => item.applicationId === application.id,
      ) ?? {},
    [application.id, workspace.applicationDeploymentConfigs],
  );
  const form = useForm<ApplicationForm>({
    resolver: zodResolver(schema),
    defaultValues: formValues(config),
  });
  const dirty = form.formState.isDirty;
  const ports = useFieldArray({ control: form.control, name: "ports" });
  const unloadGuard = useWorkspaceUnloadGuard(dirty);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);
  useAutomationOpener("application.edit", () => setRenameOpen(true));
  useAutomationScope("application-detail", application.id, tab);
  const [terminalOpen, setTerminalOpen] = useState(false);
  const notice = useOperationNotice();
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!form.formState.isDirty) form.reset(formValues(config));
  }, [config, form]);

  const save = useMutation({
    mutationFn: async (value: ApplicationForm) => {
      if (config.sourceConfig && value.source.provider === "git" && !value.source.repositoryUrl) {
        form.setError("source.repositoryUrl", { message: t("apps.repoUrlRequired") });
        throw new Error(t("apps.repoPrompt"));
      }
      return saveDeploymentConfig(application.id, configPayload(value, config));
    },
    onSuccess: (saved) => {
      form.reset(formValues(saved));
      notice(t("apps.saved"));
      onChanged();
    },
  });
  useOperationError(save.error, t("apps.saveFailed"));

  const remove = useMutation({
    mutationFn: () => deleteApplication(application.id),
    onSuccess: () => {
      notice(t("apps.deleted"));
      setDeleteOpen(false);
      onDeleted();
      onChanged();
    },
  });
  useOperationError(remove.error, t("apps.deleteFailed"));

  const secrets = useQuery({ queryKey: SECRET_ASSETS_QUERY_KEY, queryFn: fetchSecretAssets });
  const domains = useQuery({ queryKey: ["domains"], queryFn: fetchDomains });
  useOperationError(secrets.error, t("secrets.loadError"));
  useOperationError(domains.error, t("domains.loadError"));

  const runtime = useQuery({
    queryKey: ["application-runtime", application.id],
    queryFn: () => fetchApplicationRuntime(application.id),
    refetchInterval: 5000,
  });

  useEffect(() => {
    const snapshot = runtime.data;
    if (!snapshot || !workspace.project?.id) return;
    queryClient.setQueryData<ProjectWorkspace>(
      ["project-workspace", workspace.project.id],
      (current) => {
        if (!current) return current;
        const previous = current.applications.find(
          (item) => item.id === application.id,
        )?.runtimeSnapshot;
        if (previous?.checkedAt === snapshot.checkedAt) return current;
        return {
          ...current,
          applications: current.applications.map((item) =>
            item.id === application.id ? { ...item, runtimeSnapshot: snapshot } : item,
          ),
        };
      },
    );
  }, [runtime.data, queryClient, application.id, workspace.project?.id]);

  const blocker = useBlocker({
    withResolver: true,
    enableBeforeUnload: unloadGuard,
    shouldBlockFn: ({ current, next }) => {
      const a = current.search as Record<string, unknown>;
      const b = next.search as Record<string, unknown>;
      return (
        (dirty || save.isPending) &&
        (current.pathname !== next.pathname ||
          a.projectId !== b.projectId ||
          a.applicationId !== b.applicationId)
      );
    },
  });

  useEffect(() => {
    if (blocker.status === "blocked" && !dirty && !save.isPending) blocker.proceed();
  }, [blocker, dirty, save.isPending]);

  const disabled = save.isPending;
  const sourceForm = useApplicationSourceControls(
    application.id,
    tab === "source",
    form,
    save.mutateAsync,
  );
  const shared = { form, disabled };
  const servers = workspace.servers ?? [];
  const assets = secrets.data?.items ?? [];
  const managerId = form.watch("deploymentManagerServerId");
  const managerMetrics = servers.find((server) => server.id === managerId)?.health?.metrics as
    | ServerRuntimeMetrics
    | undefined;
  const gateways = managerOptions(servers, t, managerMetrics?.swarmClusterId ?? "");

  return (
    <>
      <div
        className="ops-application-page"
        data-ops-guide-form={sourceForm.open && tab === "source" ? "application.source" : undefined}
      >
        <header className="ops-application-page__header">
          <div className="ops-application-page__heading">
            <div className="ops-application-page__breadcrumb">
              <IqujiButton
                variant="ghost"
                className="ops-application-page__back-link"
                onClick={onClose}
              >
                {workspace.project?.name}
              </IqujiButton>
              <span className="ops-application-page__separator">/</span>
              <span className="ops-application-page__current">{application.name}</span>
            </div>
            <div className="ops-application-page__title-row">
              <h1 data-ops-guide-object={application.id}>{application.name}</h1>
              <ApplicationInputStatus
                status={application.inputStatus}
                changes={application.inputChanges}
                detail
              />
              <IqujiBadge tone={stateTone(runtime.data?.status)}>
                {runtime.data
                  ? stateLabel(runtime.data.status, t)
                  : runtime.isError
                    ? t("apps.statusError")
                    : t("apps.statusChecking")}
              </IqujiBadge>
            </div>
          </div>
          <div className="ops-application-page__toolbar">
            <IqujiDropdownMenu
              ariaLabel={t("apps.manage")}
              trigger={
                <IconButton
                  data-ops-guide-menu="application.edit"
                  label={t("apps.manage")}
                  icon={<IqujiIcon decorative name="more" />}
                />
              }
              items={[
                {
                  key: "terminal",
                  label: t("terminal.container"),
                  icon: <IqujiIcon decorative name="terminal" size={16} />,
                  onSelect: () => setTerminalOpen(true),
                },
                {
                  key: "rename",
                  label: t("apps.rename"),
                  icon: <IqujiIcon decorative name="pencil" size={16} />,
                  disabled: dirty || disabled,
                  onSelect: () => {
                    noteGuideFormOpen("application.edit");
                    setRenameOpen(true);
                  },
                },
                { kind: "separator", key: "separator" },
                {
                  key: "delete",
                  label: t("apps.deleteConfig"),
                  icon: <IqujiIcon decorative name="trash" size={16} />,
                  tone: "danger",
                  disabled: dirty || disabled,
                  onSelect: () => setDeleteOpen(true),
                },
              ]}
            />
          </div>
        </header>

        <div className="ops-application-page__tabs">
          <IqujiTabs
            label={t("apps.title")}
            value={tab}
            onChange={onTabChange}
            tabs={[
              {
                label: (
                  <span className="ops-row">
                    {t("apps.tabSource")}
                    <OperationActivityDot
                      scope={{
                        applicationId: application.id,
                        action:
                          form.watch("source.provider") !== "git"
                            ? "deploy_application"
                            : "request_jenkins_release",
                      }}
                    />
                  </span>
                ),
                value: "source",
              },
              { label: t("apps.tabRuntime"), value: "runtime" },
              { label: t("projects.containerSettings"), value: "container" },
              {
                label: (
                  <span className="ops-row">
                    {t("apps.tabRoutes")}
                    <OperationActivityDot
                      scope={{ applicationId: application.id, section: "application-routes" }}
                    />
                  </span>
                ),
                value: "routes",
              },
              {
                label: (
                  <span className="ops-row">
                    {t("apps.tabVariables")}
                    <ApplicationInputStatus status={application.inputStatus} />
                  </span>
                ),
                value: "variables",
              },
              { label: t("apps.tabLogs"), value: "logs" },
            ]}
          />
        </div>

        <div className="ops-application-page__content" data-tab={tab}>
          <div hidden={tab !== "source"}>
            <div className="ops-source-layout">
              <ApplicationSourceSection {...shared} servers={servers} secrets={assets} />
              <ApplicationBuildSection
                action={
                  config.sourceConfig?.provider && config.sourceConfig.provider !== "git"
                    ? "deploy_application"
                    : "request_jenkins_release"
                }
                applicationId={application.id}
                dirty={dirty}
                configured={Boolean(
                  config.sourceConfig &&
                    (config.sourceConfig.provider !== "git" || config.buildServerId) &&
                    config.deploymentManagerServerId &&
                    config.swarmRuntime?.services?.length,
                )}
                visible={tab === "source"}
                onChanged={onChanged}
              />
            </div>
          </div>
          <div hidden={tab !== "runtime"}>
            <div className="ops-form-stack">
              <ApplicationRuntimeSection
                applicationId={application.id}
                runtime={runtime.data}
                servers={servers}
                loading={runtime.isLoading}
                error={runtime.error}
                onChanged={() => {
                  void runtime.refetch();
                  onChanged();
                }}
                dirty={dirty}
                hasActiveRoutes={(workspace.routes ?? []).some(
                  (route) =>
                    route.applicationId === application.id &&
                    (route.status === "active" ||
                      Boolean(route.lastApplied) ||
                      Boolean(route.pendingRemote)),
                )}
              />
              <ApplicationServicesSection
                {...shared}
                servers={servers}
                applicationId={application.id}
              />
            </div>
          </div>
          <div hidden={tab !== "container"}>
            <ContainerFields {...shared} ports={ports} />
          </div>
          <div hidden={tab !== "routes"}>
            <div className="ops-form-stack">
              <ApplicationDirectPorts
                {...shared}
                portRows={ports.fields}
                runtime={runtime.data}
                loading={runtime.isLoading}
                error={runtime.error}
              />
              <ProjectRoutesTab
                ports={
                  config.swarmRuntime?.ports?.length ||
                  (config.swarmRuntime?.containerPort === undefined &&
                    config.swarmRuntime?.publishedPort === undefined)
                    ? config.swarmRuntime?.ports
                    : undefined
                }
                applicationId={application.id}
                routes={(workspace.routes ?? []).filter(
                  (item) => item.applicationId === application.id,
                )}
                services={config.swarmRuntime?.services ?? []}
                domains={domains.data?.items ?? []}
                onChanged={onChanged}
                dirty={dirty}
                gatewayReady={Boolean(
                  config.gatewayServerId &&
                    gateways.some(
                      (option) => option.value === config.gatewayServerId && !option.disabled,
                    ),
                )}
                gatewayField={
                  <FormSelect
                    {...shared}
                    name="gatewayServerId"
                    label={t("apps.gatewayServer")}
                    options={gateways}
                  />
                }
              />
            </div>
          </div>
          <div hidden={tab !== "variables"}>
            <div className="ops-form-stack">
              <ApplicationVariablesSection
                {...shared}
                secrets={assets}
                outputs={(workspace.availableOutputs ?? []).filter(
                  (o) => o.applicationId !== application.id,
                )}
              />
              <ApplicationOutputsSection
                {...shared}
                secrets={assets}
                routes={(workspace.routes ?? []).filter((r) => r.applicationId === application.id)}
                gitSource={form.watch("source.provider") === "git"}
              />
            </div>
          </div>
          {tab === "logs" ? (
            <ApplicationLogsSection
              applicationId={application.id}
              services={config.swarmRuntime?.services ?? []}
              runtime={runtime.data}
              runtimeLoading={runtime.isLoading}
              logCommand={config.swarmRuntime?.logCommand}
            />
          ) : null}
        </div>

        {dirty || save.isPending || (sourceForm.open && tab === "source") ? (
          <footer className="ops-application-page__footer">
            <span className="ops-muted" aria-live="polite">
              {save.isPending ? t("apps.saving") : dirty ? t("common.unsavedChanges") : null}
            </span>
            <div className="ops-row">
              <IqujiButton
                data-ops-guide-command="close"
                disabled={disabled}
                onClick={sourceForm.close}
                variant="secondary"
              >
                {dirty ? t("common.discardChanges") : t("common.close")}
              </IqujiButton>
              <IqujiButton
                data-ops-guide-command="submit"
                disabled={!dirty || disabled}
                loading={save.isPending}
                onClick={() =>
                  void form.handleSubmit(
                    (value) => save.mutate(value, { onSuccess: () => sourceForm.setOpen(false) }),
                    (errors) => {
                      const section =
                        errors.source || errors.buildServerId
                          ? "source"
                          : errors.gatewayServerId ||
                              errors.publishedPort ||
                              (Array.isArray(errors.ports) &&
                                errors.ports.some((error) => error?.publishedPort)) ||
                              (Array.isArray(errors.services) &&
                                errors.services.some((error) => error?.publishedPort))
                            ? "routes"
                            : errors.outputs || errors.variables
                              ? "variables"
                              : errors.ports ||
                                  errors.volumes ||
                                  errors.command ||
                                  errors.healthCommand ||
                                  errors.logCommand ||
                                  errors.initializeCommand ||
                                  errors.containerPort
                                ? "container"
                                : "runtime";
                      onTabChange(section);
                    },
                  )()
                }
              >
                {t("apps.saveConfig")}
              </IqujiButton>
            </div>
          </footer>
        ) : null}
      </div>
      <TerminalDrawer
        open={terminalOpen}
        onOpenChange={setTerminalOpen}
        kind="application"
        id={application.id}
        name={application.name}
        runtime={runtime.data}
        servers={servers}
      />
      <ApplicationDialog
        application={application}
        projectId={application.projectId}
        open={renameOpen}
        onOpenChange={setRenameOpen}
        onSaved={onChanged}
      />
      <IqujiConfirmDialog
        open={deleteOpen}
        title={t("apps.deleteTitle")}
        description={t("apps.deleteDesc")}
        closeLabel={t("apps.deleteCloseLabel")}
        cancelLabel={t("common.cancel")}
        confirmLabel={t("apps.deleteConfirm")}
        confirmLoading={remove.isPending}
        onConfirm={() => remove.mutate()}
        onOpenChange={setDeleteOpen}
      />
      <IqujiConfirmDialog
        open={blocker.status === "blocked"}
        title={t("common.discardConfirmTitle")}
        description={t("common.discardConfirmDesc")}
        closeLabel={t("common.close")}
        cancelLabel={t("common.continueEditing")}
        confirmLabel={t("common.discardChanges")}
        confirmDisabled={save.isPending}
        onOpenChange={(open) => {
          if (!open && blocker.status === "blocked") blocker.reset();
        }}
        onConfirm={() => {
          if (blocker.status === "blocked") {
            form.reset();
            blocker.proceed();
          }
        }}
      />
    </>
  );
}
