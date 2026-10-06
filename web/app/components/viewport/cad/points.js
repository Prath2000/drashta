// Copied from a sibling drawing project (dashboard/src/render/points.js, commit 4ccf486, 2026-10-01). Keep in step by hand; do not edit the drawing code here.
/* Which points an entity claims, split out of engine.js's closure so it can
 * be asked without constructing (or dynamically importing) a renderer.
 *
 * ReviewCanvas needs the question "would an operator see a shape here, or a
 * 3-pixel anchor" answered on the ingest pass, to count the rows whose
 * exploded read produced no drawable geometry — CLAUDE.md's
 * ambiguity-is-reported rule. A second copy of the type switch over there
 * would drift the first time a type is added here, and drift in this
 * direction is silent: the count would quietly stop counting.
 *
 * Bodies are engine.js's, unmodified. Nothing here reads engine state. */
export const XY = p => (Array.isArray(p) && Number.isFinite(p[0]) && Number.isFinite(p[1])) ? p : null;

/* Every point an entity claims, for the extent and for hit-testing. A shape
 * whose coordinates did not decode contributes nothing rather than dragging
 * the extent to the origin and shrinking the whole site to a dot. */
export function pointsOf(e) {
  const out = [];
  const push = p => { const q = XY(p); if (q) out.push(q); };
  switch (e.type) {
    case 'LINE': push(e.start); push(e.end); break;
    case 'CIRCLE': case 'ARC': {
      const c = XY(e.center), r = Number.isFinite(e.radius) ? e.radius : 0;
      if (c) { out.push([c[0] - r, c[1] - r]); out.push([c[0] + r, c[1] + r]); }
      break;
    }
    case 'ELLIPSE': {
      const c = XY(e.center), a = XY(e.major_axis);
      if (c && a) {
        const r = Math.hypot(a[0], a[1]);
        out.push([c[0] - r, c[1] - r]); out.push([c[0] + r, c[1] + r]);
      } else if (c) out.push(c);
      break;
    }
    default:
      if (Array.isArray(e.vertices)) e.vertices.forEach(push);
      push(e.position);
  }
  return out;
}
