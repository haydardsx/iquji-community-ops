import {
  IqujiButton,
  IqujiDialog,
  IqujiField,
  IqujiInput,
  IqujiTextarea,
} from "@iquji-community/web-component";
import { useMutation } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { Project } from "@/contracts/types";
import { useAutomationForm } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { ActionGuideCard } from "@/shared/tutorial/ActionGuideOverlay";
import { bindTutorialProjectForm, releaseTutorialProjectForm } from "@/shared/tutorial/controller";
import { TutorialOverlay } from "@/shared/tutorial/Tutorial";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import { createProject, updateProject } from "./api";

export function ProjectDialog({
  open,
  project,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  project: Project | null;
  onOpenChange: (open: boolean) => void;
  onSaved: (project: Project) => void | Promise<void>;
}) {
  const { t } = useI18n();
  const mountedForm = useRef<HTMLElement | null>(null);
  const tutorialForm = useCallback(
    (node: HTMLDivElement | null) => {
      const dialog = !project && node?.closest<HTMLElement>('[role="dialog"]');
      if (mountedForm.current && mountedForm.current !== dialog)
        releaseTutorialProjectForm(mountedForm.current);
      mountedForm.current = dialog || null;
      if (dialog) bindTutorialProjectForm(dialog);
    },
    [project],
  );
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  useEffect(() => {
    if (!open) return;
    setName(project?.name ?? "");
    setDescription(project?.description ?? "");
  }, [open, project]);
  const notify = useOperationNotice();
  const mutation = useMutation({
    mutationFn: async () => {
      const input = { name: name.trim(), description: description.trim() };
      return project ? updateProject(project.id, input) : createProject(input);
    },
    onSuccess: async (saved) => {
      notify(t("projects.dialogProjectSaved"));
      await onSaved(saved);
      onOpenChange(false);
    },
  });
  useOperationError(mutation.error, t("common.opFailed"));
  const canSubmit = name.trim().length > 0 && !mutation.isPending;
  useAutomationForm(
    project ? "project.edit" : "project.create",
    open,
    project?.id ?? "",
    { name, description },
    { name: setName, description: setDescription },
    () => onOpenChange(false),
    async () => {
      if (!canSubmit) throw Error("AUTOMATION_FORM_INVALID");
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
            data-ops-tutorial={!project ? "project-create.save" : undefined}
            disabled={!canSubmit}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t("common.save")}
          </IqujiButton>
        </>
      }
      closeLabel={t("common.close")}
      description={project ? t("projects.dialogDescUpdate") : t("projects.dialogDescCreate")}
      onOpenChange={onOpenChange}
      open={open}
      size="sm"
      title={project ? t("projects.dialogEditProjectTitle") : t("projects.dialogNewProjectTitle")}
    >
      <div
        data-ops-guide-form={project ? "project.edit" : "project.create"}
        className="ops-form-stack"
        ref={tutorialForm}
      >
        {open && <ActionGuideCard form={project ? "project.edit" : "project.create"} />}
        {!project && <TutorialOverlay scope="project-form" />}
        <IqujiField label={t("projects.dialogProjectName")} required>
          <IqujiInput
            autoFocus
            data-ops-tutorial={!project ? "project-create.name" : undefined}
            onChange={(event) => setName(event.currentTarget.value)}
            placeholder={t("projects.dialogProjectNamePlaceholder")}
            data-ops-guide-field="name"
            value={name}
          />
        </IqujiField>
        <IqujiField label={t("projects.dialogProjectDesc")}>
          <IqujiTextarea
            onChange={(event) => setDescription(event.currentTarget.value)}
            rows={3}
            data-ops-guide-field="description"
            value={description}
          />
        </IqujiField>
      </div>
    </IqujiDialog>
  );
}
