// Writes build/client/.keep after a build so `go build` (//go:embed all:build/client)
// always finds at least one file, even after the build folder was cleaned.
import { mkdirSync, writeFileSync } from "node:fs";
mkdirSync(new URL("../build/client/", import.meta.url), { recursive: true });
writeFileSync(new URL("../build/client/.keep", import.meta.url), "placeholder for go:embed\n");
