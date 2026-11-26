import { IqujiIcon } from "@iquji-community/web-icons";
import { useI18n } from "@/shared/i18n";
import { RecordSnippet } from "@/shared/ui/record-snippet";
import type { ApplicationBuild } from "./build-api";
import "./ApplicationBuildArtifacts.css";

export function ApplicationBuildArtifacts({ artifacts }: Pick<ApplicationBuild, "artifacts">) {
  const { t } = useI18n();
  if (!artifacts?.length) return null;

  return (
    <div className="ops-build-artifacts">
      {artifacts.map((artifact) => {
        const digests = [
          ["projects.registryRepository", artifact.registryImage, "container"],
          ["projects.registryDigest", artifact.registryDigest, "code"],
          ["projects.baseImage", artifact.baseImageDigest, "code"],
          ["projects.imageDigest", artifact.imageDigest, "container"],
          ["projects.configDigest", artifact.configDigest, "code"],
          ["projects.manifestDigest", artifact.manifestDigest, "code"],
        ] as const;

        return (
          <div key={artifact.serviceSlotKey} className="ops-build-artifact-card">
            <div className="ops-build-artifact-card__head">
              <div className="ops-build-artifact-card__slot">
                <IqujiIcon decorative name="boxes" size={14} />
                <span className="ops-build-artifact-card__slot-title">
                  {t("projects.serviceSlot")}:
                </span>
                <strong className="ops-build-artifact-card__slot-name">
                  {artifact.serviceSlotKey}
                </strong>
              </div>
              {artifact.platform && (
                <span className="ops-build-artifact-card__platform">
                  <IqujiIcon decorative name="globe" size={12} />
                  {artifact.platform}
                </span>
              )}
            </div>

            <div className="ops-build-artifact-grid">
              <div className="ops-build-artifact-item ops-build-artifact-item--full">
                <span className="ops-build-artifact-label">
                  <IqujiIcon decorative name="container" size={13} />
                  {t("projects.imageReference")}
                </span>
                <div className="ops-build-artifact-value">
                  <RecordSnippet
                    value={artifact.image}
                    label={t("projects.imageReference")}
                    truncateLength={48}
                  />
                </div>
              </div>

              {artifact.serverId && (
                <div className="ops-build-artifact-item">
                  <span className="ops-build-artifact-label">
                    <IqujiIcon decorative name="server" size={13} />
                    {t("projects.artifactNode")}
                  </span>
                  <div className="ops-build-artifact-value">
                    <RecordSnippet
                      value={artifact.serverId}
                      label={t("projects.artifactNode")}
                      truncateLength={18}
                    />
                  </div>
                </div>
              )}

              {artifact.packageName && (
                <div className="ops-build-artifact-item">
                  <span className="ops-build-artifact-label">
                    <IqujiIcon decorative name="boxes" size={13} />
                    {t("projects.packageName")}
                  </span>
                  <div className="ops-build-artifact-value ops-build-artifact-text">
                    {artifact.packageName} {artifact.packageVersion}
                  </div>
                </div>
              )}

              {digests.map(
                ([label, value, icon]) =>
                  value && (
                    <div key={label} className="ops-build-artifact-item">
                      <span className="ops-build-artifact-label">
                        <IqujiIcon decorative name={icon} size={13} />
                        {t(label)}
                      </span>
                      <div className="ops-build-artifact-value">
                        <RecordSnippet value={value} label={t(label)} truncateLength={16} />
                      </div>
                    </div>
                  ),
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
