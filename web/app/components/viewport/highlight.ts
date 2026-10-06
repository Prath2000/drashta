// A tiny line tokenizer for the code and diff views (ported from the
// prototype ViewCode.dc.html). It is not a full parser: it colours keywords,
// strings, comments, numbers and types well enough to read code at a glance.
//
// Token kinds: p plain, k keyword, s string, c comment, n number/literal,
// t type/tag/key, a attribute/variable. CSS classes are `vp-tk-<kind>`.

export type TokKind = "p" | "k" | "s" | "c" | "n" | "t" | "a";
export interface Seg {
  t: string;
  k: TokKind;
}
export type Lang = "go" | "gomod" | "js" | "json" | "html" | "xml" | "css" | "make" | "md" | "text";

/** [lang, human label] for a path. */
export function langOf(path: string): [Lang, string] {
  const base = (path.split("/").pop() ?? path).toLowerCase();
  if (base === "makefile" || base.endsWith(".mk")) return ["make", "Makefile"];
  if (base === "go.mod" || base === "go.sum") return ["gomod", "Go module"];
  const ext = base.includes(".") ? (base.split(".").pop() ?? "") : "";
  const map: Record<string, [Lang, string]> = {
    go: ["go", "Go"],
    js: ["js", "JavaScript"],
    mjs: ["js", "JavaScript"],
    cjs: ["js", "JavaScript"],
    ts: ["js", "TypeScript"],
    jsx: ["js", "JSX"],
    tsx: ["js", "TSX"],
    rs: ["js", "Rust"],
    html: ["html", "HTML"],
    htm: ["html", "HTML"],
    json: ["json", "JSON"],
    plist: ["xml", "Property list"],
    xml: ["xml", "XML"],
    svg: ["xml", "SVG"],
    css: ["css", "CSS"],
    md: ["md", "Markdown"],
    markdown: ["md", "Markdown"],
    txt: ["text", "Plain text"],
  };
  return map[ext] ?? ["text", ext ? ext.toUpperCase() : "Text"];
}

interface Cfg {
  kw: Set<string>;
  lit: Set<string>;
  ty: Set<string>;
  line: string[];
  block: boolean;
  quotes: string;
  multi: string;
  keyColon?: boolean;
}

const set = (s: string) => new Set(s ? s.split(" ") : []);
const CFG: Record<string, Cfg> = {
  go: {
    kw: set("break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var"),
    lit: set("true false nil iota"),
    ty: set("string int int8 int16 int32 int64 uint uint8 uint16 uint32 uint64 uintptr byte rune bool error float32 float64 any complex128"),
    line: ["//"],
    block: true,
    quotes: "\"'`",
    multi: "`",
  },
  gomod: { kw: set("module go require replace exclude toolchain retract"), lit: set(""), ty: set(""), line: ["//"], block: false, quotes: '"`', multi: "" },
  js: {
    kw: set(
      "async await break case catch class const continue debugger default delete do else export extends finally for from function if import in instanceof let new of return static super switch this throw try typeof var void while yield get set fn pub mut impl struct enum use mod match loop where trait",
    ),
    lit: set("true false null undefined NaN Infinity None Some Ok Err self Self"),
    ty: set("document window console Math JSON Object Array Promise Map Set Date String Vec Option Result"),
    line: ["//"],
    block: true,
    quotes: "\"'`",
    multi: "`",
  },
  json: { kw: set(""), lit: set("true false null"), ty: set(""), line: [], block: false, quotes: '"', multi: "", keyColon: true },
};

interface St {
  block: string | null;
  depth: number;
  fence?: boolean;
  sub?: string | null;
  subSt?: St;
  pending?: string | null;
}

function closeAt(line: string, from: number, q: string): number {
  for (let j = from; j < line.length; j++) {
    if (line[j] === "\\" && q !== "`") {
      j++;
      continue;
    }
    if (line[j] === q) return j;
  }
  return -1;
}

type Raw = [string, TokKind][];

function scanC(line: string, cfg: Cfg, st: St): Raw {
  const out: Raw = [];
  let i = 0;
  let buf = "";
  const flush = () => {
    if (buf) {
      out.push([buf, "p"]);
      buf = "";
    }
  };
  const push = (t: string, k: TokKind) => {
    if (t) {
      flush();
      out.push([t, k]);
    }
  };
  while (i < line.length) {
    if (st.block === "c") {
      const e = line.indexOf("*/", i);
      if (e < 0) {
        push(line.slice(i), "c");
        i = line.length;
        break;
      }
      push(line.slice(i, e + 2), "c");
      i = e + 2;
      st.block = null;
      continue;
    }
    if (st.block) {
      const e = closeAt(line, i, st.block);
      if (e < 0) {
        push(line.slice(i), "s");
        i = line.length;
        break;
      }
      push(line.slice(i, e + 1), "s");
      i = e + 1;
      st.block = null;
      continue;
    }
    const ch = line[i];
    const rest = line.slice(i);
    if (cfg.line.some((m) => rest.startsWith(m))) {
      push(rest, "c");
      break;
    }
    if (cfg.block && rest.startsWith("/*")) {
      push("/*", "c");
      st.block = "c";
      i += 2;
      continue;
    }
    if (cfg.quotes.includes(ch)) {
      const e = closeAt(line, i + 1, ch);
      if (e < 0) {
        if (cfg.multi.includes(ch)) st.block = ch;
        push(rest, "s");
        break;
      }
      const tok = line.slice(i, e + 1);
      const isKey = !!cfg.keyColon && /^\s*:/.test(line.slice(e + 1));
      push(tok, isKey ? "t" : "s");
      i = e + 1;
      continue;
    }
    if (/[0-9]/.test(ch) && !/[\w$.]/.test(line[i - 1] || "")) {
      const m = rest.match(/^(0[xX][0-9a-fA-F_]+|\d[\d_]*(\.\d+)?([eE][+-]?\d+)?)/);
      if (m) {
        push(m[0], "n");
        i += m[0].length;
        continue;
      }
    }
    if (/[A-Za-z_$]/.test(ch)) {
      const w = (rest.match(/^[A-Za-z_$][\w$]*/) as RegExpMatchArray)[0];
      const k: TokKind | null = cfg.kw.has(w) ? "k" : cfg.lit.has(w) ? "n" : cfg.ty.has(w) ? "t" : null;
      if (k) push(w, k);
      else buf += w;
      i += w.length;
      continue;
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}

function scanCSS(line: string, st: St): Raw {
  const out: Raw = [];
  let i = 0;
  let buf = "";
  const flush = () => {
    if (buf) {
      out.push([buf, "p"]);
      buf = "";
    }
  };
  const push = (t: string, k: TokKind) => {
    if (t) {
      flush();
      out.push([t, k]);
    }
  };
  st.depth = st.depth || 0;
  while (i < line.length) {
    if (st.block === "c") {
      const e = line.indexOf("*/", i);
      if (e < 0) {
        push(line.slice(i), "c");
        break;
      }
      push(line.slice(i, e + 2), "c");
      i = e + 2;
      st.block = null;
      continue;
    }
    const ch = line[i];
    const rest = line.slice(i);
    if (rest.startsWith("/*")) {
      push("/*", "c");
      st.block = "c";
      i += 2;
      continue;
    }
    if (ch === '"' || ch === "'") {
      const e = closeAt(line, i + 1, ch);
      const end = e < 0 ? line.length : e + 1;
      push(line.slice(i, end), "s");
      i = end;
      continue;
    }
    if (ch === "{") {
      st.depth++;
      buf += ch;
      i++;
      continue;
    }
    if (ch === "}") {
      st.depth = Math.max(0, st.depth - 1);
      buf += ch;
      i++;
      continue;
    }
    if (ch === "@") {
      const m = rest.match(/^@[\w-]+/);
      if (m) {
        push(m[0], "k");
        i += m[0].length;
        continue;
      }
    }
    if (st.depth > 0 && ch === "#") {
      const m = rest.match(/^#[0-9a-fA-F]{3,8}\b/);
      if (m) {
        push(m[0], "n");
        i += m[0].length;
        continue;
      }
    }
    if (/[0-9.]/.test(ch) && !/[\w-]/.test(line[i - 1] || "")) {
      const m = rest.match(/^\d*\.?\d+(px|em|rem|%|s|ms|vh|vw|vmin|vmax|deg|fr|ch)?/);
      if (m) {
        push(m[0], "n");
        i += m[0].length;
        continue;
      }
    }
    if (/[A-Za-z_-]/.test(ch)) {
      const w = (rest.match(/^[A-Za-z_-][\w-]*/) as RegExpMatchArray)[0];
      if (st.depth > 0 && /^\s*:/.test(line.slice(i + w.length))) push(w, "t");
      else if (st.depth === 0) push(w, "k");
      else buf += w;
      i += w.length;
      continue;
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}

function scanMake(line: string): Raw {
  const out: Raw = [];
  const pushVars = (s: string, base: TokKind) => {
    s.split(/(\$\([^)]*\)|\$\{[^}]*\}|\$\$|\$[@<^*?])/).forEach((p, idx) => {
      if (p) out.push([p, idx % 2 ? "k" : base]);
    });
  };
  const hash = line.search(/(^|[^\\$])#/);
  let code = line;
  let comment = "";
  if (hash >= 0) {
    const h = line[hash] === "#" ? hash : hash + 1;
    code = line.slice(0, h);
    comment = line.slice(h);
  }
  if (!/^\t/.test(code)) {
    const m = code.match(/^(\s*[\w.$()/%-]+(?:\s+[\w.$()/%-]+)*)(\s*::?)(?!=)(.*)$/);
    const a = code.match(/^(\s*[\w.-]+)(\s*(?:::?=|\?=|\+=|=))(.*)$/);
    if (a) {
      out.push([a[1], "a"]);
      out.push([a[2], "p"]);
      pushVars(a[3], "p");
    } else if (m) {
      out.push([m[1], "t"]);
      out.push([m[2], "p"]);
      pushVars(m[3], "p");
    } else if (/^\s*(ifeq|ifneq|ifdef|ifndef|else|endif|include|-include|export|define|endef)\b/.test(code)) {
      const w = (code.match(/^\s*\S+/) as RegExpMatchArray)[0];
      out.push([w, "k"]);
      pushVars(code.slice(w.length), "p");
    } else pushVars(code, "p");
  } else {
    code.split(/("[^"]*"|'[^']*')/).forEach((p, idx) => {
      if (!p) return;
      if (idx % 2) out.push([p, "s"]);
      else pushVars(p, "p");
    });
  }
  if (comment) out.push([comment, "c"]);
  return out;
}

function scanML(line: string, st: St): Raw {
  const out: Raw = [];
  let i = 0;
  let buf = "";
  const flush = () => {
    if (buf) {
      out.push([buf, "p"]);
      buf = "";
    }
  };
  const push = (t: string, k: TokKind) => {
    if (t) {
      flush();
      out.push([t, k]);
    }
  };
  while (i < line.length) {
    if (st.sub && !st.block) {
      const closeTag = st.sub === "js" ? "</script" : "</style";
      const e = line.toLowerCase().indexOf(closeTag, i);
      const part = e < 0 ? line.slice(i) : line.slice(i, e);
      flush();
      const sub = st.subSt ?? { block: null, depth: 0 };
      st.subSt = sub;
      const segs = st.sub === "js" ? scanC(part, CFG.js, sub) : scanCSS(part, sub);
      segs.forEach((s) => out.push(s));
      if (e < 0) break;
      i = e;
      st.sub = null;
      continue;
    }
    if (st.block === "c") {
      const e = line.indexOf("-->", i);
      if (e < 0) {
        push(line.slice(i), "c");
        break;
      }
      push(line.slice(i, e + 3), "c");
      i = e + 3;
      st.block = null;
      continue;
    }
    const ch = line[i];
    const rest = line.slice(i);
    if (st.block === "tag") {
      if (rest.startsWith("/>") || ch === ">") {
        const t = ch === ">" ? ">" : "/>";
        push(t, "t");
        i += t.length;
        st.block = null;
        if (st.pending && t === ">") {
          st.sub = st.pending;
          st.subSt = { block: null, depth: 0 };
        }
        st.pending = null;
        continue;
      }
      if (ch === '"' || ch === "'") {
        const e = line.indexOf(ch, i + 1);
        const end = e < 0 ? line.length : e + 1;
        push(line.slice(i, end), "s");
        i = end;
        continue;
      }
      if (/[\w:@.-]/.test(ch)) {
        const w = (rest.match(/^[\w:@.-]+/) as RegExpMatchArray)[0];
        push(w, "a");
        i += w.length;
        continue;
      }
      buf += ch;
      i++;
      continue;
    }
    if (rest.startsWith("<!--")) {
      push("<!--", "c");
      st.block = "c";
      i += 4;
      continue;
    }
    if (ch === "<" && /[A-Za-z!?/]/.test(line[i + 1] || "")) {
      const m = (rest.match(/^<\/?[!?]?[\w:.-]*/) as RegExpMatchArray)[0];
      push(m, "t");
      i += m.length;
      st.block = "tag";
      const name = m.replace(/^<\/?[!?]?/, "").toLowerCase();
      st.pending = m.startsWith("</") ? null : name === "script" ? "js" : name === "style" ? "css" : null;
      continue;
    }
    if (ch === "&") {
      const m = rest.match(/^&#?\w+;/);
      if (m) {
        push(m[0], "n");
        i += m[0].length;
        continue;
      }
    }
    buf += ch;
    i++;
  }
  flush();
  return out;
}

function scanMd(line: string, st: St): Raw {
  if (/^\s*```/.test(line)) {
    st.fence = !st.fence;
    return [[line, "c"]];
  }
  if (st.fence) return [[line, "s"]];
  if (/^#{1,6}\s.*$/.test(line)) return [[line, "k"]];
  const out: Raw = [];
  const m = line.match(/^(\s*(?:[-*+]|\d+[.)]|>)\s+)(.*)$/);
  let rest = line;
  if (m) {
    out.push([m[1], "t"]);
    rest = m[2];
  }
  rest.split(/(`[^`]+`|\*\*[^*]+\*\*)/).forEach((p, idx) => {
    if (!p) return;
    out.push([p, idx % 2 ? (p[0] === "`" ? "s" : "a") : "p"]);
  });
  return out;
}

/** Tokenizes whole lines with state carried across lines (block comments, multi-line strings). */
export function tokenize(lines: string[], lang: Lang): Seg[][] {
  const st: St = { block: null, depth: 0 };
  return lines.map((ln) => tokenizeLine(ln, lang, st));
}

function tokenizeLine(ln: string, lang: Lang, st: St): Seg[] {
  let raw: Raw;
  if (lang === "go" || lang === "js" || lang === "json" || lang === "gomod") raw = scanC(ln, CFG[lang], st);
  else if (lang === "html" || lang === "xml") raw = scanML(ln, st);
  else if (lang === "css") raw = scanCSS(ln, st);
  else if (lang === "make") raw = scanMake(ln);
  else if (lang === "md") raw = scanMd(ln, st);
  else raw = [[ln, "p"]];
  const merged: Seg[] = [];
  for (const [t, k] of raw) {
    if (!t) continue;
    const last = merged[merged.length - 1];
    if (last && last.k === k) last.t += t;
    else merged.push({ t, k });
  }
  return merged;
}

/**
 * Tokenizes diff lines independently per hunk (each hunk starts with fresh
 * state, since a hunk can begin in the middle of a block).
 */
export function tokenizeHunk(lines: string[], lang: Lang): Seg[][] {
  return tokenize(lines, lang);
}
