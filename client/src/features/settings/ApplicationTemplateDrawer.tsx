import { IqujiBadge, IqujiButton, IqujiField, IqujiInput } from "@iquji-community/web-component";
import { IqujiTextEditor } from "@iquji-community/web-editor/text";
import { IqujiIcon, type IqujiIconName } from "@iquji-community/web-icons";
import { useEffect, useMemo, useState } from "react";
import { useI18n } from "@/shared/i18n";
import { InspectorDrawer } from "@/shared/ui/inspector-drawer";
import "./ApplicationTemplatesSection.css";

export interface TemplateDraft {
  original: string | null;
  id: string;
  name: string;
  description: string;
  config: string;
}

export interface ApplicationTemplateDrawerProps {
  open: boolean;
  draft: TemplateDraft | null;
  loading?: boolean;
  onClose: () => void;
  onSave: (item: TemplateDraft) => void;
}

const ID_PATTERN = /^[a-z0-9][a-z0-9-_]*$/;

export function resolveTemplateIcon(id: string): IqujiIconName {
  const lower = id.toLowerCase();
  if (lower.includes("git")) return "code";
  if (
    lower.includes("db") ||
    lower.includes("sql") ||
    lower.includes("mongo") ||
    lower.includes("redis")
  ) {
    return "server";
  }
  return "boxes";
}

export function ApplicationTemplateDrawer({
  open,
  draft,
  loading = false,
  onClose,
  onSave,
}: ApplicationTemplateDrawerProps) {
  const { t } = useI18n();
  const [form, setForm] = useState<TemplateDraft>(
    () =>
      draft ?? {
        original: null,
        id: "",
        name: "",
        description: "",
        config: "{}",
      },
  );

  useEffect(() => {
    if (draft) {
      setForm(draft);
    }
  }, [draft]);

  const jsonParseResult = useMemo(() => {
    try {
      const parsed = JSON.parse(form.config);
      return { ok: true, data: parsed, error: null };
    } catch (err) {
      return {
        ok: false,
        data: null,
        error: err instanceof Error ? err.message : String(err),
      };
    }
  }, [form.config]);

  const idValid = useMemo(() => {
    const trimmed = form.id.trim();
    if (!trimmed) return false;
    return ID_PATTERN.test(trimmed);
  }, [form.id]);

  const isEditing = Boolean(form.original);
  const canSave =
    !loading && Boolean(form.name.trim()) && (isEditing ? true : idValid) && jsonParseResult.ok;

  const handleFormat = () => {
    if (jsonParseResult.ok && jsonParseResult.data) {
      setForm((prev) => ({
        ...prev,
        config: JSON.stringify(jsonParseResult.data, null, 2),
      }));
    }
  };

  const handleSave = () => {
    if (!canSave) return;
    onSave({
      original: form.original,
      id: form.id.trim(),
      name: form.name.trim(),
      description: form.description.trim(),
      config: form.config,
    });
  };

  const iconName = resolveTemplateIcon(form.id);

  return (
    <InspectorDrawer
      open={open && Boolean(draft)}
      size="lg"
      title={
        isEditing
          ? t("templates.editTemplateTitle", { name: form.name || form.id })
          : t("templates.newTemplateTitle")
      }
      description={isEditing ? t("templates.editTemplateDesc") : t("templates.newTemplateDesc")}
      onClose={onClose}
      footer={
        <>
          <IqujiButton disabled={loading} onClick={onClose} variant="secondary">
            {t("common.cancel")}
          </IqujiButton>
          <IqujiButton disabled={!canSave} loading={loading} onClick={handleSave}>
            {t("common.save")}
          </IqujiButton>
        </>
      }
    >
      <div className="ops-form-stack">
        <div className="ops-form-hero">
          <div className="ops-form-hero__icon">
            <IqujiIcon decorative name={iconName} size={22} />
          </div>
          <div className="ops-form-hero__body">
            <div className="ops-form-hero__headline">
              <h4 className="ops-form-hero__title">
                {isEditing ? form.name || form.id : t("templates.newTemplateTitle")}
              </h4>
              {form.id ? (
                <IqujiBadge tone="neutral" className="ops-font-mono">
                  {form.id}
                </IqujiBadge>
              ) : null}
            </div>
            <p className="ops-form-hero__desc">
              {isEditing ? t("templates.editTemplateDesc") : t("templates.newTemplateDesc")}
            </p>
          </div>
        </div>

        <section className="ops-form-section">
          <div className="ops-form-section__header">
            <h5 className="ops-form-section__title">{t("templates.basicInfo")}</h5>
          </div>
          <div className="ops-form-stack">
            <IqujiField
              label={
                <span className="ops-row">
                  <span>{t("templates.id")}</span>
                  {isEditing ? (
                    <IqujiBadge tone="neutral">{t("templates.idLocked")}</IqujiBadge>
                  ) : null}
                </span>
              }
              description={isEditing ? undefined : t("templates.idHelp")}
              error={
                !isEditing && form.id.trim() && !idValid ? t("templates.idInvalid") : undefined
              }
              required={!isEditing}
            >
              <IqujiInput
                disabled={isEditing || loading}
                onChange={(e) => setForm((prev) => ({ ...prev, id: e.currentTarget.value }))}
                placeholder="my-service"
                value={form.id}
              />
            </IqujiField>

            <IqujiField label={t("common.name")} required>
              <IqujiInput
                disabled={loading}
                onChange={(e) => setForm((prev) => ({ ...prev, name: e.currentTarget.value }))}
                placeholder="e.g. Gitea Service"
                value={form.name}
              />
            </IqujiField>

            <IqujiField label={t("common.description")}>
              <IqujiInput
                disabled={loading}
                onChange={(e) =>
                  setForm((prev) => ({
                    ...prev,
                    description: e.currentTarget.value,
                  }))
                }
                placeholder={t("templates.description")}
                value={form.description}
              />
            </IqujiField>
          </div>
        </section>

        <section className="ops-form-section">
          <div className="ops-form-section__header">
            <h5 className="ops-form-section__title">{t("templates.deployConfig")}</h5>
            <p className="ops-form-section__description">{t("templates.deployConfigHelp")}</p>
          </div>

          <div className="ops-template-drawer__editor-wrapper">
            <div className="ops-template-drawer__editor-toolbar">
              <div className="ops-template-drawer__editor-status">
                {jsonParseResult.ok ? (
                  <IqujiBadge tone="success">
                    <IqujiIcon decorative name="check" size={12} />
                    <span>{t("templates.jsonValid")}</span>
                  </IqujiBadge>
                ) : (
                  <IqujiBadge tone="danger">
                    <IqujiIcon decorative name="circle-alert" size={12} />
                    <span>{t("templates.jsonInvalid")}</span>
                  </IqujiBadge>
                )}
              </div>
              <IqujiButton
                compact
                disabled={!jsonParseResult.ok || loading}
                iconStart={<IqujiIcon decorative name="code" size={14} />}
                onClick={handleFormat}
                variant="secondary"
              >
                {t("templates.formatJson")}
              </IqujiButton>
            </div>

            <IqujiTextEditor
              ariaLabel={t("templates.config")}
              documentKey={form.original ?? "new-template"}
              labels={{
                edit: t("operations.editorEdit"),
                preview: t("operations.editorPreview"),
                format: t("operations.editorFormat"),
                invalid: t("operations.editorInvalid"),
              }}
              onChange={(config) => setForm((prev) => ({ ...prev, config }))}
              preset="json"
              value={form.config}
            />
          </div>

          {jsonParseResult.error ? (
            <div className="ops-template-drawer__json-error">{jsonParseResult.error}</div>
          ) : null}
        </section>
      </div>
    </InspectorDrawer>
  );
}
