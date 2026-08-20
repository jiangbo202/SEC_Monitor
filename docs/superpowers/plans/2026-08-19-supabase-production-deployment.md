# Supabase/PostgreSQL Production Deployment Plan

**Status:** Deferred — recorded for later implementation. Do not change the current SQLite/Docker runtime until this plan is explicitly resumed.

**Goal:** Remove the production dependency on a continuously awake MacBook by running scheduled synchronization against a hosted PostgreSQL database and making the production UI available through a protected deployed service.

## Target Architecture

```text
Local Go/Vue development  ─────> Supabase development project
Automated tests           ─────> Ephemeral PostgreSQL

GitHub Actions scheduler  ──┐
Deployed Go/Vue service   ──┼──> Supabase production project
Read-only operator access ──┘
```

- Use separate Supabase projects for development and production; never share credentials or data between them.
- Use one PostgreSQL database per project with private `sec_monitor` and `discovery` schemas.
- Keep the Vue frontend behind the Go API. Do not expose database administrator or service-role credentials to the browser.
- Target PostgreSQL-only application runtime. SQLite may remain only until migration and rollback verification are complete.
- Treat versioned SQL migrations as the schema source of truth; production startup must not mutate schemas with GORM AutoMigrate.
- GitHub Actions may own production schedules, but it is not the production web host.

## Deferred Work Items

- [ ] Add PostgreSQL/GORM connectivity, explicit schema selection, TLS enforcement, bounded connection pools, and startup validation.
- [ ] Convert the 35 main-database models and 46 discovery-database models into reviewed, versioned SQL migrations.
- [ ] Replace SQLite-specific SQL, JSON extraction, duration calculations, file health, `VACUUM`, integrity checks, backup, compaction, and recovery UI behavior.
- [ ] Move automated tests from SQLite `:memory:` databases to isolated PostgreSQL instances; CI must never run destructive tests against the shared development project.
- [ ] Create separate Supabase development and production projects and separate encryption keys, database credentials, provider credentials, and GitHub environments.
- [ ] Keep Supabase schemas private or disable unused Data API exposure; create least-privilege runtime, migration, scheduler, and read-only operator roles.
- [ ] Add PostgreSQL advisory locks or durable task leases so GitHub Actions, manual runs, and deployed services cannot execute the same synchronization slot concurrently.
- [ ] Add a production GitHub Actions workflow with timezone-aware schedules, manual dispatch, concurrency control, sanitized logs, failure notification, and production-only secrets.
- [ ] Decide one scheduler owner per task and disable duplicate in-process schedules when GitHub Actions owns them.
- [ ] Deploy the combined Go/Vue image to an always-on host and protect it with application authentication, Cloudflare Access, Tailscale, VPN, or an equivalent access boundary.
- [ ] Add an optional production read-only profile for local operator access: no scheduler, migrations, write routes, or administrator database role.
- [ ] Build and rehearse the two-database SQLite-to-PostgreSQL import, including primary keys, foreign keys, sequences, encrypted configuration, row counts, checksums, and rollback.
- [ ] Replace local SQLite backups with managed PostgreSQL backups plus an independently stored logical `pg_dump`; document restore verification.
- [ ] Run a staged cutover: development parity, production shadow sync, read validation, scheduler handoff, final import, rollback window, and post-cutover monitoring.

## Acceptance Criteria

- A closed or powered-off development Mac does not interrupt production synchronization.
- Local development can only reach the development project by default.
- Tests use disposable PostgreSQL data and cannot reach development or production without an explicit protected override.
- Production synchronization has exactly one durable execution per scheduled slot.
- The deployed product UI can inspect production data without starting the application locally.
- Production database credentials are backend-only, access is authenticated, and sensitive values are absent from logs and workflow artifacts.
- Schema changes are reproducible from committed migrations in both development and production.
- Backup restoration and rollback are demonstrated before SQLite is retired.

## Decisions Still Required When Resumed

- Select the always-on Go/Vue hosting provider.
- Select the production access-control mechanism.
- Decide whether all schedules move to GitHub Actions or only long-running discovery jobs.
- Select the Supabase region and service tier after measuring latency, storage growth, connection usage, and backup requirements.
