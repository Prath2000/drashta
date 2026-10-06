// Types for aci.js (copied from a sibling project; see the header of that file).

/** One row of `chitralipi layers --as json` (only the part the colour needs). */
export interface CadLayerColourSource {
  color?: { rgb?: { r: number; g: number; b: number } | null; index?: number } | null;
}

/** An AutoCAD Color Index (0-255) as a CSS colour, or null. */
export function aciRGB(i: number): string | null;
/** The CSS colour a layer row draws in, or null when the file did not say. */
export function layerColour(layer: CadLayerColourSource | null | undefined): string | null;
export function rgbHex(r: number, g: number, b: number): string;
export function trueColorRGB(c: { r: number; g: number; b: number } | null | undefined): string | null;
