import { IqujiAlert, IqujiLoadingState, IqujiLogViewer } from "@iquji-community/web-component";
import { useQuery } from "@tanstack/react-query";
import { lazy, Suspense } from "react";
import type { OperationRun } from "@/contracts/types";
import { errorMessage, fetchJson } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { isFinishedOperation } from "@/shared/operations/log-api";
import { useOperationLogRead } from "@/shared/operations/use-operation-log-read";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import "@/shared/ui/operation-history.css";
import { type BuildArtifactKind, fetchBuildArtifact } from "./build-api";

const JsonInspector = lazy(() =>
  import("@/shared/ui/json-inspector").then((module) => ({ default: module.JsonInspector })),
);
export type BuildArtifactSelection = {
  kind: BuildArtifactKind;
  label: string;
  url: string;
  operationId: string;
};

export function BuildArtifactDrawer({
  artifact,
  onClose,
}: {
  artifact: BuildArtifactSelection;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const query = useQuery({
    queryKey: ["application-build-artifact", artifact.url, artifact.kind],
    queryFn: async () => {
      // 先读取操作版本，再读取日志；只确认实际展示过的终态版本。
      const run = await fetchJson<OperationRun>(
        `operations/runs/${encodeURIComponent(artifact.operationId)}`,
      );
      const content = await fetchBuildArtifact(artifact.url, artifact.kind);
      return { content, run };
    },
    refetchInterval: (query) =>
      !isFinishedOperation(query.state.data?.run.status ?? "") ? 2000 : false,
    staleTime: 0,
  });
  useOperationLogRead(query.data?.run, artifact.kind === "console" && query.isSuccess);
  return (
    <InspectorDrawer
      bodyClassName="ops-log-drawer__body--fill"
      className="ops-log-drawer"
      open
      onClose={onClose}
      size="xl"
      title={artifact.label}
    >
      {query.isLoading ? <IqujiLoadingState label={t("projects.readingBuildDetail")} /> : null}
      {query.error ? (
        <IqujiAlert title={t("projects.readBuildDetailFailed")} tone="danger">
          {errorMessage(query.error)}
        </IqujiAlert>
      ) : null}
      {query.data !== undefined ? (
        artifact.kind === "console" ? (
          <div className="ops-log-drawer__fill">
            <IqujiLogViewer
              fill
              label={artifact.label}
              logs={String(query.data.content).split(/\r?\n/)}
              emptyLabel={t("operations.emptyLogs")}
              onRefresh={() => void query.refetch()}
              refreshing={query.isFetching}
              refreshLabel={t("common.refresh")}
            />
          </div>
        ) : (
          <div className="ops-log-drawer__fill">
            <Suspense fallback={<IqujiLoadingState label={t("common.loading")} />}>
              <JsonInspector
                value={query.data.content}
                label={artifact.label}
                documentKey={artifact.url}
              />
            </Suspense>
          </div>
        )
      ) : null}
    </InspectorDrawer>
  );
}
