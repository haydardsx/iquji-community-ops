import { useIqujiNotification } from "@iquji-community/web-component";
import { useQueryClient } from "@tanstack/react-query";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
} from "react";
import type { OperationRun } from "@/contracts/types";
import { ApiError, errorMessage, fetchJson, operationErrorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { OPERATION_ACTIVITY_QUERY_KEY, useOperationActivity } from "./activity";
import { isTerminalOperation } from "./confirmed-operation";
import { trackedOperationCompletion } from "./tracked-operation";

type TrackedOperation = { run: OperationRun; label: string; submittedRevision: number };
const OperationTracker = createContext<((run: OperationRun, label: string) => void) | null>(null);

export function OperationTrackerProvider({ children }: { children: ReactNode }) {
  const [tracked, setTracked] = useState<Record<string, TrackedOperation>>({});
  const notification = useIqujiNotification();
  const queryClient = useQueryClient();
  const { t } = useI18n();
  const completed = useRef(new Set<string>());
  const activity = useOperationActivity();
  const reconciling = useRef(new Set<string>());
  const lookupRevision = useRef(new Map<string, number>());
  const finish = useCallback(
    (run: OperationRun, label: string) => {
      if (completed.current.has(run.id)) return;
      completed.current.add(run.id);
      notification.close(`operation-read-${run.id}`);
      const statusSuffix =
        run.status === "succeeded"
          ? t("state.complete")
          : run.status === "cancelled"
            ? t("state.cancelled")
            : t("state.failed");

      notification.open({
        key: `operation-${run.id}`,
        title: `${label} ${statusSuffix}`,
        description:
          run.errorCode || run.summary
            ? operationErrorMessage(run.errorCode || run.summary)
            : undefined,
        tone:
          run.status === "succeeded"
            ? "success"
            : run.status === "cancelled"
              ? "warning"
              : "danger",
        duration: run.status === "failed" ? 0 : 4500,
      });
      void queryClient.invalidateQueries({ queryKey: ["operation-runs"] });
    },
    [notification, queryClient, t],
  );

  const track = useCallback(
    (run: OperationRun, label: string) => {
      void queryClient.invalidateQueries({ queryKey: OPERATION_ACTIVITY_QUERY_KEY });
      if (isTerminalOperation(run)) {
        finish(run, label);
        return;
      }
      setTracked((current) => ({
        ...current,
        [run.id]: { run, label, submittedRevision: activity.revision },
      }));
      void queryClient.invalidateQueries({ queryKey: ["operation-runs"] });
    },
    [finish, queryClient, activity.revision],
  );

  useEffect(() => {
    const remove = (id: string) =>
      setTracked((current) =>
        Object.fromEntries(Object.entries(current).filter(([key]) => key !== id)),
      );
    for (const { run, label, submittedRevision } of Object.values(tracked)) {
      if (reconciling.current.has(run.id)) continue;
      if (!activity.items.some((item) => item.id === run.id)) {
        if (
          activity.revision <= submittedRevision ||
          lookupRevision.current.get(run.id) === activity.revision
        )
          continue;
        lookupRevision.current.set(run.id, activity.revision);
      }
      reconciling.current.add(run.id);
      void trackedOperationCompletion(run.id, submittedRevision, activity, (id) =>
        fetchJson<OperationRun>(`operations/runs/${encodeURIComponent(id)}`),
      )
        .then((terminal) => {
          if (!terminal) return;
          finish(terminal, label);
          remove(run.id);
        })
        .catch((error) => {
          if (error instanceof ApiError && error.status === 404) {
            remove(run.id);
            return;
          }
          notification.open({
            key: `operation-read-${run.id}`,
            title: t("activity.readFailed"),
            description: errorMessage(error),
            tone: "danger",
          });
        })
        .finally(() => reconciling.current.delete(run.id));
    }
  }, [activity, tracked, finish, notification, t]);

  return <OperationTracker.Provider value={track}>{children}</OperationTracker.Provider>;
}

export function useOperationTracker() {
  const track = useContext(OperationTracker);
  if (!track) throw new Error("操作跟踪需要 OperationTrackerProvider。");
  return track;
}
