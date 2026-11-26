// 日志页只向引导暴露选中服务和副本的视图操作。查询函数由页面上的按钮单独持有。
const selectors = new Map<string, (slot: string, task?: string) => void>();
export function bindLogGuide(id: string, select: (slot: string, task?: string) => void) {
  selectors.set(id, select);
  return () => {
    if (selectors.get(id) === select) selectors.delete(id);
  };
}
export function locateApplicationLog(id: string, slot: string, task?: string) {
  const select = selectors.get(id);
  if (!select) throw Error("AUTOMATION_TARGET_UNAVAILABLE");
  select(slot, task);
}
