import {
  IconButton,
  IqujiBadge,
  IqujiButton,
  IqujiEmptyState,
  IqujiInput,
  IqujiSelect,
  IqujiTooltip,
} from "@iquji-community/web-component";
import { IqujiIcon } from "@iquji-community/web-icons";
import { useMemo, useState } from "react";
import { useI18n } from "@/shared/i18n";
import {
  type Command,
  changePermissionLevel,
  describeScopeSummary,
  type Level,
  type PolicyDraft,
  permissionLevel,
} from "./automation-permissions";
import { McpScopeDialog } from "./McpScopeDialog";

export interface AutomationPermissionsProps {
  commands: Command[];
  draft: PolicyDraft;
  setDraft: (draft: PolicyDraft) => void;
  disabled: boolean;
}

/**
 * MCP 客户端抽屉内的平整工具权限清单
 */
export function AutomationPermissions({
  commands,
  draft,
  setDraft,
  disabled,
}: AutomationPermissionsProps) {
  const { t } = useI18n();
  const [searchQuery, setSearchQuery] = useState("");
  const [groupFilter, setGroupFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState("all");
  const [scopeCommand, setScopeCommand] = useState<Command | null>(null);

  const editable = useMemo(() => commands.filter((cmd) => cmd.name !== "action_guide"), [commands]);

  const groups = useMemo(() => [...new Set(editable.map((cmd) => cmd.group))], [editable]);

  const filteredCommands = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    return editable.filter((cmd) => {
      if (groupFilter !== "all" && cmd.group !== groupFilter) return false;

      const scopes = draft[cmd.name] ?? {};
      const level = permissionLevel(scopes);
      if (statusFilter === "granted" && level === "disabled") return false;
      if (statusFilter === "disabled" && level !== "disabled") return false;
      if (statusFilter === "guide" && (!cmd.guide || level !== "guide")) return false;
      if (statusFilter === "direct" && (!cmd.guide || !scopes.direct)) return false;
      if (statusFilter === "mixed" && level !== "mixed") return false;

      if (q) {
        const title = t(`automation.label.${cmd.name}`).toLowerCase();
        const desc = t(`automation.effect.${cmd.name}`).toLowerCase();
        const name = cmd.name.toLowerCase();
        const groupLabel = t(`automation.group.${cmd.group}`).toLowerCase();
        return title.includes(q) || desc.includes(q) || name.includes(q) || groupLabel.includes(q);
      }
      return true;
    });
  }, [editable, groupFilter, statusFilter, draft, searchQuery, t]);

  const activeGroups = useMemo(
    () => [...new Set(filteredCommands.map((cmd) => cmd.group))],
    [filteredCommands],
  );

  const handleLevelChange = (cmd: Command, next: Level) => {
    const updated = changePermissionLevel(draft, cmd.name, next);
    setDraft(updated);
    if (next === "mixed" && cmd.fields?.includes("target")) {
      setScopeCommand(cmd);
    }
  };

  return (
    <div className="ops-mcp-permissions">
      <div className="ops-mcp-toolbar">
        <div className="ops-mcp-toolbar__search">
          <IqujiInput
            aria-label={t("automation.searchPlaceholder")}
            onChange={(e) => setSearchQuery(e.currentTarget.value)}
            placeholder={t("automation.searchPlaceholder")}
            value={searchQuery}
          />
        </div>
        <div className="ops-mcp-toolbar__filters">
          <IqujiSelect
            aria-label={t("automation.filterAllGroups")}
            compact
            onValueChange={(v) => setGroupFilter(v ?? "all")}
            options={[
              { value: "all", label: t("automation.filterAllGroups") },
              ...groups.map((g) => ({
                value: g,
                label: t(`automation.group.${g}`),
              })),
            ]}
            value={groupFilter}
          />
          <IqujiSelect
            aria-label={t("automation.filterAllLevels")}
            compact
            onValueChange={(v) => setStatusFilter(v ?? "all")}
            options={[
              { value: "all", label: t("automation.filterAllLevels") },
              { value: "granted", label: t("automation.filterGranted") },
              { value: "direct", label: t("automation.filterDirect") },
              { value: "guide", label: t("automation.filterGuide") },
              { value: "mixed", label: t("automation.filterMixed") },
              { value: "disabled", label: t("automation.filterDisabled") },
            ]}
            value={statusFilter}
          />
        </div>
      </div>

      {filteredCommands.length === 0 ? (
        <IqujiEmptyState
          action={
            <IqujiButton
              compact
              onClick={() => {
                setSearchQuery("");
                setGroupFilter("all");
                setStatusFilter("all");
              }}
              variant="secondary"
            >
              {t("automation.clearFilters")}
            </IqujiButton>
          }
          icon={<IqujiIcon decorative name="search" size={24} />}
          title={t("automation.noMatchingCommands")}
        />
      ) : (
        <div className="ops-mcp-groups">
          {activeGroups.map((group) => {
            const groupCmds = filteredCommands.filter((cmd) => cmd.group === group);
            return (
              <section className="ops-mcp-group" key={group}>
                <header className="ops-mcp-group__header">
                  <span>{t(`automation.group.${group}`)}</span>
                  <span className="ops-mcp-group__count">{groupCmds.length}</span>
                </header>

                <div className="ops-flat-list">
                  {groupCmds.map((cmd) => {
                    const scopes = draft[cmd.name] ?? {};
                    const level = permissionLevel(scopes);
                    const hasTargets = Boolean(cmd.fields?.includes("target"));
                    const isActive = level !== "disabled";
                    const scopeMeta = isActive ? describeScopeSummary(scopes, cmd, t) : null;

                    const levelOptions = cmd.guide
                      ? [
                          { value: "disabled", label: t("automation.denied") },
                          { value: "guide", label: t("automation.guideOnly") },
                          { value: "direct", label: t("automation.directAction") },
                          ...(hasTargets
                            ? [{ value: "mixed", label: t("automation.mixedModes") }]
                            : []),
                        ]
                      : [
                          { value: "disabled", label: t("automation.denied") },
                          { value: "direct", label: t("automation.enabled") },
                        ];

                    return (
                      <div className="ops-mcp-row" key={cmd.name}>
                        <div className="ops-mcp-row__info">
                          <span className="ops-mcp-row__title">
                            {t(`automation.label.${cmd.name}`)}
                          </span>
                          <code className="ops-mcp-row__code">{cmd.name}</code>
                          {cmd.confirm ? (
                            <IqujiBadge tone="warning">
                              {t("automation.requiresConfirmBadge")}
                            </IqujiBadge>
                          ) : null}
                          {hasTargets && scopeMeta ? (
                            <IqujiBadge tone={scopeMeta.invalid ? "warning" : "neutral"}>
                              {scopeMeta.label}
                            </IqujiBadge>
                          ) : null}
                          <IqujiTooltip content={t(`automation.effect.${cmd.name}`)}>
                            <IconButton
                              compact
                              icon={<IqujiIcon decorative name="circle-info" size={14} />}
                              label={t(`automation.effect.${cmd.name}`)}
                              variant="ghost"
                            />
                          </IqujiTooltip>
                        </div>

                        <div className="ops-mcp-row__controls">
                          {hasTargets && isActive ? (
                            <IqujiButton
                              compact
                              disabled={disabled}
                              iconStart={
                                <IqujiIcon decorative name="sliders-horizontal" size={13} />
                              }
                              onClick={() => setScopeCommand(cmd)}
                              variant="secondary"
                            >
                              {t("automation.scopeBtn")}
                            </IqujiButton>
                          ) : null}

                          <div className="ops-mcp-row__select">
                            <IqujiSelect
                              aria-label={t(`automation.label.${cmd.name}`)}
                              compact
                              disabled={disabled}
                              onValueChange={(v) => {
                                if (
                                  v === "disabled" ||
                                  v === "guide" ||
                                  v === "direct" ||
                                  v === "mixed"
                                ) {
                                  handleLevelChange(cmd, v);
                                }
                              }}
                              options={levelOptions}
                              value={level}
                            />
                          </div>
                        </div>
                      </div>
                    );
                  })}
                </div>
              </section>
            );
          })}
        </div>
      )}

      <McpScopeDialog
        command={scopeCommand}
        disabled={disabled}
        key={scopeCommand?.name ?? "closed"}
        onApply={(next) => {
          if (!scopeCommand) return;
          setDraft({ ...draft, [scopeCommand.name]: next });
        }}
        onClose={() => setScopeCommand(null)}
        scopes={scopeCommand ? draft[scopeCommand.name] : undefined}
      />
    </div>
  );
}
