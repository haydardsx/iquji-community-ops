import type { ReactNode } from "react";

export function StatCard({
  helper,
  icon,
  label,
  testId,
  value,
}: {
  helper: ReactNode;
  icon?: ReactNode;
  label: string;
  testId?: string;
  value: ReactNode;
}) {
  return (
    <div className="ops-stat" data-testid={testId}>
      <div className="ops-stat__header">
        <span className="ops-stat__label">{label}</span>
        {icon ? <span className="ops-stat__icon">{icon}</span> : null}
      </div>
      <strong className="ops-stat__value">{value}</strong>
      <span className="ops-stat__helper">{helper}</span>
    </div>
  );
}
