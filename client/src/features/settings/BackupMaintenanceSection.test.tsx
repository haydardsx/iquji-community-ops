import { IqujiNotificationProvider, IqujiOverlayProvider } from "@iquji-community/web-component";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { OperationActivityProvider } from "@/shared/operations/activity";
import { OperationTrackerProvider } from "@/shared/operations/operation-tracker";
import { renderToStaticMarkup } from "@/test/render";
import { BackupExportDrawer } from "./BackupExportDrawer";
import { BackupMaintenanceSection } from "./BackupMaintenanceSection";
import { BackupRestoreDrawer } from "./BackupRestoreDrawer";
import {
  formatDependencySummary,
  formatRecordCountLabel,
  formatScopeLabel,
  resetClientAfterFactoryReset,
} from "./backup-maintenance-format";
import { DataMaintenanceDialog } from "./DataMaintenanceDialog";

vi.mock("@iquji-community/web-component", async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    IqujiDrawer: ({
      open,
      title,
      description,
      children,
      actions,
    }: {
      open: boolean;
      title?: ReactNode;
      description?: ReactNode;
      children?: ReactNode;
      actions?: ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-drawer">
          <h2>{title}</h2>
          {description ? <p>{description}</p> : null}
          <div>{children}</div>
          <div>{actions}</div>
        </div>
      ) : null,
    IqujiDialog: ({
      open,
      title,
      children,
      actions,
    }: {
      open: boolean;
      title?: ReactNode;
      children?: ReactNode;
      actions?: ReactNode;
    }) =>
      open ? (
        <div data-testid="iquji-dialog">
          <h2>{title}</h2>
          <div>{children}</div>
          <div>{actions}</div>
        </div>
      ) : null,
  };
});

function renderWithProviders(children: ReactNode, locale: "zh-CN" | "en-US" = "zh-CN") {
  const queryClient = new QueryClient();
  return renderToStaticMarkup(
    <QueryClientProvider client={queryClient}>
      <IqujiOverlayProvider>
        <IqujiNotificationProvider closeLabel="关闭通知">
          <OperationActivityProvider>
            <OperationTrackerProvider>{children}</OperationTrackerProvider>
          </OperationActivityProvider>
        </IqujiNotificationProvider>
      </IqujiOverlayProvider>
    </QueryClientProvider>,
    locale,
  );
}

it("备份与数据维护主界面保持简洁操作入口而不平铺表单控件", () => {
  const html = renderWithProviders(<BackupMaintenanceSection />, "zh-CN");
  expect(html).toContain(">备份与数据维护</h1>");
  expect(html).toContain("备份与恢复");
  expect(html).toContain("导出备份");
  expect(html).toContain("导入恢复");
  expect(html).toContain("本地数据维护");
  expect(html).toContain("清空日志");
  expect(html).toContain("出厂重置");
  // 主视图不应平铺输入口令框或选择文件占位键名
  expect(html).not.toContain("ops-backup-export-passphrase");
  expect(html).not.toContain("backup.selectFilePlaceholder");
  expect(html).not.toContain(">日志<");
});

it("备份与数据维护主界面支持英文词条", () => {
  const html = renderWithProviders(<BackupMaintenanceSection />, "en-US");
  expect(html).toContain(">Backup &amp; Maintenance</h1>");
  expect(html).toContain("Export backup");
  expect(html).toContain("Restore from backup");
  expect(html).toContain("Local Data Maintenance");
});

it("导出抽屉、恢复抽屉与维护确认弹窗均可正常打开渲染", () => {
  const exportHtml = renderWithProviders(<BackupExportDrawer onClose={vi.fn()} open />);
  expect(exportHtml).toContain("导出备份");
  expect(exportHtml).toContain("生成预览");

  const restoreHtml = renderWithProviders(<BackupRestoreDrawer onClose={vi.fn()} open />);
  expect(restoreHtml).toContain("从备份恢复");
  expect(restoreHtml).toContain("选择文件");

  const dialogHtml = renderWithProviders(
    <DataMaintenanceDialog action="clear_logs" onClose={vi.fn()} />,
  );
  expect(dialogHtml).toContain("清空本地日志");
  expect(dialogHtml).toContain("仅清除历史日志，保留全部业务资产");
});

it("格式化辅助函数可正确翻译业务域、统计字段和依赖关系", () => {
  const mockT = ((key: string) => key) as unknown as (key: string) => string;
  expect(formatScopeLabel("projects", mockT)).toBe("backup.scopeProjects");
  expect(formatRecordCountLabel("secretMaterials", mockT)).toBe("backup.countSecretMaterials");
  const depText = formatDependencySummary(
    {
      scope: "servers",
      requiredByScope: "projects",
      resource: { kind: "server", id: "srv-1", name: "prod-node" },
      reason: "PROJECT_SERVER_REFERENCE",
    },
    mockT,
  );
  expect(depText).toContain("backup.scopeProjects → backup.scopeServers");
  expect(depText).toContain("prod-node：backup.depReasonProjectServer");
});

it("出厂重置前端清理函数清空本地 UI 偏好键与查询缓存", () => {
  const removed: string[] = [];
  vi.stubGlobal("window", {
    localStorage: {
      removeItem: (key: string) => {
        removed.push(key);
      },
    },
  });
  const queryClient = new QueryClient();
  const clearSpy = vi.spyOn(queryClient, "clear");

  try {
    resetClientAfterFactoryReset(queryClient);
    expect(removed).toEqual(["iquops.theme", "iquops.language", "iquops.sidebarCollapsed"]);
    expect(clearSpy).toHaveBeenCalledTimes(1);
  } finally {
    vi.unstubAllGlobals();
  }
});
