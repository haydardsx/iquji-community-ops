import {
  IqujiButton,
  IqujiSettingsActionRow,
  IqujiSettingsContent,
  IqujiSettingsSection,
} from "@iquji-community/web-component";
import { useState } from "react";
import { useI18n } from "@/shared/i18n";
import type { DataMaintenanceAction } from "./api";
import { BackupExportDrawer } from "./BackupExportDrawer";
import { BackupRestoreDrawer } from "./BackupRestoreDrawer";
import { DataMaintenanceDialog } from "./DataMaintenanceDialog";

/** 设置 → 备份与数据维护：主界面保持简洁操作入口，具体流程由抽屉与弹窗承担 */
export function BackupMaintenanceSection() {
  const { t } = useI18n();
  const [exportOpen, setExportOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const [maintenanceAction, setMaintenanceAction] = useState<DataMaintenanceAction | null>(null);

  return (
    <IqujiSettingsContent
      description={t("settings.backupMaintenanceDesc")}
      title={t("settings.backupMaintenance")}
    >
      <IqujiSettingsSection title={t("backup.sectionTitle")}>
        <IqujiSettingsActionRow
          action={
            <IqujiButton compact onClick={() => setExportOpen(true)} variant="secondary">
              {t("backup.openExportDrawer")}
            </IqujiButton>
          }
          description={t("backup.exportRowDesc")}
          label={t("backup.exportRowLabel")}
        />
        <IqujiSettingsActionRow
          action={
            <IqujiButton compact onClick={() => setRestoreOpen(true)} variant="secondary">
              {t("backup.openRestoreDrawer")}
            </IqujiButton>
          }
          description={t("backup.restoreRowDesc")}
          label={t("backup.restoreRowLabel")}
        />
      </IqujiSettingsSection>

      <IqujiSettingsSection title={t("maintenance.sectionTitle")}>
        <IqujiSettingsActionRow
          action={
            <IqujiButton
              compact
              onClick={() => setMaintenanceAction("clear_logs")}
              variant="secondary"
            >
              {t("maintenance.openClearLogsDialog")}
            </IqujiButton>
          }
          description={t("maintenance.clearLogsRowDesc")}
          label={t("maintenance.clearLogsRowLabel")}
        />
        <IqujiSettingsActionRow
          action={
            <IqujiButton
              compact
              onClick={() => setMaintenanceAction("factory_reset")}
              variant="danger"
            >
              {t("maintenance.openFactoryResetDialog")}
            </IqujiButton>
          }
          description={t("maintenance.factoryResetRowDesc")}
          label={t("maintenance.factoryResetRowLabel")}
        />
      </IqujiSettingsSection>

      <BackupExportDrawer onClose={() => setExportOpen(false)} open={exportOpen} />
      <BackupRestoreDrawer onClose={() => setRestoreOpen(false)} open={restoreOpen} />
      <DataMaintenanceDialog
        action={maintenanceAction}
        onClose={() => setMaintenanceAction(null)}
      />
    </IqujiSettingsContent>
  );
}
