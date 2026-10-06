import { useState, type MouseEvent } from "react";
import type { Note, Snapshot } from "~/lib/types";

export interface Pin {
  note: Note;
  n: number;
}

/** Notes with a point on the image, numbered 1, 2, 3 in the order they arrive. */
export function pinsOf(notes: Note[] | undefined): Pin[] {
  return (notes ?? [])
    .filter((n) => typeof n.x === "number" && typeof n.y === "number")
    .map((note, i) => ({ note, n: i + 1 }));
}

/**
 * The image of one snapshot version with numbered ivory pins.
 * The frame takes the image's own shape, so a pin at x,y (0..1) sits on the same
 * spot of the picture whatever the size. Not in annotate mode, a click opens the
 * snapshot in the viewport. In annotate mode a click reports a new pin position.
 */
export function SnapFrame({
  snap,
  pins,
  annotate,
  draftPin,
  hot,
  onHot,
  onOpen,
  onDrop,
}: {
  snap: Snapshot;
  pins: Pin[];
  annotate: boolean;
  draftPin: { x: number; y: number } | null;
  hot: string | null;
  onHot: (id: string | null) => void;
  onOpen: () => void;
  onDrop: (x: number, y: number) => void;
}) {
  const [ratio, setRatio] = useState<number | null>(null);
  const click = (e: MouseEvent<HTMLButtonElement>) => {
    if (!annotate) {
      onOpen();
      return;
    }
    const r = e.currentTarget.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) return;
    const x = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
    const y = Math.min(1, Math.max(0, (e.clientY - r.top) / r.height));
    onDrop(Math.round(x * 1000) / 1000, Math.round(y * 1000) / 1000);
  };
  const label = snap.title + " v" + snap.version;
  return (
    <div className={"tk-frame" + (annotate ? " annotate" : "")} style={{ aspectRatio: ratio ? String(ratio) : "16 / 10" }}>
      <img
        className="tk-frame-img"
        src={snap.url}
        alt=""
        onLoad={(e) => {
          const { naturalWidth: w, naturalHeight: h } = e.currentTarget;
          if (w > 0 && h > 0) setRatio(w / h);
        }}
      />
      <button
        type="button"
        className="tk-frame-hit"
        aria-label={annotate ? "Drop a pin on " + label : "Open " + label + " to annotate"}
        onClick={click}
      />
      {pins.map((p) => (
        <button
          key={p.note.id}
          type="button"
          className={"tk-pin" + (p.note.resolved ? " resolved" : "") + (hot === p.note.id ? " hot" : "")}
          style={{ left: (p.note.x as number) * 100 + "%", top: (p.note.y as number) * 100 + "%" }}
          aria-label={"Pin " + p.n + ": " + p.note.text}
          onMouseEnter={() => onHot(p.note.id)}
          onMouseLeave={() => onHot(null)}
          onFocus={() => onHot(p.note.id)}
          onBlur={() => onHot(null)}
        >
          {p.n}
        </button>
      ))}
      {draftPin ? (
        <span className="tk-pin draft" aria-hidden="true" style={{ left: draftPin.x * 100 + "%", top: draftPin.y * 100 + "%" }}>
          +
        </span>
      ) : null}
    </div>
  );
}
