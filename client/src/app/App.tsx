import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { IqujiMotionProvider } from "@iquji-community/web-motion";
import { IqujiQueryProvider } from "@iquji-community/web-query";
import { IqujiThemeProvider } from "@iquji-community/web-theme";
import { RouterProvider } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { IqujiLocaleProvider, opsCatalogs, useI18n } from "@/shared/i18n";
import { OperationActivityProvider } from "@/shared/operations/activity";
import { OperationTrackerProvider } from "@/shared/operations/operation-tracker";
import { router } from "./router";
import { WorkspaceInteractions } from "./WorkspaceInteractions";

function LocalizedNotificationProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  return (
    <IqujiNotificationProvider closeLabel={t("common.closeNotification")}>
      <OperationActivityProvider>
        <OperationTrackerProvider>{children}</OperationTrackerProvider>
      </OperationActivityProvider>
    </IqujiNotificationProvider>
  );
}

export function App() {
  return (
    <IqujiThemeProvider defaultMode="system" density="compact" storageKey="iquops.theme">
      <IqujiLocaleProvider
        catalogs={opsCatalogs}
        defaultLocale="zh-CN"
        storageKey="iquops.language"
      >
        <IqujiQueryProvider>
          <IqujiMotionProvider>
            <IqujiOverlayProvider>
              <LocalizedNotificationProvider>
                <WorkspaceInteractions />
                <RouterProvider router={router} />
              </LocalizedNotificationProvider>
            </IqujiOverlayProvider>
          </IqujiMotionProvider>
        </IqujiQueryProvider>
      </IqujiLocaleProvider>
    </IqujiThemeProvider>
  );
}
