import { IqujiTextEditor } from "@iquji-community/web-editor/text";
import { useI18n } from "@/shared/i18n";

export function JsonInspector({
  value,
  label,
  documentKey,
}: {
  value: unknown;
  label: string;
  documentKey?: string;
}) {
  const { t } = useI18n();
  return (
    <IqujiTextEditor
      ariaLabel={label}
      className="ops-json-inspector"
      documentKey={documentKey}
      labels={{
        edit: t("operations.editorEdit"),
        preview: t("operations.editorPreview"),
        format: t("operations.editorFormat"),
        invalid: t("operations.editorInvalid"),
      }}
      onChange={() => undefined}
      preset="json"
      readOnly
      value={JSON.stringify(value, null, 2)}
    />
  );
}
