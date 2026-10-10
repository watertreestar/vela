# OIDC live acceptance — 2026-10-10

## Scope

This production-like acceptance used isolated containers on `ubuntu-a`. A
temporary HTTPS OIDC/JWKS fixture issued RS256 tokens and proxied API requests
to a separate auth-enabled Conductor container. Existing Vela services and
ports were not changed.

This run proves the platform integration contract. It is not acceptance of a
real enterprise IdP, production certificate authority, or production account
lifecycle.

## Environment

- OIDC and API HTTPS endpoint: isolated port `18443`;
- audience: `vela`;
- role claim: `roles`;
- expected external role: `operator`;
- subject claim: `sub`;
- Conductor auth: enabled;
- OIDC broker: enabled with Authorization Code + PKCE inputs;
- session store: enabled using the isolated JSON store;
- TLS: two-day test CA with IP SAN and required CA/key-usage extensions.

The temporary CA, TLS private key, JWT signing key, bearer tokens, session
HMAC key, Runner token, containers, network, and derived images were removed
after acceptance.

## Passed evidence

- HTTPS was required for issuer, discovery, JWKS, authorization, token, and
  Conductor API URLs.
- Discovery metadata matched the configured issuer and endpoints.
- Discovery advertised PKCE `S256` support.
- JWKS exposed one usable RSA/RS256 signing key with a stable key ID.
- The access token passed algorithm, issuer, audience, expiry, role, and
  subject checks.
- Conductor accepted the OIDC operator token on the protected audit endpoint.
- The server-side broker exchanged code + verifier inputs for a platform
  session.
- The broker result contained the expected `operator` role, `oidc:alice`
  subject, and a persisted session ID.
- The platform session accessed the protected endpoint successfully.
- Logout revoked the persisted session.
- Reusing the revoked session returned HTTP 401.

## Fixture support

[`scripts/oidc_fixture_server.mjs`](../../../scripts/oidc_fixture_server.mjs)
provides the isolated test-only HTTPS discovery, JWKS, token, and reverse-proxy
surface. It accepts any authorization code by design and must never be used as
an identity provider outside controlled acceptance tests.

## Remaining production requirements

1. Repeat against the selected enterprise IdP and its publicly trusted or
   enterprise-managed certificate chain.
2. Validate real authorization-code issuance, redirect URI policy, consent,
   and PKCE enforcement at the IdP.
3. Confirm production claim names, group/role mapping, subject stability, and
   disabled-user behavior.
4. Use PostgreSQL-backed session storage and verify revocation across process
   restart and multiple Conductor replicas.
5. Confirm signing-key rotation, JWKS cache behavior, clock skew, token expiry,
   and emergency logout procedures.
6. Store client secrets and session HMAC keys in the production secret
   backend and validate rotation.
