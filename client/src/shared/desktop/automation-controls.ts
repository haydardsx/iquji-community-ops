import { useLayoutEffect, useMemo, useRef } from "react";

import { bindGuideForm } from "@/shared/tutorial/action-guide-state";

export type ViewControls = Record<string, { value?: string; run: (input: string) => unknown }>;
type Fields = Record<string, (value: string) => void>;
type FormOptions = { manualFields?: readonly string[]; operationReceipt?: boolean };
type Form = FormOptions & {
  target: string;
  values: Record<string, string>;
  fields: Fields;
  close: () => void;
  submit: () => Promise<unknown>;
};
const forms = new Map<string, () => Form>();
const openers = new Map<string, () => void>();
const scopes = new Map<
  string,
  () => {
    target: string;
    tab: string;
    select?: (tab: string) => void;
    controls?: ViewControls;
    facts?: Record<string, string | boolean>;
  }
>();

function useHandle<T>(
  registry: Map<string, () => T>,
  id: string,
  enabled: boolean,
  current: () => T,
) {
  const ref = useRef(current);
  ref.current = current;
  const handle = useMemo(
    () => (enabled ? { id, read: () => ref.current() } : undefined),
    [enabled, id],
  );
  useLayoutEffect(() => {
    if (!handle) return;
    const { id, read } = handle;
    registry.set(id, read);
    return () => {
      if (registry.get(id) === read) registry.delete(id);
    };
  }, [handle, registry]);
}

export function useAutomationForm(
  id: string,
  open: boolean,
  objectId: string,
  values: Record<string, string>,
  fields: Fields,
  close: () => void,
  submit: () => Promise<unknown>,
  options?: FormOptions,
) {
  useHandle(forms, id, open, () => ({
    ...options,
    target: objectId ? `${id}/${objectId}` : id,
    values,
    fields,
    close,
    submit,
  }));
  useLayoutEffect(() => {
    if (open) {
      const read = forms.get(id);
      if (read) bindGuideForm(id, read, read().target);
    }
  }, [open, id]);
}
export function useAutomationOpener(id: string, open: () => void, enabled = true) {
  useHandle(openers, id, enabled, open);
}
export function useAutomationScope(
  id: string,
  target: string,
  tab: string,
  select?: (value: string) => void,
  controls?: ViewControls,
  facts?: Record<string, string | boolean>,
) {
  useHandle(scopes, id, true, () => ({ target, tab, select, controls, facts }));
}
export function fillAutomationField(form: Form, field: string, value: string) {
  if (form.manualFields?.includes(field)) throw Error("AUTOMATION_MANUAL_INPUT_REQUIRED");
  if (!Object.hasOwn(form.fields, field)) throw Error("AUTOMATION_FIELD_UNAVAILABLE");
  form.fields[field](value);
}
export async function submitAutomationForm(form: Form) {
  const run = await form.submit();
  if (!form.operationReceipt) return;
  if (
    !run ||
    typeof run !== "object" ||
    !("id" in run) ||
    typeof run.id !== "string" ||
    !run.id ||
    !("status" in run) ||
    typeof run.status !== "string" ||
    !run.status
  )
    throw Error("AUTOMATION_OPERATION_UNVERIFIED");
  return { operationRunId: run.id, status: run.status };
}

export function formState(ownedTarget?: string) {
  return {
    forms: Array.from(forms, ([id, read]) => ({
      id,
      target: read().target,
      fields: Object.keys(read().fields),
      ...(read().manualFields ? { manualFields: read().manualFields } : {}),
      values: read().target === ownedTarget ? read().values : undefined,
    })),
    openers: Array.from(openers.keys()),
    scopes: Object.fromEntries(
      Array.from(scopes, ([id, read]) => {
        const { target, tab, controls, facts } = read();
        return [
          id,
          {
            target,
            tab,
            ...(facts ? { facts } : {}),
            controls: Object.fromEntries(
              Object.entries(controls ?? {}).map(([key, v]) => [key, v.value ?? null]),
            ),
          },
        ];
      }),
    ),
  };
}
export const liveControls = { forms, openers, scopes };
