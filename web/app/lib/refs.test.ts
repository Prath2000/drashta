import { describe, expect, it } from "vitest";
import { formatRef, parseRef, sameRef } from "./refs";

describe("refs", () => {
  it("parses a full ref with project and fragment", () => {
    expect(parseRef("acme-api:file:src/x.go#L40")).toEqual({ type: "file", projectId: "acme-api", id: "src/x.go", frag: "L40" });
  });
  it("parses a local ref with a default project", () => {
    expect(parseRef("commit:abc1234", "drashta")).toEqual({ type: "commit", projectId: "drashta", id: "abc1234" });
  });
  it("keeps slashes and colons in the id", () => {
    expect(parseRef("notes-app:snapshot:T-1/v3")?.id).toBe("T-1/v3");
    expect(parseRef("p:session:121c3b19-ada3")?.type).toBe("session");
  });
  it("rejects junk", () => {
    expect(parseRef("")).toBeNull();
    expect(parseRef("nothing")).toBeNull();
    expect(parseRef("p:bogus:1")).toBeNull();
  });
  it("round-trips and compares", () => {
    const r = parseRef("acme-api:server:8090")!;
    expect(formatRef(r)).toBe("acme-api:server:8090");
    expect(sameRef("acme-api:file:a.go#L1", "acme-api:file:a.go#L9")).toBe(true);
  });
});
