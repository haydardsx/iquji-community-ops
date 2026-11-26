import type { ReactNode } from "react";

export type InfoListItem = {
  id: string;
  label: ReactNode;
  value: ReactNode;
};

export function InfoList({ items }: { items: InfoListItem[] }) {
  return (
    <dl className="ops-info-list">
      {items.map((item) => (
        <div className="ops-info-list__row" key={item.id}>
          <dt>{item.label}</dt>
          <dd>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}
