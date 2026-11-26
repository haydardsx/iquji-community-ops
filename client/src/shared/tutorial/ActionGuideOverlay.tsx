import { IqujiTour } from "@iquji-community/web-component";
import { useEffect, useSyncExternalStore } from "react";
import { liveControls } from "@/shared/desktop/automation-controls";
import { useI18n } from "@/shared/i18n";
import {
  actionGuideSnapshot,
  closeActionGuide,
  guideLocation,
  noteGuideFormOpen,
  subscribeActionGuide,
} from "./action-guide-state";

export function ActionGuideOverlay() {
  const guide = useSyncExternalStore(subscribeActionGuide, actionGuideSnapshot, () => undefined);
  useEffect(() => {
    if (!guide) return;
    const check = () => {
      if (
        guideLocation(guide.action, guide.target) !== guide.location ||
        !guide.anchor.isConnected ||
        !guide.anchor.getClientRects().length ||
        guide.anchor.closest('[hidden],[inert],[aria-hidden="true"]')
      )
        closeActionGuide();
    };
    const observer = new MutationObserver(check);
    observer.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      attributeFilter: ["hidden", "inert", "aria-hidden"],
    });
    const click = (event: MouseEvent) => {
      if (!event.isTrusted || guide.action !== "form_open" || !(event.target instanceof Element))
        return;
      const button = event.target.closest("[data-ops-guide-open]");
      if (button === guide.anchor && button.getAttribute("data-ops-guide-open") === guide.target) {
        noteGuideFormOpen(guide.target);
        // 内联字段已经可编辑，用户按指引点击后才建立该表单的引导会话。
        if (button.hasAttribute("data-ops-guide-field")) liveControls.openers.get(guide.target)?.();
      }
    };
    document.addEventListener("click", click, true);
    return () => {
      observer.disconnect();
      document.removeEventListener("click", click, true);
    };
  }, [guide]);
  return <ActionGuideCard />;
}
export function ActionGuideCard({ form }: { form?: string }) {
  const guide = useSyncExternalStore(subscribeActionGuide, actionGuideSnapshot, () => undefined);
  const { t } = useI18n();
  if (!guide) return null;
  const dialog = guide.anchor.closest('[role="dialog"]');
  if (
    dialog
      ? !form ||
        !Array.from(dialog.querySelectorAll<HTMLElement>("[data-ops-guide-form]")).some(
          (node) => node.dataset.opsGuideForm === form,
        )
      : Boolean(form)
  )
    return null;
  return (
    <IqujiTour
      open
      closeLabel={t("common.close")}
      onClose={closeActionGuide}
      step={{
        id: guide.action,
        target: () => (guide.anchor.isConnected ? guide.anchor : null),
        title: <span data-ops-action-card="">{t(`automation.label.${guide.action}`)}</span>,
        description: (
          <>
            {guide.author} · {t(`tutorial.guide.${guide.hint}`)}
          </>
        ),
      }}
    />
  );
}
