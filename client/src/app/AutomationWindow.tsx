import { IqujiConfirmDialog } from "@iquji-community/web-component";
import { useEffect, useState } from "react";
import { matchingPageCatalog, type PageCatalog as Pages } from "@/contracts/pages";
import {
  fillAutomationField,
  formState,
  liveControls,
  submitAutomationForm,
} from "@/shared/desktop/automation-controls";
import { windowRequest } from "@/shared/desktop/automation-window-api";
import { isTauriDesktop } from "@/shared/desktop/external-url";
import { workspaceHasDrafts } from "@/shared/desktop/workspace-interactions";
import { useI18n } from "@/shared/i18n";
import { actionGuideActive } from "@/shared/tutorial/action-guide-state";
import { awaitUI, navigateAutomationPage, pause, visibleDialog } from "./automation-navigation";
import { router } from "./router";

type Args = Record<string, string>;
type Ticket = {
  id: string;
  client: string;
  clientId: string;
  confirm: boolean;
  window: boolean;
  call: { name: string; arguments?: Args | null };
};
type Decision = (accepted: boolean) => void;
function ensureFreeWorkspace() {
  if (workspaceHasDrafts() || liveControls.forms.size || visibleDialog())
    throw Error("AUTOMATION_DRAFT_ACTIVE");
}
function state(client: string, pages: Pages) {
  const current = router.state.location;
  return {
    page: Object.entries(pages).find(([, p]) => p.path === current.pathname)?.[0],
    search: current.search,
    ...formState(
      editing?.client === client && editing.read === liveControls.forms.get(editing.id)
        ? editing.target
        : undefined,
    ),
  };
}
// 仅有打开表单的客户端能继续填写，人工打开的抽屉保留其草稿。
let editing: { id: string; target: string; client: string; read: unknown } | undefined;
async function operate(ticket: Ticket, pages: Pages) {
  const { name } = ticket.call;
  const a = ticket.call.arguments ?? {};
  if (name === "action_guide") {
    const { guideAction } = await import("./action-guide");
    return guideAction(a, pages, ticket.clientId, ticket.client, editing);
  }
  if (name.startsWith("tutorial_")) {
    const { operateTutorial } = await import("@/shared/tutorial/controller");
    return operateTutorial(name, a, ticket.clientId, ticket.client);
  }
  if (name === "page_state") return state(ticket.clientId, pages);
  if (name.startsWith("page_") || name.startsWith("form_")) {
    const { tutorialActive } = await import("@/shared/tutorial/controller");
    if (tutorialActive() || actionGuideActive()) throw Error("AUTOMATION_TUTORIAL_ACTIVE");
  }
  if (name === "page_open" || name === "form_open" || name === "page_control") {
    ensureFreeWorkspace();
  }
  if (name === "page_control") {
    const controls = liveControls.scopes.get(a.target)?.().controls;
    if (!controls || !Object.hasOwn(controls, a.field)) throw Error("AUTOMATION_FIELD_UNAVAILABLE");
    await controls[a.field].run(a.value ?? "");
    await pause(20);
    return state(ticket.clientId, pages);
  }
  if (name === "page_open") {
    await navigateAutomationPage(a, pages, ensureFreeWorkspace);
    return state(ticket.clientId, pages);
  }
  if (name === "form_open") {
    const open = liveControls.openers.get(a.target);
    if (!open) throw Error("AUTOMATION_FORM_UNAVAILABLE");
    const id = a.target.split("/")[0];
    open();
    await awaitUI(() => liveControls.forms.has(id));
    const read = liveControls.forms.get(id);
    if (!read || (id !== a.target && read().target !== a.target))
      throw Error("AUTOMATION_FORM_UNAVAILABLE");
    editing = { id, target: read().target, client: ticket.clientId, read };
    return state(ticket.clientId, pages);
  }
  if (name === "form_fill" || name === "form_submit" || name === "form_close") {
    const read = editing && liveControls.forms.get(editing.id);
    if (
      !read ||
      editing?.client !== ticket.clientId ||
      editing.target !== a.target ||
      read().target !== a.target ||
      editing.read !== read
    )
      throw Error("AUTOMATION_DRAFT_ACTIVE");
    if (name === "form_fill") {
      fillAutomationField(read(), a.field, a.value ?? "");
      await pause(20);
      if (editing && editing.read === read) editing.target = read().target;
      return state(ticket.clientId, pages);
    }
    let receipt: { operationRunId: string; status: string } | undefined;
    const form = read();
    if (name === "form_close") form.close();
    else receipt = await submitAutomationForm(form);
    editing = undefined;
    await awaitUI(() => !liveControls.forms.has(a.target.split("/")[0]) && !visibleDialog());
    return { ...state(ticket.clientId, pages), ...(receipt ? { receipt } : {}) };
  }
  throw Error("AUTOMATION_COMMAND_UNKNOWN");
}
export function AutomationWindow() {
  const { t } = useI18n();
  const [pending, setPending] = useState<{ ticket: Ticket; decision: Decision } | null>(null);
  useEffect(() => {
    if (!isTauriDesktop()) return;
    const controller = new AbortController();
    const signal = controller.signal;
    // 用户接管输入后，已有客户端不得继续覆盖这份草稿。
    let confirming = false;
    const userEdited = (event: Event) => {
      if (confirming || !event.isTrusted || !editing || !liveControls.forms.size) return;
      // 人工专填的字段可以补齐已打开的表单，客户端仍只能请求原有的提交确认。
      const node = event.target instanceof Element ? event.target : null;
      const read = liveControls.forms.get(editing.id);
      const field = node?.closest<HTMLElement>("[data-ops-guide-field]")?.dataset.opsGuideField;
      if (
        read &&
        read === editing.read &&
        read().target === editing.target &&
        field &&
        read().manualFields?.includes(field) &&
        node?.closest<HTMLElement>("[data-ops-guide-form]")?.dataset.opsGuideForm === editing.id
      )
        return;
      editing = undefined;
    };
    window.addEventListener("pointerdown", userEdited, { capture: true, signal });
    window.addEventListener("input", userEdited, { capture: true, signal });
    window.addEventListener("change", userEdited, { capture: true, signal });
    async function listen() {
      let pages: Pages | undefined;
      while (!signal.aborted) {
        try {
          if (!pages) {
            const known = await windowRequest<Pages>("pages", {}, signal);
            if (!matchingPageCatalog(known)) throw Error("AUTOMATION_CONTRACT_MISMATCH");
            pages = known;
          }
          const ticket = await windowRequest<Ticket | null>("next", {}, signal);
          if (!ticket || signal.aborted) continue;
          let response: { approved?: boolean; value?: unknown; error?: string } = {};
          try {
            if (ticket.confirm) {
              confirming = true;
              const approved = await new Promise<boolean>((resolve) => {
                let finished = false;
                const decide = (accepted: boolean) => {
                  if (finished) return;
                  finished = true;
                  clearTimeout(timer);
                  signal.removeEventListener("abort", cancel);
                  resolve(accepted);
                };
                const cancel = () => decide(false);
                const timer = setTimeout(cancel, 85000);
                signal.addEventListener("abort", cancel, { once: true });
                async function check() {
                  while (!finished && !signal.aborted) {
                    await pause(1200);
                    if (finished) return;
                    try {
                      if (!(await windowRequest<boolean>("active", { id: ticket?.id }, signal)))
                        cancel();
                    } catch {
                      cancel();
                    }
                  }
                }
                void check();
                setPending({ ticket, decision: decide });
              });
              confirming = false;
              setPending(null);
              if (!approved) throw Error("AUTOMATION_DECLINED");
              await pause(20);
            }
            if (!(await windowRequest<boolean>("begin", { id: ticket.id }, signal)))
              throw Error("AUTOMATION_REQUEST_EXPIRED");
            response = {
              approved: true,
              value: ticket.window ? await operate(ticket, pages) : undefined,
            };
          } catch (error) {
            response = {
              error: error instanceof Error ? error.message : "AUTOMATION_COMMAND_FAILED",
            };
          }
          await windowRequest("reply", { id: ticket.id, response }, signal);
        } catch {
          if (!signal.aborted) await pause(1500);
        }
      }
    }
    void listen();
    return () => controller.abort();
  }, []);
  return (
    <IqujiConfirmDialog
      open={Boolean(pending)}
      title={t("automation.confirm")}
      description={
        pending
          ? `${pending.ticket.client} · ${t(`automation.label.${pending.ticket.call.name}`)} (${pending.ticket.call.name}) · ${[pending.ticket.call.arguments?.target, pending.ticket.call.arguments?.slot, pending.ticket.call.arguments?.task].filter(Boolean).join(" · ")}。 ${t(`automation.effect.${pending.ticket.call.name}`)}`
          : ""
      }
      closeLabel={t("common.close")}
      cancelLabel={t("common.cancel")}
      confirmLabel={t("automation.allow")}
      onConfirm={() => pending?.decision(true)}
      onOpenChange={(open) => {
        if (!open) pending?.decision(false);
      }}
    />
  );
}
