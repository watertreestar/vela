# Nomad ACL and mTLS live acceptance — 2026-10-11

## Scope

An isolated Nomad 1.10.5 server and Linux client ran on the `ubuntu-a`
Docker context. The fixture generated a temporary CA, server certificate,
client certificate, ACL bootstrap token, and read-only client token. HTTP and
RPC TLS were enabled, and the HTTPS listener required a trusted client
certificate.

The fixture did not reuse or mutate the shared Nomad cluster at
`192.168.1.1`. All credentials and certificates existed only in a uniquely
named Docker volume that was deleted after the run.

## Passed evidence

- the HTTPS endpoint presented a certificate chained to the isolated CA;
- the readiness client authenticated with the generated client certificate;
- a request without the client certificate failed during the TLS handshake;
- ACL token self reported a non-management `client` token;
- the read-only token listed the ready Linux packaging node;
- an anonymous request to `/v1/nodes` returned an authorization denial;
- the read-only token could not list `/v1/acl/tokens`;
- the Linux client registered through TLS-protected Nomad RPC;
- node detail confirmed `kernel.name=linux` and
  `meta.node_class=linux-packager`;
- every readiness confirmation light passed except the intentionally optional
  Windows-node check, which remained a warning.

## Defects found during acceptance

Nomad's `/v1/nodes` endpoint returns node list stubs that do not reliably
contain `Attributes` or `Meta`. The readiness gate now loads `/v1/node/<id>`
for incomplete ready-node entries before checking kernel and node class.

Listing ACL policy names is permitted to a normal authenticated client token
and therefore was not a valid ACL-administration denial proof. The gate now
checks `/v1/acl/tokens`, which requires ACL management capability and returned
the expected denial for the fixture token.

The aggregate production-readiness runner now passes `NOMAD_TOKEN` only
through the child environment rather than serializing it into process argv.

## Cleanup evidence

The acceptance script removed the uniquely named server, client, and checker
containers, Docker network, and credential volume. Its final label-based
inspection found zero residual containers, networks, volumes, certificates,
or ACL tokens.

## Remaining production requirements

1. Deploy these controls on the intended HA Nomad servers and all Linux and
   Windows clients with production CA rotation and revocation procedures.
2. Split Conductor submit/Variable permissions from observer/readiness
   permissions and retain policy definitions in infrastructure as code.
3. Store client keys and Nomad tokens in the production secret manager and
   audit their workload delivery and rotation.
4. Re-run the full Linux and Windows packaging E2E against that production
   ACL/mTLS cluster; this isolated acceptance proves the security controls and
   gate semantics, not the final cluster rollout.
