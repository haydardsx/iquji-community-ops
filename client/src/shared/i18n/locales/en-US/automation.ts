import type { zhCNAutomation } from "../zh-CN/automation";

export const enUSAutomation: { [K in keyof typeof zhCNAutomation]: string } = {
  "automation.intro": "Manage local MCP clients and their tool permissions.",
  "automation.desktopTitle": "Desktop app required",
  "automation.newClient": "New client",
  "automation.editClient": "Edit client",
  "automation.emptyTitle": "No MCP clients",
  "automation.emptyBody":
    "Create a client to assign tool permissions and generate a connection config.",
  "automation.colClient": "Client",
  "automation.colPermissions": "Permissions",
  "automation.colActions": "Actions",
  "automation.clientPlaceholder": "e.g. Claude Desktop, Cursor",
  "automation.toolsSectionTitle": "Tool Permissions",
  "automation.grantedRatio": "{granted} / {total} authorized",
  "automation.summaryBreakdown": "{direct} direct · {guide} guide",
  "automation.noPermissions": "No authorized tools",

  "automation.guideOnly": "Guide only",
  "automation.directAction": "Direct",
  "automation.mixedModes": "By target",
  "automation.enabled": "Allowed",
  "automation.denied": "Disabled",
  "automation.guideScope": "Guide-only targets",
  "automation.directScope": "Direct targets",
  "automation.directCoversAll":
    "Direct access already covers all targets, including guide targets.",
  "automation.directOverlap": "Targets with both guide and direct access: {targets}.",
  "automation.effectiveExceptions": "Preapproved: {targets}",
  "automation.scope": "Scope",
  "automation.allTargets": "All targets",
  "automation.specificTargets": "Specified targets",
  "automation.targetList": "Allowed target IDs",
  "automation.targetListHint": "One exact ID per line. Wildcards are not supported.",
  "automation.approvedTargets": "Preapproved target IDs",
  "automation.approvedHint": "Leave blank to confirm every time; one ID per line",
  "automation.invalidScope":
    "Enter valid exact target IDs. Preapproved IDs must be within the allowed scope.",
  "automation.savePermissions": "Save changes",
  "automation.scopeBtn": "Scope",
  "automation.scopeDialogTitle": "Target Scope · {name}",
  "automation.scopeAllSummary": "All targets",
  "automation.scopeLimitedSummary": "{count} targets",
  "automation.scopePreapprovedSummary": "{count} preapproved",
  "automation.scopeNeedTargets": "Targets required",

  "automation.revokeConfirmTitle": "Revoke client?",
  "automation.revokeConfirmDesc":
    "Revoking “{name}” immediately invalidates its access token and blocks connected sessions.",
  "automation.configDialogTitle": "Connection Configuration",
  "automation.configGeneratedTip":
    "This config includes the access token and is only shown once. Copy and store it securely now.",

  "automation.searchPlaceholder": "Search tools by name or ID...",
  "automation.filterAllGroups": "All groups",
  "automation.filterAllLevels": "All statuses",
  "automation.filterGranted": "Authorized",
  "automation.filterDisabled": "Disabled",
  "automation.filterGuide": "Guide only",
  "automation.filterDirect": "Direct",
  "automation.filterMixed": "By target",
  "automation.requiresConfirmBadge": "Confirm",
  "automation.noMatchingCommands": "No matching tools",
  "automation.clearFilters": "Clear filters",

  "automation.group.tutorial": "Guidance",
  "automation.group.data": "Data Queries",
  "automation.group.window": "UI & Forms",
  "automation.group.logs": "Logs",
  "automation.group.operations": "Operations",

  "automation.label.tutorial_hint": "Show step hint",
  "automation.effect.tutorial_hint":
    "Display a text hint next to a tutorial step, attributed to this client.",
  "automation.label.tutorial_close": "Close tutorial",
  "automation.effect.tutorial_close": "Dismiss the active tutorial started by this client.",
  "automation.label.tutorial_previous": "Previous step",
  "automation.effect.tutorial_previous": "Go back to the previous step in the active tutorial.",
  "automation.label.tutorial_next": "Next step",
  "automation.effect.tutorial_next": "Advance to the next step in the active tutorial.",
  "automation.label.tutorial_goto": "Jump to step",
  "automation.effect.tutorial_goto":
    "Navigate to a specific tutorial step; reports if a form must be opened first.",
  "automation.label.tutorial_start": "Start tutorial",
  "automation.effect.tutorial_start":
    "Navigate to a tutorial entry page and display step guidance.",
  "automation.label.tutorial_state": "Read tutorial progress",
  "automation.effect.tutorial_state": "Read the active tutorial and current step progress.",
  "automation.label.tutorial_list": "List tutorials & steps",
  "automation.effect.tutorial_list":
    "List built-in tutorials, step IDs, target pages, and anchor controls.",
  "automation.label.action_guide": "Guide action",
  "automation.effect.action_guide":
    "Highlight an authorized control in the window so the user can complete the action.",
  "automation.label.command_catalog": "Command catalog",
  "automation.effect.command_catalog":
    "Read available commands, arguments, and page catalog for this client.",
  "automation.label.monitoring_summary": "Monitoring summary",
  "automation.effect.monitoring_summary":
    "Read workspace project, server, Swarm, and monitoring totals.",
  "automation.label.projects_list": "List projects & apps",
  "automation.effect.projects_list":
    "List project and application names and IDs across the workspace.",
  "automation.label.servers_list": "List servers",
  "automation.effect.servers_list":
    "List managed server names, addresses, runtimes, and observed status.",
  "automation.label.tunnels_list": "List tunnels",
  "automation.effect.tunnels_list": "List tunnel instances, bound servers, and running state.",
  "automation.label.templates_list": "List templates",
  "automation.effect.templates_list":
    "List template sources, ports, variables, and default deployment specs.",
  "automation.label.application_runtime": "Application runtime",
  "automation.effect.application_runtime":
    "Read Swarm services, nodes, and replica IDs for an application.",
  "automation.label.container_logs": "Container logs",
  "automation.effect.container_logs":
    "Read up to 30 minutes of stdout and stderr logs for an application service slot.",
  "automation.label.application_log_configure": "Configure app log command",
  "automation.effect.application_log_configure":
    "Set or clear the in-container log query command for an application.",
  "automation.label.application_logs": "Run app log query",
  "automation.effect.application_logs":
    "Run the saved log query command in a selected replica and return redacted output.",
  "automation.label.application_release": "Release application",
  "automation.effect.application_release":
    "Build or pull artifacts for an application and deploy to target services.",
  "automation.label.application_restart": "Restart application",
  "automation.effect.application_restart":
    "Queue an application restart; running replicas restart briefly.",
  "automation.label.operation_get": "Get operation status",
  "automation.effect.operation_get": "Read stage, state, and error summary for an OperationRun ID.",
  "automation.label.page_state": "Read window state",
  "automation.effect.page_state": "Read the active page, selection, tab, and open form fields.",
  "automation.label.page_open": "Open page or tab",
  "automation.effect.page_open": "Navigate to Home, Projects, Servers, Tunnels, or Settings.",
  "automation.label.page_control": "Operate view controls",
  "automation.effect.page_control":
    "Trigger registered filters, refreshes, or appearance theme/locale controls.",
  "automation.label.form_open": "Open form",
  "automation.effect.form_open": "Open a registered edit or creation form on the current page.",
  "automation.label.form_fill": "Fill form",
  "automation.effect.form_fill": "Write field values into a form opened by this client.",
  "automation.label.form_close": "Close form",
  "automation.effect.form_close": "Close a form opened by this client and discard drafts.",
  "automation.label.form_submit": "Submit form",
  "automation.effect.form_submit":
    "Save form data; creating a tunnel also submits its start operation.",
};
