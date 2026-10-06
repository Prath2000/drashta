// Types for points.js (copied from a sibling project; see the header of that file).

export function XY(p: unknown): [number, number, ...number[]] | null;
export function pointsOf(e: Record<string, unknown>): Array<[number, number, ...number[]]>;
