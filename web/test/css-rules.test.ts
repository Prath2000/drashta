// Decision U5: one font weight (400), no italics, no bold anywhere.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const APP = join(__dirname, "..", "app");

function files(dir: string, exts: string[]): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...files(p, exts));
    else if (exts.some((e) => name.endsWith(e))) out.push(p);
  }
  return out;
}

describe("font rules (U5)", () => {
  const css = files(APP, [".css"]);

  it("finds the stylesheet", () => {
    expect(css.length).toBeGreaterThan(0);
  });

  it("uses font-weight 400 only in CSS", () => {
    const bad: string[] = [];
    for (const f of css) {
      const text = readFileSync(f, "utf8");
      for (const m of text.matchAll(/font-weight\s*:\s*([^;}\n]+)/gi)) {
        if (m[1].trim() !== "400") bad.push(f + ": " + m[0]);
      }
      for (const m of text.matchAll(/\bfont\s*:[^;}]*\b(bold|bolder|[5-9]00)\b/gi)) bad.push(f + ": " + m[0]);
    }
    expect(bad).toEqual([]);
  });

  it("never uses italic or oblique in CSS", () => {
    const bad = css.filter((f) => /\b(italic|oblique)\b/i.test(readFileSync(f, "utf8")));
    expect(bad).toEqual([]);
  });

  it("sets no other weight or italic in TSX inline styles", () => {
    const bad: string[] = [];
    for (const f of files(APP, [".tsx", ".ts"])) {
      if (f.endsWith(".test.ts") || f.endsWith(".test.tsx")) continue;
      const text = readFileSync(f, "utf8");
      for (const m of text.matchAll(/fontWeight\s*:\s*["']?([^,}"'\s]+)/g)) if (m[1] !== "400") bad.push(f + ": " + m[0]);
      if (/fontStyle\s*:\s*["']italic/.test(text)) bad.push(f + ": fontStyle italic");
      if (/<(b|strong|i|em)[\s>]/.test(text)) bad.push(f + ": bold/italic tag");
    }
    expect(bad).toEqual([]);
  });
});
