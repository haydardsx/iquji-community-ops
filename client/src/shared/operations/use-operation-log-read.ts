import { useEffect, useRef } from "react";
import type { OperationRun } from "@/contracts/types";
import { useOperationActivity } from "./activity";
import { isFinishedOperation } from "./log-api";

/** 调用方须先读取此操作版本，再读取并展示对应日志。 */
export function useOperationLogRead(run: OperationRun | undefined, displayed: boolean) {
  const { markRead } = useOperationActivity();
  const acknowledged = useRef(new Set<string>());
  useEffect(() => {
    if (!displayed || !run || !isFinishedOperation(run.status)) return;
    const key = `${run.id}:${run.status}:${run.updatedAt}`;
    if (acknowledged.current.has(key)) return;
    acknowledged.current.add(key);
    void markRead(run.id, run.updatedAt, run.status).catch(() => acknowledged.current.delete(key));
  }, [run, displayed, markRead]);
}
