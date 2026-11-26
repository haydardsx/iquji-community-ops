import type { PageID, PageCatalog as Pages } from "@/contracts/pages";
import { fetchJson } from "@/shared/api/client";
import { liveControls } from "@/shared/desktop/automation-controls";
import { router } from "./router";
export const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
export async function awaitUI(predicate: () => boolean) {
  for (let i = 0; i < 200; i++) {
    if (predicate()) return;
    await pause(40);
  }
  throw Error("AUTOMATION_TARGET_UNAVAILABLE");
}
export function visibleDialog(except?: Element | null) {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).some(
    (el) =>
      el !== except &&
      !Array.from(el.querySelectorAll("[data-ops-action-card]")).some(
        (card) => card.closest('[role="dialog"]') === el,
      ) &&
      el.getClientRects().length > 0 &&
      getComputedStyle(el).visibility === "visible" &&
      !el.closest('[aria-hidden="true"],[inert]'),
  );
}

export async function navigateAutomationPage(
  a: Record<string, string>,
  pages: Pages,
  ensureAvailable: () => void,
) {
  if (!Object.hasOwn(pages, a.page) || (a.tab && !pages[a.page as PageID]?.tabs.includes(a.tab)))
    throw Error("AUTOMATION_PAGE_INVALID");
  const page = a.page as PageID;
  const idKey = (
    { servers: "serverId", projects: "projectId", tunnels: "tunnelId" } as Record<string, string>
  )[page];
  if ((a.target && !idKey) || (a.application && page !== "projects"))
    throw Error("AUTOMATION_ARGUMENT_INVALID");
  if (a.target) {
    const route = page === "tunnels" ? "servers/tunnels" : page;
    const list = await fetchJson<{ items: { id: string }[] }>(route);
    if (!list.items.some((row) => row.id === a.target)) throw Error("AUTOMATION_OBJECT_DELETED");
  }
  if (page === "projects" && a.tab && a.tab !== "applications" && !a.application)
    throw Error("AUTOMATION_OBJECT_REQUIRED");
  if (a.application && a.tab === "applications") throw Error("AUTOMATION_PAGE_INVALID");
  if (a.application) {
    if (!a.target) throw Error("AUTOMATION_ARGUMENT_REQUIRED");
    const apps = await fetchJson<{ items: { id: string }[] }>(
      `projects/${encodeURIComponent(a.target)}/applications`,
    );
    if (!apps.items.some((row) => row.id === a.application))
      throw Error("AUTOMATION_OBJECT_DELETED");
  }
  ensureAvailable();
  const search: Record<string, string> = {};
  if (a.target) search[idKey] = a.target;
  if (a.application) search.applicationId = a.application;
  if (page === "projects" && a.tab === "applications") search.projectView = "overview";
  if (a.tab && page === "projects")
    search[a.tab === "applications" ? "projectTab" : "applicationTab"] = a.tab;
  if (a.tab && page === "home") search.tab = a.tab;
  await router.navigate({
    to: pages[page].path,
    search,
    hash: page === "settings" ? a.tab : undefined,
  });
  if (page === "settings" && a.tab) window.dispatchEvent(new HashChangeEvent("hashchange"));
  await awaitUI(
    () =>
      liveControls.scopes.has(page) &&
      (!a.target || liveControls.scopes.get(page)?.().target === a.target),
  );
  if (a.tab && ["servers", "tunnels"].includes(page)) {
    const read = liveControls.scopes.get(`${page}-detail`);
    if (!read) throw Error("AUTOMATION_OBJECT_REQUIRED");
    read().select?.(a.tab);
    await awaitUI(() => read().tab === a.tab);
  }
  if (a.application)
    await awaitUI(() => {
      const app = liveControls.scopes.get("application-detail")?.();
      return app?.target === a.application && (!a.tab || app.tab === a.tab);
    });
  if (a.tab && (page === "settings" || page === "home"))
    await awaitUI(() => liveControls.scopes.get(page)?.().tab === a.tab);
}
