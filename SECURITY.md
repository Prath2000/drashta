# Security policy

## Reporting a vulnerability

Please report problems privately. Do not open a public issue.

- Use GitHub private vulnerability reporting: the "Report a vulnerability" button on the Security tab of `https://github.com/Prath2000/drashta`.
- Include the version (`drashta -version`), steps to reproduce, and what you expect an attacker could do.
- You can expect a first reply within about a week. This is a hobby-scale project, so there is no fixed fix time.

## Supported versions

Only the latest commit on `main` is supported. There are no maintained release branches.

## Scope

In scope:

- A web page (or other browser content) reaching the local server despite the Host, Origin, Sec-Fetch-Site and token checks.
- Running a command or reading or writing a file outside what the API should allow (see `internal/runx` and `internal/pathguard`).
- Secrets from Claude Code transcripts reaching the index, search or API without redaction.
- The embedded-page and frame checks (`/api/servers/embed`).

Out of scope:

- Anything that needs another process already running under your user account. That is trusted by design.
- Exposing the port to a network on purpose.
- Secrets that your own tools write to files drashta is asked to show.

## Threat model summary

- drashta binds to `127.0.0.1` only.
- Local processes under your user are trusted.
- Browsers are blocked by Host, Origin and `Sec-Fetch-Site` checks. Mutating calls also need the `X-Drashta-Token` header; the token is embedded in `index.html`.
- Commands run without a shell, through `internal/runx`, with paths checked by `internal/pathguard`. The editor command is set only in `config.toml`.
- Transcript text is redacted before indexing. Redaction is pattern based and may miss a new secret format; reports of missed patterns are welcome.
