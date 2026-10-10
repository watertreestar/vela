#!/usr/bin/env node

// Test-only OIDC/JWKS fixture. It intentionally accepts any authorization
// code and must never be deployed as an identity provider.

import { createSign, generateKeyPairSync } from "node:crypto";
import fs from "node:fs";
import http from "node:http";
import https from "node:https";

const issuer = process.env.OIDC_FIXTURE_ISSUER;
const audience = process.env.OIDC_FIXTURE_AUDIENCE || "vela";
const role = process.env.OIDC_FIXTURE_ROLE || "operator";
const upstream = new URL(process.env.OIDC_FIXTURE_API || "http://vela-oidc-gate-api:8080");
const port = Number(process.env.OIDC_FIXTURE_PORT || "8443");
const certFile = process.env.OIDC_FIXTURE_CERT_FILE || "/fixture/server.pem";
const keyFile = process.env.OIDC_FIXTURE_KEY_FILE || "/fixture/server.key";

if (!issuer) {
  throw new Error("OIDC_FIXTURE_ISSUER is required");
}

const keyId = "vela-oidc-fixture";
const { privateKey, publicKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const publicJwk = publicKey.export({ format: "jwk" });
const jwks = {
  keys: [{ ...publicJwk, kid: keyId, use: "sig", alg: "RS256" }],
};

function json(response, status, body) {
  const payload = JSON.stringify(body);
  response.writeHead(status, {
    "content-type": "application/json",
    "content-length": Buffer.byteLength(payload),
    "cache-control": "no-store",
  });
  response.end(payload);
}

function accessToken() {
  const now = Math.floor(Date.now() / 1000);
  const header = Buffer.from(JSON.stringify({ alg: "RS256", typ: "JWT", kid: keyId })).toString("base64url");
  const claims = Buffer.from(JSON.stringify({
    iss: issuer,
    aud: audience,
    sub: "alice",
    roles: [role],
    iat: now,
    exp: now + 3600,
  })).toString("base64url");
  const signingInput = `${header}.${claims}`;
  const signature = createSign("RSA-SHA256").update(signingInput).end().sign(privateKey).toString("base64url");
  return `${signingInput}.${signature}`;
}

function proxy(request, response) {
  const headers = { ...request.headers, host: upstream.host };
  const proxyRequest = http.request({
    protocol: upstream.protocol,
    hostname: upstream.hostname,
    port: upstream.port,
    method: request.method,
    path: request.url,
    headers,
  }, (proxyResponse) => {
    response.writeHead(proxyResponse.statusCode || 502, proxyResponse.headers);
    proxyResponse.pipe(response);
  });
  proxyRequest.on("error", (error) => json(response, 502, { error: "upstream_unavailable", detail: error.message }));
  request.pipe(proxyRequest);
}

const server = https.createServer({
  cert: fs.readFileSync(certFile),
  key: fs.readFileSync(keyFile),
}, (request, response) => {
  const url = new URL(request.url || "/", issuer);
  if (url.pathname === "/.well-known/openid-configuration") {
    json(response, 200, {
      issuer,
      jwks_uri: `${issuer}/jwks`,
      authorization_endpoint: `${issuer}/authorize`,
      token_endpoint: `${issuer}/token`,
      response_types_supported: ["code"],
      grant_types_supported: ["authorization_code"],
      code_challenge_methods_supported: ["S256"],
      id_token_signing_alg_values_supported: ["RS256"],
    });
    return;
  }
  if (url.pathname === "/jwks") {
    json(response, 200, jwks);
    return;
  }
  if (url.pathname === "/authorize") {
    json(response, 200, { status: "fixture", responseType: "code" });
    return;
  }
  if (url.pathname === "/token" && request.method === "POST") {
    request.resume();
    json(response, 200, {
      access_token: accessToken(),
      token_type: "Bearer",
      expires_in: 3600,
    });
    return;
  }
  if (url.pathname === "/health") {
    json(response, 200, { status: "ok" });
    return;
  }
  if (url.pathname.startsWith("/api/")) {
    proxy(request, response);
    return;
  }
  json(response, 404, { error: "not_found" });
});

server.listen(port, "0.0.0.0", () => {
  process.stdout.write(`OIDC fixture listening on ${port}\n`);
});
