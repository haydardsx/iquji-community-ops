import { type PageID, workspacePages } from "@/contracts/pages";

export type Guide = {
  action: string;
  target: string;
  owner: string;
  author: string;
  anchor: HTMLElement;
  hint: string;
  location: string;
};
const listeners = new Set<() => void>();
let current: Guide | undefined;
let pending: string | undefined;
let opening: { id: string; owner: string } | undefined;
let owned: { owner: string; read: unknown } | undefined;

export function guideLocation(action: string, target: string) {
  if (
    action === "form_open" &&
    ["project.create", "server.create", "tunnel.create"].includes(target)
  )
    return window.location.pathname;
  if (
    action === "page_open" &&
    Object.hasOwn(workspacePages, target) &&
    window.location.pathname === workspacePages[target as PageID].path
  )
    return window.location.pathname + window.location.hash;
  return window.location.href;
}
export const actionGuideSnapshot = () => current;
export const actionGuideActive = () => Boolean(current || pending);
export const guideOwnsForm = (owner: string, read: unknown) =>
  Boolean(read && owned?.owner === owner && owned.read === read);
export function subscribeActionGuide(fn: () => void) {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}
function publish(guide?: Guide) {
  current = guide;
  for (const fn of listeners) fn();
}
export function closeActionGuide() {
  publish();
}
export function beginActionGuide(owner: string) {
  if (pending || (current && current.owner !== owner)) throw Error("AUTOMATION_TUTORIAL_OWNED");
  pending = owner;
  publish();
}
export function showActionGuide(guide: Guide) {
  publish(guide);
}
export function endActionGuide(located = false) {
  pending = undefined;
  if (!located) publish();
}
// 只有用户点击当前指引的入口，才把随后实际挂载的表单交给该客户端继续定位。
export function noteGuideFormOpen(id: string) {
  if (current?.action !== "form_open" || current.target !== id) return;
  const request = { id, owner: current.owner };
  opening = request;
  setTimeout(() => {
    if (opening === request) opening = undefined;
  }, 250);
}
export function bindGuideForm(id: string, read: unknown, target = id) {
  if (!opening || (opening.id !== id && opening.id !== target)) return;
  owned = { owner: opening.owner, read };
  opening = undefined;
  closeActionGuide();
}
