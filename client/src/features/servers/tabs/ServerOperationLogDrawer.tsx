import {
  IqujiButton,
  type IqujiLogErrorInfo,
  IqujiLogViewer,
} from "@iquji-community/web-component";
import { useInfiniteQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { errorMessage, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { orderedLogEntries } from "@/shared/operations/log-api";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import "@/shared/ui/operation-history.css";
import { fetchServerOperationLogs } from "../api";

export function ServerOperationLogDrawer({
  serverId,
  open,
  onClose,
  title: titleProp,
  description,
  error,
  headerToolbar,
  headerMeta,
}: {
  serverId: string;
  open: boolean;
  onClose: () => void;
  title?: string;
  description?: ReactNode;
  error?: IqujiLogErrorInfo;
  headerToolbar?: ReactNode;
  headerMeta?: ReactNode;
}) {
  const { t } = useI18n();
  const title = titleProp ?? t("servers.viewLogs");
  const logs = useInfiniteQuery({
    queryKey: ["server-operation-logs", serverId],
    queryFn: ({ pageParam }) => fetchServerOperationLogs(serverId, undefined, pageParam),
    initialPageParam: 1,
    getNextPageParam: (last) =>
      last.page * last.pageSize < last.total ? last.page + 1 : undefined,
    enabled: open,
    refetchInterval: open ? 3000 : false,
  });
  const entries = orderedLogEntries(logs.data?.pages);
  const structuredLogs = entries.map((item) => ({
    ...item,
    message: operationErrorMessage(item.message),
  }));
  return (
    <InspectorDrawer
      bodyClassName="ops-log-drawer__body--fill"
      className="ops-log-drawer"
      open={open}
      onClose={onClose}
      title={title}
      description={description}
      headerToolbar={headerToolbar}
      headerMeta={headerMeta}
      size="lg"
    >
      <div className="ops-log-drawer__fill">
        <IqujiLogViewer
          fill
          label={title}
          logs={structuredLogs}
          loading={logs.isLoading}
          error={
            logs.isError
              ? {
                  title: t("servers.logReadFailed"),
                  message: errorMessage(logs.error),
                }
              : error
          }
          actions={
            logs.hasNextPage ? (
              <IqujiButton
                compact
                variant="ghost"
                loading={logs.isFetchingNextPage}
                onClick={() => void logs.fetchNextPage()}
              >
                {t("operations.loadMore")}
              </IqujiButton>
            ) : undefined
          }
          onRefresh={() => {
            void logs.refetch();
          }}
          refreshLabel={logs.isFetching ? t("servers.refreshing") : t("common.refresh")}
          refreshing={logs.isFetching}
        />
      </div>
    </InspectorDrawer>
  );
}
