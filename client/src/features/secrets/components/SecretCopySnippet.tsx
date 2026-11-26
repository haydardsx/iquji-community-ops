import { IconButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useState } from "react";
import { useI18n } from "@/shared/i18n";

export function SecretCopySnippet({
  value,
  displayValue,
  label,
  className = "",
}: {
  value: string;
  displayValue?: string;
  label?: string;
  className?: string;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);

  const handleCopy = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (!value) return;
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 忽略无法写入剪贴板的静默异常
    }
  };

  const displayText = displayValue ?? value;
  const copyLabel = copied
    ? t("common.copied")
    : label
      ? t("common.copyTarget", { target: label })
      : t("common.copyContent");

  return (
    <div
      className={`ops-secret-snippet ${className}`}
      title={label ? `${label}：${displayText}` : displayText}
    >
      <code className="ops-secret-snippet__code">{displayText || "—"}</code>
      {value ? (
        <IconButton
          aria-label={copyLabel}
          className={`ops-secret-snippet__copy-btn ${copied ? "is-copied" : ""}`}
          compact
          icon={<IqujiIcon decorative name={copied ? "check" : "copy"} size={13} />}
          label={copyLabel}
          onClick={handleCopy}
          variant="ghost"
        />
      ) : null}
    </div>
  );
}
