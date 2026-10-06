import { describe, expect, it } from "vitest";
import { lineFrag } from "./FileBody";
import { fmtDur, fmtSize } from "./fmt";
import { langOf, tokenize } from "./highlight";
import { inline, parseMd, parseText } from "./markdown";
import { parsePatch, splitMessage } from "./patch";

describe("tokenize", () => {
  it("colours Go keywords, strings, comments and numbers", () => {
    const [line] = tokenize(['func main() { x := "hi" // say 42'], "go");
    const kinds = Object.fromEntries(line.map((s) => [s.t.trim(), s.k]));
    expect(kinds["func"]).toBe("k");
    expect(kinds['"hi"']).toBe("s");
    expect(line[line.length - 1]).toEqual({ t: "// say 42", k: "c" });
  });

  it("carries a block comment across lines", () => {
    const toks = tokenize(["/* start", "still comment", "end */ var x = 1"], "js");
    expect(toks[1]).toEqual([{ t: "still comment", k: "c" }]);
    expect(toks[2][0]).toEqual({ t: "end */", k: "c" });
  });

  it("knows languages by path", () => {
    expect(langOf("a/Makefile")[0]).toBe("make");
    expect(langOf("go.mod")[0]).toBe("gomod");
    expect(langOf("x.tsx")).toEqual(["js", "TSX"]);
    expect(langOf("notes.weird")).toEqual(["text", "WEIRD"]);
  });

  it("colours JSON keys apart from values", () => {
    const [line] = tokenize(['{"a": "b", "n": 3}'], "json");
    expect(line.find((s) => s.t === '"a"')?.k).toBe("t");
    expect(line.find((s) => s.t === '"b"')?.k).toBe("s");
  });
});

describe("markdown", () => {
  it("parses blocks with their source line numbers", () => {
    const md = ["# Title", "", "Some **bold** and `code`.", "", "- one", "- two", "", "```go", "x := 1", "```", "| a | b |", "|---|---|", "| 1 | 2 |"];
    const blocks = parseMd(md);
    expect(blocks.map((b) => b.kind)).toEqual(["h1", "p", "list", "code", "table"]);
    expect(blocks[1]).toMatchObject({ from: 3, to: 3 });
    expect(blocks[2]).toMatchObject({ from: 5, to: 6 });
    const table = blocks[4];
    expect(table.kind === "table" && table.rows).toEqual([
      ["a", "b"],
      ["1", "2"],
    ]);
  });

  it("splits inline spans and never keeps link targets", () => {
    expect(inline("see [docs](https://x.y) now")).toEqual([
      { t: "see ", k: "plain" },
      { t: "docs", k: "link" },
      { t: " now", k: "plain" },
    ]);
  });

  it("renders plain text paragraphs and caps headings", () => {
    const b = parseText(["NOTES", "", "line one", "line two", "", "- a", "- b"]);
    expect(b.map((x) => x.kind)).toEqual(["h3", "p", "list"]);
  });
});

describe("parsePatch", () => {
  const patch = [
    "diff --git a/x.go b/x.go",
    "index 111..222 100644",
    "--- a/x.go",
    "+++ b/x.go",
    "@@ -1,3 +1,4 @@ package x",
    " a",
    "-b",
    "+c",
    "+d",
    " e",
    "diff --git a/img.png b/img.png",
    "Binary files a/img.png and b/img.png differ",
  ].join("\n");

  it("counts added and removed lines and numbers both sides", () => {
    const files = parsePatch(patch);
    expect(files).toHaveLength(2);
    expect(files[0]).toMatchObject({ path: "x.go", add: 2, del: 1 });
    expect(files[0].hunks[0].lines.map((l) => [l.k, l.o, l.n])).toEqual([
      [" ", 1, 1],
      ["-", 2, ""],
      ["+", "", 2],
      ["+", "", 3],
      [" ", 3, 4],
    ]);
    expect(files[1].meta[0]).toMatch(/^Binary files/);
  });

  it("splits fossil one-line messages into bullets", () => {
    expect(splitMessage("Fix it  - a.go: one  - b.go: two")).toEqual({ headline: "Fix it", bullets: ["`a.go`: one", "`b.go`: two"] });
  });
});

describe("formatters", () => {
  it("formats sizes, durations and line fragments", () => {
    expect(fmtSize(4300)).toBe("4.2 KB");
    expect(fmtSize(null)).toBe("—");
    expect(fmtDur(4 * 3600000 + 17 * 60000)).toBe("4h 17m");
    expect(lineFrag("L40")).toEqual({ from: 40, to: 40 });
    expect(lineFrag("L40-L52")).toEqual({ from: 40, to: 52 });
    expect(lineFrag("x=0.4")).toBeNull();
  });
});
