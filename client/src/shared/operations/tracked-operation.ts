import type { OperationRun } from "@/contracts/types";
import { isTerminalOperation } from "./confirmed-operation";

/** 终态已在别处读过时只补查一次，不为正常活动任务恢复逐项轮询。 */
export async function trackedOperationCompletion(
  id: string,
  submittedRevision: number,
  activity: { items: OperationRun[]; revision: number },
  get: (id: string) => Promise<OperationRun>,
) {
  const current = activity.items.find((item) => item.id === id);
  if (current) return isTerminalOperation(current) ? current : undefined;
  if (activity.revision <= submittedRevision) return undefined;
  const stored = await get(id);
  return isTerminalOperation(stored) ? stored : undefined;
}
