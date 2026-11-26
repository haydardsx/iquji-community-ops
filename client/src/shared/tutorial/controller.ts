import {
  automationLimits,
  type PageID as Page,
  workspacePageOrder,
  workspacePages,
} from "@/contracts/pages";
import { liveControls } from "@/shared/desktop/automation-controls";
import { workspaceHasDrafts } from "@/shared/desktop/workspace-interactions";
import type { TranslateFn } from "@/shared/i18n";

import { actionGuideActive } from "./action-guide-state";

export const tutorials = {
  workspace: workspacePageOrder,
  "project-create": ["open", "name", "save"],
} as const;
export type TutorialID = keyof typeof tutorials;
type Host = {
  t: TranslateFn;
  page: () => string;
  go: (page: Page) => Promise<void>;
  expand: (enabled: boolean) => void;
};
type Active = {
  id: TutorialID;
  index: number;
  owner: string;
  hint?: { author: string; message: string };
};
let host: Host | undefined;
let active: Active | undefined;
let pending: { id: TutorialID; owner: string } | undefined;
let generation = 0;
let form: HTMLElement | undefined;
let formRevision = 0;
let opening = false;
const listeners = new Set<() => void>();
const publish = (value?: Active) => {
  active = value;
  for (const notify of listeners) notify();
};
export const tutorialSnapshot = () => active;
export const tutorialActive = () => !!pending || !!active;
export function subscribeTutorial(notify: () => void) {
  listeners.add(notify);
  return () => {
    listeners.delete(notify);
  };
}
export function bindTutorialHost(next: Host) {
  host = next;
  return () => {
    if (host === next) host = undefined;
  };
}
const stepAt = (s: Active) => tutorials[s.id][s.index];
const pageFor = (id: TutorialID, step: string): Page =>
  id === "workspace" ? (step as Page) : "projects";
const anchorFor = (id: TutorialID, step: string) =>
  id === "workspace" && step === "projects" ? "project-create.open" : `${id}.${step}`;
export function tutorialElement(id: TutorialID, step: string) {
  const anchor = `[data-ops-tutorial="${anchorFor(id, step)}"]`;
  const element = document.querySelector<HTMLElement>(
    id === "workspace" && step === "settings" ? `${anchor} input[type="search"]` : anchor,
  );
  if (
    !element?.isConnected ||
    element.closest("[hidden], [inert], [aria-hidden=true]") ||
    getComputedStyle(element).visibility === "hidden" ||
    !element.getClientRects().length
  )
    return null;
  return element;
}
export function closeTutorial() {
  generation++;
  pending = undefined;
  form = undefined;
  opening = false;
  host?.expand(false);
  publish();
}
export function markTutorialProjectOpen() {
  opening = active?.id === "project-create" && stepAt(active) === "open";
}
export function bindTutorialProjectForm(element: HTMLElement) {
  if (active?.id !== "project-create") return;
  if (form && stepAt(active) !== "open") {
    form = element;
    formRevision++;
    return;
  }
  if (!opening) return;
  opening = false;
  form = element;
  formRevision++;
  publish({ ...active, index: 1, hint: undefined });
}
export function releaseTutorialProjectForm(element: HTMLElement) {
  if (element !== form) return;
  const revision = ++formRevision;
  // React 在同一次提交中可重新绑定同一弹窗的 ref，待提交结束后再判定是否已关闭。
  queueMicrotask(() => {
    if (element === form && revision === formRevision) closeTutorial();
  });
}
export function syncTutorialPath(path: string) {
  if (!pending && active && workspacePages[pageFor(active.id, stepAt(active))].path !== path)
    closeTutorial();
}
function describe(id: TutorialID, step: string, t: TranslateFn) {
  return {
    step,
    page: pageFor(id, step),
    targetId: anchorFor(id, step),
    title: t(`tutorial.${id}.${step}.title`),
    description: t(`tutorial.${id}.${step}.description`),
  };
}
function result(t: TranslateFn) {
  if (!active) return { active: false };
  const { id, index, hint } = active;
  const step = stepAt(active);
  return {
    active: true,
    tutorial: id,
    ...describe(id, step, t),
    index: index + 1,
    total: tutorials[id].length,
    available: !!tutorialElement(id, step),
    ...(hint ? { hint } : {}),
  };
}
function hasDialog() {
  return Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]')).some(
    (el) =>
      !el.closest("[aria-hidden=true],[hidden],[inert]") &&
      !el.querySelector("[data-ops-tutorial-card]") &&
      el.getClientRects().length > 0 &&
      getComputedStyle(el).visibility !== "hidden",
  );
}
export async function operateTutorial(
  name: string,
  args: Record<string, string>,
  clientId: string,
  clientName: string,
) {
  return runTutorial(name, args, `mcp:${clientId}`, clientName);
}
export async function manualTutorial(name: string, args: Record<string, string>) {
  return runTutorial(name, args, active?.owner ?? "user", "");
}
async function runTutorial(
  name: string,
  args: Record<string, string>,
  owner: string,
  author: string,
) {
  const ui = host;
  if (!ui) throw Error("AUTOMATION_WINDOW_UNAVAILABLE");
  if (name === "tutorial_list")
    return {
      items: (Object.keys(tutorials) as TutorialID[])
        .filter((id) => !args.target || args.target === id)
        .map((id) => ({
          id,
          title: ui.t(`tutorial.${id}.title`),
          steps: tutorials[id].map((step) => describe(id, step, ui.t)),
        })),
    };
  if (name === "tutorial_state")
    return !active || (active.owner === owner && (!args.target || args.target === active.id))
      ? result(ui.t)
      : { active: false };
  if (actionGuideActive()) throw Error("AUTOMATION_TUTORIAL_ACTIVE");
  if (!Object.hasOwn(tutorials, args.target)) throw Error("AUTOMATION_TUTORIAL_INVALID");
  const id = args.target as TutorialID;
  if (pending && pending.owner !== owner) throw Error("AUTOMATION_TUTORIAL_OWNED");
  if (active && active.owner !== owner) throw Error("AUTOMATION_TUTORIAL_OWNED");
  if (name === "tutorial_close") {
    if ((active && active.id !== id) || (pending && pending.id !== id))
      throw Error("AUTOMATION_TUTORIAL_INVALID");
    closeTutorial();
    return result(ui.t);
  }
  if (pending) throw Error("AUTOMATION_TUTORIAL_BUSY");
  if (active && active.id !== id) throw Error("AUTOMATION_TUTORIAL_ACTIVE");
  const steps: readonly string[] = tutorials[id];
  let index = 0;
  switch (name) {
    case "tutorial_start":
      index = args.step ? steps.indexOf(args.step) : 0;
      break;
    case "tutorial_next":
    case "tutorial_previous":
      if (!active) throw Error("AUTOMATION_TUTORIAL_INACTIVE");
      index = active.index + (name === "tutorial_next" ? 1 : -1);
      break;
    case "tutorial_goto":
    case "tutorial_hint":
      index = steps.indexOf(args.step);
      break;
    default:
      throw Error("AUTOMATION_COMMAND_UNKNOWN");
  }
  const step = steps[index];
  if (!step) throw Error("AUTOMATION_TUTORIAL_STEP_INVALID");
  const requiresForm = id === "project-create" && step !== "open";
  if (requiresForm && (!form?.isConnected || !liveControls.forms.has("project.create")))
    throw Error("AUTOMATION_TUTORIAL_FORM_REQUIRED");
  if (!requiresForm && (workspaceHasDrafts() || liveControls.forms.size || hasDialog()))
    throw Error("AUTOMATION_DRAFT_ACTIVE");
  const message = args.message?.trim();
  if (
    name === "tutorial_hint" &&
    (!message ||
      Array.from(args.message).length > automationLimits.maxHintCharacters ||
      /<\/?[a-z!]|(?:\w+:\/\/|www\.|mailto:|javascript:|data:)|\[[^\]]*\]\(|\{[^{}]*:[^{}]*\}/i.test(
        message,
      ))
  )
    throw Error("AUTOMATION_TUTORIAL_HINT_INVALID");
  const page = pageFor(id, step);
  pending = { id, owner };
  const current = ++generation;
  try {
    ui.expand(true);
    if (ui.page() !== workspacePages[page].path) await ui.go(page);
    for (let attempt = 0; attempt < 80; attempt++) {
      if (current !== generation) throw Error("AUTOMATION_TUTORIAL_INACTIVE");
      const found = tutorialElement(id, step);
      if (found && (!requiresForm || found.closest('[role="dialog"]') === form)) {
        const hint = name === "tutorial_hint" && message ? { author, message } : undefined;
        publish({ id, index, owner, hint });
        return result(ui.t);
      }
      await new Promise((resolve) => setTimeout(resolve, 35));
    }
    throw Error("AUTOMATION_TARGET_UNAVAILABLE");
  } finally {
    if (current === generation) {
      pending = undefined;
      if (!active || ui.page() !== workspacePages[pageFor(active.id, stepAt(active))].path)
        closeTutorial();
    }
  }
}
