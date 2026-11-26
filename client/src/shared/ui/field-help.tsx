import {
  IconButton,
  IqujiField,
  type IqujiFieldProps,
  IqujiPopover,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useI18n } from "@/shared/i18n";
import "./field-help.css";

type HelpProps = { label: string; help: string };

export function FieldHelp({ label, help }: HelpProps) {
  const { t } = useI18n();
  return (
    <IqujiPopover
      align="start"
      collisionPadding={12}
      popupClassName="ops-field-help-popup"
      trigger={
        <IconButton
          compact
          className="ops-field-help-trigger"
          icon={<IqujiIcon name="circle-help" size={14} decorative />}
          label={t("common.fieldHelp", { label })}
        />
      }
    >
      {help}
    </IqujiPopover>
  );
}

export function SettingFieldLabel({ label, help }: HelpProps) {
  return (
    <span className="ops-setting-row__label ops-label-with-help">
      {label}
      <FieldHelp label={label} help={help} />
    </span>
  );
}

export function FieldWithHelp({
  label,
  help,
  ...props
}: Omit<IqujiFieldProps, "description" | "label" | "className"> & {
  label: string;
  help?: string;
}) {
  if (!help) return <IqujiField {...props} label={label} />;
  return (
    <div className="ops-field-with-help">
      <FieldHelp label={label} help={help} />
      <IqujiField {...props} label={label} description={help} />
    </div>
  );
}
