import {
  IconButton,
  IqujiButton,
  IqujiDropdownMenu,
  IqujiTour,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useRouter } from "@tanstack/react-router";
import { useLayoutEffect, useState, useSyncExternalStore } from "react";
import { workspacePages } from "@/contracts/pages";
import { useI18n } from "@/shared/i18n";
import {
  bindTutorialHost,
  closeTutorial,
  manualTutorial,
  subscribeTutorial,
  syncTutorialPath,
  type TutorialID,
  tutorialElement,
  tutorialSnapshot,
  tutorials,
} from "./controller";

export function TutorialMenu({
  expand,
  path,
}: {
  expand: (enabled: boolean) => void;
  path: string;
}) {
  const { t } = useI18n();
  const router = useRouter();
  const [error, setError] = useState("");
  useLayoutEffect(
    () =>
      bindTutorialHost({
        t,
        expand,
        page: () => router.state.location.pathname,
        go: async (page) => {
          await router.navigate({ to: workspacePages[page].path, search: {} });
        },
      }),
    [router, t, expand],
  );
  useLayoutEffect(() => {
    syncTutorialPath(path);
    setError("");
  }, [path]);
  return (
    <>
      <IqujiDropdownMenu
        ariaLabel={t("tutorial.menu")}
        trigger={
          <IconButton
            compact
            variant="ghost"
            label={t("tutorial.menu")}
            icon={<IqujiIcon name="book-open" decorative />}
          />
        }
        items={(Object.keys(tutorials) as TutorialID[]).map((id) => ({
          key: id,
          label: t(`tutorial.${id}.title`),
          onSelect: () => {
            setError("");
            void manualTutorial("tutorial_start", { target: id }).catch(() =>
              setError(t("tutorial.startBlocked")),
            );
          },
        }))}
      />
      {error && <span role="status">{error}</span>}
    </>
  );
}
export function TutorialOverlay({ scope = "page" }: { scope?: "page" | "project-form" }) {
  const current = useSyncExternalStore(subscribeTutorial, tutorialSnapshot, () => undefined);
  const { t } = useI18n();
  const [failed, setFailed] = useState<typeof current>();
  const id = current?.id,
    index = current?.index ?? 0;
  const step = id ? tutorials[id][index] : undefined;
  if (
    !current ||
    !id ||
    !step ||
    (scope === "project-form") !== (id === "project-create" && step !== "open")
  )
    return null;
  const last = index === tutorials[id].length - 1;
  const advance = (back: boolean) => {
    setFailed(undefined);
    void manualTutorial(back ? "tutorial_previous" : "tutorial_next", { target: id }).catch(() =>
      setFailed(current),
    );
  };
  return (
    <IqujiTour
      open
      closeLabel={t("common.close")}
      onClose={closeTutorial}
      progress={t("tutorial.progress", { index: index + 1, total: tutorials[id].length })}
      step={{
        id: `${id}.${step}`,
        target: () => tutorialElement(id, step),
        title: <span data-ops-tutorial-card="">{t(`tutorial.${id}.${step}.title`)}</span>,
        description: (
          <>
            {t(`tutorial.${id}.${step}.description`)}
            {current.hint && (
              <p>
                <strong>{current.hint.author}</strong> · {current.hint.message}
              </p>
            )}
            {failed === current && <p role="status">{t("tutorial.stepBlocked")}</p>}
          </>
        ),
      }}
      actions={
        <>
          {index > 0 && !(id === "project-create" && step === "name") && (
            <IqujiButton compact variant="secondary" onClick={() => advance(true)}>
              {t("tutorial.previous")}
            </IqujiButton>
          )}
          {last ? (
            <IqujiButton compact onClick={closeTutorial}>
              {t("tutorial.done")}
            </IqujiButton>
          ) : (
            !(id === "project-create" && step === "open") && (
              <IqujiButton compact onClick={() => advance(false)}>
                {t("tutorial.next")}
              </IqujiButton>
            )
          )}
        </>
      }
    />
  );
}
