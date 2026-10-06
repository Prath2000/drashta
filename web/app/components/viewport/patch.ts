// Unified diff parser (git `diff --git` and fossil `Index:` styles), ported
// from the prototype. Pure function; the DiffView component draws the result.

export interface DiffLine {
  /** "+" added, "-" removed, " " context, "\\" a "No newline" marker. */
  k: "+" | "-" | " " | "\\";
  o: number | "";
  n: number | "";
  t: string;
}
export interface Hunk {
  header: string;
  lines: DiffLine[];
}
export interface DiffFile {
  path: string;
  hunks: Hunk[];
  add: number;
  del: number;
  /** "new file mode", "Binary files … differ", "rename from …" and so on. */
  meta: string[];
}

export function parsePatch(patch: string | null | undefined): DiffFile[] {
  const files: DiffFile[] = [];
  let f: DiffFile | null = null;
  let h: Hunk | null = null;
  let oN = 0;
  let nN = 0;
  const L = String(patch || "")
    .replace(/\r\n?/g, "\n")
    .split("\n");
  for (let i = 0; i < L.length; i++) {
    const l = L[i];
    let m: RegExpMatchArray | null;
    if ((m = l.match(/^diff --git a\/(.+?) b\/(.+)$/))) {
      f = { path: m[2], hunks: [], add: 0, del: 0, meta: [] };
      files.push(f);
      h = null;
      continue;
    }
    if ((m = l.match(/^Index: (.+?)\s*$/))) {
      f = { path: m[1], hunks: [], add: 0, del: 0, meta: [] };
      files.push(f);
      h = null;
      continue;
    }
    if (!f) continue;
    if (!h) {
      if (/^={5,}/.test(l) || /^(---|\+\+\+)( |$)/.test(l) || /^index [0-9a-f]/.test(l)) continue;
      if (/^(new file mode|deleted file mode|rename (from|to)|similarity index|Binary files|old mode|new mode|DELETED|ADDED)/.test(l)) {
        f.meta.push(l);
        continue;
      }
    }
    if ((m = l.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$/))) {
      h = { header: l, lines: [] };
      f.hunks.push(h);
      oN = +m[1];
      nN = +m[3];
      continue;
    }
    if (!h) continue;
    const c = l[0];
    if (c === "+") {
      h.lines.push({ k: "+", o: "", n: nN++, t: l.slice(1) });
      f.add++;
    } else if (c === "-") {
      h.lines.push({ k: "-", o: oN++, n: "", t: l.slice(1) });
      f.del++;
    } else if (c === " ") h.lines.push({ k: " ", o: oN++, n: nN++, t: l.slice(1) });
    else if (c === "\\") h.lines.push({ k: "\\", o: "", n: "", t: l });
    else if (l === "" && i < L.length - 1) h.lines.push({ k: " ", o: oN++, n: nN++, t: "" });
  }
  return files;
}

/** Commit message -> headline + bullets (fossil puts the whole message on one line, bullets split by "  - "). */
export function splitMessage(msg: string): { headline: string; bullets: string[] } {
  const parts = String(msg || "")
    .trim()
    .split(/\s{2,}-\s+|\s-\s(?=[\w./-]+\.\w+:)/);
  return {
    headline: parts[0] || "(no message)",
    bullets: parts.slice(1).map((b) => b.replace(/^([\w./-]+\.\w+):/, "`$1`:")),
  };
}
