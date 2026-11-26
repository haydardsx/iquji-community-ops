import { IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import type { SecretAsset, Server, ServerRuntimeMetrics } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { ApplicationCommandField } from "./ApplicationCommandField";
import { ApplicationConfigPanel } from "./ApplicationConfigPanel";
import { type ConfigFormProps, FormSelect, FormText } from "./ApplicationFields";
import { packageManagerLabels, packageManagers } from "./application-source";
import { RegistrySyncSection } from "./RegistrySyncSection";

const packagePlaceholders: Record<
  (typeof packageManagers)[number],
  { packageName: string; version: string; baseImage: string }
> = {
  apt: { packageName: "nginx", version: "1.24.0", baseImage: "debian:bookworm-slim" },
  dnf: { packageName: "nginx", version: "1.24.0", baseImage: "rockylinux:9" },
  yum: { packageName: "nginx", version: "1.20.1", baseImage: "rockylinux:9" },
  npm: { packageName: "@example/cli", version: "1.0.0", baseImage: "node:22-alpine" },
  pypi: { packageName: "uvicorn", version: "0.30.0", baseImage: "python:3.12-slim" },
};

function buildServerOptions(servers: Server[], t: (key: string) => string) {
  return [
    { label: t("projects.serverNone"), value: "" },
    ...servers.map((server) => {
      const metrics = server.health?.metrics as ServerRuntimeMetrics | undefined;
      const ready = metrics?.jenkins === true;
      const suffix = t(ready ? "state.ready" : "projects.jenkinsNotReady");
      return { label: `${server.name} · ${suffix}`, value: server.id, disabled: !ready };
    }),
  ];
}

/** Git 认证下拉选项：显示名称与脱敏摘要，材料缺失的凭据不可选，失效引用单独提示。 */
export function gitCredentialOptions(
  secrets: SecretAsset[],
  currentRef: string | undefined,
  t?: (key: any) => string,
): Array<{ label: string; value: string; disabled?: boolean }> {
  const usable = secrets.filter(
    (item) =>
      item.assetType === "git_token" &&
      item.secretRef &&
      item.materialStatus !== "missing" &&
      item.materialStatus !== "pending",
  );
  const options: Array<{ label: string; value: string; disabled?: boolean }> = [
    { label: t ? t("projects.noCredential") : "无认证（公开仓库）", value: "" },
    ...usable.map((item) => ({
      label: `${item.name} · ${item.maskedSummary ?? (t ? t("projects.masked") : "已脱敏")}`,
      value: item.secretRef ?? "",
    })),
  ];
  if (currentRef && !usable.some((item) => item.secretRef === currentRef)) {
    options.push({
      disabled: true,
      label: t ? t("projects.credentialUnavailable") : "当前凭据不可用，请重新选择",
      value: currentRef,
    });
  }
  return options;
}

export function ApplicationSourceSection({
  form,
  disabled,
  servers,
  secrets,
}: ConfigFormProps & { servers: Server[]; secrets: SecretAsset[] }) {
  const { t } = useI18n();
  const [advanced, setAdvanced] = useState(false);
  const currentSourcePath = (form.watch("source.sourcePath") ?? ".").trim() || ".";
  const provider = form.watch("source.provider");
  const packageManager = form.watch("source.packageManager");
  const pkgPlaceholder = packagePlaceholders[packageManager] ?? packagePlaceholders.apt;
  const override = form.watch("source.registryMirrorUrl")?.trim();
  const buildServer = form.watch("buildServerId");
  const gateway = form.watch("deploymentManagerServerId");
  const services = form.watch("services") ?? [];
  const selectedNodes = new Set(
    provider === "git" ? [buildServer, gateway] : services.map((slot) => slot.serverId),
  );
  const inherited = servers
    .filter((server) => selectedNodes.has(server.id))
    .map((server) => {
      const configured = server.platformProfile?.dockerRegistryMirrorUrl;
      const observed = (server.health?.metrics as ServerRuntimeMetrics | undefined)
        ?.dockerRegistryMirrors;
      if (typeof configured === "string" && configured) {
        return `${server.name} · ${t("projects.mirrorOrigin.server")} · ${configured}`;
      }
      const actual =
        observed?.join(", ") || t(observed ? "projects.mirrorDefault" : "servers.notProbed");
      return `${server.name} · ${t("projects.mirrorOrigin.daemon")} · ${actual}`;
    });
  return (
    <>
      <FormSelect
        form={form}
        disabled={disabled}
        name="source.provider"
        guideOpen="application.source"
        label={t("projects.provider")}
        options={[
          { value: "git", label: "Git + Jenkins" },
          { value: "image", label: t("projects.imageSource") },
          { value: "package", label: t("projects.packageSource") },
        ]}
      />
      {provider !== "package" && (
        <FormText
          form={form}
          disabled={disabled}
          name="source.registryMirrorUrl"
          label={t("projects.pullMirror")}
          placeholder={t("projects.inheritPullMirror")}
          help={[t("projects.pullMirrorHelp"), ...(override ? [] : inherited)].join(" · ")}
        />
      )}
      {provider === "image" ? (
        <ApplicationConfigPanel
          icon="container"
          title={t("projects.imageSource")}
          description={t("projects.fixedImageHint")}
        >
          <FormText
            form={form}
            disabled={disabled}
            name="source.image"
            label={t("projects.imageReference")}
            placeholder="registry.example.com/team/service:1.0"
            required
          />
        </ApplicationConfigPanel>
      ) : provider === "package" ? (
        <ApplicationConfigPanel
          icon="boxes"
          title={t("projects.packageSource")}
          description={t("projects.packageSourceHint")}
        >
          <div className="ops-package-coords-grid">
            <FormSelect
              form={form}
              disabled={disabled}
              name="source.packageManager"
              label={t("projects.packageManager")}
              options={packageManagers.map((value) => ({
                label: packageManagerLabels[value],
                value,
              }))}
            />
            <FormText
              form={form}
              disabled={disabled}
              name="source.packageName"
              label={t("projects.packageName")}
              placeholder={pkgPlaceholder.packageName}
              required
            />
            <FormText
              form={form}
              disabled={disabled}
              name="source.version"
              label={t("projects.packageVersion")}
              placeholder={pkgPlaceholder.version}
              required
            />
          </div>
          <FormText
            form={form}
            disabled={disabled}
            name="source.baseImage"
            label={t("projects.baseImage")}
            placeholder={pkgPlaceholder.baseImage}
            required
          />
          <ApplicationCommandField
            form={form}
            disabled={disabled}
            name="source.command"
            label={t("projects.imageDefaultCommand")}
            variant="embedded"
            required
          />
        </ApplicationConfigPanel>
      ) : (
        <>
          <ApplicationConfigPanel
            icon="code"
            title={t("projects.sourceRepoSectionTitle")}
            description={t("projects.sourceRepoSectionDesc")}
          >
            <FormText
              form={form}
              disabled={disabled}
              name="source.repositoryUrl"
              label={t("projects.repoUrl")}
              placeholder="https://git.example.org/team/repo.git"
            />
            <div className="ops-source-grid--3">
              <FormSelect
                form={form}
                disabled={disabled}
                name="source.refType"
                label={t("projects.refType")}
                options={[
                  { label: t("projects.refTypeBranch"), value: "branch" },
                  { label: t("projects.refTypeTag"), value: "tag" },
                  { label: t("projects.refTypeCommit"), value: "commit" },
                ]}
              />
              <FormText
                form={form}
                disabled={disabled}
                name="source.ref"
                label={t("projects.ref")}
                required
              />
              <FormSelect
                form={form}
                disabled={disabled}
                name="source.authSecretRef"
                label={t("projects.authSecret")}
                options={gitCredentialOptions(secrets, form.watch("source.authSecretRef"), t)}
              />
            </div>
          </ApplicationConfigPanel>
          <ApplicationConfigPanel
            icon="wrench"
            title={t("projects.buildEnvSectionTitle")}
            description={t("projects.buildEnvSectionDesc")}
          >
            <div className="ops-source-grid--2">
              <FormSelect
                form={form}
                disabled={disabled}
                name="buildServerId"
                label={t("projects.buildServer")}
                options={buildServerOptions(servers, t)}
              />
              <FormText
                form={form}
                disabled={disabled}
                name="source.jenkinsfilePath"
                label={t("projects.jenkinsfilePath")}
                required
              />
            </div>

            <div className="ops-advanced-disclosure">
              <div className="ops-advanced-disclosure__bar">
                <IqujiButton
                  variant="ghost"
                  compact
                  className="ops-advanced-disclosure__trigger"
                  iconStart={
                    <IqujiIcon
                      decorative
                      name={advanced ? "chevron-down" : "chevron-right"}
                      size={14}
                    />
                  }
                  aria-expanded={advanced}
                  aria-controls="application-source-advanced"
                  onClick={() => setAdvanced(!advanced)}
                >
                  {advanced ? t("projects.advancedCollapse") : t("projects.advanced")}
                </IqujiButton>
                {!advanced ? (
                  <span className="ops-advanced-disclosure__hint">
                    {t("projects.sourcePathSummary", { path: currentSourcePath })}
                  </span>
                ) : null}
              </div>
              {advanced ? (
                <div id="application-source-advanced" className="ops-advanced-disclosure__body">
                  <FormText
                    form={form}
                    disabled={disabled}
                    name="source.sourcePath"
                    label={t("projects.sourcePath")}
                  />
                </div>
              ) : null}
            </div>
          </ApplicationConfigPanel>
          <RegistrySyncSection form={form} disabled={disabled} secrets={secrets} />
        </>
      )}
    </>
  );
}
export { ApplicationVariablesSection } from "./ApplicationVariablesSection";
