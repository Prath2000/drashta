import { useEffect, useId, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { api } from "~/lib/api";
import { invalidate } from "~/lib/query";
import { toast } from "~/lib/store";
import type { Feature, TaskStatus } from "~/lib/types";
import { TRACKER_KEY } from "./actions";

/**
 * The inline create form that opens under a column header (02-ui-spec §6.3).
 * Title is required. A feature is optional ("No feature").
 */
export function NewTaskForm({
  status,
  defaultProject,
  projects,
  features,
  onClose,
}: {
  status: TaskStatus;
  defaultProject: string;
  projects: { id: string; name: string }[];
  features: Feature[];
  onClose: () => void;
}) {
  const uid = useId();
  const titleRef = useRef<HTMLInputElement>(null);
  const [title, setTitle] = useState("");
  const [project, setProject] = useState(defaultProject);
  const [feature, setFeature] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const options = features.filter((f) => f.projectId === project);

  useEffect(() => {
    titleRef.current?.focus();
  }, []);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const t = title.trim();
    if (!t) {
      setError("Give the task a title.");
      titleRef.current?.focus();
      return;
    }
    if (!project) {
      setError("Pick a project.");
      return;
    }
    setBusy(true);
    try {
      const res = await api.createTask(project, { title: t, featureId: feature || null, status });
      toast("Task " + res.task.id + " created");
      invalidate(TRACKER_KEY);
      invalidate("/boot");
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "could not create the task");
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
    <form className="tk-newform" aria-label="New task" onSubmit={submit} onKeyDown={onKey} noValidate>
      <label className="tk-field" htmlFor={uid + "-t"}>
        Title
      </label>
      <input
        id={uid + "-t"}
        ref={titleRef}
        type="text"
        className="tk-input"
        value={title}
        placeholder="What needs doing?"
        onChange={(e) => {
          setTitle(e.target.value);
          setError("");
        }}
      />
      <label className="tk-field" htmlFor={uid + "-p"}>
        Project
      </label>
      <select
        id={uid + "-p"}
        className="tk-input"
        value={project}
        onChange={(e) => {
          setProject(e.target.value);
          setFeature("");
        }}
      >
        {projects.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name}
          </option>
        ))}
      </select>
      <label className="tk-field" htmlFor={uid + "-f"}>
        Feature
      </label>
      <select id={uid + "-f"} className="tk-input" value={feature} onChange={(e) => setFeature(e.target.value)}>
        <option value="">No feature</option>
        {options.map((f) => (
          <option key={f.id} value={f.id}>
            {f.title}
          </option>
        ))}
      </select>
      {options.length === 0 ? <span className="tk-hint">This project has no features yet.</span> : null}
      {error ? (
        <span className="tk-error" role="alert">
          {error}
        </span>
      ) : null}
      <div className="tk-formrow">
        <button type="submit" className="tk-primary" disabled={busy}>
          Add task
        </button>
        <button type="button" className="tk-ghost" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}
