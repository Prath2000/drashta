## What and why

<!-- Plain English: what changed and why. -->

## Checks

- [ ] `gofmt -l .` prints nothing
- [ ] `go vet ./... && go test -race -count=1 ./...` pass
- [ ] `cd web && npm run typecheck && npx vitest run` pass
- [ ] UI change: headless screenshots at 1440x900 and 1000x665 attached
- [ ] No real secrets, transcripts or personal paths in code or fixtures
- [ ] Redaction patterns only added, not removed
- [ ] Does not contradict `docs/decisions.md` (or links the issue that changes it)
