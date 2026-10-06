// Docs tab: the .md / .txt files of the project as cards; click → rendered preview.
import type { Doc } from "~/lib/types";
import { ref } from "./data";
import { base, rel } from "./fmt";
import { C, EmptyBox } from "./ui";

export const DOC_CAP = 120;

export function DocsTab(props: { p: string; docs: Doc[] | undefined; loading: boolean; error: unknown; openItem: (r: string) => void }) {
  const { p, openItem } = props;
  if (props.loading && !props.docs) return <div style={{ fontSize: 12, color: C.muted }}>reading docs…</div>;
  if (props.error && !props.docs) return <EmptyBox>could not read the docs list.</EmptyBox>;
  const list = (props.docs ?? []).slice().sort((a, b) => a.path.localeCompare(b.path));
  if (!list.length) return <EmptyBox testId="docs-empty">No .md or .txt files in the indexed tree.</EmptyBox>;
  return (
    <div data-testid="tab-docs">
      <div style={{ display: "grid", gridTemplateColumns: "repeat(2, minmax(0, 1fr))", gap: 12 }}>
        {list.slice(0, DOC_CAP).map((d) => {
          const name = base(d.path);
          const meta = [d.lines ? d.lines + " lines" : "", d.mtime ? rel(d.mtime) : ""].filter(Boolean).join(" · ");
          const title = d.title && d.title !== name ? d.title : "";
          return (
            <button
              key={d.path}
              type="button"
              className="pj-hover"
              onClick={() => openItem(ref(p, "file", d.path))}
              style={{
                minHeight: 96,
                display: "flex",
                flexDirection: "column",
                gap: 6,
                padding: "14px 16px",
                background: C.panel,
                border: "1px solid " + C.hair,
                borderRadius: 2,
                color: C.text,
                textAlign: "left",
                cursor: "pointer",
                minWidth: 0,
              }}
            >
              <span style={{ display: "flex", justifyContent: "space-between", gap: 12, width: "100%" }}>
                <span className="pj-trunc" style={{ fontSize: 15, color: C.text }}>
                  {name}
                </span>
                <span style={{ flex: "none", fontSize: 11, color: C.muted }}>{meta}</span>
              </span>
              <span style={{ fontSize: 11, color: C.muted, overflowWrap: "anywhere" }}>{d.path}</span>
              <span
                style={{
                  fontSize: 12,
                  lineHeight: 1.5,
                  color: "#c9c4b8",
                  display: "-webkit-box",
                  WebkitLineClamp: 2,
                  WebkitBoxOrient: "vertical",
                  overflow: "hidden",
                }}
              >
                {title || "open to read."}
              </span>
            </button>
          );
        })}
      </div>
      {list.length > DOC_CAP ? (
        <div style={{ marginTop: 10, fontSize: 11, color: C.muted }}>
          first {DOC_CAP} of {list.length} shown
        </div>
      ) : null}
    </div>
  );
}
