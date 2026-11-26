import type { PageCatalog, PageID } from "@/contracts/pages";
import { fetchJson } from "@/shared/api/client";
import { liveControls } from "@/shared/desktop/automation-controls";
import { workspaceHasDrafts } from "@/shared/desktop/workspace-interactions";
import {
  beginActionGuide,
  endActionGuide,
  guideLocation,
  guideOwnsForm,
  showActionGuide,
} from "@/shared/tutorial/action-guide-state";
import { tutorialActive } from "@/shared/tutorial/controller";
import { awaitUI, navigateAutomationPage, visibleDialog } from "./automation-navigation";

type Args = Record<string, string>;
type Editing = { client: string; read: unknown; target: string };
type Located = { anchor: HTMLElement; hint: string };
type Context = { a: Args; pages: PageCatalog; owner: string; editing?: Editing };
const free = () => {
  if (tutorialActive()) throw Error("AUTOMATION_TUTORIAL_ACTIVE");
  if (workspaceHasDrafts() || liveControls.forms.size || visibleDialog())
    throw Error("AUTOMATION_DRAFT_ACTIVE");
};
function visible(el: HTMLElement) {
  return (
    !!el.getClientRects().length &&
    getComputedStyle(el).visibility !== "hidden" &&
    !el.closest('[hidden],[inert],[aria-hidden="true"]')
  );
}
function tagged(attribute: string, key: string, root: ParentNode = document) {
  return Array.from(root.querySelectorAll<HTMLElement>(`[${attribute}]`)).find(
    (el) => el.getAttribute(attribute) === key && visible(el),
  );
}
function result(anchor: HTMLElement | undefined | null, hint: string): Located {
  if (!anchor || !visible(anchor)) throw Error("AUTOMATION_TARGET_UNAVAILABLE");
  if (anchor.closest(':disabled,[aria-disabled="true"]'))
    throw Error("AUTOMATION_OBJECT_UNAVAILABLE");
  return { anchor, hint };
}
function selectedTab(selector: string) {
  return Array.from(document.querySelectorAll<HTMLElement>(selector)).find(visible);
}
async function pageOpen({ a, pages }: Context) {
  free();
  await navigateAutomationPage(a, pages, free);
  free();
  if (a.tab) {
    if (a.page === "settings") return result(tagged("data-ops-guide-category", a.tab), "page");
    if (a.page === "home") return result(tagged("data-ops-guide-tab", a.tab), "page");
    if (a.application)
      return result(
        selectedTab('.ops-application-page__tabs [role="tab"][aria-selected="true"]'),
        "page",
      );
    if (a.page === "servers" || a.page === "tunnels") {
      const panel = a.page === "servers" ? "server-detail-panel" : "tunnel-tab-panel";
      return result(
        selectedTab(`[role="tab"][aria-controls="${panel}"][aria-selected="true"]`),
        "page",
      );
    }
  }
  if (a.target || a.application || a.tab) {
    const object = () =>
      tagged(
        "data-ops-guide-object",
        a.application || a.target || liveControls.scopes.get(a.page)?.().target || "",
      );
    await awaitUI(() => Boolean(object()));
    free();
    return result(object(), "page");
  }
  return result(selectedTab('nav button[aria-current="page"]'), "page");
}
async function pageControl({ a, pages }: Context) {
  free();
  if (a.target === "home" && !liveControls.scopes.has("home"))
    await navigateAutomationPage({ page: "home" }, pages, free);
  if (a.target === "appearance" && !liveControls.scopes.has("appearance"))
    await navigateAutomationPage({ page: "settings", tab: "appearance" }, pages, free);
  free();
  const fields = liveControls.scopes.get(a.target)?.().controls;
  if (!fields || !Object.hasOwn(fields, a.field)) throw Error("AUTOMATION_FIELD_UNAVAILABLE");
  return result(tagged("data-ops-guide-control", `${a.target}.${a.field}`), "control");
}
const formPages: Record<string, PageID> = {
  project: "projects",
  application: "projects",
  server: "servers",
  tunnel: "tunnels",
};
async function formOpen({ a, pages }: Context) {
  free();
  const page = formPages[a.target?.split(".")[0]];
  if (!page) throw Error("AUTOMATION_FORM_UNAVAILABLE");
  if (!liveControls.scopes.has(page)) await navigateAutomationPage({ page }, pages, free);
  free();
  if (!liveControls.openers.has(a.target)) throw Error("AUTOMATION_FORM_UNAVAILABLE");
  const opener = tagged("data-ops-guide-open", a.target);
  if (opener)
    return result(opener, opener.hasAttribute("data-ops-guide-field") ? "inlineForm" : "open");
  return result(tagged("data-ops-guide-menu", a.target), "openMenu");
}
function formControl({ a, owner, editing }: Context) {
  const id = a.target?.split("/")[0];
  const read = liveControls.forms.get(id);
  if (
    !read ||
    read().target !== a.target ||
    !(
      guideOwnsForm(owner, read) ||
      (editing?.client === owner && editing.read === read && editing.target === a.target)
    )
  )
    throw Error("AUTOMATION_DRAFT_ACTIVE");
  const body = tagged("data-ops-guide-form", id);
  if (!body) throw Error("AUTOMATION_FORM_UNAVAILABLE");
  const dialog = body.closest('[role="dialog"]');
  if (visibleDialog(dialog)) throw Error("AUTOMATION_DRAFT_ACTIVE");
  if (a.action === "form_fill") {
    if (read().manualFields?.includes(a.field))
      return result(tagged("data-ops-guide-field", a.field, body), "manualPassword");
    if (!Object.hasOwn(read().fields, a.field)) throw Error("AUTOMATION_FIELD_UNAVAILABLE");
    return result(tagged("data-ops-guide-field", a.field, body), "fill");
  }
  const command = a.action === "form_submit" ? "submit" : "close";
  const marker = tagged("data-ops-guide-command", command, dialog || body);
  return result(marker?.closest<HTMLElement>("button") || marker, command);
}
const appTabs: Record<string, string> = {
  application_release: "source",
  application_restart: "runtime",
  application_log_configure: "container",
  application_logs: "logs",
};
async function appAction({ a, pages }: Context) {
  free();
  if (!a.target || (a.action === "application_logs" && a.task && !a.slot))
    throw Error("AUTOMATION_ARGUMENT_REQUIRED");
  const projects = await fetchJson<{ items: { id: string; applicationIds?: string[] }[] }>(
    "projects",
  );
  const project = projects.items.find((item) => item.applicationIds?.includes(a.target));
  if (!project) throw Error("AUTOMATION_OBJECT_DELETED");
  free();
  await navigateAutomationPage(
    { page: "projects", target: project.id, application: a.target, tab: appTabs[a.action] },
    pages,
    free,
  );
  free();
  if (a.action === "application_logs") {
    if (!a.slot) return result(tagged("data-ops-guide-action", "log_slot"), "logSlot");
    const { locateApplicationLog } = await import("@/features/projects/application-log-guide");
    locateApplicationLog(a.target, a.slot, a.task);
    await awaitUI(() => {
      const button = tagged("data-ops-guide-action", a.action);
      return (
        button?.dataset.opsGuideSlot === a.slot &&
        (!a.task || button?.dataset.opsGuideTask === a.task)
      );
    });
  }
  free();
  return result(tagged("data-ops-guide-action", a.action), a.action);
}
export const guideHandlers = {
  page_open: pageOpen,
  page_control: pageControl,
  form_open: formOpen,
  form_fill: formControl,
  form_close: formControl,
  form_submit: formControl,
  application_release: appAction,
  application_restart: appAction,
  application_log_configure: appAction,
  application_logs: appAction,
};
export async function guideAction(
  a: Args,
  pages: PageCatalog,
  owner: string,
  author: string,
  editing?: Editing,
) {
  if (!Object.hasOwn(guideHandlers, a.action)) throw Error("AUTOMATION_COMMAND_UNKNOWN");
  if (tutorialActive()) throw Error("AUTOMATION_TUTORIAL_ACTIVE");
  beginActionGuide(owner);
  try {
    const { anchor, hint } = await guideHandlers[a.action as keyof typeof guideHandlers]({
      a,
      pages,
      owner,
      editing,
    });
    const target = a.target || a.page;
    anchor.scrollIntoView({ block: "nearest", inline: "nearest" });
    showActionGuide({
      action: a.action,
      target,
      owner,
      author,
      anchor,
      hint,
      location: guideLocation(a.action, target),
    });
    await awaitUI(
      () =>
        !!Array.from(document.querySelectorAll<HTMLElement>("[data-ops-action-card]")).find(
          visible,
        ),
    );
    endActionGuide(true);
    return {
      action: a.action,
      target,
      located: true,
      executed: false,
      hint,
      page: Object.entries(pages).find(([, page]) => page.path === window.location.pathname)?.[0],
    };
  } catch (error) {
    endActionGuide();
    throw error;
  }
}
