import type { OperationRun } from "@/contracts/types";

/** Go 端 `domain.TaskResponse` 的公开字段；确认与取消都以其中的 `id` 为操作运行标识。 */
export type TaskResponse = {
  id: string;
  action: string;
  status: string;
  target: { kind: string; id: string; name?: string };
  createdAt: string;
  updatedAt: string;
  traceId?: string;
  logRef?: string;
};

/** 把任务响应转成前端统一消费的 `OperationRun`；字段不匹配时直接失败，避免跟踪错目标。 */
export function toOperationRun(task: TaskResponse): OperationRun {
  if (!task.id || !task.action || !task.target?.kind || !task.target.id) {
    throw new Error("操作响应缺少标识，未继续确认。");
  }
  return {
    id: task.id,
    action: task.action,
    status: task.status,
    targetKind: task.target.kind,
    targetId: task.target.id,
    targetName: task.target.name,
    createdAt: task.createdAt,
    updatedAt: task.updatedAt,
  };
}
