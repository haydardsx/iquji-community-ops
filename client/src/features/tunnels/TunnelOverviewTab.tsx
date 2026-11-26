import {
  IqujiBadge,
  IqujiButton,
  IqujiEmptyState,
  useIqujiNotification,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import { openExternalUrl } from "@/shared/desktop/external-url";
import { useI18n } from "@/shared/i18n";
import { SurfacePanel } from "@/shared/ui/surface-panel";
import type { FrpTunnelDetail } from "./types";
import { copyToClipboard } from "./utils";

export function TunnelOverviewTab({
  disabled,
  onEdit,
  tunnel,
}: {
  tunnel: FrpTunnelDetail;
  disabled: boolean;
  onEdit: () => void;
}) {
  const { t } = useI18n();
  const notify = useIqujiNotification();
  const [copied, setCopied] = useState<string | null>(null);
  const mappings = tunnel.mappings ?? [];

  const copy = async (url: string, name: string) => {
    if (!(await copyToClipboard(url))) return;
    setCopied(name);
    setTimeout(() => setCopied(null), 2000);
  };

  const open = async (url: string) => {
    const ok = await openExternalUrl(url);
    if (!ok) {
      notify.open({
        title: t("tunnels.openFailed"),
        tone: "danger",
      });
    }
  };

  return (
    <SurfacePanel title={t("tunnels.overviewTitle")}>
      {mappings.length === 0 ? (
        <IqujiEmptyState
          action={
            disabled ? undefined : (
              <IqujiButton onClick={onEdit} variant="primary">
                {t("tunnels.configMappings")}
              </IqujiButton>
            )
          }
          body={t("tunnels.overviewEmptyBody")}
          title={t("tunnels.overviewEmptyTitle")}
        />
      ) : (
        <div className="ops-tunnel-mappings">
          {mappings.map((mapping) => {
            const url = mapping.publicUrl ?? "";
            const probe = mapping.targetType === "http_probe";
            return (
              <article className="ops-tunnel-mapping" key={mapping.name}>
                <div className="ops-tunnel-mapping__main">
                  <div className="ops-tunnel-mapping__title">
                    <span className="ops-tunnel-mapping__name">{mapping.name}</span>
                    <IqujiBadge tone={probe ? "warning" : "info"}>
                      {probe ? t("tunnels.typeHttpProbe") : t("tunnels.typeLocalService")}
                    </IqujiBadge>
                    <IqujiBadge tone={mapping.publicScheme === "https" ? "success" : "neutral"}>
                      {(mapping.publicScheme || "http").toUpperCase()}
                    </IqujiBadge>
                    <span className="ops-tunnel-mapping__target">
                      {probe
                        ? t("tunnels.builtInProbeTarget")
                        : `${mapping.localHost || "127.0.0.1"}:${mapping.localPort}`}
                    </span>
                  </div>

                  {url ? (
                    <div className="ops-tunnel-mapping__url-row">
                      <IqujiIcon decorative name="link" size={12} />
                      <a
                        className="ops-tunnel-mapping__url"
                        href={url}
                        onClick={(event) => {
                          event.preventDefault();
                          void open(url);
                        }}
                        rel="noopener noreferrer"
                        target="_blank"
                      >
                        {url}
                      </a>
                    </div>
                  ) : (
                    <span className="ops-muted" style={{ fontSize: "12px" }}>
                      {t("tunnels.ingressPendingStart")}
                    </span>
                  )}
                </div>

                {url ? (
                  <div className="ops-tunnel-mapping__actions">
                    <IqujiButton
                      compact
                      onClick={() => void copy(url, mapping.name)}
                      variant="secondary"
                    >
                      <IqujiIcon
                        decorative
                        name={copied === mapping.name ? "check" : "copy"}
                        size={13}
                      />
                      {copied === mapping.name ? t("common.copied") : t("common.copy")}
                    </IqujiButton>
                    <IqujiButton compact onClick={() => void open(url)} variant="ghost">
                      <IqujiIcon decorative name="external-link" size={13} />
                      {t("tunnels.open")}
                    </IqujiButton>
                  </div>
                ) : null}
              </article>
            );
          })}
        </div>
      )}
    </SurfacePanel>
  );
}
