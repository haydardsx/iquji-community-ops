import { useIqujiNotification } from "@iquji-community/web-component";
import { useCallback, useEffect, useRef } from "react";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";

export function useOperationNotice(title?: string, tone: "info" | "success" = "success") {
  const notification = useIqujiNotification();
  const { t } = useI18n();
  const effectiveTitle = title ?? t("common.opCompleted");

  return useCallback(
    (message: string | null) => {
      if (message) notification.open({ title: effectiveTitle, description: message, tone });
    },
    [notification, effectiveTitle, tone],
  );
}

export function useOperationError(error: unknown, title: string) {
  const notification = useIqujiNotification();
  const { t } = useI18n();
  const shown = useRef<unknown>(null);
  useEffect(() => {
    if (!error) {
      shown.current = null;
      return;
    }
    if (shown.current === error) return;
    shown.current = error;
    notification.open({
      title,
      description: errorMessage(error, t),
      tone: "danger",
      duration: 0,
    });
  }, [error, title, notification, t]);
}
