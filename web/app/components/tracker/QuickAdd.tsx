import { useId, useState, type FormEvent, type KeyboardEvent } from "react";
import { api } from "~/lib/api";
import { invalidate } from "~/lib/query";
import { toast } from "~/lib/store";
import type { Milestone } from "~/lib/types";
import { TRACKER_KEY } from "./actions";

type Kind = "milestone" | "feature";

/**
 * "+ New milestone" and "+ New feature" at the top of the Milestones view.
 * Both open a small inline form and write through the tracker API.
 */
export function QuickAdd({
  projects,
  milestones,
  defaultProject,
}: {
  projects: { id: string; name: string }[];
  milestones: Milestone[];
  defaultProject: string;
}) {
  const [open, setOpen] = useState<Kind | null>(null);
  return (
    <div className="tk-quick">
      <div className="tk-quick-btns">
        <button type="button" className="tk-ghost" aria-expanded={open === "milestone"} onClick={() => setOpen(open === "milestone" ? null : "milestone")}>
          + New milestone
        </button>
        <button type="button" className="tk-ghost" aria-expanded={open === "feature"} onClick={() => setOpen(open === "feature" ? null : "feature")}>
          + New feature
        </button>
      </div>
      {open ? <QuickForm key={open} kind={open} projects={projects} milestones={milestones} defaultProject={defaultProject} onClose={() => setOpen(null)} /> : null}
    </div>
  );
}

function QuickForm({
  kind,
  projects,
  milestones,
  defaultProject,
  onClose,
}: {
  kind: Kind;
  projects: { id: string; name: string }[];
  milestones: Milestone[];
  defaultProject: string;
  onClose: () => void;
}) {
  const uid = useId();
  const [project, setProject] = useState(defaultProject);
  const [title, setTitle] = useState("");
  const [due, setDue] = useState("");
  const [milestone, setMilestone] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const label = kind === "milestone" ? "milestone" : "feature";
  const options = milestones.filter((m) => m.projectId === project);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const t = title.trim();
    if (!t) {
      setError("Give the " + label + " a title.");
      return;
    }
    if (!project) {
      setError("Pick a project.");
      return;
    }
    setBusy(true);
    try {
      if (kind === "milestone") {
        const res = await api.createMilestone(project, { title: t, due: due || null });
        toast("Milestone " + res.milestone.id + " created");
      } else {
        const res = await api.createFeature(project, { title: t, milestoneId: milestone || null });
        toast("Feature " + res.feature.id + " created");
      }
      invalidate(TRACKER_KEY);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not create the " + label);
      setBusy(false);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLFormElement>) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      onClose();
    }
  };

  return (
    <form className="tk-quickform" aria-label={"New " + label} onSubmit={submit} onKeyDown={onKey} noValidate>
      <div className="tk-qf-field">
        <label className="tk-field" htmlFor={uid + "-p"}>
          Project
        </label>
        <select
          id={uid + "-p"}
          className="tk-input"
          value={project}
          onChange={(e) => {
            setProject(e.target.value);
            setMilestone("");
          }}
        >
          {projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}
            </option>
          ))}
        </select>
      </div>
      <div className="tk-qf-field grow">
        <label className="tk-field" htmlFor={uid + "-t"}>
          Title
        </label>
        <input
          id={uid + "-t"}
          className="tk-input"
          value={title}
          autoFocus
          placeholder={kind === "milestone" ? "What is the target?" : "What part of the project is it?"}
          onChange={(e) => {
            setTitle(e.target.value);
            setError("");
          }}
        />
      </div>
      {kind === "milestone" ? (
        <div className="tk-qf-field">
          <label className="tk-field" htmlFor={uid + "-d"}>
            Due
          </label>
          <input id={uid + "-d"} type="date" className="tk-input" value={due} onChange={(e) => setDue(e.target.value)} />
        </div>
      ) : (
        <div className="tk-qf-field">
          <label className="tk-field" htmlFor={uid + "-m"}>
            Milestone
          </label>
          <select id={uid + "-m"} className="tk-input" value={milestone} onChange={(e) => setMilestone(e.target.value)}>
            <option value="">No milestone</option>
            {options.map((m) => (
              <option key={m.id} value={m.id}>
                {m.title}
              </option>
            ))}
          </select>
        </div>
      )}
      <div className="tk-qf-actions">
        <button type="submit" className="tk-primary" disabled={busy}>
          Add {label}
        </button>
        <button type="button" className="tk-ghost" onClick={onClose}>
          Cancel
        </button>
      </div>
      {error ? (
        <span className="tk-error full" role="alert">
          {error}
        </span>
      ) : null}
    </form>
  );
}
