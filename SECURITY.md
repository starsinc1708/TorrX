# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly.

**Do not open a public GitHub issue for security vulnerabilities.**

Instead, please email the maintainer or use [GitHub Security Advisories](https://github.com/starsinc1708/TorrX/security/advisories/new) to report the issue privately.

### What to include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### Response timeline

- **Acknowledgment**: within 48 hours
- **Initial assessment**: within 1 week
- **Fix or mitigation**: as soon as possible, depending on severity

## Security Considerations

T◎RRX is designed for **self-hosted, trusted LAN environments**. By default:

- No authentication is required on API endpoints.
- CORS is permissive (`Access-Control-Allow-Origin: *`).
- Observability dashboards use default credentials.

If you expose T◎RRX to untrusted networks, you should:

1. Place it behind a VPN or authenticated reverse proxy.
2. Change default Grafana credentials in `deploy/.env`.
3. Restrict network access to admin endpoints.

A `SECURITY_PROFILE=hardened` mode is planned — see the [roadmap](docs/roadmap/Prioritized_Improvements.md) for details.
