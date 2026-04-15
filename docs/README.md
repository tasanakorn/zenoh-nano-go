# zenoh-nano-go Documentation

Design documents for `zenoh-nano-go`, a pure Go Zenoh protocol client.

## Product Requirement Documents

| ID      | Title                                 | Status              | Version | Path                                                        |
| ------- | ------------------------------------- | ------------------- | ------- | ----------------------------------------------------------- |
| PRD-001 | zenoh-nano-go — Pure Go Zenoh Client  | Implemented (v0.1.0) | v0.1.0  | [prd/prd-001-zenoh-nano-go.md](prd/prd-001-zenoh-nano-go.md) |

## Other documents

| File                                         | Description                                         |
| -------------------------------------------- | --------------------------------------------------- |
| [wire-format-notes.md](wire-format-notes.md) | Wire format corrections found during live interop   |
| [latency.md](latency.md)                     | Expected start-to-put latency by connection mode    |

## Conventions

- PRDs live in `docs/prd/` and are named `prd-NNN-<slug>.md` (zero-padded, three digits).
- Each PRD begins with a metadata table (Status, Version, Author, Package) and follows the section order: Goals, Non-goals, Background & Motivation, Design, Changes by Component, Edge Cases, Migration, Testing.
- Status values: `Proposed`, `Accepted`, `Implemented`, `Superseded`.
- When a PRD status changes, update both the PRD metadata table and this index row.
