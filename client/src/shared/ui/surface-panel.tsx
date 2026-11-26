import { IqujiPanel } from "@iquji-community/web-component";
import type { ReactNode } from "react";

export function SurfacePanel({
  actions,
  children,
  description,
  title,
  tone,
}: {
  actions?: ReactNode;
  children: ReactNode;
  description?: ReactNode;
  title: ReactNode;
  tone?: "muted" | "warning" | "danger";
}) {
  return (
    <IqujiPanel
      className="ops-surface"
      data-tone={tone}
      header={
        <div className="ops-surface__header">
          <div className="ops-surface__copy">
            <h2 className="ops-surface__title">{title}</h2>
            {description ? <p className="ops-surface__description">{description}</p> : null}
          </div>
          {actions ? <div className="ops-surface__actions">{actions}</div> : null}
        </div>
      }
    >
      <div className="ops-surface__body">{children}</div>
    </IqujiPanel>
  );
}
