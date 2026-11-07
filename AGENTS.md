# AGENTS.md

## Engineering principles

- **Future-ready by default.** Every change you implement must be modular,
  cleanly separated, and secure. Structure code so that future changes are easy
  to add without rewriting what already exists. Prefer clear interfaces,
  minimal coupling, and sensible defaults.
- **Security is not optional.** Validate inputs, handle errors explicitly, and
  never expose secrets or credentials in code or logs.
- **Preferred language: Go.** Use Go unless the task clearly calls for
  something else (shell glue, frontend, etc.). If you think a different
  language fits better, **ask the user first** before writing any code in that
  language.

## Planning — mandatory before any programming task

When the user requests a programming task, **do not start coding immediately.**

1. Read the existing `PLAN.md` in the project root (if it exists).
2. Create or update `PLAN.md` to reflect the requested task. Include:
   - **Goal:** what the user asked for, in your own words.
   - **Steps:** a numbered breakdown of what you will implement.
   - **Open questions:** anything you need clarified before starting.
3. Present the plan to the user and wait for confirmation before writing code.
4. As you complete steps, update `PLAN.md` to mark them done
   (e.g. `- [x] step`).
5. Keep `PLAN.md` up to date when requirements change mid-conversation.

## Testing

- After every **2nd completed phase** in `PLAN.md`, create or extend
  **integration tests** that exercise the application the way a real user or
  external system would. At minimum, integration tests must exist once the
  project is finished.
- Prefer running integration tests against a **Docker Compose stack** whenever
  the project involves services, APIs, or databases. Provide a
  `docker-compose.test.yml` (or extend the existing one) so the tests can spin
  up all dependencies.
- Unit tests are still welcome at any stage, but they do not replace
  integration tests.

## Backwards compatibility

- **We are in Developer Mode.** Do not preserve backwards compatibility unless
  the user explicitly asks for it.
- However, if a change you are about to make **breaks existing functionality**,
  warn the user clearly before proceeding. Explain what will break and why.

## Docker & deployment

- Every application you build must be **runnable in Docker**. Provide a
  `Dockerfile` (multi-stage where sensible).
- Orchestrate services with **Docker Compose**. Keep a `docker-compose.yml` at
  the project root.
- For persistent data, prefer **local bind mounts** (e.g. `./data:/app/data`)
  over named Docker volumes, so the user can inspect and back up files
  directly from the host filesystem.

## Diagrams

- When visualising architecture, flows, or relationships, use **Mermaid**
  diagrams (in Markdown fenced code blocks with `mermaid` as language).
- Place diagrams in `PLAN.md`, `README.md`, or inline in your response —
  wherever they add the most clarity.

## Project tagging — at the end of every response

Update the `tag=` line in the `.aiwa` file in the project root.

- Keep **10–20 tags maximum**. Choose words a human would search for:
  technologies, protocols, problem domains, key libraries.
- If the existing tags already match the project content, leave them unchanged.
- Tags are comma-separated, lowercase, no spaces: `tag=go,grpc,auth,docker`
- If no `tag=` line exists, add one after the `type=` line.
- Do not touch any other lines in `.aiwa`.
