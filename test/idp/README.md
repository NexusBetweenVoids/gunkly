# Identity providers for the SSO CI test

Real Keycloak and Authentik instances that `.github/workflows/sso-idp.yml` starts and signs in to
(`web/e2e-idp/`). **Everything here is synthetic and for CI only**: the client secret, the test users
and their password are public on purpose and protect nothing. Never reuse them anywhere real.

Image versions are pinned to exact tags. Bump them on purpose (and update `docs/sso.md`) so a provider
change shows up as a reviewed diff instead of a surprise.
