import {
  IqujiButton,
  IqujiDiagnosticBanner,
  IqujiDialog,
  IqujiField,
  IqujiSegmentedControl,
  IqujiTextarea,
} from "@iquji-community/web-component";
import { useEffect, useState } from "react";
import { useI18n } from "@/shared/i18n";
import {
  type ActionScopes,
  type Command,
  isActionScopeValid,
  type Mode,
  overlappingTargets,
  type Scope,
} from "./automation-permissions";

type McpScopeDialogProps = {
  command: Command | null;
  scopes: ActionScopes | undefined;
  disabled: boolean;
  onClose: () => void;
  onApply: (next: ActionScopes) => void;
};

function ModeScopeEditor({
  command,
  mode,
  scope,
  disabled,
  onChange,
}: {
  command: Command;
  mode: Mode;
  scope: Scope;
  disabled: boolean;
  onChange: (patch: Partial<Scope>) => void;
}) {
  const { t } = useI18n();
  const sectionTitle = command.guide
    ? t(mode === "guide" ? "automation.guideScope" : "automation.directScope")
    : t("automation.scope");

  return (
    <section className="ops-form-section">
      <div className="ops-form-section__header">
        <h4 className="ops-form-section__title">{sectionTitle}</h4>
      </div>
      <div className="ops-form-stack">
        <IqujiSegmentedControl
          disabled={disabled}
          label={sectionTitle}
          onChange={(next) => onChange({ limited: next === "limited" })}
          options={[
            { value: "all", label: t("automation.allTargets") },
            { value: "limited", label: t("automation.specificTargets") },
          ]}
          value={scope.limited ? "limited" : "all"}
        />

        {scope.limited ? (
          <IqujiField
            description={t("automation.targetListHint")}
            label={t("automation.targetList")}
            required
          >
            <IqujiTextarea
              className="ops-font-mono"
              disabled={disabled}
              onChange={(e) => onChange({ targets: e.currentTarget.value })}
              rows={2}
              value={scope.targets}
            />
          </IqujiField>
        ) : null}

        {mode === "direct" && command.confirm ? (
          <IqujiField
            description={t("automation.approvedHint")}
            label={t("automation.approvedTargets")}
          >
            <IqujiTextarea
              className="ops-font-mono"
              disabled={disabled}
              onChange={(e) => onChange({ exceptions: e.currentTarget.value })}
              rows={2}
              value={scope.exceptions}
            />
          </IqujiField>
        ) : null}
      </div>
    </section>
  );
}

/** 命令目标范围与免确认规则弹窗 */
export function McpScopeDialog({
  command,
  scopes,
  disabled,
  onClose,
  onApply,
}: McpScopeDialogProps) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<ActionScopes>(() => ({
    ...(scopes?.guide ? { guide: { ...scopes.guide } } : {}),
    ...(scopes?.direct ? { direct: { ...scopes.direct } } : {}),
  }));

  useEffect(() => {
    if (!command) return;
    setDraft({
      ...(scopes?.guide ? { guide: { ...scopes.guide } } : {}),
      ...(scopes?.direct ? { direct: { ...scopes.direct } } : {}),
    });
  }, [command, scopes]);

  if (!command) return null;

  const overlap = overlappingTargets(draft);
  const valid = isActionScopeValid(draft);

  return (
    <IqujiDialog
      actions={
        <>
          <IqujiButton onClick={onClose} variant="secondary">
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton
            disabled={disabled || !valid}
            onClick={() => {
              onApply(draft);
              onClose();
            }}
            variant="primary"
          >
            {t("common.confirm")}
          </IqujiButton>
        </>
      }
      className="ops-mcp-scope-modal"
      closeLabel={t("common.close")}
      destroyOnClose
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      open
      size="md"
      title={t("automation.scopeDialogTitle", { name: t(`automation.label.${command.name}`) })}
    >
      <div className="ops-form-stack ops-mcp-scope-dialog">
        {!valid ? (
          <IqujiDiagnosticBanner compact title={t("automation.invalidScope")} tone="warning" />
        ) : null}

        {overlap === "all" || overlap.length > 0 ? (
          <IqujiDiagnosticBanner
            compact
            title={
              overlap === "all"
                ? t("automation.directCoversAll")
                : t("automation.directOverlap", { targets: overlap.join(", ") })
            }
            tone="warning"
          />
        ) : null}

        {(["guide", "direct"] as const).map((mode) => {
          const scope = draft[mode];
          if (!scope) return null;
          return (
            <ModeScopeEditor
              command={command}
              disabled={disabled}
              key={mode}
              mode={mode}
              onChange={(patch) =>
                setDraft((prev) => ({
                  ...prev,
                  [mode]: { ...scope, ...patch },
                }))
              }
              scope={scope}
            />
          );
        })}
      </div>
    </IqujiDialog>
  );
}
