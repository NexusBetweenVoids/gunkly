# Security Policy

## Reporting a vulnerability

Use GitHub private vulnerability reporting: <https://github.com/NexusBetweenVoids/gunkly/security/advisories/new>

Do not open public issues for vulnerabilities. Include affected version, reproduction steps and impact. Use synthetic data only; never send real instance exports or secrets.

## Supported versions

Only the latest release receives security fixes. Pre-1.0 releases may require upgrading to the latest version.

## Scope

In scope: the Gunkly code, container image and documented configuration, including tenant isolation, authentication, mark-done and calendar tokens, the egress guard and Web Push handling.

Out of scope: vulnerabilities in third-party services you configure, self-hosters' own infrastructure and reverse proxies, social engineering, denial of service through volumetric traffic, and findings that require already-compromised hosts or admin accounts. See `docs/THREAT_MODEL.md`.

## Response

This is a volunteer project. We aim to acknowledge reports within 7 days, give an initial assessment within 14 days, and fix confirmed issues by severity, coordinating disclosure and crediting reporters who wish it. Please allow a reasonable time before public disclosure.
