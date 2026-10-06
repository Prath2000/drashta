// Copied from a sibling drawing project (dashboard/src/render/engine.js, commit 4ccf486, 2026-10-01). Keep in step by hand; do not edit the drawing code here.
/* A viewer engine, lifted from
 * (that project's internal/engine/testdata/engine.js) and mounted rather than
 * rewritten — M5.md is explicit that this stays imperative canvas code with
 * its ingest path left alone. Three things changed from the salvaged original:
 *
 *   1. The colour table moved to ./aci.js (M5's file layout), imported
 *      rather than inlined.
 *   2. setHighlight() and centerOn() were ADDED. The salvaged engine never
 *      tracked an entity's handle at all — only its layer index, for the
 *      solo/mute rail — so "click a search hit, see it highlighted and
 *      centred" needed new surface.
 *   3. XY() and pointsOf() moved out of createEngine()'s closure into
 *      ./points.js, imported rather than inlined — bodies unchanged, and
 *      both were already pure (they read no engine state). See that file
 *      for why they had to be reachable without the engine.
 *
 * Everything else below this comment is the original file, unmodified.
 *
 * Decodes nothing. Every number here arrived from the chitralipi binary as
 * JSONL, one entity per line, in the drawing's own coordinates.
 */
import { aciRGB, layerColour } from './aci.js';
import { XY, pointsOf } from './points.js';

/* Handle normalisation, mirroring internal/engine.NormalizeHandle exactly:
 * uppercase hex, no 0x, no leading zeros, empty stays empty. A highlight
 * compared under a different normalisation than the index used is M5's
 * named failure mode — the entity renders, nothing highlights, and there is
 * no error. */
function normalizeHandle(h) {
  if (h == null) return '';
  let s = String(h).trim().toUpperCase();
  if (s.startsWith('0X')) s = s.slice(2);
  s = s.replace(/^0+/, '');
  return s === '' ? '0' : s;
}

/* Types whose pointsOf() bbox is just an anchor, not their drawn extent —
 * TEXT/MTEXT/ATTRIB/ATTDEF render glyphs extending well past their
 * insertion point (drawText's `height` * string length), and an INSERT read
 * from the archive stream is a placement marker carrying only a position.
 * Culling them by that anchor point would make a label vanish as soon as its
 * origin (not its visible glyphs) crosses outside the viewport.
 *
 * INSERT stays in this set even though the *index* no longer treats a
 * placement as a point: internal/indexer's Backfill widens the stored node's
 * bbox to cover its exploded contents, but this engine draws the archive's
 * entity stream, where an INSERT is still the raw record with a position and
 * no contents. The two are different reads of the same drawing, and this one
 * has not changed. The exploded stream is what gives the canvas real
 * outlines — see ReviewCanvas.jsx. */
const NO_CULL_ANCHOR_ONLY = new Set(['TEXT', 'MTEXT', 'ATTRIB', 'ATTDEF', 'INSERT']);

export function createEngine() {
  const S = {
    cv: null, ctx: null, dpr: 1,
    hooks: {},
    ents: [],
    layers: [],            // rows from `layers --as json`
    layerIndex: new Map(), // name -> row position
    counts: [],            // entities seen per layer, from the stream itself
    blocks: new Map(),     // block name -> { name, ids, handle }
    /* Both are sets, because a desk solos more than one track. Solo was a
       single index once, so soloing a second layer dropped the first without
       saying it had. */
    muted: new Set(), solo: new Set(), hover: -1,
    view: { x: 0, y: 0, s: 1 },
    extent: null,          // [minx, miny, maxx, maxy], grown as entities arrive
    fitted: false,
    /* `fitted` and `userMoved` are not the same question, and conflating them
     * is what made a canvas open at the wrong zoom.
     *
     * `fitted` means "something has been framed, so a later batch must not
     * re-frame it" — centring on a hit sets it too. `userMoved` means "a
     * person has taken control of this view", and only a pan or a wheel sets
     * it. The resize path needs the second: a canvas that gains its real size
     * (it was display:none, so its rect was zero and the scale was computed
     * for a 1px box) must re-apply whatever it was showing, while a canvas
     * the user has panned must be left alone. */
    userMoved: false,
    lastFrame: null,       // the box frameExtentTo last framed, to re-apply on resize

    /* AutoCAD modelspace furniture: the grid and the crosshair. Off by
     * default — the small preview canvases want the geometry and nothing
     * else — and turned on by the panes that are standing in for modelspace. */
    modelspace: false,
    cursor: null,          // {x, y} in device pixels, or null when the pointer is away
    dirty: true,

    // The drill-down target, added for M5. Kept as the *normalised* handle so
    // every comparison uses the one form everything else keys on.
    highlightHandle: null,
    hitIndices: [],         // indices into S.ents matching highlightHandle
    centeredOnHit: false,   // true once centerOn has framed the hit, so a
                             // later batch does not re-centre and fight a
                             // user who has since panned

    // plan/05-review-canvas.md: the review canvas's own overlay, added for
    // phase 05. problems maps a normalised handle to one of 'resolved' |
    // 'waiting' | 'straddling' | 'unlabelled' — everything not in the map
    // draws exactly as it always has, so a plain Search->View mount (no
    // setProblems call) is unaffected byte-for-byte.
    problems: new Map(),
    problemIndices: [],      // indices into S.ents carrying a problem — drawn on top, same shape as hitIndices
    // 'pan' (default, unchanged drag-to-pan/wheel-zoom) or 'select', which
    // ReviewCanvas turns on for a marquee drag. A click (mousedown+mouseup
    // with no movement) fires hooks.click in either mode — only a *drag* is
    // mode-dependent, because a click is never ambiguous with a pan.
    interactionMode: 'pan',
    marquee: null,           // {x0,y0,x1,y1} in screen px while dragging in select mode
  };

  function grow(x, y) {
    if (!Number.isFinite(x) || !Number.isFinite(y)) return;
    const e = S.extent;
    if (!e) { S.extent = [x, y, x, y]; return; }
    if (x < e[0]) e[0] = x;
    if (y < e[1]) e[1] = y;
    if (x > e[2]) e[2] = x;
    if (y > e[3]) e[3] = y;
  }

  const layerIdx = name => (S.layerIndex.has(name) ? S.layerIndex.get(name) : -1);

  /* The colour AutoCAD draws an entity in, resolved once at ingest the same
   * way _li is (so setLayers must come first — see ReviewCanvas/CadViewport).
   *
   * `draw_color` is a block child's colour with ByBlock and layer-0 already
   * resolved through every INSERT above it (internal/explode/draw.go); an
   * archive line carries only its own `color`, and being outside any block,
   * its ByBlock is ACI 7 — what AutoCAD does with it. An explicit ACI or
   * truecolour is the entity's own and never the layer's. With no colour at
   * all (an archive written before chitralipi emitted entity colour) the
   * layer's colour is drawn: the same pixels the viewer always drew, not a
   * claim the entity is ByLayer.
   *
   * _col is a CSS colour decided now; _cl a layer row whose colour is read
   * at draw time. */
  function resolveColour(o) {
    o._col = null; o._cl = -1;
    const own = o.draw_layer || o.layer;
    const c = o.draw_color || o.color;
    if (c && typeof c === 'object') {
      if (typeof c.rgb === 'string' && /^#[0-9a-f]{6}$/i.test(c.rgb)) { o._col = c.rgb; return; }
      if (Number.isFinite(c.index)) { o._col = aciRGB(c.index); if (o._col) return; }
      if (c.layer) { o._cl = layerIdx(c.layer); return; }
      if (c.by === 'block') { o._col = aciRGB(7); return; }
    }
    o._cl = layerIdx(own);
  }

  /* One JSONL line. Returns false for the header and summary lines, which are
   * objects too but are not shapes. */
  function ingestLine(line) {
    let o;
    try { o = JSON.parse(line); } catch { return false; }
    if (!o || typeof o !== 'object') return false;

    if (o.file && !o.type) { S.meta = o; return false; }
    if (o.summary) { S.summary = o.summary; return false; }
    if (!o.type) return false;

    // The layer row an entity answers to for solo/mute/count. `draw_layer` is
    // the layer AutoCAD shows a block's child on once layer-0 has resolved
    // (internal/explode/draw.go); `group_layer` is a caller's own grouping
    // on top of that — the review canvas files every child under the row
    // it belongs to. The entity's own `layer` is the fallback, and is what
    // an archive line (never inside a block) always means.
    o._li = layerIdx(o.group_layer || o.draw_layer || o.layer);
    if (o._li >= 0) S.counts[o._li] = (S.counts[o._li] || 0) + 1;
    resolveColour(o);

    if (o.type === 'INSERT' && o.name) {
      let b = S.blocks.get(o.name);
      if (!b) { b = { name: o.name, handle: o.block_ref || null, ids: [] }; S.blocks.set(o.name, b); }
      b.ids.push(S.ents.length);
    }

    const pts = pointsOf(o);
    let bbox = null;
    for (const p of pts) {
      grow(p[0], p[1]);
      if (!bbox) bbox = [p[0], p[1], p[0], p[1]];
      else {
        if (p[0] < bbox[0]) bbox[0] = p[0];
        if (p[1] < bbox[1]) bbox[1] = p[1];
        if (p[0] > bbox[2]) bbox[2] = p[0];
        if (p[1] > bbox[3]) bbox[3] = p[1];
      }
    }
    o._pts = pts;
    // Reused by frame()'s viewport culling — an entity whose position
    // couldn't be determined (bbox null) always draws, the same fail-open
    // rule internal/archive/filter.go's coarsePosition uses for the same
    // reason: "couldn't tell" must never be silently treated as "off screen".
    // The same null (always-draw) applies to anchor-only types (see
    // NO_CULL_ANCHOR_ONLY) and to a bulged polyline, whose arcThrough sag
    // can bow the curve outside the straight vertex-only bbox above.
    const hasBulge = Array.isArray(o.bulges) && o.bulges.some((b) => b);
    o._bbox = (NO_CULL_ANCHOR_ONLY.has(o.type) || hasBulge) ? null : bbox;

    if (S.highlightHandle && normalizeHandle(o.handle) === S.highlightHandle) {
      o._hit = true;
      S.hitIndices.push(S.ents.length);
    }

    if (S.problems.size) {
      const p = S.problems.get(normalizeHandle(o.handle));
      if (p) { o._problem = p; S.problemIndices.push(S.ents.length); }
    }

    S.ents.push(o);
    return true;
  }

  /* A batch from the Go side. Refitting on every batch would make the drawing
   * lurch as the extent grows; refitting only while the user has not touched
   * the view keeps the site framed as it fills in, and leaves it alone the
   * moment they pan. */
  function ingest(lines) {
    let n = 0;
    for (const l of lines) if (ingestLine(l)) n++;
    if (n && !S.fitted) fit();
    if (S.hitIndices.length && !S.centeredOnHit) {
      centerOnEntity(S.hitIndices[0]);
    }
    S.dirty = true;
    if (S.hooks.progress) S.hooks.progress(S.ents.length);
    return n;
  }

  /* ---------- view ------------------------------------------------------- */

  function sizeCanvas(c) {
    if (!c) return;
    const r = c.getBoundingClientRect();
    const w = Math.max(1, Math.round(r.width * S.dpr));
    const h = Math.max(1, Math.round(r.height * S.dpr));
    // The v1 bug: the element shrank when a panel opened but the backing
    // store did not, and the drawing smeared into garbage. The element is the
    // thing to watch, not the window.
    if (c.width !== w || c.height !== h) { c.width = w; c.height = h; S.dirty = true; }
  }

  /* The extent to frame, which is not always the extent that exists.
   *
   * Real drawings contain strays: one INSERT placed millions of units from a
   * site occupying a few hundred metres. Framing the true bounding box there
   * renders the entire site as a speck on an empty screen. So the frame comes
   * from the 1st-99th percentile of shape positions, and everything is still
   * drawn; the count of what falls outside the frame is reported, never
   * hidden. */
  const SAMPLE = 4000;

  function percentileExtent() {
    const n = S.ents.length;
    if (n < 50) return S.extent;
    const step = Math.max(1, Math.floor(n / SAMPLE));
    const xs = [], ys = [];
    for (let i = 0; i < n; i += step) {
      const p = S.ents[i]._pts[0];
      if (p) { xs.push(p[0]); ys.push(p[1]); }
    }
    if (xs.length < 20) return S.extent;
    xs.sort((a, b) => a - b); ys.sort((a, b) => a - b);
    const lo = i => i[Math.floor(i.length * 0.01)];
    const hi = i => i[Math.ceil(i.length * 0.99) - 1];
    const px0 = lo(xs), px1 = hi(xs), py0 = lo(ys), py1 = hi(ys);
    const sw = px1 - px0, sh = py1 - py0;
    if (!(sw > 0) || !(sh > 0)) return S.extent;

    const cx = (px0 + px1) / 2, cy = (py0 + py1) / 2;
    const wx = sw * 1.5, wy = sh * 1.5;
    let e = null;
    for (const ent of S.ents) {
      const p = ent._pts[0];
      if (!p) continue;
      if (Math.abs(p[0] - cx) > wx || Math.abs(p[1] - cy) > wy) continue;
      if (!e) e = [p[0], p[1], p[0], p[1]];
      else {
        if (p[0] < e[0]) e[0] = p[0];
        if (p[1] < e[1]) e[1] = p[1];
        if (p[0] > e[2]) e[2] = p[0];
        if (p[1] > e[3]) e[3] = p[1];
      }
    }
    return e || S.extent;
  }

  /* How many shapes sit outside the framed view. Stated, never hidden. */
  function outside(e) {
    if (!e) return 0;
    let n = 0;
    for (const ent of S.ents) {
      const p = ent._pts[0];
      if (p && (p[0] < e[0] || p[0] > e[2] || p[1] < e[1] || p[1] > e[3])) n++;
    }
    return n;
  }

  function fit() {
    if (!S.cv || !S.extent) return;
    S.frame_extent = percentileExtent();
    frameExtentTo(S.frame_extent);
  }

  /* Pans and zooms so `[x0,y0,x1,y1]` fills the canvas with padding. Shared by
   * fit() (the whole drawing) and centerOn() (one entity's bbox) so both
   * produce the same kind of view rather than two slightly different framing
   * rules. */
  function frameExtentTo(box) {
    if (!S.cv || !box) return;
    const [x0, y0, x1, y1] = box;
    const w = Math.max(x1 - x0, 1e-9), h = Math.max(y1 - y0, 1e-9);
    const pad = 40 * S.dpr;
    const s = Math.min((S.cv.width - pad * 2) / w, (S.cv.height - pad * 2) / h);
    S.view.s = s;
    S.view.x = (S.cv.width - w * s) / 2 - x0 * s;
    // Y is flipped in `project`: a DWG counts upward and a canvas counts
    // downward, and a site drawn upside-down reads as a design choice rather
    // than a bug.
    S.view.y = (S.cv.height + h * s) / 2 + y0 * s;
    S.lastFrame = [x0, y0, x1, y1];
    S.dirty = true;
  }

  /* Re-applies the last framing against the canvas's current size. Called on
   * resize, so a pane that was framed while hidden — zero rect, therefore a
   * scale computed for a 1px canvas — corrects itself the moment it is shown,
   * without deciding afresh what to look at. */
  function reframe() {
    if (S.lastFrame) frameExtentTo(S.lastFrame);
    else fit();
  }

  const project = p => [p[0] * S.view.s + S.view.x, S.view.y - p[1] * S.view.s];

  /* Centres and zooms on one entity, once its handle is known to have
   * arrived. Padded generously — M5's acceptance criterion is that a hit is
   * "visibly highlighted and centred", not merely present on an unpanned
   * whole-drawing view. */
  function centerOnEntity(idx) {
    const e = S.ents[idx];
    if (!e || !S.cv) return;
    const pts = e._pts && e._pts.length ? e._pts : pointsOf(e);
    if (!pts.length) return;
    let x0 = pts[0][0], y0 = pts[0][1], x1 = x0, y1 = y0;
    for (const [x, y] of pts) {
      if (x < x0) x0 = x; if (y < y0) y0 = y;
      if (x > x1) x1 = x; if (y > y1) y1 = y;
    }
    // A degenerate box (a POINT, a TEXT origin) gets a minimum window around
    // it rather than a zoom to infinity.
    const minSpan = Math.max(1, S.extent ? Math.max(S.extent[2] - S.extent[0], S.extent[3] - S.extent[1]) * 0.02 : 1);
    if (x1 - x0 < minSpan) { const m = (minSpan - (x1 - x0)) / 2; x0 -= m; x1 += m; }
    if (y1 - y0 < minSpan) { const m = (minSpan - (y1 - y0)) / 2; y0 -= m; y1 += m; }
    frameExtentTo([x0, y0, x1, y1]);
    S.fitted = true;
    S.centeredOnHit = true;
  }

  /* ---------- drawing ------------------------------------------------------ */

  /* Mute beats solo. Hiding is the stronger statement, so a layer that is both
     soloed and muted stays off rather than reappearing.

     li === -1 is an entity naming a layer the table does not have. It has no
     row to mute, but solo says "only these", so it goes when anything is
     soloed — otherwise soloing one layer still leaves strays on the canvas and
     the isolation it promises is not one. */
  const visible = li => !S.muted.has(li) && (S.solo.size === 0 || S.solo.has(li));

  function alphaOf(e) {
    if (e._hit) return 1;
    if (S.hover >= 0 && e._li !== S.hover) return 0.12;
    return 1;
  }

  function colourOf(e) {
    if (e._col) return e._col;
    if (e._cl >= 0 && S.layers[e._cl] && S.layers[e._cl]._css) return S.layers[e._cl]._css;
    // No colour decoded for this layer. Chrome grey, so it is visibly "this
    // program does not know" rather than a colour someone might trust.
    return '#6b7280';
  }

  /* Modelspace furniture. Held here with the other pens rather than read from
   * CSS, for the same reason every other colour in this file is: the canvas is
   * drawn, not styled, and a var() lookup per frame is not free. */
  const GRID_MINOR = 'rgba(255,255,255,0.045)';
  const GRID_MAJOR = 'rgba(255,255,255,0.10)';
  const CROSSHAIR  = 'rgba(255,255,255,0.55)';

  const HIGHLIGHT_COLOUR = '#ff2d55';

  /* plan/05-review-canvas.md §2's encoding. Red is shared with
   * HIGHLIGHT_COLOUR on purpose — both mean "look here" — but red alone is
   * not the signal: waiting/straddling differ only by the ring, and
   * unlabelled is amber with a hatch and dashed outline, never red, because
   * it is not a per-entity problem at all — it is the one fix that clears
   * every red entity waiting on it. */
  const PROBLEM_RED = '#ff2d55';
  const UNLABELLED_COLOUR = '#f5a623';

  function problemStyle(e) {
    switch (e._problem) {
      // Drawn exactly as AutoCAD draws it. A resolved row used to recede to
      // 40% alpha, which on a derived layer means every row and a whole
      // canvas in grey; only a row that still needs someone overrides the
      // drawing's own colours. The class stays for the click handler.
      case 'resolved': return null;
      case 'waiting': return { colour: PROBLEM_RED, alpha: 1, dash: null, hatch: false, ring: false };
      case 'straddling': return { colour: PROBLEM_RED, alpha: 1, dash: null, hatch: false, ring: true };
      case 'unlabelled': return { colour: UNLABELLED_COLOUR, alpha: 1, dash: [6, 4], hatch: true, ring: false };
      default: return null;
    }
  }

  /* A polyline segment with a bulge is a circular arc.
   *
   * A POSITIVE bulge curves counter-clockwise, which for a left-to-right chord
   * means the arc passes underneath. The intuitive opposite reading mirrors
   * every curved line about its own chord — smooth, plausible, and wrong
   * everywhere at once. */
  function arcThrough(g, a, b, bulge) {
    const theta = 4 * Math.atan(bulge);
    const chord = Math.hypot(b[0] - a[0], b[1] - a[1]);
    if (!Number.isFinite(theta) || chord === 0) { g.lineTo(...project(b)); return; }
    const r = chord / (2 * Math.sin(Math.abs(theta) / 2));
    const mid = [(a[0] + b[0]) / 2, (a[1] + b[1]) / 2];
    const d = Math.sqrt(Math.max(0, r * r - (chord / 2) ** 2));
    const nx = -(b[1] - a[1]) / chord, ny = (b[0] - a[0]) / chord;
    const sign = (Math.abs(theta) > Math.PI ? -1 : 1) * Math.sign(bulge);
    const c = [mid[0] - nx * d * sign, mid[1] - ny * d * sign];
    const a0 = Math.atan2(a[1] - c[1], a[0] - c[0]);
    const a1 = Math.atan2(b[1] - c[1], b[0] - c[0]);
    const pc = project(c);
    // The canvas Y axis is flipped relative to the drawing's, so a
    // counter-clockwise arc in drawing space is a clockwise one on screen.
    g.arc(pc[0], pc[1], r * S.view.s, -a0, -a1, bulge > 0);
  }

  function drawEnt(g, e) {
    // A review-canvas problem class wins over the entity's own CAD-layer
    // colour, but never over an active search highlight — the two never
    // co-occur in practice (different pages), and if they ever did, "this
    // is the thing you searched for" is still the more specific claim.
    const problem = e._hit ? null : problemStyle(e);
    const col = e._hit ? HIGHLIGHT_COLOUR : (problem?.colour ?? colourOf(e));
    g.strokeStyle = col;
    g.fillStyle = col;
    g.globalAlpha = problem ? problem.alpha : alphaOf(e);
    g.lineWidth = e._hit ? Math.max(2.5, S.dpr * 2.5) : Math.max(1, S.dpr * 0.75);
    g.setLineDash(problem?.dash || []);
    g.beginPath();

    switch (e.type) {
      case 'LINE': {
        const a = XY(e.start), b = XY(e.end);
        if (!a || !b) return;
        g.moveTo(...project(a)); g.lineTo(...project(b)); g.stroke();
        break;
      }
      case 'CIRCLE': {
        const c = XY(e.center);
        if (!c || !Number.isFinite(e.radius)) return;
        const p = project(c);
        g.arc(p[0], p[1], Math.abs(e.radius) * S.view.s, 0, Math.PI * 2);
        g.stroke();
        break;
      }
      case 'ARC': {
        const c = XY(e.center);
        if (!c || !Number.isFinite(e.radius)) return;
        const p = project(c);
        const a0 = (e.start_angle_deg || 0) * Math.PI / 180;
        const a1 = (e.end_angle_deg || 0) * Math.PI / 180;
        g.arc(p[0], p[1], Math.abs(e.radius) * S.view.s, -a0, -a1, true);
        g.stroke();
        break;
      }
      case 'ELLIPSE': {
        const c = XY(e.center), ax = XY(e.major_axis);
        if (!c || !ax) return;
        const p = project(c);
        const major = Math.hypot(ax[0], ax[1]) * S.view.s;
        const ratio = Number.isFinite(e.axis_ratio) ? e.axis_ratio : 1;
        g.ellipse(p[0], p[1], major, major * ratio, -Math.atan2(ax[1], ax[0]), 0, Math.PI * 2);
        g.stroke();
        break;
      }
      case 'LWPOLYLINE': case 'POLYLINE_2D': case 'POLYLINE_3D': {
        const v = e.vertices;
        if (!Array.isArray(v) || v.length === 0) return;
        const bulges = Array.isArray(e.bulges) ? e.bulges : null;
        g.moveTo(...project(v[0]));
        for (let i = 1; i < v.length; i++) {
          const b = bulges ? bulges[i - 1] : 0;
          if (b) arcThrough(g, v[i - 1], v[i], b); else g.lineTo(...project(v[i]));
        }
        if (e.closed) {
          const b = bulges ? bulges[v.length - 1] : 0;
          if (b) arcThrough(g, v[v.length - 1], v[0], b); else g.closePath();
        }
        g.stroke();
        break;
      }
      case 'POINT': {
        const p0 = XY(e.position);
        if (!p0) return;
        const p = project(p0);
        const r = e._hit ? 3 : 1;
        g.fillRect(p[0] - r, p[1] - r, r * 2, r * 2);
        break;
      }
      case 'TEXT': case 'MTEXT': case 'ATTRIB': case 'ATTDEF': drawText(g, e); break;
      case 'INSERT': {
        // The block-content join (block_ref -> owner_block) is exported and
        // unused here on purpose: a cross drawn at the insert point is
        // honestly a marker rather than a symbol pretending to be geometry.
        const p0 = XY(e.position);
        if (!p0) return;
        const p = project(p0);
        g.globalAlpha = alphaOf(e) * (e._hit ? 1 : 0.8);
        const r = e._hit ? 8 : 3;
        g.moveTo(p[0] - r, p[1]); g.lineTo(p[0] + r, p[1]);
        g.moveTo(p[0], p[1] - r); g.lineTo(p[0], p[1] + r);
        g.stroke();
        break;
      }
      default:
        // A shape this program does not decode. It is not drawn and not
        // invented; the reader already named it on stderr.
        break;
    }
    drawProblemOverlay(g, e, problem);
    g.globalAlpha = 1;
    g.setLineDash([]);
  }

  /* The hatch/badge/ring half of §2's encoding, drawn over whatever drawEnt
   * already put down. Bbox rather than the shape's own outline — a hatch
   * that followed a polygon's real silhouette would need a second fill
   * path per entity type, and the encoding only has to say "this one",
   * not trace it exactly. */
  function drawProblemOverlay(g, e, problem) {
    if (!problem || (!problem.hatch && !problem.ring)) return;
    const pts = e._pts && e._pts.length ? e._pts : pointsOf(e);
    if (!pts.length) return;
    let x0 = pts[0][0], y0 = pts[0][1], x1 = x0, y1 = y0;
    for (const [x, y] of pts) {
      if (x < x0) x0 = x; if (y < y0) y0 = y;
      if (x > x1) x1 = x; if (y > y1) y1 = y;
    }
    const corners = [[x0, y0], [x1, y0], [x1, y1], [x0, y1]].map(project);
    const left = Math.min(...corners.map((p) => p[0]));
    const right = Math.max(...corners.map((p) => p[0]));
    const top = Math.min(...corners.map((p) => p[1]));
    const bottom = Math.max(...corners.map((p) => p[1]));
    const pc = [(left + right) / 2, (top + bottom) / 2];

    if (problem.hatch) {
      g.save();
      g.beginPath();
      g.rect(left, top, Math.max(right - left, 1), Math.max(bottom - top, 1));
      g.clip();
      g.strokeStyle = UNLABELLED_COLOUR;
      g.globalAlpha = 0.5;
      g.lineWidth = Math.max(1, S.dpr);
      g.setLineDash([]);
      const span = bottom - top;
      const step = Math.max(6 * S.dpr, 6);
      for (let x = left - span; x < right; x += step) {
        g.beginPath();
        g.moveTo(x, top);
        g.lineTo(x + span, bottom);
        g.stroke();
      }
      g.restore();

      // A "?" badge at the centre — the hatch plus a dashed outline reads
      // as "incomplete" on its own, but the badge is what a colour-blind
      // simulation still shows as unambiguously "answer needed here".
      g.save();
      g.globalAlpha = 1;
      g.fillStyle = UNLABELLED_COLOUR;
      g.font = `${Math.max(12, 14 * S.dpr)}px ui-sans-serif, sans-serif`;
      g.textAlign = 'center';
      g.textBaseline = 'middle';
      g.fillText('?', pc[0], pc[1]);
      g.restore();
    }

    if (problem.ring) {
      const half = Math.hypot(right - left, bottom - top) / 2;
      const r = Math.max(half, 10 * S.dpr) + 6 * S.dpr;
      g.save();
      g.globalAlpha = 1;
      g.strokeStyle = PROBLEM_RED;
      g.lineWidth = Math.max(2, S.dpr * 2);
      g.setLineDash([]);
      g.beginPath();
      g.arc(pc[0], pc[1], r, 0, Math.PI * 2);
      g.stroke();
      g.restore();
    }
  }

  function drawText(g, e) {
    const p0 = XY(e.position);
    if (!p0) return;
    const p = project(p0);
    const h = (Number.isFinite(e.height) ? e.height : 0) * S.view.s;
    const s = e.text_plain != null ? e.text_plain : e.text;

    if (h < 5) { g.fillRect(p[0], p[1], Math.max(1, h), Math.max(1, h)); return; }

    if (s == null || s === '') {
      // A string that did not decode is a dashed ghost box, never invented
      // characters. A guessed label is indistinguishable from a real one and
      // this program exists to stop exactly that.
      g.save();
      g.setLineDash([3, 3]);
      g.strokeRect(p[0], p[1] - h, h * 4, h);
      g.restore();
      return;
    }
    g.save();
    g.translate(p[0], p[1]);
    if (e.rotation_deg) g.rotate(-e.rotation_deg * Math.PI / 180);
    g.font = `${h}px ui-sans-serif, sans-serif`;
    g.fillText(s, 0, 0);
    g.restore();
  }

  /* The world-space rect currently visible on the canvas, inverting
   * project(). Used to cull entities before drawing them — without it,
   * frame() walks and fully draws every entity in S.ents on every dirty
   * frame (i.e. every tick of an active pan/zoom gesture), so cost scales
   * with total entity count in the drawing rather than what's actually on
   * screen, and panning/zooming a dense layer feels sluggish even zoomed
   * into a small corner of it. */
  function visibleWorldRect() {
    const xs = [(0 - S.view.x) / S.view.s, (S.cv.width - S.view.x) / S.view.s];
    const ys = [(S.view.y - 0) / S.view.s, (S.view.y - S.cv.height) / S.view.s];
    return [Math.min(...xs), Math.min(...ys), Math.max(...xs), Math.max(...ys)];
  }

  function intersects(a, b) {
    return a[0] <= b[2] && a[2] >= b[0] && a[1] <= b[3] && a[3] >= b[1];
  }

  /* An entity with no bbox (its position couldn't be determined) always
   * draws — the same fail-open rule internal/archive/filter.go's
   * coarsePosition uses, so "couldn't tell" is never silently treated as
   * "off screen". */
  function onScreen(e, rect) {
    return !e._bbox || intersects(e._bbox, rect);
  }

  /* AutoCAD's modelspace grid. Spacing is chosen in world units from a 1-2-5
   * ladder so that a cell is never smaller than ~14 device pixels: at any
   * zoom the grid reads as a grid rather than as fog, and it steps by a round
   * number of drawing units rather than by whatever the zoom happens to be.
   * Every tenth line is the major one, as AutoCAD draws it. */
  const GRID_MIN_PX = 14;

  function gridStep(scale) {
    const want = GRID_MIN_PX * S.dpr / scale;   // world units per cell, minimum
    const pow = Math.pow(10, Math.floor(Math.log10(want)));
    for (const m of [1, 2, 5, 10]) {
      if (pow * m >= want) return pow * m;
    }
    return pow * 10;
  }

  function drawGrid(g, rect) {
    const step = gridStep(S.view.s);
    if (!Number.isFinite(step) || step <= 0) return;
    const [x0, y0, x1, y1] = rect;
    // Guard against a degenerate view asking for millions of lines.
    if ((x1 - x0) / step > 4000 || (y1 - y0) / step > 4000) return;

    for (const major of [false, true]) {
      const s = major ? step * 10 : step;
      g.beginPath();
      for (let x = Math.ceil(x0 / s) * s; x <= x1; x += s) {
        const px = x * S.view.s + S.view.x;
        g.moveTo(px, 0); g.lineTo(px, S.cv.height);
      }
      for (let y = Math.ceil(y0 / s) * s; y <= y1; y += s) {
        const py = S.view.y - y * S.view.s;
        g.moveTo(0, py); g.lineTo(S.cv.width, py);
      }
      g.strokeStyle = major ? GRID_MAJOR : GRID_MINOR;
      g.lineWidth = 1;
      g.stroke();
    }
  }

  /* The modelspace crosshair: full-width and full-height, with the pickbox
   * square at the centre. Drawn rather than done with a CSS cursor because
   * the lines span the canvas — that is what makes it a CAD crosshair and not
   * a mouse pointer. */
  function drawCrosshair(g) {
    const c = S.cursor;
    if (!c) return;
    g.save();
    g.strokeStyle = CROSSHAIR;
    g.lineWidth = 1;
    g.beginPath();
    g.moveTo(0, c.y); g.lineTo(S.cv.width, c.y);
    g.moveTo(c.x, 0); g.lineTo(c.x, S.cv.height);
    g.stroke();
    const pick = 5 * S.dpr;
    g.strokeRect(c.x - pick, c.y - pick, pick * 2, pick * 2);
    g.restore();
  }

  function frame() {
    S._raf = requestAnimationFrame(frame);
    if (!S.dirty || !S.ctx) return;
    S.dirty = false;

    const g = S.ctx;
    g.setTransform(1, 0, 0, 1, 0, 0);
    g.clearRect(0, 0, S.cv.width, S.cv.height);
    g.lineJoin = 'round';
    g.lineCap = 'round';

    const rect = visibleWorldRect();
    if (S.modelspace) drawGrid(g, rect);
    for (const e of S.ents) {
      if (!visible(e._li)) continue;
      if (e._hit || e._problem) continue; // drawn last, on top
      if (!onScreen(e, rect)) continue;
      drawEnt(g, e);
    }
    // The highlighted entity is drawn in a second pass so it is never
    // occluded by whatever streamed in after it.
    for (const idx of S.hitIndices) {
      const e = S.ents[idx];
      if (e && visible(e._li) && onScreen(e, rect)) drawEnt(g, e);
    }
    // Problem entities the same way, and after the highlight pass: the
    // review canvas never sets both on the same handle, but if it ever did,
    // "this needs a decision" is the more urgent claim to leave on top.
    for (const idx of S.problemIndices) {
      const e = S.ents[idx];
      if (e && visible(e._li) && onScreen(e, rect)) drawEnt(g, e);
    }
    if (S.marquee) drawMarquee(g);
    // Last, so it is never under the drawing.
    if (S.modelspace) drawCrosshair(g);
    if (S.hooks.frame) S.hooks.frame(scaleInfo());
  }

  /* The rubber-band rect while a select-mode drag is in progress. Screen
   * space already — S.marquee is built directly from pointer coordinates,
   * so this needs no project() call. */
  function drawMarquee(g) {
    const { x0, y0, x1, y1 } = S.marquee;
    const x = Math.min(x0, x1), y = Math.min(y0, y1);
    const w = Math.abs(x1 - x0), h = Math.abs(y1 - y0);
    g.save();
    g.setLineDash([4 * S.dpr, 3 * S.dpr]);
    g.lineWidth = Math.max(1, S.dpr);
    g.strokeStyle = '#3b82f6';
    g.fillStyle = 'rgba(59, 130, 246, 0.12)';
    g.fillRect(x, y, w, h);
    g.strokeRect(x, y, w, h);
    g.restore();
  }

  function scaleInfo() {
    if (!S.cv || !S.view.s) return { metres: 0, px: 0, across: 0 };
    const across = S.cv.width / S.view.s;
    const raw = across / 6;
    const pow = Math.pow(10, Math.floor(Math.log10(Math.max(raw, 1e-9))));
    const metres = [1, 2, 5, 10].map(m => m * pow).find(m => m >= raw) || pow * 10;
    return { metres, px: (metres * S.view.s) / S.dpr, across: Math.round(across) };
  }

  /* ---------- interaction --------------------------------------------------- */

  /* Ray-casting point-in-polygon over a closed entity's own vertices —
   * straight edges only (bulges are ignored for hit-testing, unlike
   * drawEnt's arcThrough), which is the right trade for "did this click
   * land inside roughly this shape", not for rendering it. */
  function pointInRing(pt, verts) {
    let inside = false;
    for (let i = 0, j = verts.length - 1; i < verts.length; j = i++) {
      const a = verts[i], b = verts[j];
      if (!a || !b) continue;
      const yi = a[1], yj = b[1];
      if ((yi > pt[1]) !== (yj > pt[1])) {
        const xInt = (b[0] - a[0]) * (pt[1] - yi) / (yj - yi) + a[0];
        if (pt[0] < xInt) inside = !inside;
      }
    }
    return inside;
  }

  function isClosedPolygon(e) {
    return (e.type === 'LWPOLYLINE' || e.type === 'POLYLINE_2D' || e.type === 'POLYLINE_3D')
      && e.closed && Array.isArray(e.vertices) && e.vertices.length >= 3;
  }

  /* Only problem-classed entities are hit-testable — this is the review
   * canvas's own interaction, not a general picker, and everything else on
   * the canvas is context. A closed polygon (a boundary) is tested exactly;
   * everything else (trackers, placements) is tested against a fixed
   * screen-pixel tolerance around its own points, since a click is never
   * pixel-perfect on a point-like entity. Smallest-bbox-area wins among
   * overlapping polygon matches, the same nesting tie-break
   * internal/attrexec/containment.go uses server-side. */
  function hitTestAt(wx, wy) {
    let best = null, bestArea = Infinity;
    for (const idx of S.problemIndices) {
      const e = S.ents[idx];
      if (!e || !isClosedPolygon(e)) continue;
      if (!pointInRing([wx, wy], e.vertices)) continue;
      const area = e._bbox ? (e._bbox[2] - e._bbox[0]) * (e._bbox[3] - e._bbox[1]) : Infinity;
      if (area < bestArea) { bestArea = area; best = e; }
    }
    if (best) return best;

    const tolWorld = (14 * S.dpr) / S.view.s;
    let nearest = null, nearestDist = tolWorld;
    for (const idx of S.problemIndices) {
      const e = S.ents[idx];
      if (!e || isClosedPolygon(e) || !e._pts || !e._pts.length) continue;
      for (const p of e._pts) {
        const d = Math.hypot(p[0] - wx, p[1] - wy);
        if (d < nearestDist) { nearestDist = d; nearest = e; }
      }
    }
    return nearest;
  }

  /* Every problem entity (any point of its geometry) whose position falls
   * inside the marquee's world-space rect, restricted to 'straddling' —
   * §3.3's bulk assign is for straddlers specifically; an unlabelled
   * polygon is numbered by a single click (§3.1) and a waiting entity has
   * no direct fix at all (its polygon does), so including either in a
   * marquee selection would offer a write nothing downstream expects. */
  function marqueeSelect(rectScreen) {
    const x0s = Math.min(rectScreen.x0, rectScreen.x1), x1s = Math.max(rectScreen.x0, rectScreen.x1);
    const y0s = Math.min(rectScreen.y0, rectScreen.y1), y1s = Math.max(rectScreen.y0, rectScreen.y1);
    const wx0 = (x0s - S.view.x) / S.view.s, wx1 = (x1s - S.view.x) / S.view.s;
    const wy0 = (S.view.y - y1s) / S.view.s, wy1 = (S.view.y - y0s) / S.view.s;
    const out = [];
    for (const idx of S.problemIndices) {
      const e = S.ents[idx];
      if (!e || e._problem !== 'straddling') continue;
      const pts = e._pts && e._pts.length ? e._pts : pointsOf(e);
      if (!pts.length) continue;
      if (pts.some(([x, y]) => x >= wx0 && x <= wx1 && y >= wy0 && y <= wy1)) out.push(e.handle);
    }
    return out;
  }

  let unbindPointer = () => {};

  function bindPointer() {
    let drag = null;
    const canvasXY = e => {
      const r = S.cv.getBoundingClientRect();
      return [(e.clientX - r.left) * S.dpr, (e.clientY - r.top) * S.dpr];
    };
    const onDown = e => {
      const [cx, cy] = canvasXY(e);
      drag = { x: e.clientX, y: e.clientY, moved: false };
      if (S.interactionMode === 'select') {
        S.marquee = { x0: cx, y0: cy, x1: cx, y1: cy };
        S.dirty = true;
      }
    };
    const onMove = e => {
      if (!drag) return;
      const dx = (e.clientX - drag.x) * S.dpr, dy = (e.clientY - drag.y) * S.dpr;
      if (Math.abs(dx) + Math.abs(dy) > 2) drag.moved = true;
      if (S.interactionMode === 'select') {
        if (S.marquee) { const [cx, cy] = canvasXY(e); S.marquee.x1 = cx; S.marquee.y1 = cy; }
        drag.x = e.clientX; drag.y = e.clientY;
        S.dirty = true;
        return; // a select-mode drag never pans the view
      }
      S.view.x += dx; S.view.y += dy;
      drag.x = e.clientX; drag.y = e.clientY;
      S.fitted = true;   // the user has framed it; stop refitting as data arrives
      S.userMoved = true;
      S.dirty = true;
    };
    const onUp = e => {
      if (!drag) return;
      if (S.interactionMode === 'select' && drag.moved && S.marquee) {
        const rect = S.marquee;
        S.marquee = null;
        S.dirty = true;
        if (S.hooks.marquee) S.hooks.marquee(marqueeSelect(rect));
      } else if (!drag.moved) {
        // A plain click, in either mode — never ambiguous with a pan or a
        // marquee, since nothing moved.
        const [cx, cy] = canvasXY(e);
        const wx = (cx - S.view.x) / S.view.s;
        const wy = (S.view.y - cy) / S.view.s;
        const hit = hitTestAt(wx, wy);
        if (hit && S.hooks.click) S.hooks.click(hit);
        if (S.marquee) { S.marquee = null; S.dirty = true; }
      } else if (S.marquee) {
        S.marquee = null;
        S.dirty = true;
      }
      drag = null;
    };
    const onWheel = e => {
      e.preventDefault();
      const r = S.cv.getBoundingClientRect();
      const mx = (e.clientX - r.left) * S.dpr, my = (e.clientY - r.top) * S.dpr;
      const k = Math.exp(-e.deltaY * 0.0015);
      S.view.x = mx - (mx - S.view.x) * k;
      S.view.y = my - (my - S.view.y) * k;
      S.view.s *= k;
      S.fitted = true;
      S.userMoved = true;
      S.dirty = true;
    };

    /* The crosshair needs the pointer whether or not a drag is in progress,
     * and onMove above returns early when there is none. Its own listener,
     * on the canvas rather than the window, so the lines disappear when the
     * pointer leaves the drawing instead of tracking it across the page. */
    const onHover = e => {
      if (!S.modelspace) return;
      const [cx, cy] = canvasXY(e);
      S.cursor = { x: cx, y: cy };
      S.dirty = true;
    };
    const onLeave = () => {
      if (!S.cursor) return;
      S.cursor = null;
      S.dirty = true;
    };

    S.cv.addEventListener('mousedown', onDown);
    S.cv.addEventListener('mousemove', onHover);
    S.cv.addEventListener('mouseleave', onLeave);
    addEventListener('mousemove', onMove);
    addEventListener('mouseup', onUp);
    S.cv.addEventListener('wheel', onWheel, { passive: false });

    unbindPointer = () => {
      S.cv.removeEventListener('mousedown', onDown);
      S.cv.removeEventListener('mousemove', onHover);
      S.cv.removeEventListener('mouseleave', onLeave);
      removeEventListener('mousemove', onMove);
      removeEventListener('mouseup', onUp);
      S.cv.removeEventListener('wheel', onWheel);
    };
  }

  let resizeObserver = null;
  let onWindowResize = null;

  /* ---------- API ------------------------------------------------------------ */

  const API = {
    get entityCount() { return S.ents.length; },
    get layerCount() { return S.counts; },
    get layers() { return S.layers; },
    get blocks() { return S.blocks; },
    get meta() { return S.meta; },
    get summary() { return S.summary; },
    get state() { return S; },

    init(opts) {
      S.cv = opts.canvas;
      S.ctx = S.cv.getContext('2d');
      S.dpr = Math.max(1, Math.min(2, window.devicePixelRatio || 1));
      S.hooks = opts.hooks || {};
      sizeCanvas(S.cv);
      bindPointer();
      frame();
      resizeObserver = new ResizeObserver(() => { sizeCanvas(S.cv); if (!S.userMoved) reframe(); S.dirty = true; });
      resizeObserver.observe(S.cv.parentElement || S.cv);
      onWindowResize = () => sizeCanvas(S.cv);
      addEventListener('resize', onWindowResize);
    },

    /* Undoes init(): stops the render loop and every listener it attached.
     * Added for M5 — the salvaged app never unmounted a canvas, and a
     * dashboard that switches drawings does. Without this, every visit to
     * the Drawing view leaves a requestAnimationFrame loop running. */
    destroy() {
      if (S._raf) cancelAnimationFrame(S._raf);
      unbindPointer();
      if (resizeObserver) resizeObserver.disconnect();
      if (onWindowResize) removeEventListener('resize', onWindowResize);
    },

    /* Discards the previous drawing. Called before a new file streams in, so a
     * failed open leaves an empty canvas rather than the last drawing with new
     * shapes accumulating on top of it.
     *
     * S.problems and S.interactionMode are deliberately NOT cleared here —
     * ReviewCanvas streams several drawings into one engine instance without
     * calling reset() between them (real-world UTM means a multi-file
     * overlay is valid, plan/05-review-canvas.md §1), and a per-drawing
     * reset() must not throw away the review session's classification.
     * problemIndices is cleared because it is derived (indices into the
     * S.ents array reset() just emptied), not input; it repopulates itself
     * as ingestLine consults S.problems again for whatever streams in next. */
    reset() {
      S.ents = []; S.blocks = new Map(); S.counts = [];
      S.extent = null; S.fitted = false; S.userMoved = false; S.lastFrame = null;
      S.meta = null; S.summary = null;
      S.muted.clear(); S.solo.clear(); S.hover = -1;
      S.highlightHandle = null; S.hitIndices = []; S.centeredOnHit = false;
      S.problemIndices = []; S.marquee = null;
      S.dirty = true;
    },

    setLayers(rows) {
      S.layers = rows || [];
      S.layerIndex = new Map();
      S.counts = new Array(S.layers.length).fill(0);
      S.layers.forEach((L, i) => {
        S.layerIndex.set(L.name, i);
        L._css = layerColour(L);   // null stays null; no dot for it
      });
      // Entities already ingested keep the _li they got at ingest. A caller
      // that only ever appends to the table (CadViewport, drawing after
      // drawing) keeps those indices valid, so their counts are recounted
      // rather than zeroed — zeroing dropped the rail's figures back to
      // whatever the newest drawing contributed.
      for (const e of S.ents) {
        if (e._li >= 0 && e._li < S.counts.length) S.counts[e._li] += 1;
      }
    },

    ingest,
    fit,
    /* The CSS colour an ingested entity draws in, outside any highlight or
       problem overlay — what AutoCAD would show. */
    colourOf,
    /* The true extent, the extent actually framed, and how many shapes lie
     * beyond the frame. The app states the last of these rather than letting a
     * stray silently sit off-screen. */
    get extent() { return S.extent; },
    get frameExtent() { return S.frame_extent || S.extent; },
    outsideFrame() { return outside(S.frame_extent); },
    setHover(i) { if (S.hover !== i) { S.hover = i; S.dirty = true; } },
    toggleSolo(i) { S.solo.has(i) ? S.solo.delete(i) : S.solo.add(i); S.dirty = true; return S.solo.has(i); },
    toggleMute(i) { S.muted.has(i) ? S.muted.delete(i) : S.muted.add(i); S.dirty = true; return S.muted.has(i); },
    /* Used by shift-click, which sets a whole range to one state rather than
       flipping each row and leaving the range half on. */
    setSolo(is, on) {
      is.forEach(i => (on ? S.solo.add(i) : S.solo.delete(i)));
      S.dirty = true;
    },
    clearFilters() { S.solo.clear(); S.muted.clear(); S.dirty = true; },

    /* How many shapes the current solo/mute leaves on the canvas.
       Shapes and not layers, because soloing a layer the stream never filed
       anything against is the easiest way to end up looking at nothing —
       and a blank canvas is what a drawing that failed to decode looks like,
       so the window says which one this is. */
    drawableShapes() {
      let n = 0;
      for (let i = 0; i < S.counts.length; i++) if (visible(i)) n += S.counts[i] || 0;
      return n;
    },

    /* ---- M5 additions: drill-down highlighting ---------------------------- */

    /* Sets the handle a drill-down is looking for, normalised the same way
     * the index is. Call before ingest() starts (or at any point during it —
     * entities already ingested are re-scanned) so the match survives no
     * matter when in the stream the entity happens to arrive. */
    setHighlight(handle) {
      S.highlightHandle = normalizeHandle(handle);
      S.hitIndices = [];
      S.centeredOnHit = false;
      if (!S.highlightHandle) { S.dirty = true; return; }
      S.ents.forEach((e, i) => {
        const hit = normalizeHandle(e.handle) === S.highlightHandle;
        e._hit = hit;
        if (hit) S.hitIndices.push(i);
      });
      if (S.hitIndices.length) centerOnEntity(S.hitIndices[0]);
      S.dirty = true;
    },

    /* True once the highlighted handle has actually been seen in the stream.
     * A drill-down whose handle never arrives — the handle-normalisation trap
     * M5.md warns about — is silent otherwise: the canvas fills in and
     * nothing ever marks itself, with no error anywhere. */
    get highlightFound() { return S.hitIndices.length > 0; },

    /* Centres and zooms the view on an explicit bbox — {min_x,min_y,max_x,
     * max_y}, the same shape a search hit's `bbox` field carries — without
     * requiring the entity to be a highlight match. Used when the caller
     * already knows where to look (e.g. a region search) before any data has
     * streamed in. */
    centerOn(bbox) {
      if (!bbox || !bbox.has_box && bbox.has_box !== undefined) return;
      const box = [bbox.min_x ?? bbox.minX, bbox.min_y ?? bbox.minY,
                   bbox.max_x ?? bbox.maxX, bbox.max_y ?? bbox.maxY];
      if (box.some(v => !Number.isFinite(v))) return;
      frameExtentTo(box);
      S.fitted = true;
    },

    /* ---- plan/05-review-canvas.md additions --------------------------- */

    /* map: normalised-or-raw handle -> 'resolved' | 'waiting' | 'straddling'
     * | 'unlabelled', a Map or a plain object either way. Rescans S.ents the
     * same way setHighlight does, so it can be called before or after (or
     * repeatedly during) a stream — a handle that arrives later still picks
     * up its classification at ingest time. Call with an empty map/Map to
     * clear every entity's problem state. */
    setProblems(map) {
      const entries = map instanceof Map ? map.entries() : Object.entries(map || {});
      const normalized = new Map();
      for (const [k, v] of entries) if (v) normalized.set(normalizeHandle(k), v);
      S.problems = normalized;
      S.problemIndices = [];
      S.ents.forEach((e, i) => {
        const p = S.problems.get(normalizeHandle(e.handle));
        if (p) { e._problem = p; S.problemIndices.push(i); }
        else if (e._problem) delete e._problem;
      });
      S.dirty = true;
    },

    /* 'pan' (default) or 'select'. Switching modes cancels any in-progress
     * marquee rather than leaving a rect the new mode will never resolve. */
    /* Re-applies the last framing at the canvas's current size, without
     * deciding afresh what to look at. This is what the resize path needs: a
     * pane framed while it was display:none was framed against a zero rect,
     * and the scale that produced is only right for a 1px canvas. */
    refit() { reframe(); },

    /* Turns on the AutoCAD modelspace furniture — the grid and the crosshair.
     * A pane that is standing in for modelspace asks for it; the small
     * preview canvases do not, because a crosshair in a 220x160 flyout is
     * chrome over the one thing it exists to show. */
    setModelspace(on) {
      S.modelspace = !!on;
      if (!on) S.cursor = null;
      S.dirty = true;
    },

    setInteractionMode(mode) {
      S.interactionMode = mode === 'select' ? 'select' : 'pan';
      S.marquee = null;
      S.dirty = true;
    },

    /* Direct world-space hit-test, exposed for callers that already have a
     * world point (tests, or a caller not going through the canvas's own
     * pointer events at all) — bindPointer's onUp uses the same function
     * internally for a real click. */
    hitTest(wx, wy) { return hitTestAt(wx, wy); },

    aciRGB, layerColour, scaleInfo,
  };

  return API;
}
