import {
  IqujiButton,
  IqujiDialog,
  IqujiField,
  IqujiInput,
  IqujiSelect,
} from "@iquji-community/web-component";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Application } from "@/contracts/types";
import { useAutomationForm } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { ActionGuideCard } from "@/shared/tutorial/ActionGuideOverlay";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import { createApplication, fetchApplicationTemplates, updateApplication } from "./api";

export function ApplicationDialog({
  open,
  application,
  projectId,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  application?: Application;
  projectId: string;
  onOpenChange: (open: boolean) => void;
  onSaved: (application: Application) => void | Promise<void>;
}) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [templateId, setTemplateId] = useState("");
  const templates = useQuery({
    queryKey: ["application-templates"],
    queryFn: fetchApplicationTemplates,
    enabled: open && !application,
  });
  useOperationError(templates.error, t("common.opFailed"));
  useEffect(() => {
    if (open) setTemplateId("");
    if (open) setName(application?.name ?? "");
  }, [open, application?.name]);
  const notify = useOperationNotice();
  const mutation = useMutation({
    mutationFn: () =>
      application
        ? updateApplication(application.id, { name: name.trim() })
        : createApplication(projectId, { name: name.trim(), templateId: templateId || undefined }),
    onSuccess: async (saved) => {
      await onSaved(saved);
      notify(application ? t("projects.dialogAppRenamed") : t("projects.dialogAppCreated"));
      onOpenChange(false);
    },
  });
  useOperationError(mutation.error, t("common.opFailed"));
  useAutomationForm(
    application ? "application.edit" : "application.create",
    open,
    application?.id ?? projectId,
    { name, templateId },
    {
      name: setName,
      templateId: (value) => {
        if (value && !templates.data?.items.some((row) => row.id === value))
          throw Error("AUTOMATION_OBJECT_DELETED");
        setTemplateId(value);
      },
    },
    () => onOpenChange(false),
    async () => {
      if (!name.trim() || mutation.isPending) throw Error("AUTOMATION_FORM_INVALID");
      return mutation.mutateAsync();
    },
  );
  return (
    <IqujiDialog
      actions={
        <>
          <IqujiButton
            data-ops-guide-command="close"
            disabled={mutation.isPending}
            onClick={() => onOpenChange(false)}
            variant="secondary"
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            data-ops-guide-command="submit"
            disabled={!name.trim() || mutation.isPending}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {application ? t("projects.dialogSaveName") : t("projects.dialogCreateAndConfigure")}
          </IqujiButton>
        </>
      }
      closeLabel={t("common.close")}
      description={
        application ? t("projects.dialogAppDescRename") : t("projects.dialogAppDescCreate")
      }
      onOpenChange={onOpenChange}
      open={open}
      size="sm"
      title={application ? t("projects.dialogRenameAppTitle") : t("projects.dialogNewAppTitle")}
    >
      <div
        data-ops-guide-form={application ? "application.edit" : "application.create"}
        className="ops-form-stack"
      >
        {open && <ActionGuideCard form={application ? "application.edit" : "application.create"} />}
        {!application && (
          <IqujiField label={t("projects.template")}>
            <IqujiSelect
              options={[
                { label: t("projects.blankApplication"), value: "" },
                ...(templates.data?.items ?? []).map((item) => ({
                  label: item.name,
                  value: item.id,
                })),
              ]}
              data-ops-guide-field="templateId"
              value={templateId}
              onValueChange={(value) => {
                const id = value ?? "";
                setTemplateId(id);
                if (!name.trim() || templates.data?.items.some((item) => item.name === name))
                  setName(templates.data?.items.find((item) => item.id === id)?.name ?? "");
              }}
            />
          </IqujiField>
        )}
        <IqujiField label={t("projects.dialogAppName")} required>
          <IqujiInput
            autoFocus
            onChange={(event) => setName(event.currentTarget.value)}
            placeholder={t("projects.dialogAppNamePlaceholder")}
            data-ops-guide-field="name"
            value={name}
          />
        </IqujiField>
      </div>
    </IqujiDialog>
  );
}
