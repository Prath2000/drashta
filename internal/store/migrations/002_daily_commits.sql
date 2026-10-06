-- 002_daily_commits: exact commit numbers per project and local day (R, cache).
--
-- The commits table keeps only the newest 200 commits per project, so counting
-- its rows gives wrong numbers for a busy project. The indexer reads the exact
-- numbers with one `git log --numstat` (and `fossil timeline`) pass over the
-- visible window and writes them here (store.SetDailyCommitStats).
--
-- RecomputeDaily takes commits, lines_plus and lines_minus of daily_activity
-- from this table when it has a row for that project and day. A day with no row
-- here still counts the commit rows (old behaviour). A row with zeros means
-- "this day was counted and had no commit".
CREATE TABLE daily_commits(
  project_id  TEXT NOT NULL,
  day         TEXT NOT NULL,
  commits     INTEGER NOT NULL DEFAULT 0,
  lines_plus  INTEGER NOT NULL DEFAULT 0,
  lines_minus INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(project_id, day)
);
