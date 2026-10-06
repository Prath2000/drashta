# drashta docs

drashta (द्रष्टा, "the one who sees") is a local command center for all projects in `~/projects`: a Go backend with an embedded React app on `127.0.0.1:4455`.

Read in this order:

1. [decisions.md](decisions.md): what is decided, and when. **Binding**: if another doc disagrees, this one wins.
2. [architecture.md](architecture.md): package map and data flow (the quick tour of the code).
3. [development.md](development.md): build, test and verify recipes.
4. [spec/01-roadmap.md](spec/01-roadmap.md): the phases, from prototype to desktop.
5. [spec/06-open-questions.md](spec/06-open-questions.md): questions and their answers or proposed defaults.
6. [spec/02-ui-spec.md](spec/02-ui-spec.md): screens and behaviour.
7. [spec/03-architecture.md](spec/03-architecture.md): the original architecture plan.
8. [spec/04-data-model.md](spec/04-data-model.md): data, files, database and API.
9. [superpowers/specs/2026-10-01-drashta-design.md](superpowers/specs/2026-10-01-drashta-design.md): the consolidated design spec (it settles conflicts between the files above).
10. [adr/](adr/): architecture decision records. [../CONTEXT.md](../CONTEXT.md): glossary.

Status: the Go backend and the React screens are built and pass their tests (Go with `-race`, web unit tests, an end-to-end test). The planning docs in `spec/` were written before the build; where they differ from the code, the design spec and `decisions.md` are the better guide, then the code.
