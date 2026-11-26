import type { IqujiIconName } from "@iquji-community/web-icons";
import type { ReactNode } from "react";
import { SectionPanel } from "@/shared/ui/section-panel";
import "./ApplicationSourceSection.css";

export function ApplicationConfigPanel({
  icon,
  title,
  description,
  children,
}: {
  icon: IqujiIconName;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <SectionPanel
      icon={icon}
      title={title}
      description={description}
      bodyClassName="ops-config-panel__body"
    >
      {children}
    </SectionPanel>
  );
}
