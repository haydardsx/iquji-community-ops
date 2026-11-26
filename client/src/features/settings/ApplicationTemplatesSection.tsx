import {
  IconButton,
  IqujiBadge,
  IqujiButton,
  IqujiConfirmDialog,
  IqujiDialog,
  IqujiEmptyState,
  IqujiField,
  IqujiInput,
  IqujiLoadingState,
  IqujiSelect,
  IqujiSettingsContent,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { type ApplicationTemplate, fetchApplicationTemplates } from "@/features/projects/api";
import { fetchJson } from "@/shared/api/client";
import { useI18n } from "@/shared/i18n";
import { FileSelector } from "@/shared/ui/file-selector";
import { useOperationError, useOperationNotice } from "@/shared/ui/use-operation-feedback";
import {
  ApplicationTemplateDrawer,
  resolveTemplateIcon,
  type TemplateDraft,
} from "./ApplicationTemplateDrawer";
import "./ApplicationTemplatesSection.css";

const endpoint = "projects/application-templates";
const queryKey = ["application-templates"];
const emptyConfig = {
  sourceConfig: { provider: "image", image: "" },
  swarmRuntime: { ports: [] },
};

type Preview = { revision: string; count: number; conflicts: string[] };

function extractTemplateMeta(config?: Record<string, unknown>) {
  if (!config) return { image: null, portsCount: 0, envCount: 0 };
  const source = (config.sourceConfig as Record<string, unknown>) ?? {};
  const image =
    typeof source.image === "string" && source.image.trim() ? source.image.trim() : null;
  const swarm = (config.swarmRuntime as Record<string, unknown>) ?? {};
  const ports = Array.isArray(swarm.ports) ? swarm.ports.length : 0;
  const env = Array.isArray(config.environmentVariables) ? config.environmentVariables.length : 0;
  return { image, portsCount: ports, envCount: env };
}

export function ApplicationTemplatesSection() {
  const { t } = useI18n();
  const client = useQueryClient();
  const notify = useOperationNotice();
  const catalog = useQuery({ queryKey, queryFn: fetchApplicationTemplates });
  const [search, setSearch] = useState("");
  const [draft, setDraft] = useState<TemplateDraft | null>(null);
  const [deleted, setDeleted] = useState<ApplicationTemplate | null>(null);
  const [importing, setImporting] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [preview, setPreview] = useState<{ document: unknown; result: Preview } | null>(null);
  const [policy, setPolicy] = useState("skip");

  const openEditor = (item?: ApplicationTemplate) =>
    setDraft({
      original: item?.id ?? null,
      id: item?.id ?? "",
      name: item?.name ?? "",
      description: item?.description ?? "",
      config: JSON.stringify(item?.config ?? emptyConfig, null, 2),
    });

  const mutation = useMutation({
    mutationFn: async (
      payload:
        | { action: "save"; item: TemplateDraft }
        | { action: "delete" }
        | { action: "preview" }
        | { action: "import" },
    ) => {
      if (payload.action === "save") {
        const item = {
          id: payload.item.id.trim(),
          name: payload.item.name.trim(),
          description: payload.item.description,
          config: JSON.parse(payload.item.config),
        };
        await fetchJson(payload.item.original ? `${endpoint}/${payload.item.original}` : endpoint, {
          method: payload.item.original ? "PUT" : "POST",
          body: item,
        });
        setDraft(null);
      } else if (payload.action === "delete" && deleted) {
        await fetchJson(`${endpoint}/${deleted.id}`, { method: "DELETE" });
        setDeleted(null);
      } else if (payload.action === "preview" && file) {
        const document: unknown = JSON.parse(await file.text());
        const result = await fetchJson<Preview>(`${endpoint}/import/preview`, {
          method: "POST",
          body: { document },
        });
        setPreview({ document, result });
        return;
      } else if (payload.action === "import" && preview) {
        await fetchJson(`${endpoint}/import/apply`, {
          method: "POST",
          body: { document: preview.document, revision: preview.result.revision, policy },
        });
        setImporting(false);
      } else return;
      await client.invalidateQueries({ queryKey });
      notify(t("common.opCompleted"));
    },
  });

  useOperationError(catalog.error ?? mutation.error, t("common.opFailed"));

  const exporting = () => {
    if (!catalog.data) return;
    const link = document.createElement("a");
    link.download = "application-templates.json";
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(catalog.data, null, 2)], { type: "application/json" }),
    );
    link.href = url;
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  };

  const allItems = catalog.data?.items ?? [];
  const filteredItems = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return allItems;
    return allItems.filter(
      (item) =>
        item.name.toLowerCase().includes(q) ||
        item.id.toLowerCase().includes(q) ||
        item.description.toLowerCase().includes(q),
    );
  }, [allItems, search]);

  const headerActions = (
    <div className="ops-row">
      <IqujiButton
        compact
        iconStart={<IqujiIcon name="hard-drive-download" decorative size={14} />}
        onClick={() => {
          setFile(null);
          setPreview(null);
          setPolicy("skip");
          setImporting(true);
        }}
        variant="secondary"
      >
        {t("templates.import")}
      </IqujiButton>
      <IqujiButton
        compact
        disabled={!catalog.data || allItems.length === 0}
        iconStart={<IqujiIcon name="download" decorative size={14} />}
        onClick={exporting}
        variant="secondary"
      >
        {t("templates.export")}
      </IqujiButton>
      <IqujiButton
        compact
        iconStart={<IqujiIcon name="plus" decorative size={14} />}
        onClick={() => openEditor()}
      >
        {t("common.add")}
      </IqujiButton>
    </div>
  );

  return (
    <IqujiSettingsContent
      actions={headerActions}
      description={t("templates.description")}
      title={t("templates.title")}
    >
      <div className="ops-templates-toolbar">
        <div className="ops-templates-toolbar__search">
          <IqujiInput
            aria-label={t("templates.searchPlaceholder")}
            onChange={(e) => setSearch(e.currentTarget.value)}
            placeholder={t("templates.searchPlaceholder")}
            value={search}
          />
        </div>
        <div className="ops-templates-toolbar__stats">
          {search.trim() ? (
            <span>
              {t("templates.filteredBadge", {
                filtered: filteredItems.length,
                total: allItems.length,
              })}
            </span>
          ) : (
            <span>{t("templates.countBadge", { count: allItems.length })}</span>
          )}
        </div>
      </div>

      {catalog.isPending ? <IqujiLoadingState label={t("common.loading")} /> : null}

      {!catalog.isPending && allItems.length === 0 ? (
        <IqujiEmptyState
          action={
            <IqujiButton
              iconStart={<IqujiIcon decorative name="plus" size={14} />}
              onClick={() => openEditor()}
            >
              {t("common.add")}
            </IqujiButton>
          }
          body={t("templates.emptyCatalogDesc")}
          icon={<IqujiIcon decorative name="boxes" size={32} />}
          title={t("templates.emptyCatalog")}
        />
      ) : null}

      {!catalog.isPending && allItems.length > 0 && filteredItems.length === 0 ? (
        <IqujiEmptyState
          action={
            <IqujiButton onClick={() => setSearch("")} variant="secondary">
              {t("templates.clearSearch")}
            </IqujiButton>
          }
          body={t("templates.noMatchDesc")}
          icon={<IqujiIcon decorative name="search" size={28} />}
          title={t("templates.noMatch")}
        />
      ) : null}

      <div className="ops-templates-list">
        {filteredItems.map((item) => {
          const meta = extractTemplateMeta(item.config);
          const icon = resolveTemplateIcon(item.id);
          return (
            <div className="ops-template-card" key={item.id}>
              <div className="ops-template-card__icon">
                <IqujiIcon decorative name={icon} size={20} />
              </div>

              <div className="ops-template-card__body">
                <div className="ops-template-card__header">
                  <span className="ops-template-card__name">{item.name}</span>
                  <IqujiBadge tone="neutral" className="ops-font-mono">
                    {item.id}
                  </IqujiBadge>
                  {meta.image ? (
                    <span className="ops-template-tag">
                      <IqujiIcon decorative name="container" size={12} />
                      <span>{meta.image}</span>
                    </span>
                  ) : null}
                </div>

                {item.description ? (
                  <p className="ops-template-card__desc">{item.description}</p>
                ) : null}

                {(meta.portsCount > 0 || meta.envCount > 0) && (
                  <div className="ops-template-card__tags">
                    {meta.portsCount > 0 ? (
                      <span className="ops-template-tag">
                        <IqujiIcon decorative name="network" size={11} />
                        <span>{t("templates.portsCount", { count: meta.portsCount })}</span>
                      </span>
                    ) : null}
                    {meta.envCount > 0 ? (
                      <span className="ops-template-tag">
                        <IqujiIcon decorative name="sliders-horizontal" size={11} />
                        <span>{t("templates.variablesCount", { count: meta.envCount })}</span>
                      </span>
                    ) : null}
                  </div>
                )}
              </div>

              <div className="ops-template-card__actions">
                <IconButton
                  icon={<IqujiIcon decorative name="pencil" />}
                  label={t("common.edit")}
                  onClick={() => openEditor(item)}
                />
                <IconButton
                  icon={<IqujiIcon decorative name="trash" />}
                  label={t("common.delete")}
                  onClick={() => setDeleted(item)}
                />
              </div>
            </div>
          );
        })}
      </div>

      <ApplicationTemplateDrawer
        draft={draft}
        loading={mutation.isPending}
        onClose={() => {
          if (!mutation.isPending) setDraft(null);
        }}
        onSave={(savedDraft) => mutation.mutate({ action: "save", item: savedDraft })}
        open={Boolean(draft)}
      />

      <IqujiDialog
        actions={
          <IqujiButton
            disabled={!file}
            loading={mutation.isPending}
            onClick={() => mutation.mutate(preview ? { action: "import" } : { action: "preview" })}
          >
            {t(preview ? "templates.import" : "templates.preview")}
          </IqujiButton>
        }
        closeLabel={t("common.close")}
        onOpenChange={(open) => {
          if (!mutation.isPending) setImporting(open);
        }}
        open={importing}
        title={t("templates.import")}
      >
        <FileSelector
          accept=".json,application/json"
          ariaLabel={t("templates.file")}
          buttonLabel={t("templates.file")}
          disabled={mutation.isPending}
          file={file}
          onFileChange={(next) => {
            setFile(next);
            setPreview(null);
          }}
          placeholder={t("templates.fileHint")}
        />
        {preview && <p>{t("templates.count", { count: preview.result.count })}</p>}
        {preview && preview.result.conflicts.length > 0 && (
          <IqujiField
            label={t("templates.conflicts", { ids: preview.result.conflicts.join(", ") })}
          >
            <IqujiSelect
              onValueChange={(next) => setPolicy(next ?? "skip")}
              options={["skip", "overwrite"].map((value) => ({
                value,
                label: t(value === "skip" ? "templates.skip" : "templates.overwrite"),
              }))}
              value={policy}
            />
          </IqujiField>
        )}
        {preview && (
          <IqujiButton
            compact
            onClick={() => mutation.mutate({ action: "preview" })}
            variant="ghost"
          >
            {t("templates.preview")}
          </IqujiButton>
        )}
      </IqujiDialog>

      <IqujiConfirmDialog
        cancelLabel={t("common.cancel")}
        closeLabel={t("common.close")}
        confirmLabel={t("common.delete")}
        confirmLoading={mutation.isPending}
        description={t("templates.confirmDelete", { name: deleted?.name ?? "" })}
        onConfirm={() => mutation.mutate({ action: "delete" })}
        onOpenChange={(open) => {
          if (!open) setDeleted(null);
        }}
        open={Boolean(deleted)}
        title={t("common.delete")}
      />
    </IqujiSettingsContent>
  );
}
