import { useIqujiNotification } from "@iquji-community/web-component";
import { useRef, useState } from "react";
import { errorMessage } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import {
  type ConfirmedOperationInput,
  ConfirmedOperationSession,
  isTerminalOperation,
} from "./confirmed-operation";
import { useOperationTracker } from "./operation-tracker";

export function useConfirmedOperation() {
  const session = useRef(new ConfirmedOperationSession());
  const [isPending, setPending] = useState(false);
  const [ids, setIds] = useState({
    operationId: null as string | null,
  });
  const notification = useIqujiNotification();
  const track = useOperationTracker();
  const { t } = useI18n();
  const sync = () => setIds({ operationId: session.current.operationId });

  const submit = async (input: ConfirmedOperationInput) => {
    setPending(true);
    try {
      const run = await session.current.submit(input);
      if (!isTerminalOperation(run))
        notification.open({
          key: `operation-${run.id}`,
          title: t("common.opSubmitted"),
          description: input.label,
          tone: "info",
        });
      track(run, input.label);
      return run;
    } catch (error) {
      notification.open({
        title: `${input.label} ${t("common.opFailed")}`,
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      });
      throw error;
    } finally {
      sync();
      setPending(false);
    }
  };

  const cancelPending = async () => {
    setPending(true);
    try {
      await session.current.cancel();
      notification.open({ title: t("common.opCancelled"), tone: "info" });
    } catch (error) {
      notification.open({
        title: t("common.opFailed"),
        description: errorMessage(error),
        tone: "danger",
        duration: 0,
      });
      throw error;
    } finally {
      sync();
      setPending(false);
    }
  };

  return {
    submit,
    isPending,
    ...ids,
    cancelPending,
    reset: () => {
      session.current.reset();
      sync();
    },
  };
}
