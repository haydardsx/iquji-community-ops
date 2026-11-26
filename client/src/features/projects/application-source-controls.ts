import { useState } from "react";
import type { UseFormReturn } from "react-hook-form";
import { useAutomationForm, useAutomationOpener } from "@/shared/desktop/automation-controls";
import type { ApplicationForm } from "./model";

export function useApplicationSourceControls(
  applicationId: string,
  visible: boolean,
  form: UseFormReturn<ApplicationForm>,
  save: (value: ApplicationForm) => Promise<unknown>,
) {
  const [open, setOpen] = useState(false);
  const provider = form.watch("source.provider");
  const gitFields = [
    "source.repositoryUrl",
    "source.ref",
    "source.authSecretRef",
    "source.sourcePath",
    "source.jenkinsfilePath",
    "source.registryMirrorUrl",
    "buildServerId",
    "registry.repository",
    "registry.username",
    "registry.secretRef",
  ] as const;
  const imageFields = ["source.image", "source.registryMirrorUrl"] as const;
  const packageFields = ["source.baseImage", "source.packageName", "source.version"] as const;
  const fields =
    provider === "git" ? gitFields : provider === "image" ? imageFields : packageFields;
  const setters: Record<string, (value: string) => void> = {};
  const values: Record<string, string> = {};
  for (const key of fields) {
    setters[key] = (value) =>
      form.setValue(key, value, { shouldDirty: true, shouldValidate: true });
    values[key] = form.watch(key);
  }
  setters["source.provider"] = (v) => {
    if (v !== "git" && v !== "image" && v !== "package") throw Error("AUTOMATION_FIELD_INVALID");
    form.setValue("source.provider", v, { shouldDirty: true });
  };
  values["source.provider"] = provider;
  if (provider === "git") {
    setters["source.refType"] = (value) => {
      if (value !== "branch" && value !== "tag" && value !== "commit")
        throw Error("AUTOMATION_FIELD_INVALID");
      form.setValue("source.refType", value, { shouldDirty: true });
    };
    values["source.refType"] = form.watch("source.refType");
    setters["registry.mode"] = (v) => {
      if (v !== "enabled" && v !== "disabled") throw Error("AUTOMATION_FIELD_INVALID");
      form.setValue("registry.mode", v, { shouldDirty: true });
    };
    values["registry.mode"] = form.watch("registry.mode");
  }
  const close = () => {
    form.reset();
    setOpen(false);
  };
  useAutomationOpener("application.source", () => setOpen(true), visible);
  useAutomationForm(
    "application.source",
    open && visible,
    applicationId,
    values,
    setters,
    close,
    async () => {
      await form.handleSubmit(
        async (data) => {
          await save(data);
          setOpen(false);
        },
        () => {
          throw Error("AUTOMATION_FORM_INVALID");
        },
      )();
    },
  );
  return { open, setOpen, close };
}
