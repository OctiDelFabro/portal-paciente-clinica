---
version: 1
slug: "frontend-src-main-tsx"
primary_target: "frontend/src/main.tsx"
related_targets: []
---

# Surface brief: patient portal (frontend/)

Scope: whole portal SPA (sign-in, doctor search, availability, quote and booking, my appointments, clinical history; doctor view of assigned appointments and note entry). Visitor mode: Operate. Code-led build.

Audience and job: patients book and manage their appointments; doctors review appointments and add notes. Demo projected from a notebook during the group presentation.

## Direction contract

THESIS: An appointment is a travel document. Each booking is issued as a segmented pass with tabular figures, and "Mis turnos" is a live board ranked by start time where a changed status moves in place and keeps its alert until seen. Refuses the white-and-teal card portal with stethoscope icons.

OWN-WORLD: Ink shell rail (#0B0D10) with slate and steel, cool panel ground (#E6E8EB), white document surfaces, one alert yellow reserved for change and pending states (energy cost pending, slot taken, cancelled), red only for errors. Condensed grotesk for codes, folios and times; monospace uppercase labels with tabular digits; a highly legible humanist sans for body. Perforated pass edge, hairline segment dividers, no radius beyond 2px.

STORY: The patient signs in, finds a doctor, picks a slot, reads the price (base plus energy estimated by the provider) and confirms; a pass is issued with its folio and appears on the board. When a dependency fails the screen says so plainly and the task still completes where the system allows.

FIRST VIEWPORT: Left ink rail with brand and navigation (Buscar, Mis turnos, Historia; Agenda for doctors), session at the bottom. Main area: search field with specialty, minors and sort controls over a results table (doctor, specialties, next available, action). Right column: "Próximo turno" pass. Primary action: Ver disponibilidad on a row.

FORM: challenger vernacular-ephemera-boarding-pass-and-gate-board (fused; product supplies every fact), chosen by the user over the assigned grounded direction. seed:a9404b967c409e43

FINISH: "unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance"

## Unresolved
- Cloud deploy target pending; the UI talks only to the gateway (/api).
