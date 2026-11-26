import { IqujiConfirmDialog, useIqujiNotification } from "@iquji-community/web-component";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  listenToWorkspace,
  reloadWorkspace,
  workspaceHasDrafts,
} from "@/shared/desktop/workspace-interactions";
import { useI18n } from "@/shared/i18n";

export function WorkspaceInteractions() {
  const { t } = useI18n();
  const notification = useIqujiNotification();
  const [pending, setPending] = useState<boolean | null>(null);
  const busy = useRef(false);
  const reload = useCallback(
    async (ignoreCache: boolean, discardDrafts = false) => {
      if (busy.current) return;
      busy.current = true;
      try {
        await reloadWorkspace(ignoreCache, discardDrafts);
      } catch {
        notification.open({ title: t("shell.reloadFailed"), tone: "danger", duration: 0 });
      } finally {
        busy.current = false;
      }
    },
    [notification, t],
  );
  useEffect(
    () =>
      listenToWorkspace((ignoreCache) => {
        if (workspaceHasDrafts()) setPending(ignoreCache);
        else void reload(ignoreCache);
      }),
    [reload],
  );
  return (
    <IqujiConfirmDialog
      open={pending !== null}
      title={t("common.discardConfirmTitle")}
      description={t("common.discardConfirmDesc")}
      closeLabel={t("common.close")}
      cancelLabel={t("common.continueEditing")}
      confirmLabel={t("shell.discardAndReload")}
      onOpenChange={(open) => {
        if (!open) setPending(null);
      }}
      onConfirm={() => {
        if (pending !== null) void reload(pending, true);
        setPending(null);
      }}
    />
  );
}
