import type { zhCNTutorial } from "../zh-CN/tutorial";

export const enUSTutorial: Record<keyof typeof zhCNTutorial, string> = {
  "tutorial.guide.page": "The requested page or tab is highlighted.",
  "tutorial.guide.control": "Use the highlighted control to make this change.",
  "tutorial.guide.open": "Open the form using this control.",
  "tutorial.guide.openMenu": "Open this menu and choose Edit.",
  "tutorial.guide.fill": "Enter the value in the highlighted field.",
  "tutorial.guide.close": "Review your draft before closing the form with this button.",
  "tutorial.guide.submit": "Review the form and use this button when ready to save.",
  "tutorial.guide.application_release": "Use this button to review the release confirmation.",
  "tutorial.guide.application_restart":
    "Open runtime operations and choose Restart to review the application before confirming.",
  "tutorial.guide.application_log_configure":
    "Enter the log query command in the highlighted editor and save when ready.",
  "tutorial.guide.application_logs":
    "The selected running replica is ready. Click the query button to run the log command.",
  "tutorial.guide.dockerRepoMirror": "Docker package repository",
  "tutorial.guide.inlineForm": "Click the highlighted field to start editing this form.",
  "tutorial.guide.logSlot": "Select a service, then a running replica to query.",
  "tutorial.guide.manualPassword":
    "Enter the read-only Jenkins account password here, then review and submit the form.",
  "tutorial.menu": "Tutorials",
  "tutorial.next": "Next",
  "tutorial.previous": "Previous",
  "tutorial.done": "End tutorial",
  "tutorial.progress": "Step {index} of {total}",
  "tutorial.startBlocked": "Finish editing or close the dialog, then open the tutorial.",
  "tutorial.stepBlocked": "Follow the current step or close the tutorial.",
  "tutorial.workspace.title": "Explore the workspace",
  "tutorial.project-create.title": "Create your first project",
  "tutorial.workspace.home.title": "Review runtime health",
  "tutorial.workspace.home.description":
    "Filter projects, servers and Swarm to find resources that need attention.",
  "tutorial.workspace.projects.title": "Manage projects and applications",
  "tutorial.workspace.projects.description":
    "Use the create button to organize applications, or select an existing project from the sidebar.",
  "tutorial.workspace.servers.title": "Register a server",
  "tutorial.workspace.servers.description":
    "Add a server here, then check Docker, Swarm and Jenkins in its details.",
  "tutorial.workspace.tunnels.title": "Configure tunnels",
  "tutorial.workspace.tunnels.description":
    "Use the create button to select a proxy server and set forwarding rules.",
  "tutorial.workspace.settings.title": "Find workspace settings",
  "tutorial.workspace.settings.description":
    "Search for settings such as templates, credentials or automation access. Select General to view the control plane version and location.",
  "tutorial.project-create.open.title": "Open the new project form",
  "tutorial.project-create.open.description":
    "Click the highlighted create button to open the form. The tutorial will move to the name field.",
  "tutorial.project-create.name.title": "Enter a project name",
  "tutorial.project-create.name.description":
    "Enter a name and an optional description, then choose Next.",
  "tutorial.project-create.save.title": "Review and save the project",
  "tutorial.project-create.save.description":
    "Review the name and description. Click the highlighted Save button to create the project, or end the tutorial to continue editing.",
};
