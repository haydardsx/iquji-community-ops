import type { Project } from "@/contracts/types";

export function ProjectHeader({ project }: { project: Project }) {
  return (
    <header className="ops-project-header">
      <div className="ops-project-heading">
        <h1 data-ops-guide-object={project.id}>{project.name}</h1>
        {project.description ? <p>{project.description}</p> : null}
      </div>
    </header>
  );
}
