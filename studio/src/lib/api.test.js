import { afterEach, describe, expect, it, vi } from "vitest";
import {
  clearAuthState,
  buildOIDCAuthorizationURL,
  completeOIDCRedirect,
  exportAuditArchive,
  exportBuildReleaseEvidenceArchive,
  listAuditEvents,
  listProducts,
  logoutAuth,
  oidcCallbackParams,
  roleFromJWT,
  setAuthState,
  uploadArtifact,
} from "./api";

describe("api error contract", () => {
  afterEach(() => {
    clearAuthState();
    vi.restoreAllMocks();
  });

  it("keeps structured i18n and correlation fields from conductor errors", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 422,
      json: async () => ({
        error: {
          code: "BUILD_RELEASE_GATE_BLOCKED",
          status: 422,
          messageKey: "errors.build.releaseGateBlocked",
          message: "release gate blocked",
          severity: "warning",
          category: "business",
          retryable: false,
          userActionKey: "actions.fixReleaseGate",
          userAction: "fix release gate blockers before publishing",
          requestId: "req-test",
          correlationId: "corr-test",
          details: [{ code: "release.fixed-version", message: "release requires fixed version" }],
        },
      }),
    });

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 422,
      code: "BUILD_RELEASE_GATE_BLOCKED",
      messageKey: "errors.build.releaseGateBlocked",
      userActionKey: "actions.fixReleaseGate",
      requestId: "req-test",
      correlationId: "corr-test",
      details: [{ code: "release.fixed-version" }],
    });
  });

  it("supports legacy error arrays", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 400,
      json: async () => ({ error: "bad request", errors: ["field is required"] }),
    });

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 400,
      code: "REQUEST_FAILED",
      message: "bad request：field is required",
      details: [{ message: "field is required" }],
    });
  });

  it("falls back when the response is not JSON", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: false,
      status: 500,
      json: async () => {
        throw new Error("not json");
      },
    });

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 500,
      code: "REQUEST_FAILED",
      message: "Request failed: 500",
      severity: "critical",
      category: "system",
    });
  });

  it("maps network failures to a retryable API unavailable error", async () => {
    global.fetch = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));

    await expect(listProducts()).rejects.toMatchObject({
      name: "ApiError",
      status: 0,
      code: "API_UNAVAILABLE",
      messageKey: "errors.api.unavailable",
      message: "API unavailable",
      severity: "critical",
      category: "network",
      retryable: true,
      userActionKey: "actions.checkApiService",
      details: [{ code: "api.baseUrl", title: "API 地址", message: "http://localhost:8080" }],
    });
  });

  it("adds bearer authorization to API requests when a token is configured", async () => {
    setAuthState({ role: "operator", token: "operator-token" });
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => [],
    });

    await listProducts();

    expect(global.fetch).toHaveBeenCalledWith(
      "http://localhost:8080/api/products",
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: "Bearer operator-token",
        }),
      }),
    );
  });

  it("posts logout with the current bearer token", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ status: "ok", revoked: true }),
    });

    await logoutAuth("session-token");

    expect(global.fetch).toHaveBeenCalledWith(
      "http://localhost:8080/api/auth/logout",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({
          Authorization: "Bearer session-token",
        }),
      }),
    );
  });

  it("adds bearer authorization to upload requests without forcing JSON content type", async () => {
    setAuthState({ role: "operator", token: "operator-token" });
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ id: "upload-1" }),
    });

    await uploadArtifact(new File(["payload"], "payload.bin"));

    expect(global.fetch).toHaveBeenCalledWith(
      "http://localhost:8080/api/uploads",
      expect.objectContaining({
        method: "POST",
        headers: { Authorization: "Bearer operator-token" },
      }),
    );
  });

  it("builds audit event and archive query parameters", async () => {
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => [],
    });

    await listAuditEvents({ action: "build.download", buildId: "build-1", limit: "25" });
    await exportAuditArchive({ productId: "lsap", uploadId: "upload-1" });

    const eventsUrl = new URL(global.fetch.mock.calls[0][0]);
    expect(eventsUrl.pathname).toBe("/api/audit-events");
    expect(eventsUrl.searchParams.get("action")).toBe("build.download");
    expect(eventsUrl.searchParams.get("buildId")).toBe("build-1");
    expect(eventsUrl.searchParams.get("limit")).toBe("25");

    const archiveUrl = new URL(global.fetch.mock.calls[1][0]);
    expect(archiveUrl.pathname).toBe("/api/audit-events/archive");
    expect(archiveUrl.searchParams.get("productId")).toBe("lsap");
    expect(archiveUrl.searchParams.get("uploadId")).toBe("upload-1");
  });

  it("exports signed release evidence archive for a build", async () => {
    setAuthState({ role: "viewer", token: "viewer-token" });
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ schemaVersion: "packaging.release.evidence/v1" }),
    });

    await exportBuildReleaseEvidenceArchive("build-1");

    expect(global.fetch).toHaveBeenCalledWith(
      "http://localhost:8080/api/builds/build-1/release-evidence/archive",
      expect.objectContaining({
        headers: expect.objectContaining({
          Authorization: "Bearer viewer-token",
        }),
      }),
    );
  });
});

function jwtWithClaims(claims) {
  const encode = (payload) =>
    window.btoa(JSON.stringify(payload)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  return `${encode({ alg: "RS256" })}.${encode(claims)}.signature`;
}

describe("OIDC browser auth helpers", () => {
  afterEach(() => {
    clearAuthState();
    window.sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it("builds an OIDC authorization redirect URL", () => {
    const url = new URL(buildOIDCAuthorizationURL({
      authorizationUrl: "https://idp.example.test/auth",
      clientId: "packaging-ui",
      responseType: "token",
      scope: "openid profile",
      redirectUri: "https://packaging.example.test/callback",
    }, "state-1"));

    expect(url.origin + url.pathname).toBe("https://idp.example.test/auth");
    expect(url.searchParams.get("client_id")).toBe("packaging-ui");
    expect(url.searchParams.get("response_type")).toBe("token");
    expect(url.searchParams.get("redirect_uri")).toBe("https://packaging.example.test/callback");
    expect(url.searchParams.get("state")).toBe("state-1");
    expect(url.searchParams.get("nonce")).toBe("state-1");
  });

  it("builds an OIDC authorization code URL with PKCE challenge", () => {
    const url = new URL(buildOIDCAuthorizationURL({
      authorizationUrl: "https://idp.example.test/auth",
      clientId: "packaging-ui",
      responseType: "code",
      scope: "openid profile",
      redirectUri: "https://packaging.example.test/callback",
    }, "state-1", { challenge: "challenge-1" }));

    expect(url.searchParams.get("response_type")).toBe("code");
    expect(url.searchParams.get("code_challenge")).toBe("challenge-1");
    expect(url.searchParams.get("code_challenge_method")).toBe("S256");
  });

  it("parses callback tokens and stores studio role from JWT claims", async () => {
    const token = jwtWithClaims({ roles: ["packaging-operator"], sub: "alice" });
    window.sessionStorage.setItem("packaging.oidc.state", "state-1");
    window.history.pushState({}, "", `/#access_token=${token}&state=state-1`);

    const result = await completeOIDCRedirect({
      roleClaim: "roles",
      operatorRole: "packaging-operator",
      adminRole: "packaging-admin",
      viewerRole: "packaging-viewer",
    });

    expect(result).toMatchObject({ ok: true, role: "operator" });
    expect(JSON.parse(window.localStorage.getItem("packaging.auth"))).toMatchObject({
      token,
      role: "operator",
      source: "oidc",
    });
  });

  it("exchanges an authorization code with the PKCE verifier and stores the returned token", async () => {
    const token = jwtWithClaims({ roles: ["packaging-admin"], sub: "alice" });
    window.sessionStorage.setItem("packaging.oidc.state", "state-1");
    window.sessionStorage.setItem("packaging.oidc.codeVerifier", "verifier-1");
    window.history.pushState({}, "", "/?code=code-1&state=state-1");
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ access_token: token, token_type: "Bearer" }),
    });

    const result = await completeOIDCRedirect({
      tokenUrl: "https://idp.example.test/token",
      clientId: "packaging-ui",
      responseType: "code",
      redirectUri: "https://packaging.example.test/callback",
      roleClaim: "roles",
      adminRole: "packaging-admin",
      operatorRole: "packaging-operator",
      viewerRole: "packaging-viewer",
    });

    expect(result).toMatchObject({ ok: true, role: "admin" });
    expect(global.fetch).toHaveBeenCalledWith(
      "https://idp.example.test/token",
      expect.objectContaining({
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
      }),
    );
    const body = global.fetch.mock.calls[0][1].body;
    expect(body.get("grant_type")).toBe("authorization_code");
    expect(body.get("client_id")).toBe("packaging-ui");
    expect(body.get("code")).toBe("code-1");
    expect(body.get("code_verifier")).toBe("verifier-1");
    expect(JSON.parse(window.localStorage.getItem("packaging.auth"))).toMatchObject({
      token,
      role: "admin",
      source: "oidc",
    });
    expect(window.sessionStorage.getItem("packaging.oidc.codeVerifier")).toBeNull();
  });

  it("can exchange an authorization code through the conductor OIDC broker", async () => {
    const sessionToken = jwtWithClaims({ roles: ["packaging-operator"], sub: "alice" });
    window.sessionStorage.setItem("packaging.oidc.state", "state-1");
    window.sessionStorage.setItem("packaging.oidc.codeVerifier", "verifier-1");
    window.history.pushState({}, "", "/?code=code-1&state=state-1");
    global.fetch = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ token: sessionToken, tokenType: "Bearer", role: "operator" }),
    });

    const result = await completeOIDCRedirect({
      brokerUrl: "/api/auth/oidc/token",
      clientId: "packaging-ui",
      responseType: "code",
      redirectUri: "https://packaging.example.test/callback",
      roleClaim: "roles",
      adminRole: "packaging-admin",
      operatorRole: "packaging-operator",
      viewerRole: "packaging-viewer",
    });

    expect(result).toMatchObject({ ok: true, role: "operator" });
    expect(global.fetch).toHaveBeenCalledWith(
      "http://localhost:8080/api/auth/oidc/token",
      expect.objectContaining({
        method: "POST",
        headers: { "Content-Type": "application/json" },
      }),
    );
    expect(JSON.parse(global.fetch.mock.calls[0][1].body)).toMatchObject({
      code: "code-1",
      codeVerifier: "verifier-1",
      redirectUri: "https://packaging.example.test/callback",
    });
    expect(JSON.parse(window.localStorage.getItem("packaging.auth"))).toMatchObject({
      token: sessionToken,
      role: "operator",
      source: "oidc",
    });
  });

  it("rejects mismatched OIDC callback state before token exchange", async () => {
    window.sessionStorage.setItem("packaging.oidc.state", "expected");
    window.sessionStorage.setItem("packaging.oidc.codeVerifier", "verifier-1");
    window.history.pushState({}, "", "/?code=code-1&state=actual");
    global.fetch = vi.fn();

    const result = await completeOIDCRedirect({
      tokenUrl: "https://idp.example.test/token",
      clientId: "packaging-ui",
      responseType: "code",
    });

    expect(result).toMatchObject({ ok: false, error: "OIDC state mismatch" });
    expect(global.fetch).not.toHaveBeenCalled();
    expect(window.localStorage.getItem("packaging.auth")).toBeNull();
  });

  it("maps JWT role claim values to studio roles", () => {
    expect(roleFromJWT(jwtWithClaims({ groups: "packaging-admin packaging-user" }), {
      roleClaim: "groups",
      adminRole: "packaging-admin",
      operatorRole: "packaging-operator",
      viewerRole: "packaging-viewer",
    })).toBe("admin");
  });

  it("merges OIDC callback params from hash and search", () => {
    const params = oidcCallbackParams("#access_token=a&state=hash", "?state=query&expires_in=3600");

    expect(params.get("access_token")).toBe("a");
    expect(params.get("state")).toBe("query");
    expect(params.get("expires_in")).toBe("3600");
  });
});
