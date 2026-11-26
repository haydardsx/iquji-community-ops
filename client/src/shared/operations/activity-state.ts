import type { OperationActivityItem } from "./activity";

/** 已读可能使终态跳过活动快照；旧活动消失时仍须刷新其业务范围。 */
export function operationActivityChanges(
  previous: OperationActivityItem[],
  current: OperationActivityItem[],
) {
  const before = new Map(previous.map((item) => [item.id, item]));
  const after = new Set(current.map((item) => item.id));
  return [
    ...current.filter((item) => before.get(item.id)?.readVersion !== item.readVersion),
    ...previous.filter((item) => item.active && !after.has(item.id)),
  ];
}
