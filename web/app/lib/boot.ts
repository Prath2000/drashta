// Boot data (GET /api/boot): projects, counts, statuses, author.
// useBoot() is the one way components read it. The shell copies counts and the
// last scan time into the store when it arrives (SSE keeps them fresh after that).
import { useEffect } from "react";
import { useApi } from "./query";
import { applyBoot } from "./events";
import type { Boot, ProjectSummary } from "./types";

export const BOOT_KEY = "/boot";

export function useBoot() {
  return useApi<Boot>(BOOT_KEY);
}

/** Shell only: pushes boot counts into the store whenever boot (re)loads. */
export function useBootSync(): void {
  const { data } = useBoot();
  useEffect(() => {
    if (data) applyBoot(data);
  }, [data]);
}

/** Finds a project in boot data. */
export function findProject(boot: Boot | undefined, id: string | null | undefined): ProjectSummary | null {
  if (!boot || !id) return null;
  return boot.projects.find((p) => p.id === id) ?? null;
}
