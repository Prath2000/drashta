import { useEffect, useId, useState, type FormEvent } from "react";
import type { Verdict } from "~/lib/types";

/**
 * Approve / Changes / Pending for one snapshot version (02-ui-spec §6.4).
 * "Changes" opens a note box: a change request needs a review note.
 */
export function VerdictBar({
  versionLabel,
  verdict,
  reviewNote,
  hint,
  busy,
  onSet,
}: {
  versionLabel: string;
  verdict: Verdict;
  reviewNote: string;
  hint: string;
  busy: boolean;
  /** Resolves true when the server accepted the verdict. */
  onSet: (v: Verdict, note?: string) => Promise<boolean>;
}) {
  const uid = useId();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(reviewNote);
  const [error, setError] = useState("");

  // Another version was picked: close the box.
  useEffect(() => {
    setOpen(false);
    setError("");
    setDraft(reviewNote);
  }, [versionLabel, reviewNote]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const note = draft.trim();
    if (!note) {
      setError("What needs to change?");
      return;
    }
    if (await onSet("changes", note)) setOpen(false);
  };

  return (
    <div className="tk-verdict">
      <div className="tk-verdict-row">
        <span className="tk-label">Verdict · {versionLabel}</span>
        <button
          type="button"
          className="tk-vbtn teal"
          aria-pressed={verdict === "approved"}
          disabled={busy}
          onClick={() => void onSet("approved", reviewNote || undefined)}
        >
          ✓ Approve
        </button>
        <button
          type="button"
          className="tk-vbtn coral"
          aria-pressed={verdict === "changes" || open}
          disabled={busy}
          onClick={() => {
            setError("");
            setDraft(reviewNote);
            setOpen(!open);
          }}
        >
          ± Changes
        </button>
        <button
          type="button"
          className="tk-vbtn muted"
          aria-pressed={verdict === "pending"}
          disabled={busy}
          onClick={() => void onSet("pending", reviewNote || undefined)}
        >
          ◯ Pending
        </button>
        <span className="tk-grow" />
        <span className="tk-verdict-hint">{hint}</span>
      </div>
      {open ? (
        <form className="tk-changes" onSubmit={submit} aria-label="Request changes" noValidate>
          <label htmlFor={uid} className="sr-only">
            What needs to change
          </label>
          <textarea
            id={uid}
            className="tk-textarea"
            rows={2}
            value={draft}
            placeholder="What needs to change?"
            onChange={(e) => {
              setDraft(e.target.value);
              setError("");
            }}
          />
          <div className="tk-changes-side">
            <button type="submit" className="tk-coral" disabled={busy}>
              Request changes
            </button>
            <button type="button" className="tk-ghost sm tk-hit" onClick={() => setOpen(false)}>
              Cancel
            </button>
          </div>
          {error ? (
            <span className="tk-error full" role="alert">
              {error}
            </span>
          ) : null}
        </form>
      ) : null}
    </div>
  );
}
