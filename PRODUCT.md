# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users
- Patients of a single clinic who look for a doctor, book, review and cancel their own appointments, and read their clinical history.
- Doctors who review the appointments assigned to them and record an encounter note in a patient's clinical history.
- Reception exists in the domain (docs/SPEC.md) but is out of scope for the frontend of the group presentation.

## Product Purpose
Patient portal of a clinic, built as a university microservices project (Arquitectura de Software 2026). The portal must cover the whole journey "from the user's entry to the shared capability": sign-in, doctor search, availability, booking with its price, own appointments and clinical history. Success for this delivery: the flow runs end to end during the group presentation and makes the architecture visible (search engine, cache, provider integration, degradation when a dependency fails).

## Positioning
The appointment price shown before confirming includes the energy cost of the equipment the consultation uses, estimated by another group's capability (kWh) and priced with the clinic's own tariff. When that provider fails, booking still works and the energy cost is shown as pending.

## Operating Context
- Demonstrated live from a notebook, projected, during the group presentation; desktop first, responsive must still be correct.
- Runs locally against the API gateway (`http://localhost:8080`, `/api/*`) started with Docker Compose; the frontend runs with Vite.
- Demo accounts are seeded patients (pat-001, pat-002 not enabled, pat-003 minor) and doctors doc-001 / doc-002.

## Capabilities and Constraints
- Login and registration through Pacientes (JWT issued by Pacientes, validated by the gateway). A self-registered patient starts pending verification (RF-13) and cannot book until enabled.
- Doctor search (OpenSearch) with text query, specialty filter, "accepts minors" filter, sort by name or next available, pagination.
- Availability per doctor and day (cached in Redis, up to 30 s stale); a slot shown as free can still be taken (409 SLOT_UNAVAILABLE) and the UI must explain it.
- Booking and cancellation follow the 24-hour minimum notice; errors come with stable codes.
- Clinical history (MongoDB) shows the patient's encounters with notes; doctors append notes.
- Any dependency may fail during the controlled-failure demo; each screen needs an explicit degraded state, never a fake success.
- Stack: React 19 + TypeScript + Vite (frontend/), no backend access other than the gateway.

## Brand Commitments
- Clinic name: "Clínica Arroyo Claro", fictitious, created for the project; must be labeled as fictitious in the docs.
- UI copy in Spanish with Rioplatense voseo, matching the existing frontend ("Buscá", "gestioná").

## Evidence on Hand
- No real clinic data, photos, testimonials or logos exist; do not fabricate them. Doctor and patient names are seed data.

## Product Principles
- Truth over polish: show real states (pending, unavailable, stale) instead of hiding them.
- One clear path: each screen has one primary action that moves the patient forward.
- Explain every rejection in plain words with what the patient can do next.
- The architecture should be observable from the UI during the demo without turning it into a debug screen.

## Accessibility & Inclusion
WCAG 2.2 AA: keyboard operable, visible focus, sufficient contrast, labels on every field; patients of all ages.
