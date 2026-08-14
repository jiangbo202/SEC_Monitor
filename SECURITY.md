# Security Policy

## Supported version

Security fixes are made on the current `main` branch. This is a local-first
research tool, not a hardened multi-tenant service.

## Deployment boundary

**SEC Monitor has no built-in authentication or authorization. Do not expose
it directly to the public internet.** The supplied Docker Compose file binds
the service to `127.0.0.1:9090` intentionally.

For remote use, place it behind a VPN or a reverse proxy that enforces TLS and
authentication. Do not change the port mapping to a public interface unless
you have implemented and tested those controls.

## Secrets and data

- Never commit `.env`, SQLite databases, backups, logs, browser traces, API
  keys, Telegram tokens, or exported research data.
- Set `CONFIG_ENCRYPTION_KEY` before saving credentials. It encrypts sensitive
  configuration at rest; it does not replace host, volume, or network access
  controls.
- AI analysis, Telegram and market-data providers are external services.
  Treat the prompt, local facts and destination configuration as data that can
  leave the machine when a user triggers a request.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Report it
privately to the repository owner through GitHub's private security advisory
feature, including reproduction steps, affected version/commit and impact.

You can expect an acknowledgement within 7 days. Please allow time for a fix
or mitigation before public disclosure.
