import { IqujiBadge } from "@iquji-community/web-component";
import { useI18n } from "@/shared/i18n";

export function ServerFrpsIdleDot() {
  const { t } = useI18n();
  const label = t("servers.frpsIdle");
  return <IqujiBadge aria-label={label} role="img" title={label} tone="danger" variant="dot" />;
}
