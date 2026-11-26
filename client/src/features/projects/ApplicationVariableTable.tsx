import { IqujiButton, IqujiEmptyState, IqujiPopover } from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import type { ReactNode } from "react";
import type { SecretAsset } from "@/contracts/types";
import { useI18n } from "@/shared/i18n";
import { SectionPanel } from "@/shared/ui/section-panel";
import "./ApplicationVariableTable.css";

export function applicationCredentialOptions(
  secrets: SecretAsset[],
  purpose: "application_secret" | "registry_secret" = "application_secret",
) {
  return secrets.flatMap((asset) =>
    asset.assetType === purpose && asset.materialStatus === "available" && asset.secretRef
      ? [{ value: asset.secretRef, label: asset.name }]
      : [],
  );
}

export function variableReferenceOptions(
  options: { value: string; label: string }[],
  current: string | undefined,
  unavailableLabel: string,
) {
  return current && !options.some((option) => option.value === current)
    ? [...options, { value: current, label: unavailableLabel, disabled: true }]
    : options;
}

export function ApplicationVariableTable({
  title,
  actionLabel,
  onAdd,
  disabled,
  columns,
  count,
  emptyTitle,
  description,
  guide,
  children,
}: {
  title: string;
  actionLabel: string;
  onAdd: () => void;
  disabled: boolean;
  columns: string[];
  count: number;
  emptyTitle: string;
  description: string;
  guide?: ReactNode;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <SectionPanel
      className="ops-variable-settings"
      icon="code"
      title={title}
      description={description}
      badge={count > 0 ? <span className="ops-variables-count-badge">{count}</span> : null}
      actions={
        <div className="ops-variable-table__actions">
          {guide ? (
            <IqujiPopover
              side="bottom"
              align="end"
              sideOffset={8}
              collisionPadding={12}
              popupClassName="ops-variable-guide-popover"
              trigger={
                <IqujiButton
                  compact
                  variant="ghost"
                  className="ops-variable-table__guide-btn"
                  iconStart={<IqujiIcon decorative name="circle-info" size={14} />}
                >
                  {t("apps.varGuide")}
                </IqujiButton>
              }
            >
              {guide}
            </IqujiPopover>
          ) : null}
          <IqujiButton
            compact
            variant="secondary"
            iconStart={<IqujiIcon decorative name="plus" size={14} />}
            disabled={disabled}
            onClick={onAdd}
          >
            {actionLabel}
          </IqujiButton>
        </div>
      }
    >
      {count === 0 ? (
        <IqujiEmptyState title={emptyTitle} body={description} />
      ) : (
        <div className="ops-variables-table" data-columns={columns.length}>
          <div className="ops-variables-table__header" aria-hidden="true">
            {columns.map((label) => (
              <span key={label}>{label}</span>
            ))}
          </div>
          {children}
        </div>
      )}
    </SectionPanel>
  );
}

export function RemoveVariableButton({
  disabled,
  onClick,
  label,
}: {
  disabled: boolean;
  onClick: () => void;
  label: string;
}) {
  const { t } = useI18n();
  return (
    <IqujiButton
      className="ops-variable-remove"
      compact
      variant="ghost"
      disabled={disabled}
      aria-label={label}
      title={t("common.remove")}
      onClick={onClick}
    >
      <IqujiIcon decorative name="trash" size={14} />
    </IqujiButton>
  );
}
