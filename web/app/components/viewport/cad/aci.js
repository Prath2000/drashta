// Copied from a sibling drawing project (dashboard/src/render/aci.js, commit 4ccf486, 2026-10-01). Keep in step by hand; do not edit the drawing code here.
/* The ACI→RGB table, verbatim from a sibling project's desktop app
 * (that project's internal/engine/testdata/engine.js). Do not "clean this up" into a
 * formula — see M5.md's note on this exact function. AutoCAD's Color Index
 * table rounds its hue and saturation axes differently, and one shared
 * rounding rule cannot reproduce both:
 *
 *   - the hue ramp TRUNCATES — index 20 is ff3f00, not the ff4000 that
 *     rounding 255x0.25 = 63.75 gives;
 *   - the desaturated channel ROUNDS — index 19 is 4f3535, where 79x2/3 =
 *     52.67 truncates to 0x34 and rounds to the correct 0x35.
 *
 * Both are checked against the published table in the harness. An off-by-one
 * channel is invisible, which is exactly why it would never have been found
 * later: the dot is the layer's real colour, so it is either the table's
 * value or it is nothing.
 */

const ACI_FIXED = [
  '#ffffff', '#ff0000', '#ffff00', '#00ff00', '#00ffff',
  '#0000ff', '#ff00ff', '#ffffff', '#808080', '#c0c0c0',
];
const ACI_GREY = [0x33, 0x50, 0x69, 0x82, 0xbe, 0xff];
const ACI_VALUES = [255, 189, 129, 104, 79];

const hex2 = n => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, '0');
export const rgbHex = (r, g, b) => `#${hex2(r)}${hex2(g)}${hex2(b)}`;

/* An AutoCAD Color Index as a CSS colour, or null when there is no colour to
 * give. Null is not grey: a layer whose colour did not decode gets no dot at
 * all, and a default here would invent one. */
export function aciRGB(i) {
  if (!Number.isFinite(i) || i < 0 || i > 255) return null;
  if (i < 10) return ACI_FIXED[i];
  if (i >= 250) { const b = ACI_GREY[i - 250]; return rgbHex(b, b, b); }

  const p = (i - 10) % 10;
  const hue = Math.floor((i - 10) / 10) * 15;
  const v = ACI_VALUES[Math.floor(p / 2)];
  // Even positions are fully saturated; odd ones lift the two minor channels
  // to two-thirds of the value.
  const lo = p % 2 === 0 ? 0 : Math.round(v * 2 / 3);

  const sector = Math.floor(hue / 60);
  const frac = (hue % 60) / 60;
  const step = Math.floor((v - lo) * frac);
  const up = lo + step, down = v - step;

  switch (sector) {
    case 0: return rgbHex(v, up, lo);
    case 1: return rgbHex(down, v, lo);
    case 2: return rgbHex(lo, v, up);
    case 3: return rgbHex(lo, down, v);
    case 4: return rgbHex(up, lo, v);
    default: return rgbHex(v, lo, down);
  }
}

export function trueColorRGB(c) {
  if (!c || !Number.isFinite(c.r) || !Number.isFinite(c.g) || !Number.isFinite(c.b)) return null;
  return rgbHex(c.r, c.g, c.b);
}

/* The colour a layer row and its shapes draw in, or null when the file did
 * not say. Callers must handle null rather than substituting anything. */
export function layerColour(layer) {
  const c = layer && layer.color;
  if (!c) return null;
  return trueColorRGB(c.rgb) || (('index' in c) ? aciRGB(c.index) : null);
}
