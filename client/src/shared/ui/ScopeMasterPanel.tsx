import type { ReactNode } from "react";

export function ScopeMasterPanel({
  action,
  children,
  count,
  search,
  title,
}: {
  action?: ReactNode;
  children: ReactNode;
  count?: number;
  search?: ReactNode;
  title: string;
}) {
  return (
    <section aria-label={title} className="ops-master-sidebar">
      <header className="ops-master-sidebar__header">
        <div className="ops-master-sidebar__title-row">
          <h2 className="ops-master-sidebar__title">{title}</h2>
          {typeof count === "number" ? (
            <span className="ops-master-sidebar__count">{count}</span>
          ) : null}
        </div>
        {action ? <div className="ops-master-sidebar__actions">{action}</div> : null}
      </header>
      {search ? <div className="ops-master-sidebar__search">{search}</div> : null}
      <div className="ops-master-sidebar__list">{children}</div>
    </section>
  );
}
