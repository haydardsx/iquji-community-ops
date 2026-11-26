import { IqujiBadge, IqujiButton } from "@iquji-community/web-component";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import type { ReactNode } from "react";

export function SelectionCard({
  active = false,
  description,
  icon,
  meta,
  onClick,
  status,
  statusTone = "neutral",
  tags,
  title,
  trailing,
}: {
  active?: boolean;
  description?: ReactNode;
  icon?: IqujiIconName;
  meta?: ReactNode;
  onClick: () => void;
  status?: string;
  statusTone?: "neutral" | "info" | "success" | "warning" | "danger";
  tags?: string[];
  title: ReactNode;
  trailing?: ReactNode;
}) {
  return (
    <IqujiButton
      className="ops-selection-card"
      data-active={active || undefined}
      onClick={onClick}
      variant="ghost"
    >
      <div className="ops-selection-card__top-row">
        <div className="ops-selection-card__title-group">
          {icon ? (
            <span className="ops-selection-card__icon">
              <IqujiIcon decorative name={icon} />
            </span>
          ) : null}
          <span className="ops-selection-card__title">{title}</span>
        </div>
        <div className="ops-selection-card__status-group">
          {status ? <IqujiBadge tone={statusTone}>{status}</IqujiBadge> : null}
          {trailing}
        </div>
      </div>

      {description ? <div className="ops-selection-card__description">{description}</div> : null}

      {tags && tags.length > 0 ? (
        <div className="ops-selection-card__tags">
          {tags.map((tag) => (
            <span className="ops-selection-card__tag" key={tag}>
              {tag}
            </span>
          ))}
        </div>
      ) : null}

      {meta ? <div className="ops-selection-card__meta">{meta}</div> : null}
    </IqujiButton>
  );
}
