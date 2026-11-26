import {
  IqujiButton,
  IqujiField,
  IqujiInput,
  IqujiSelect,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { Server } from "@/contracts/types";
import { errorMessage } from "@/shared/api/client";
import { useAutomationForm } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import { ActionGuideCard } from "@/shared/tutorial/ActionGuideOverlay";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { createServer, type ServerInput, updateServer } from "./api";

const withCurrent = (value: string, options: Array<{ label: string; value: string }>) =>
  options.some((option) => option.value === value)
    ? options
    : [...options, { label: value, value }];

export function ServerDialog({
  open,
  server,
  onOpenChange,
  onSaved,
}: {
  open: boolean;
  server: Server | null;
  onOpenChange: (open: boolean) => void;
  onSaved: (server: Server) => void;
}) {
  const notify = useIqujiNotification();
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [publicHost, setPublicHost] = useState("");
  const [privateHost, setPrivateHost] = useState("");
  const [port, setPort] = useState("22");
  const [sshUser, setSshUser] = useState("root");
  const [architecture, setArchitecture] = useState("unknown");
  const [packageManager, setPackageManager] = useState("unknown");
  const [serviceManager, setServiceManager] = useState("unknown");
  const [dockerRepoMirror, setDockerRepoMirror] = useState("auto");
  const [dockerRegistryMirrorUrl, setDockerRegistryMirrorUrl] = useState("");

  useEffect(() => {
    if (!open) return;
    const profile = server?.platformProfile ?? {};
    setName(server?.name ?? "");
    setPublicHost(server?.publicHost ?? "");
    setPrivateHost(server?.privateHost ?? "");
    setPort(String(server?.port ?? 22));
    setSshUser(server?.sshUser ?? "root");
    setArchitecture(String(profile.architecture ?? "unknown"));
    setPackageManager(String(profile.packageManager ?? "unknown"));
    setServiceManager(String(profile.serviceManager ?? "unknown"));
    setDockerRepoMirror(String(profile.dockerRepoMirror ?? "auto"));
    setDockerRegistryMirrorUrl(String(profile.dockerRegistryMirrorUrl ?? ""));
  }, [open, server]);

  const mutation = useMutation({
    mutationFn: () => {
      const payload: ServerInput = {
        name: name.trim(),
        publicHost: publicHost.trim(),
        privateHost: privateHost.trim(),
        port: Number(port),
        sshUser: sshUser.trim() || "root",
        platformProfile: {
          ...server?.platformProfile,
          architecture,
          packageManager,
          serviceManager,
          dockerRepoMirror,
          dockerRegistryMirrorUrl: dockerRegistryMirrorUrl.trim(),
          overrideSource: "manual",
        },
      };
      return server ? updateServer(server.id, payload) : createServer(payload);
    },
    onSuccess: (saved) => {
      onSaved(saved);
      onOpenChange(false);
      notify.open({
        title: t("common.allSaved"),
        description: t("servers.probeToVerify"),
        tone: "success",
      });
    },
    onError: (error) =>
      notify.open({
        title: t("common.opFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      }),
  });

  const validPort = Number.isInteger(Number(port)) && Number(port) >= 1 && Number(port) <= 65535;
  const canSubmit = Boolean(name.trim() && publicHost.trim() && validPort);

  useAutomationForm(
    server ? "server.edit" : "server.create",
    open,
    server?.id ?? "",
    {
      name,
      publicHost,
      privateHost,
      port,
      sshUser,
      dockerRegistryMirrorUrl,
      architecture,
      packageManager,
      serviceManager,
      dockerRepoMirror,
    },
    {
      name: setName,
      architecture: setArchitecture,
      packageManager: setPackageManager,
      serviceManager: setServiceManager,
      dockerRepoMirror: setDockerRepoMirror,
      publicHost: setPublicHost,
      privateHost: setPrivateHost,
      port: setPort,
      sshUser: setSshUser,
      dockerRegistryMirrorUrl: setDockerRegistryMirrorUrl,
    },
    () => onOpenChange(false),
    async () => {
      if (!canSubmit) throw Error("AUTOMATION_FORM_INVALID");
      return mutation.mutateAsync();
    },
  );
  return (
    <InspectorDrawer
      open={open}
      onClose={() => {
        if (!mutation.isPending) onOpenChange(false);
      }}
      size="lg"
      title={
        server ? `${t("servers.dialogEditTitle")} · ${server.name}` : t("servers.dialogAddTitle")
      }
      description={t("servers.dialogDesc")}
      footer={
        <>
          <IqujiButton
            data-ops-guide-command="close"
            disabled={mutation.isPending}
            variant="secondary"
            onClick={() => onOpenChange(false)}
          >
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            data-ops-guide-command="submit"
            disabled={!canSubmit}
            loading={mutation.isPending}
            onClick={() => mutation.mutate()}
          >
            {t("common.save")}
          </IqujiButton>
        </>
      }
    >
      <div
        data-ops-guide-form={server ? "server.edit" : "server.create"}
        className="ops-form-stack"
      >
        {open && <ActionGuideCard form={server ? "server.edit" : "server.create"} />}
        <section className="ops-form-section">
          <h3 className="ops-form-section__title">{t("servers.title")}</h3>
          <IqujiField label={t("servers.nameLabel")} required>
            <IqujiInput
              data-ops-guide-field="name"
              value={name}
              onChange={(event) => setName(event.currentTarget.value)}
              placeholder={t("servers.namePlaceholder")}
            />
          </IqujiField>
          <IqujiField label={t("servers.publicHostLabel")} required>
            <IqujiInput
              data-ops-guide-field="publicHost"
              value={publicHost}
              onChange={(event) => setPublicHost(event.currentTarget.value)}
              placeholder={t("servers.publicHostPlaceholder")}
            />
          </IqujiField>
          <IqujiField label={t("servers.privateHostLabel")}>
            <IqujiInput
              data-ops-guide-field="privateHost"
              value={privateHost}
              onChange={(event) => setPrivateHost(event.currentTarget.value)}
              placeholder={t("servers.privateHostPlaceholder")}
            />
          </IqujiField>
          <div className="ops-form-grid">
            <IqujiField
              label={t("servers.sshPortLabel")}
              error={!validPort ? "1 - 65535" : undefined}
            >
              <IqujiInput
                type="number"
                min={1}
                max={65535}
                data-ops-guide-field="port"
                value={port}
                onChange={(event) => setPort(event.currentTarget.value)}
              />
            </IqujiField>
            <IqujiField label={t("servers.sshUserLabel")}>
              <IqujiInput
                data-ops-guide-field="sshUser"
                value={sshUser}
                onChange={(event) => setSshUser(event.currentTarget.value)}
                placeholder="root"
              />
            </IqujiField>
          </div>
        </section>
        <section className="ops-form-section">
          <h3 className="ops-form-section__title">{t("servers.specsTitle")}</h3>
          {server?.platformProfile?.osId ? (
            <p className="ops-muted">
              {t("servers.os")}: {String(server.platformProfile.osId)}{" "}
              {String(server.platformProfile.osVersion ?? "")}
            </p>
          ) : null}
          <div className="ops-form-grid">
            <IqujiField label={t("common.type")}>
              <IqujiSelect
                data-ops-guide-field="architecture"
                value={architecture}
                options={withCurrent(architecture, [
                  { label: t("servers.notProbed"), value: "unknown" },
                  { label: "x86_64", value: "x86_64" },
                  { label: "amd64", value: "amd64" },
                  { label: "aarch64", value: "aarch64" },
                  { label: "arm64", value: "arm64" },
                  { label: "armv7l", value: "armv7l" },
                ])}
                onValueChange={(value) => setArchitecture(value ?? "unknown")}
              />
            </IqujiField>
            <IqujiField label="Package Manager">
              <IqujiSelect
                data-ops-guide-field="packageManager"
                value={packageManager}
                options={withCurrent(packageManager, [
                  { label: t("servers.notProbed"), value: "unknown" },
                  { label: "dnf", value: "dnf" },
                  { label: "yum", value: "yum" },
                  { label: "apt", value: "apt" },
                ])}
                onValueChange={(value) => setPackageManager(value ?? "unknown")}
              />
            </IqujiField>
            <IqujiField label="Service Manager">
              <IqujiSelect
                data-ops-guide-field="serviceManager"
                value={serviceManager}
                options={withCurrent(serviceManager, [
                  { label: t("servers.notProbed"), value: "unknown" },
                  { label: "systemd", value: "systemd" },
                  { label: "service / sysvinit", value: "service" },
                ])}
                onValueChange={(value) => setServiceManager(value ?? "unknown")}
              />
            </IqujiField>
          </div>
        </section>
        <section className="ops-form-section">
          <h3 className="ops-form-section__title">{t("servers.dockerMirror")}</h3>
          <IqujiField label={t("tutorial.guide.dockerRepoMirror")}>
            <IqujiSelect
              data-ops-guide-field="dockerRepoMirror"
              value={dockerRepoMirror}
              options={withCurrent(
                dockerRepoMirror,
                ["auto", "official", "aliyun", "tencent", "tsinghua"].map((value) => ({
                  value,
                  label: value,
                })),
              )}
              onValueChange={(value) => setDockerRepoMirror(value ?? "auto")}
            />
          </IqujiField>
          <IqujiField label="Docker Registry Mirror URL">
            <IqujiInput
              data-ops-guide-field="dockerRegistryMirrorUrl"
              value={dockerRegistryMirrorUrl}
              onChange={(event) => setDockerRegistryMirrorUrl(event.currentTarget.value)}
              placeholder={t("projects.inheritPullMirror")}
            />
          </IqujiField>
        </section>
      </div>
    </InspectorDrawer>
  );
}
