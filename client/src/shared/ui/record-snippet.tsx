import { IconButton } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { type MouseEvent, useState } from "react";
import { useI18n } from "@/shared/i18n";

export function RecordSnippet({
  value,
  displayValue,
  label,
  className = "",
  truncateLength = 18,
}: {
  value?: string;
  displayValue?: string;
  label?: string;
  className?: string;
  truncateLength?: number;
}) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);

  if (!value) {
    return <span className="ops-muted ops-text-caption">—</span>;
  }

  const handleCopy = async (e: MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // 忽略无法写入剪贴板的静默异常
    }
  };

  const displayText =
    displayValue ??
    (value.length > truncateLength
      ? `${value.slice(0, Math.max(6, truncateLength - 4))}…${value.slice(-4)}`
      : value);

  const copyLabel = copied
    ? t("common.copied")
    : label
      ? t("common.copyTarget", { target: label })
      : t("common.copyContent");

  return (
    <span
      className={`ops-record-snippet ${className}`}
      title={label ? `${label}：${value}` : value}
    >
      <code className="ops-record-snippet__code">{displayText}</code>
      <IconButton
        aria-label={copyLabel}
        className={`ops-record-snippet__copy-btn ${copied ? "is-copied" : ""}`}
        compact
        icon={<IqujiIcon decorative name={copied ? "check" : "copy"} size={11} />}
        label={copyLabel}
        onClick={handleCopy}
        variant="ghost"
      />
    </span>
  );
}
