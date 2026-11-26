import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import { useI18n } from "@/shared/i18n";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import type { FrpProbeHit } from "./types";
import { copyToClipboard, formatFriendlyTime, formatRelativeTime, getMethodTone } from "./utils";

export function probeCurl(method: string, host: string, requestPath: string) {
  if (!/^[A-Za-z][A-Za-z0-9-]*$/.test(method) || !host || /[\s/@?#\\]/.test(host)) return "";
  if (!requestPath.startsWith("/") || /[\r\n\0]/.test(requestPath)) return "";
  const address = `http://${host}${requestPath}`;
  if (!URL.canParse(address)) return "";
  const quoted = address.replaceAll("'", "'\\''");
  return `curl --globoff -X ${method} -- '${quoted}'`;
}

export function TunnelProbeDetailDrawer({
  hit,
  onClose,
}: {
  hit: FrpProbeHit | null;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [copiedCurl, setCopiedCurl] = useState(false);

  const fullPath = hit ? `${hit.path}${hit.rawQuery ? `?${hit.rawQuery}` : ""}` : "";
  const curl = hit ? probeCurl(hit.method, hit.host ?? "", fullPath) : "";

  const handleCopyCurl = async () => {
    if (!curl || !(await copyToClipboard(curl))) return;
    setCopiedCurl(true);
    setTimeout(() => setCopiedCurl(false), 2000);
  };

  return (
    <InspectorDrawer
      description={hit ? formatFriendlyTime(hit.receivedAt) : undefined}
      footer={
        <IqujiButton onClick={onClose} variant="secondary">
          {t("common.close")}
        </IqujiButton>
      }
      headerMeta={
        hit ? (
          <div className="ops-probe-detail__meta">
            <IqujiBadge tone={getMethodTone(hit.method)}>{hit.method}</IqujiBadge>
            <span className="ops-probe-mapping-tag">{hit.mappingName}</span>
            <span className="ops-muted">{formatRelativeTime(hit.receivedAt, t)}</span>
          </div>
        ) : undefined
      }
      onClose={onClose}
      open={hit !== null}
      size="lg"
      title={t("tunnels.probeHitDetailsTitle")}
    >
      {hit ? (
        <div className="ops-probe-detail">
          <div className="ops-probe-detail__request">
            <span className="ops-probe-detail__request-label">{t("tunnels.probeRequestLine")}</span>
            <div className="ops-probe-detail__request-line">
              <IqujiBadge tone={getMethodTone(hit.method)}>{hit.method}</IqujiBadge>
              <code className="ops-probe-detail__request-path">{fullPath}</code>
            </div>
          </div>

          <dl className="ops-probe-detail__grid">
            <div className="ops-probe-detail__cell">
              <dt>{t("tunnels.probeReceiveTime")}</dt>
              <dd>{formatFriendlyTime(hit.receivedAt)}</dd>
            </div>
            <div className="ops-probe-detail__cell">
              <dt>{t("tunnels.probeColMapping")}</dt>
              <dd>{hit.mappingName}</dd>
            </div>
            <div className="ops-probe-detail__cell">
              <dt>{t("tunnels.probeTargetHost")}</dt>
              <dd>
                <code>{hit.host || "—"}</code>
              </dd>
            </div>
            <div className="ops-probe-detail__cell">
              <dt>{t("tunnels.probeColSource")}</dt>
              <dd>
                <code>{hit.remoteAddr}</code>
              </dd>
            </div>
            <div className="ops-probe-detail__cell ops-probe-detail__cell--full">
              <dt>User-Agent</dt>
              <dd>{hit.userAgent || "—"}</dd>
            </div>
          </dl>

          <div className="ops-probe-curl-box">
            <div className="ops-probe-curl-box__head">
              <span className="ops-probe-curl-box__title">
                <IqujiIcon decorative name="terminal" size={13} />
                cURL
              </span>
              <IqujiButton
                compact
                disabled={!curl}
                onClick={() => void handleCopyCurl()}
                variant="secondary"
              >
                <IqujiIcon decorative name={copiedCurl ? "check" : "copy"} size={13} />
                {copiedCurl ? t("tunnels.copied") : t("tunnels.probeCopyCurl")}
              </IqujiButton>
            </div>
            <pre className="ops-probe-curl-box__code">
              <code>{curl}</code>
            </pre>
          </div>
        </div>
      ) : null}
    </InspectorDrawer>
  );
}
