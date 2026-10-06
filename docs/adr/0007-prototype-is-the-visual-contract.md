# ADR 0007: The frozen prototype is the visual contract

- Status: Accepted (decisions log, Process section, 2026-10-01).
- Date: 2026-10-01

## Context

- The UI went through prototypes v1, v2 and v3. The owner gave a green light to v3 on 2026-10-01 and it is frozen.
- A written spec cannot pin spacing, density and feel. Without a reference the real app would drift.
- The prototype is built in the Design-canvas `.dc.html` format. It uses mock data and has no real server.

## Decision

- The frozen design prototype (v3) is the visual authority. It is a design mockup and is not part of this repository.
- The real app matches the prototype in structure, spacing, tokens, copy and behaviour, unless the user says otherwise.
- `docs/decisions.md` wins when the prototype and the decisions plainly conflict.
- Prototype-only things are not built: the `PROTOTYPE · PLASH` pill, mock data, and the light and full detail split. The Plash class and `?desktop=1` replace the pill.
- The screens are ported by hand to React. Every screen is rendered next to the prototype at 1440×900 and 1000×665 and compared (build Wave 3).
- Font rules (U5) apply: one weight (400), no italics, no bold. A test scans the CSS.

## Consequences

- Good: no argument about how a screen should look. Visual review has a reference.
- Good: the user already approved it.
- Cost: hand porting. Prototype quirks (fixed sizes, mock-driven states) must be judged one by one. Differences are written down as deviations in the agent reports.
- Cost: changing the look means the user unfreezes the prototype first.
