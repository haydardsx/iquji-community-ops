import { IqujiPageHeader } from "@iquji-community/web-component";
import type { ReactNode } from "react";

export function AdminPageFrame({
  actions,
  children,
  context,
  description,
  eyebrow,
  title,
}: {
  actions?: ReactNode;
  children: ReactNode;
  context?: ReactNode;
  description?: string;
  eyebrow?: string;
  title: string;
}) {
  return (
    <div className="ops-page">
      <IqujiPageHeader
        actions={actions}
        description={description}
        eyebrow={eyebrow}
        title={title}
      />
      {context ? <div className="ops-page__context">{context}</div> : null}
      {children}
    </div>
  );
}
