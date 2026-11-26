import { zodResolver } from "@hookform/resolvers/zod";
import {
  IqujiButton,
  IqujiConfirmDialog,
  IqujiField,
  IqujiInput,
  IqujiMultiSelect,
  IqujiSelect,
} from "@iquji-community/web-component";
import { useMutation } from "@tanstack/react-query";
import { useBlocker } from "@tanstack/react-router";
import { useEffect, useMemo, useState } from "react";
import { Controller, useFieldArray, useForm } from "react-hook-form";
import { z } from "zod";
import type {
  ContainerPort,
  Domain,
  RouteInput,
  RouteRow,
  SwarmServiceConfig,
} from "@/contracts/types";
import { useWorkspaceUnloadGuard } from "@/shared/desktop/workspace-interactions";
import { useI18n } from "@/shared/i18n";
import { FieldHelp } from "@/shared/ui/field-help";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import { createRoute, updateRoute } from "./api";
import { createRouteServicesSchema } from "./route-services";

export function createRouteSchema(t?: (key: string) => string) {
  return z
    .object({
      domainIds: z
        .array(z.string())
        .min(1, t ? t("projects.routeDomainRequired") : "至少选择一个域名"),
      services: createRouteServicesSchema(t),
      rules: z
        .array(
          z.object({
            path: z
              .string()
              .trim()
              .startsWith("/", t ? t("projects.routePathSlash") : "路径应以 / 开头"),
            pathHandling: z.enum(["preserve", "strip_prefix", "rewrite"]),
            rewriteTargetPath: z.string(),
          }),
        )
        .min(1),
    })
    .superRefine((value, ctx) =>
      value.rules.forEach((rule, index) => {
        if (rule.pathHandling === "rewrite" && !rule.rewriteTargetPath.startsWith("/"))
          ctx.addIssue({
            code: "custom",
            path: ["rules", index, "rewriteTargetPath"],
            message: t ? t("projects.routeRewriteSlash") : "重写路径应以 / 开头",
          });
      }),
    );
}

const defaultRouteSchema = createRouteSchema();
type Form = z.infer<typeof defaultRouteSchema>;
export function RouteDialog({
  ports = [],
  applicationId,
  domains,
  services,
  open,
  route,
  onOpenChange,
  onSaved,
}: {
  applicationId: string;
  domains: Domain[];
  services: SwarmServiceConfig[];
  ports?: ContainerPort[];
  open: boolean;
  route: RouteRow | null;
  onOpenChange: (open: boolean) => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const schema = useMemo(() => createRouteSchema(t), [t]);
  const form = useForm<Form>({
    resolver: zodResolver(schema),
    defaultValues: {
      domainIds: [],
      services: [],
      rules: [{ path: "/", pathHandling: "preserve", rewriteTargetPath: "" }],
    },
  });
  const rows = useFieldArray({ control: form.control, name: "rules" });
  const [discard, setDiscard] = useState(false);
  const notice = useOperationNotice();
  useEffect(() => {
    if (open)
      form.reset({
        domainIds: route?.domainIds ?? route?.domains.map((item) => item.id) ?? [],
        services:
          route?.services.map((item) => ({
            slotKey: item.slotKey,
            portName: item.portName,
            weight: route?.services.length === 1 ? 1 : item.weight,
          })) ?? [],
        rules: route?.rules.map((item) => ({
          ...item,
          rewriteTargetPath: item.rewriteTargetPath ?? "",
        })) ?? [{ path: "/", pathHandling: "preserve", rewriteTargetPath: "" }],
      });
  }, [open, route, form]);
  const mutation = useMutation({
    mutationFn: (values: Form) => {
      const payload: RouteInput = {
        domainIds: values.domainIds,
        services: values.services.map((target) => {
          const slot = target.slotKey;
          const item = services.find((service) => service.slotKey === slot);
          if (!item) throw new Error(t("projects.routesSelectService"));
          return {
            slotKey: slot,
            portName: target.portName,
            serviceName: item.serviceName,
            weight: values.services.length === 1 ? 1 : target.weight,
          };
        }),
        rules: values.rules.map((item) => ({
          path: item.path,
          pathHandling: item.pathHandling,
          ...(item.pathHandling === "rewrite" ? { rewriteTargetPath: item.rewriteTargetPath } : {}),
        })),
        disabled: route?.disabled ?? false,
      };
      return route
        ? updateRoute(applicationId, route.id, payload)
        : createRoute(applicationId, payload);
    },
    onSuccess: (): void => {
      form.reset(form.getValues());
      notice(t("projects.routeCreated"));
      onSaved();
      if (blocker.status === "blocked") blocker.proceed();
      else onOpenChange(false);
    },
  });
  useOperationError(mutation.error, t("projects.routeCreateFailed"));
  const dirty = form.formState.isDirty;
  const unloadGuard = useWorkspaceUnloadGuard(open && dirty);
  const blocker = useBlocker({
    withResolver: true,
    disabled: !open,
    enableBeforeUnload: unloadGuard,
    shouldBlockFn: ({ current, next }) => {
      const a = current.search as Record<string, unknown>;
      const b = next.search as Record<string, unknown>;
      return (
        open &&
        (dirty || mutation.isPending) &&
        (current.pathname !== next.pathname ||
          a.projectId !== b.projectId ||
          a.applicationId !== b.applicationId)
      );
    },
  });
  const close = () => {
    if (mutation.isPending) return;
    if (form.formState.isDirty) setDiscard(true);
    else onOpenChange(false);
  };
  const rules = form.watch("rules");
  const targets = form.watch("services");
  return (
    <>
      <InspectorDrawer
        open={open}
        onClose={close}
        title={route ? t("projects.routesDialogTitleEdit") : t("projects.routesDialogTitleNew")}
        description={t("projects.routesDraftPrompt")}
        footer={
          <>
            <IqujiButton variant="secondary" disabled={mutation.isPending} onClick={close}>
              {t("common.cancel")}
            </IqujiButton>
            <IqujiButton
              loading={mutation.isPending}
              onClick={() => void form.handleSubmit((value) => mutation.mutate(value))()}
            >
              {t("common.save")}
            </IqujiButton>
          </>
        }
      >
        <div className="ops-form-stack">
          <Controller
            control={form.control}
            name="domainIds"
            render={({ field, fieldState }) => (
              <div>
                <IqujiMultiSelect
                  disabled={mutation.isPending}
                  label={t("projects.domainLabel")}
                  options={domains.map((item) => ({ label: item.name, value: item.id }))}
                  value={field.value}
                  onChange={field.onChange}
                  placeholder={t("projects.domainPlaceholder")}
                />
                {fieldState.error ? (
                  <p className="ops-field-error" role="alert">
                    {fieldState.error.message}
                  </p>
                ) : null}
              </div>
            )}
          />
          <Controller
            control={form.control}
            name="services"
            render={({ field, fieldState }) => (
              <div>
                <IqujiMultiSelect
                  disabled={mutation.isPending}
                  label={t("projects.targetApp")}
                  options={services.map((item) => ({
                    label: item.serviceName || item.slotKey,
                    value: item.slotKey,
                  }))}
                  value={field.value.map((item) => item.slotKey)}
                  onChange={(slots) =>
                    field.onChange(
                      slots.map((slotKey) => ({
                        slotKey,
                        portName:
                          field.value.find((item) => item.slotKey === slotKey)?.portName ??
                          ports.find((p) => p.protocol === "http")?.name,
                        weight:
                          slots.length === 1
                            ? 1
                            : (field.value.find((item) => item.slotKey === slotKey)?.weight ?? 1),
                      })),
                    )
                  }
                />
                {fieldState.error ? (
                  <p className="ops-field-error" role="alert">
                    {fieldState.error.message}
                  </p>
                ) : null}
              </div>
            )}
          />
          {ports.length > 0 &&
            targets.map((target, index) => (
              <Controller
                key={target.slotKey}
                control={form.control}
                name={`services.${index}.portName`}
                render={({ field }) => (
                  <IqujiField label={`${target.slotKey} · ${t("projects.httpPort")}`}>
                    <IqujiSelect
                      options={ports
                        .filter((p) => p.protocol === "http")
                        .map((p) => ({ label: `${p.name} · ${p.targetPort}`, value: p.name }))}
                      value={field.value ?? ""}
                      onValueChange={field.onChange}
                    />
                  </IqujiField>
                )}
              />
            ))}
          {targets.length > 1 ? (
            <section className="ops-form-section">
              <div className="ops-label-with-help">
                <h3 className="ops-form-section__title">{t("projects.routeWeightsTitle")}</h3>
                <FieldHelp
                  label={t("projects.routeWeightsTitle")}
                  help={[
                    t("projects.routeWeightsDescription"),
                    t("projects.routeWeightLabel"),
                  ].join(" ")}
                />
              </div>
              <div className="ops-form-grid">
                {targets.map((target, index) => (
                  <IqujiField
                    key={target.slotKey}
                    label={
                      services.find((item) => item.slotKey === target.slotKey)?.serviceName ||
                      target.slotKey
                    }
                    error={form.formState.errors.services?.[index]?.weight?.message}
                  >
                    <IqujiInput
                      type="number"
                      min={0}
                      max={2147483647}
                      step={1}
                      disabled={mutation.isPending}
                      {...form.register(`services.${index}.weight`, { valueAsNumber: true })}
                    />
                  </IqujiField>
                ))}
              </div>
            </section>
          ) : null}
          <section className="ops-form-section">
            <div className="ops-section-heading">
              <h3 className="ops-form-section__title">{t("projects.routesPathRules")}</h3>
              <IqujiButton
                compact
                variant="secondary"
                disabled={mutation.isPending}
                onClick={() =>
                  rows.append({ path: "/", pathHandling: "preserve", rewriteTargetPath: "" })
                }
              >
                {t("projects.routesAddRule")}
              </IqujiButton>
            </div>
            {rows.fields.map((row, index) => (
              <div className="ops-route-rule" key={row.id}>
                <IqujiField
                  label={t("projects.routesPath")}
                  error={form.formState.errors.rules?.[index]?.path?.message}
                >
                  <IqujiInput
                    {...form.register(`rules.${index}.path`)}
                    disabled={mutation.isPending}
                  />
                </IqujiField>
                <Controller
                  control={form.control}
                  name={`rules.${index}.pathHandling`}
                  render={({ field }) => (
                    <IqujiField label={t("projects.routesHandling")}>
                      <IqujiSelect
                        disabled={mutation.isPending}
                        value={field.value}
                        onValueChange={field.onChange}
                        options={[
                          { label: t("projects.routesHandlingPreserve"), value: "preserve" },
                          { label: t("projects.routesHandlingStrip"), value: "strip_prefix" },
                          { label: t("projects.routesHandlingRewrite"), value: "rewrite" },
                        ]}
                      />
                    </IqujiField>
                  )}
                />
                {rules[index]?.pathHandling === "rewrite" ? (
                  <IqujiField
                    label={t("projects.routesRewriteTarget")}
                    error={form.formState.errors.rules?.[index]?.rewriteTargetPath?.message}
                  >
                    <IqujiInput
                      {...form.register(`rules.${index}.rewriteTargetPath`)}
                      disabled={mutation.isPending}
                    />
                  </IqujiField>
                ) : null}
                <IqujiButton
                  compact
                  variant="ghost"
                  disabled={rows.fields.length === 1 || mutation.isPending}
                  onClick={() => rows.remove(index)}
                >
                  {t("common.remove")}
                </IqujiButton>
              </div>
            ))}
          </section>
        </div>
      </InspectorDrawer>
      <IqujiConfirmDialog
        open={discard || blocker.status === "blocked"}
        confirmDisabled={mutation.isPending}
        title={t("common.unsavedChanges")}
        cancelLabel={t("common.cancel")}
        confirmLabel={t("common.confirm")}
        closeLabel={t("common.close")}
        onOpenChange={(next) => {
          if (!next) {
            setDiscard(false);
            if (blocker.status === "blocked") blocker.reset();
          }
        }}
        onConfirm={() => {
          setDiscard(false);
          form.reset();
          if (blocker.status === "blocked") blocker.proceed();
          else onOpenChange(false);
        }}
      />
    </>
  );
}
