import { IqujiButton, IqujiDiagnosticBanner, IqujiDialog } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import { useI18n } from "@/shared/i18n";
import { JsonInspector } from "@/shared/ui/json-inspector";

type McpConfigDialogProps = {
  clientName: string;
  config: unknown | null;
  onClose: () => void;
};

/** 新建 MCP 客户端后的一次性连接配置弹窗 */
export function McpConfigDialog({ clientName, config, onClose }: McpConfigDialogProps) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);

  const handleClose = () => {
    setCopied(false);
    onClose();
  };

  const handleCopy = async () => {
    if (!config) return;
    try {
      await navigator.clipboard.writeText(JSON.stringify(config, null, 2));
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // 忽略剪贴板权限受限时的异常
    }
  };

  return (
    <IqujiDialog
      actions={
        <>
          <IqujiButton onClick={handleClose} variant="secondary">
            {t("common.close")}
          </IqujiButton>
          <IqujiButton
            iconStart={<IqujiIcon decorative name={copied ? "check" : "copy"} size={14} />}
            onClick={() => void handleCopy()}
            variant="primary"
          >
            {t(copied ? "automation.copied" : "automation.copy")}
          </IqujiButton>
        </>
      }
      closeLabel={t("common.close")}
      description={clientName}
      destroyOnClose
      onOpenChange={(open) => {
        if (!open) handleClose();
      }}
      open={config !== null}
      size="md"
      title={t("automation.configDialogTitle")}
    >
      <div className="ops-form-stack">
        <IqujiDiagnosticBanner compact title={t("automation.configGeneratedTip")} tone="warning" />
        {config !== null ? (
          <div className="ops-mcp-config-editor">
            <JsonInspector label={t("automation.config")} value={config} />
          </div>
        ) : null}
      </div>
    </IqujiDialog>
  );
}
