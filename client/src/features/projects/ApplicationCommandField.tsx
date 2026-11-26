import { IqujiBadge } from "@iquji-community/web-component";
import { IQUJI_EDITOR_PRESETS, IqujiTextEditor } from "@iquji-community/web-editor/text";
import type { IqujiIconName } from "@iquji-community/web-icons";
import { useController } from "react-hook-form";
import { useI18n } from "@/shared/i18n";
import { SectionPanel } from "@/shared/ui/section-panel";
import type { ConfigFormProps } from "./ApplicationFields";
import "./ApplicationSourceSection.css";

type CommandFieldName =
  | "source.command"
  | "command"
  | "initializeCommand"
  | "healthCommand"
  | "logCommand";

const preset = IQUJI_EDITOR_PRESETS.filter((item) => item !== "richText").find(
  (item) => String(item) === "shell",
);

const commandMeta: Record<
  CommandFieldName,
  {
    icon: IqujiIconName;
    iconTone: "accent" | "neutral" | "positive" | "warning";
    descKey: string;
    defaultDesc: string;
    badge: string;
  }
> = {
  command: {
    icon: "terminal",
    iconTone: "accent",
    descKey: "projects.commandDesc",
    defaultDesc: "覆盖容器镜像默认的入口或启动指令，指定服务进程运行参数。",
    badge: "Shell",
  },
  logCommand: {
    icon: "terminal",
    iconTone: "neutral",
    descKey: "projects.logCommandDesc",
    defaultDesc: "手动查询时，在所选运行副本内执行并显示输出。",
    badge: "Shell",
  },
  healthCommand: {
    icon: "activity",
    iconTone: "positive",
    descKey: "projects.healthCommandDesc",
    defaultDesc: "在容器内周期性执行探针命令，用于健康检测与容器就绪判定。",
    badge: "Probe",
  },
  initializeCommand: {
    icon: "rotate-ccw",
    iconTone: "neutral",
    descKey: "projects.initializeCommandDesc",
    defaultDesc: "首次部署或实例启动时执行的幂等迁移与环境初始化脚本。",
    badge: "Init Script",
  },
  "source.command": {
    icon: "terminal",
    iconTone: "accent",
    descKey: "projects.imageDefaultCommandDesc",
    defaultDesc: "包管理器安装后容器启动时执行的入口脚本与运行命令。",
    badge: "Shell",
  },
};

export function ApplicationCommandField({
  form,
  disabled,
  name,
  label,
  required,
  variant = "panel",
}: ConfigFormProps & {
  name: CommandFieldName;
  label: string;
  required?: boolean;
  variant?: "panel" | "embedded";
}) {
  const { t } = useI18n();
  const { field, fieldState } = useController({ control: form.control, name });
  const meta = commandMeta[name];
  const description = t(meta.descKey) || meta.defaultDesc;

  const titleNode = (
    <span>
      {label}
      {required ? (
        <span className="ops-required" aria-hidden="true">
          {" "}
          *
        </span>
      ) : null}
    </span>
  );

  const badgeNode = (
    <IqujiBadge tone={meta.iconTone === "positive" ? "success" : "neutral"}>
      {meta.badge}
    </IqujiBadge>
  );

  const editorBody = (
    <div
      data-ops-guide-action={
        name === "logCommand" && !disabled ? "application_log_configure" : undefined
      }
      className="ops-command-field__body"
    >
      <IqujiTextEditor
        ariaLabel={label}
        className="ops-command-editor"
        documentKey={name}
        labels={{
          edit: t("operations.editorEdit"),
          preview: t("operations.editorPreview"),
          format: t("operations.editorFormat"),
          invalid: t("projects.invalidScript"),
        }}
        value={field.value?.text ?? ""}
        onChange={(text) => field.onChange({ ...field.value, text })}
        preset={preset ?? "text"}
        readOnly={disabled}
      />
      {fieldState.error?.message ? (
        <p className="ops-field-error" role="alert">
          {fieldState.error.message}
        </p>
      ) : null}
    </div>
  );

  if (variant === "embedded") {
    return (
      <div className="ops-command-embedded">
        <div className="ops-command-embedded__header">
          <div className="ops-command-embedded__headline">
            <span className="ops-command-embedded__label">{titleNode}</span>
            {badgeNode}
          </div>
          <p className="ops-command-embedded__desc">{description}</p>
        </div>
        {editorBody}
      </div>
    );
  }

  return (
    <SectionPanel
      className="ops-command-card"
      icon={meta.icon}
      iconTone={meta.iconTone}
      title={titleNode}
      description={description}
      badge={badgeNode}
    >
      {editorBody}
    </SectionPanel>
  );
}
