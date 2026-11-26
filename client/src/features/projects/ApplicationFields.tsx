import { IqujiInput, IqujiSelect } from "@iquji-community/web-component";
import { Controller, type Path, type UseFormReturn } from "react-hook-form";
import { FieldWithHelp } from "@/shared/ui/field-help";
import type { ApplicationForm } from "./model";
export type ConfigFormProps = { form: UseFormReturn<ApplicationForm>; disabled: boolean };
export function FormText({
  form,
  disabled,
  name,
  label,
  help,
  placeholder,
  type = "text",
  required = false,
}: {
  name: Path<ApplicationForm>;
  label: string;
  help?: string;
  placeholder?: string;
  type?: "text" | "number";
  required?: boolean;
} & ConfigFormProps) {
  const error = form.getFieldState(name, form.formState).error;
  return (
    <FieldWithHelp label={label} help={help} error={error?.message} required={required}>
      <IqujiInput
        data-ops-guide-field={name}
        {...form.register(
          name,
          type === "number"
            ? { setValueAs: (value) => (value === "" ? undefined : Number(value)) }
            : {},
        )}
        disabled={disabled}
        type={type}
        placeholder={placeholder}
      />
    </FieldWithHelp>
  );
}
export function FormSelect({
  form,
  disabled,
  name,
  label,
  options,
  help,
  guideOpen,
}: {
  guideOpen?: string;
  name: Path<ApplicationForm>;
  label: string;
  help?: string;
  options: Array<{ label: string; value: string; disabled?: boolean }>;
} & ConfigFormProps) {
  return (
    <Controller
      control={form.control}
      name={name}
      render={({ field, fieldState }) => (
        <FieldWithHelp label={label} help={help} error={fieldState.error?.message}>
          <IqujiSelect
            data-ops-guide-open={guideOpen}
            data-ops-guide-field={name}
            disabled={disabled}
            options={options}
            value={typeof field.value === "string" ? field.value : ""}
            onValueChange={(value) => field.onChange(value ?? "")}
            onBlur={field.onBlur}
          />
        </FieldWithHelp>
      )}
    />
  );
}
