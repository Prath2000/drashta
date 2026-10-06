// A small markdown block parser for the "Rendered" view of docs (ported from
// the prototype). Each block keeps its source line range (from..to), so line
// notes can be shown under the block they point at.
import { langOf, tokenize, type Seg } from "./highlight";

export type InlineKind = "plain" | "code" | "strong" | "link" | "em";
export interface Inline {
  t: string;
  k: InlineKind;
}

export interface ListItem {
  indent: number;
  marker: string;
  text: string;
}

export type Block =
  | { kind: "h1" | "h2" | "h3" | "p" | "quote"; from: number; to: number; segs: Inline[]; pre?: boolean }
  | { kind: "hr"; from: number; to: number }
  | { kind: "code"; from: number; to: number; code: Seg[][] }
  | { kind: "table"; from: number; to: number; rows: string[][] }
  | { kind: "list"; from: number; to: number; ordered: boolean; items: ListItem[] };

/** Inline spans: `code`, **strong**, [link](url) (text only, never a live link: D4), *em*. */
export function inline(text: string): Inline[] {
  const segs: Inline[] = [];
  const re = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)\s]+\))|(\*[^*\s][^*]*\*)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(text))) {
    if (m.index > last) segs.push({ t: text.slice(last, m.index), k: "plain" });
    const tok = m[0];
    if (m[1]) segs.push({ t: tok.slice(1, -1), k: "code" });
    else if (m[2]) segs.push({ t: tok.slice(2, -2), k: "strong" });
    else if (m[3]) segs.push({ t: (tok.match(/^\[([^\]]+)\]/) as RegExpMatchArray)[1], k: "link" });
    else segs.push({ t: tok.slice(1, -1), k: "em" });
    last = m.index + tok.length;
  }
  if (last < text.length) segs.push({ t: text.slice(last), k: "plain" });
  if (!segs.length) segs.push({ t: "", k: "plain" });
  return segs;
}

const isFence = (l: string) => /^\s*```/.test(l);
const isList = (l: string) => /^\s*([-*+]|\d+[.)])\s+/.test(l);
const isHr = (l: string) => /^\s*([-*_])(\s*\1){2,}\s*$/.test(l);
const isHead = (l: string) => /^#{1,6}\s/.test(l);
const isTable = (l: string) => /^\s*\|/.test(l);
const isQuote = (l: string) => /^\s*>/.test(l);
const starts = (l: string) => isFence(l) || isList(l) || isHr(l) || isHead(l) || isTable(l) || isQuote(l);

/** Markdown lines -> blocks. `base` is the file line number of lines[0]. */
export function parseMd(lines: string[], base = 1): Block[] {
  const blocks: Block[] = [];
  let i = 0;
  const ln = (idx: number) => idx + base;
  while (i < lines.length) {
    const l = lines[i];
    const from = ln(i);
    if (!l.trim()) {
      i++;
      continue;
    }
    if (isFence(l)) {
      const lang = (l.match(/```\s*(\w+)/) || [])[1] || "";
      const body: string[] = [];
      i++;
      while (i < lines.length && !isFence(lines[i])) body.push(lines[i++]);
      i++;
      const lk = lang ? langOf("x." + (lang === "sh" || lang === "bash" ? "txt" : lang))[0] : "text";
      blocks.push({ kind: "code", from, to: ln(i - 1), code: tokenize(body, lk) });
      continue;
    }
    const h = l.match(/^(#{1,6})\s+(.*?)\s*#*\s*$/);
    if (h) {
      const lvl = Math.min(3, h[1].length);
      blocks.push({ kind: ("h" + lvl) as "h1" | "h2" | "h3", from, to: from, segs: inline(h[2]) });
      i++;
      continue;
    }
    if (isHr(l)) {
      blocks.push({ kind: "hr", from, to: from });
      i++;
      continue;
    }
    if (isTable(l)) {
      const rows: string[][] = [];
      while (i < lines.length && isTable(lines[i])) {
        const r = lines[i++];
        if (/^\s*\|?\s*:?-{2,}/.test(r) && !/[A-Za-z0-9]/.test(r)) continue;
        rows.push(
          r
            .trim()
            .replace(/^\|/, "")
            .replace(/\|$/, "")
            .split("|")
            .map((c) => c.trim()),
        );
      }
      blocks.push({ kind: "table", from, to: ln(i - 1), rows });
      continue;
    }
    if (isQuote(l)) {
      const buf: string[] = [];
      while (i < lines.length && isQuote(lines[i])) buf.push(lines[i++].replace(/^\s*>\s?/, ""));
      blocks.push({ kind: "quote", from, to: ln(i - 1), segs: inline(buf.join(" ")) });
      continue;
    }
    if (isList(l)) {
      const items: ListItem[] = [];
      const ordered = /^\s*\d/.test(l);
      while (i < lines.length && lines[i].trim()) {
        const x = lines[i];
        const lm = x.match(/^(\s*)([-*+]|\d+[.)])\s+(.*)$/);
        if (lm) items.push({ indent: Math.min(3, Math.floor(lm[1].length / 2)), marker: /\d/.test(lm[2]) ? lm[2] : "·", text: lm[3] });
        else if (/^\s+/.test(x) && items.length && !starts(x.trim())) items[items.length - 1].text += " " + x.trim();
        else if (starts(x)) break;
        else if (items.length) items[items.length - 1].text += " " + x.trim();
        i++;
      }
      blocks.push({ kind: "list", ordered, from, to: ln(i - 1), items });
      continue;
    }
    const buf: string[] = [];
    while (i < lines.length && lines[i].trim() && !(buf.length && starts(lines[i]))) buf.push(lines[i++].trim());
    blocks.push({ kind: "p", from, to: ln(i - 1), segs: inline(buf.join(" ")) });
  }
  return blocks;
}

/** Plain text "rendered": paragraphs split by blank lines, list-like runs kept, short ALL-CAPS lines as headings. */
export function parseText(lines: string[], base = 1): Block[] {
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    if (!lines[i].trim()) {
      i++;
      continue;
    }
    const from = i + base;
    const buf: string[] = [];
    while (i < lines.length && lines[i].trim()) buf.push(lines[i++]);
    const to = i - 1 + base;
    if (buf.every((b) => /^\s*([-*•]|\d+[.)])\s+/.test(b))) {
      out.push({ kind: "list", ordered: false, from, to, items: buf.map((b) => ({ indent: 0, marker: "·", text: b.replace(/^\s*([-*•]|\d+[.)])\s+/, "") })) });
    } else if (buf.length === 1 && buf[0].length < 70 && /^[A-Z0-9][^.]*$/.test(buf[0].trim()) && buf[0] === buf[0].toUpperCase()) {
      out.push({ kind: "h3", from, to, segs: [{ t: buf[0].trim(), k: "plain" }] });
    } else {
      out.push({ kind: "p", from, to, segs: [{ t: buf.join("\n"), k: "plain" }], pre: true });
    }
  }
  return out;
}
