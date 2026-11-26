import { IqujiPanel } from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import type { ReactNode } from "react";
import "./section-panel.css";

export interface SectionPanelProps {
  icon?: IqujiIconName;
  iconTone?: "accent" | "neutral" | "positive" | "warning";
  title: ReactNode;
  description?: ReactNode;
  badge?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  ariaLabel?: string;
}

export function SectionPanel({
  icon,
  iconTone = "accent",
  title,
  description,
  badge,
  actions,
  children,
  className = "",
  bodyClassName = "",
  ariaLabel,
}: SectionPanelProps) {
  const accessibleLabel = ariaLabel ?? (typeof title === "string" ? title : undefined);
  return (
    <IqujiPanel
      aria-label={accessibleLabel}
      className={`ops-section-panel ${className}`}
      header={
        <div className="ops-section-panel__header">
          <div className="ops-section-panel__title-group">
            {icon ? (
              <div className="ops-section-panel__icon" data-tone={iconTone}>
                <IqujiIcon decorative name={icon} size={18} />
              </div>
            ) : null}
            <div className="ops-section-panel__copy">
              <div className="ops-section-panel__headline">
                <h3 className="ops-section-panel__title">{title}</h3>
                {badge ? <div className="ops-section-panel__badge">{badge}</div> : null}
              </div>
              {description ? <p className="ops-section-panel__description">{description}</p> : null}
            </div>
          </div>
          {actions ? <div className="ops-section-panel__actions">{actions}</div> : null}
        </div>
      }
    >
      <div className={`ops-section-panel__body ${bodyClassName}`}>{children}</div>
    </IqujiPanel>
  );
}
