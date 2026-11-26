import { IqujiButton, IqujiPanel } from "@iquji-community/web-component";
import { IqujiTextEditor } from "@iquji-community/web-editor/text";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import { useI18n } from "@/shared/i18n";
import type { FrpTunnelDetail } from "./types";
import { copyToClipboard, FRPC_RUN_COMMAND, generateFrpcConfig } from "./utils";

export function TunnelConfigTab({
  httpPort,
  httpsPort,
  serverHost,
  tunnel,
}: {
  tunnel: FrpTunnelDetail;
  serverHost: string;
  httpPort: number;
  httpsPort: number;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState<"config" | "command" | null>(null);
  const config = generateFrpcConfig(tunnel, { httpPort, httpsPort, serverHost });

  const copy = async (value: string, kind: "config" | "command") => {
    if (!(await copyToClipboard(value))) return;
    setCopied(kind);
    setTimeout(() => setCopied(null), 2000);
  };

  return (
    <IqujiPanel
      header={
        <div className="ops-surface__header">
          <div className="ops-surface__copy">
            <h2 className="ops-panel-title">{t("tunnels.configRefTitle")}</h2>
            <p className="ops-surface__description">{t("tunnels.configRefDesc")}</p>
          </div>
          <div className="ops-surface__actions">
            <IqujiButton compact onClick={() => void copy(config, "config")} variant="secondary">
              <IqujiIcon decorative name="copy" />
              {copied === "config" ? t("tunnels.copiedConfig") : t("tunnels.copyConfig")}
            </IqujiButton>
          </div>
        </div>
      }
    >
      <div className="ops-stack">
        <div className="ops-tunnel-config-editor">
          <IqujiTextEditor
            ariaLabel={t("tunnels.configEditorAria")}
            labels={{
              edit: t("tunnels.editorEdit"),
              format: t("tunnels.editorFormat"),
              invalid: t("tunnels.editorInvalid"),
              preview: t("tunnels.editorPreview"),
            }}
            onChange={() => undefined}
            preset="text"
            readOnly
            value={config}
          />
        </div>
        <div className="ops-tunnel-probe-status">
          <span className="ops-tunnel-config-note">{t("tunnels.localStartCmd")}</span>
          <code className="ops-tunnel-mapping__target">{FRPC_RUN_COMMAND}</code>
          <IqujiButton
            compact
            onClick={() => void copy(FRPC_RUN_COMMAND, "command")}
            variant="ghost"
          >
            <IqujiIcon decorative name="copy" />
            {copied === "command" ? t("tunnels.copied") : t("tunnels.copyCmd")}
          </IqujiButton>
        </div>
        <p className="ops-tunnel-config-note">{t("tunnels.manualDebugNotice")}</p>
      </div>
    </IqujiPanel>
  );
}
