# Contributing

Thank you for improving SEC Monitor.

## Before opening a pull request

1. Do not include `.env`, databases, backups, logs, API keys, tokens or real
   user research data.
2. Keep changes focused and update relevant documentation.
3. Run the checks below:

```bash
go test ./...
cd web && npm run build
```

4. Add tests for changed backend behaviour where practical.
5. Use conventional commit prefixes such as `feat:`, `fix:`, `docs:` and
   `test:`.

## Security-sensitive changes

Changes affecting authentication boundaries, external URLs, secret handling,
notifications, AI providers or data deletion should explain their threat model
and include tests. Report vulnerabilities privately as described in
[SECURITY.md](SECURITY.md), not through a public issue.

## Project scope

SEC Monitor is local-first and has no built-in authentication. Contributions
must not make the default Docker deployment publicly reachable or add automatic
third-party AI calls without an explicit user action.
