import { IqujiCheckbox, IqujiDiagnosticBanner } from "@iquji-community/web-component";
import { useI18n } from "@/shared/i18n";
import type { BackupDependency, BackupScope } from "./api";
import {
  formatDependencySummary,
  formatDiagnosticCodes,
  formatRecordCountLabel,
  formatScopeLabel,
  nonZeroCountEntries,
} from "./backup-maintenance-format";

/** 统一渲染预检返回的阻断与警告横幅 */
export function BackupDiagnosticBanners({
  blockers,
  warnings,
}: {
  blockers: string[];
  warnings: string[];
}) {
  const { t } = useI18n();
  const blockerMessages = formatDiagnosticCodes(blockers, t);
  const warningMessages = formatDiagnosticCodes(warnings, t);

  if (blockerMessages.length === 0 && warningMessages.length === 0) return null;

  return (
    <>
      {blockerMessages.map((msg) => (
        <IqujiDiagnosticBanner compact key={msg} title={msg} tone="danger" />
      ))}
      {warningMessages.map((msg) => (
        <IqujiDiagnosticBanner compact key={msg} title={msg} tone="warning" />
      ))}
    </>
  );
}

/** 业务域复选栅格（用于导出与恢复抽屉） */
export function BackupScopeGrid({
  scopes,
  selected,
  disabled = false,
  onToggle,
}: {
  scopes: readonly BackupScope[];
  selected: readonly BackupScope[];
  disabled?: boolean;
  onToggle: (scope: BackupScope, checked: boolean) => void;
}) {
  const { t } = useI18n();
  return (
    <div className="ops-bm-scope-grid">
      {scopes.map((scope) => (
        <IqujiCheckbox
          checked={selected.includes(scope)}
          disabled={disabled}
          key={scope}
          label={formatScopeLabel(scope, t)}
          onChange={(event) => onToggle(scope, event.target.checked)}
        />
      ))}
    </div>
  );
}

/** 依赖补齐说明平整列表 */
export function BackupDependencyList({
  dependencies,
  title,
}: {
  dependencies: readonly BackupDependency[];
  title: string;
}) {
  const { t } = useI18n();
  if (dependencies.length === 0) return null;

  return (
    <div className="ops-form-stack">
      <p className="ops-form-section__description">{title}</p>
      <ul className="ops-flat-list ops-bm-plain-list">
        {dependencies.map((dep) => (
          <li
            className="ops-flat-row ops-text-body"
            key={`${dep.requiredByScope}-${dep.scope}-${dep.resource.kind}-${dep.resource.id}-${dep.reason}`}
          >
            <span>{formatDependencySummary(dep, t)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** 实体数量统计平整键值列表 */
export function BackupRecordCounts({
  counts,
  emptyLabel,
  title,
}: {
  counts: Record<string, number>;
  emptyLabel?: string;
  title?: string;
}) {
  const { t } = useI18n();
  const items = nonZeroCountEntries(counts);

  if (items.length === 0) {
    return emptyLabel ? <p className="ops-muted">{emptyLabel}</p> : null;
  }

  return (
    <div className="ops-form-stack">
      {title ? <p className="ops-form-section__description">{title}</p> : null}
      <dl className="ops-bm-kv-list">
        {items.map(([key, value]) => (
          <div className="ops-bm-kv-row" key={key}>
            <dt className="ops-bm-kv-term">{formatRecordCountLabel(key, t)}</dt>
            <dd className="ops-bm-kv-val ops-font-mono">{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}
