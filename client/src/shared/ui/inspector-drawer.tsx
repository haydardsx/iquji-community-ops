import { IqujiDrawer } from "@iquji-community/web-component";
import type { ReactNode } from "react";
import { useI18n } from "@/shared/i18n";

export function InspectorDrawer({
  actionBarLayout,
  bodyClassName,
  children,
  className,
  closeLabel,
  description,
  footer,
  headerMeta,
  headerToolbar,
  onClose,
  open,
  size = "md",
  title,
}: {
  actionBarLayout?: "end" | "full";
  bodyClassName?: string;
  children: ReactNode;
  className?: string;
  closeLabel?: string;
  description?: ReactNode;
  footer?: ReactNode;
  headerMeta?: ReactNode;
  headerToolbar?: ReactNode;
  onClose: () => void;
  open: boolean;
  size?: "md" | "lg" | "xl";
  title: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <IqujiDrawer
      actionBarLayout={actionBarLayout}
      actions={footer}
      bodyClassName={bodyClassName}
      className={className}
      closeLabel={closeLabel ?? t("common.closeDrawer")}
      description={description}
      headerMeta={headerMeta}
      headerToolbar={headerToolbar}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
      open={open}
      size={size}
      title={title}
    >
      {children}
    </IqujiDrawer>
  );
}
