export const API_URL = import.meta.env.VITE_API_URL || "http://localhost:8080";
const AUTH_STORAGE_KEY = "packaging.auth";
const OIDC_STATE_KEY = "packaging.oidc.state";
const OIDC_CODE_VERIFIER_KEY = "packaging.oidc.codeVerifier";

export const OIDC_CONFIG = {
  authorizationUrl: import.meta.env.VITE_OIDC_AUTHORIZATION_URL || "",
  tokenUrl: import.meta.env.VITE_OIDC_TOKEN_URL || "",
  brokerUrl: import.meta.env.VITE_OIDC_BROKER_URL || "",
  clientId: import.meta.env.VITE_OIDC_CLIENT_ID || "",
  scope: import.meta.env.VITE_OIDC_SCOPE || "openid profile email",
  responseType: import.meta.env.VITE_OIDC_RESPONSE_TYPE || "token",
  redirectUri: import.meta.env.VITE_OIDC_REDIRECT_URI || "",
  roleClaim: import.meta.env.VITE_OIDC_ROLE_CLAIM || "roles",
  adminRole: import.meta.env.VITE_OIDC_ADMIN_ROLE || "admin",
  operatorRole: import.meta.env.VITE_OIDC_OPERATOR_ROLE || "operator",
  viewerRole: import.meta.env.VITE_OIDC_VIEWER_ROLE || "viewer",
};

export class ApiError extends Error {
  constructor({
    status,
    code,
    messageKey,
    message,
    severity,
    category,
    retryable,
    userActionKey,
    userAction,
    requestId,
    correlationId,
    details,
  }) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code || "REQUEST_FAILED";
    this.messageKey = messageKey || "";
    this.severity = severity || (status >= 500 ? "critical" : "error");
    this.category = category || "system";
    this.retryable = Boolean(retryable);
    this.userActionKey = userActionKey || "";
    this.userAction = userAction || "";
    this.requestId = requestId || "";
    this.correlationId = correlationId || "";
    this.details = details || [];
  }
}

function toApiError(payload, status, fallback) {
  const error = payload?.error;
  if (error && typeof error === "object") {
    return new ApiError({
      status,
      code: error.code,
      messageKey: error.messageKey,
      message: error.message || fallback,
      severity: error.severity,
      category: error.category,
      retryable: error.retryable,
      userActionKey: error.userActionKey,
      userAction: error.userAction,
      requestId: error.requestId,
      correlationId: error.correlationId,
      details: error.details || [],
    });
  }
  const details = Array.isArray(payload?.errors)
    ? payload.errors.map((message) => ({ message }))
    : [];
  const message = typeof error === "string"
    ? `${error}${details.length ? `：${details.map((item) => item.message).join("；")}` : ""}`
    : fallback;
  return new ApiError({ status, message, details });
}

function toNetworkError() {
  return new ApiError({
    status: 0,
    code: "API_UNAVAILABLE",
    messageKey: "errors.api.unavailable",
    message: "API unavailable",
    severity: "critical",
    category: "network",
    retryable: true,
    userActionKey: "actions.checkApiService",
    details: [{ code: "api.baseUrl", title: "API 地址", message: API_URL }],
  });
}

async function fetchWithNetworkError(url, options) {
  try {
    return await fetch(url, options);
  } catch {
    throw toNetworkError();
  }
}

function readStoredAuth() {
  if (typeof window === "undefined") return { token: "", role: "" };
  try {
    const parsed = JSON.parse(window.localStorage?.getItem(AUTH_STORAGE_KEY) || "{}");
    return {
      token: typeof parsed.token === "string" ? parsed.token : "",
      role: typeof parsed.role === "string" ? parsed.role : "",
      source: typeof parsed.source === "string" ? parsed.source : "",
    };
  } catch {
    return { token: "", role: "", source: "" };
  }
}

function shouldAttachAuth(url) {
  const token = readStoredAuth().token;
  if (!token) return false;
  const raw = String(url || "");
  if (raw.startsWith("/api/")) return true;
  try {
    const requestUrl = new URL(raw, API_URL);
    const apiUrl = new URL(API_URL);
    return requestUrl.origin === apiUrl.origin;
  } catch {
    return false;
  }
}

function authHeaders(url) {
  const auth = readStoredAuth();
  if (!auth.token || !shouldAttachAuth(url)) return {};
  return { Authorization: `Bearer ${auth.token}` };
}

export function getAuthState() {
  return readStoredAuth();
}

export function setAuthState(auth) {
  if (typeof window === "undefined") return;
  const next = {
    token: String(auth?.token || "").trim(),
    role: String(auth?.role || "").trim(),
    source: String(auth?.source || "").trim(),
  };
  if (!next.token) {
    window.localStorage?.removeItem(AUTH_STORAGE_KEY);
    return;
  }
  window.localStorage?.setItem(AUTH_STORAGE_KEY, JSON.stringify(next));
}

export function clearAuthState() {
  if (typeof window !== "undefined") {
    window.localStorage?.removeItem(AUTH_STORAGE_KEY);
  }
}

export function logoutAuth(token = readStoredAuth().token) {
  const currentToken = String(token || "").trim();
  if (!currentToken) {
    return Promise.resolve({ status: "ok", revoked: false });
  }
  return request("/api/auth/logout", {
    method: "POST",
    headers: { Authorization: `Bearer ${currentToken}` },
  });
}

export function oidcLoginConfigured(config = OIDC_CONFIG) {
  if (!config.authorizationUrl || !config.clientId) return false;
  return !isOIDCCodeFlow(config) || Boolean(config.tokenUrl || config.brokerUrl);
}

export function oidcRedirectUri(config = OIDC_CONFIG) {
  if (config.redirectUri) return config.redirectUri;
  if (typeof window === "undefined") return "";
  return `${window.location.origin}${window.location.pathname}`;
}

export async function startOIDCLogin(config = OIDC_CONFIG) {
  if (typeof window === "undefined" || !oidcLoginConfigured(config)) return false;
  const state = randomState();
  window.sessionStorage?.setItem(OIDC_STATE_KEY, state);
  try {
    const pkce = isOIDCCodeFlow(config) ? await createPKCEPair() : null;
    if (pkce?.verifier) {
      window.sessionStorage?.setItem(OIDC_CODE_VERIFIER_KEY, pkce.verifier);
    }
    window.location.assign(buildOIDCAuthorizationURL(config, state, pkce));
  } catch (error) {
    window.sessionStorage?.removeItem(OIDC_STATE_KEY);
    window.sessionStorage?.removeItem(OIDC_CODE_VERIFIER_KEY);
    throw error;
  }
  return true;
}

export function buildOIDCAuthorizationURL(config = OIDC_CONFIG, state = "", pkce = null) {
  const authorizationUrl = new URL(config.authorizationUrl);
  authorizationUrl.searchParams.set("client_id", config.clientId);
  authorizationUrl.searchParams.set("response_type", config.responseType || "token");
  authorizationUrl.searchParams.set("scope", config.scope || "openid profile email");
  authorizationUrl.searchParams.set("redirect_uri", oidcRedirectUri(config));
  authorizationUrl.searchParams.set("state", state);
  authorizationUrl.searchParams.set("nonce", state);
  if (isOIDCCodeFlow(config) && pkce?.challenge) {
    authorizationUrl.searchParams.set("code_challenge", pkce.challenge);
    authorizationUrl.searchParams.set("code_challenge_method", "S256");
  }
  return authorizationUrl.toString();
}

export async function completeOIDCRedirect(config = OIDC_CONFIG) {
  if (typeof window === "undefined") return null;
  const params = oidcCallbackParams(window.location.hash, window.location.search);
  if (!params.has("access_token") && !params.has("id_token") && !params.has("code") && !params.has("error")) return null;
  if (params.has("error")) {
    return { ok: false, error: params.get("error_description") || params.get("error") || "OIDC login failed" };
  }
  const expectedState = window.sessionStorage?.getItem(OIDC_STATE_KEY) || "";
  const actualState = params.get("state") || "";
  if (expectedState && expectedState !== actualState) {
    return { ok: false, error: "OIDC state mismatch" };
  }
  let token = params.get("access_token") || params.get("id_token") || "";
  let brokerRole = "";
  if (!token && params.has("code")) {
    try {
      const tokenPayload = await exchangeOIDCCode(config, params.get("code") || "");
      token = tokenPayload.access_token || tokenPayload.id_token || "";
      brokerRole = tokenPayload.role || "";
    } catch (error) {
      return { ok: false, error: error?.message || "OIDC token exchange failed" };
    }
  }
  if (!token) {
    return { ok: false, error: "OIDC token missing" };
  }
  const role = brokerRole || roleFromJWT(token, config) || "viewer";
  setAuthState({ token, role, source: "oidc" });
  window.sessionStorage?.removeItem(OIDC_STATE_KEY);
  window.sessionStorage?.removeItem(OIDC_CODE_VERIFIER_KEY);
  cleanOIDCCallbackURL();
  return { ok: true, token, role };
}

export function oidcCallbackParams(hash, search) {
  const params = new URLSearchParams();
  for (const raw of [hash, search]) {
    const value = String(raw || "").replace(/^[#?]/, "");
    if (!value) continue;
    const current = new URLSearchParams(value);
    for (const [key, item] of current.entries()) {
      params.set(key, item);
    }
  }
  return params;
}

export function roleFromJWT(token, config = OIDC_CONFIG) {
  const claims = decodeJWTClaims(token);
  const values = claimValues(claims?.[config.roleClaim || "roles"]);
  if (values.includes(config.adminRole || "admin")) return "admin";
  if (values.includes(config.operatorRole || "operator")) return "operator";
  if (values.includes(config.viewerRole || "viewer")) return "viewer";
  return "";
}

function decodeJWTClaims(token) {
  const payload = String(token || "").split(".")[1];
  if (!payload) return null;
  try {
    const padded = payload.padEnd(payload.length + ((4 - (payload.length % 4)) % 4), "=");
    return JSON.parse(window.atob(padded.replace(/-/g, "+").replace(/_/g, "/")));
  } catch {
    return null;
  }
}

function claimValues(value) {
  if (typeof value === "string") return value.split(/[,\s]+/).filter(Boolean);
  if (Array.isArray(value)) return value.filter((item) => typeof item === "string" && item.trim()).map((item) => item.trim());
  return [];
}

function randomState() {
  const bytes = new Uint8Array(16);
  window.crypto?.getRandomValues?.(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("") || String(Date.now());
}

function isOIDCCodeFlow(config = OIDC_CONFIG) {
  return String(config.responseType || "").toLowerCase() === "code";
}

async function createPKCEPair() {
  const verifier = base64URLRandom(32);
  const subtle = window.crypto?.subtle || globalThis.crypto?.subtle;
  if (!subtle?.digest || typeof TextEncoder === "undefined") {
    throw new Error("OIDC PKCE requires Web Crypto");
  }
  const digest = await subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return { verifier, challenge: base64URLFromBytes(new Uint8Array(digest)) };
}

function base64URLRandom(size) {
  const bytes = new Uint8Array(size);
  window.crypto?.getRandomValues?.(bytes);
  return base64URLFromBytes(bytes);
}

function base64URLFromBytes(bytes) {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return window.btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

async function exchangeOIDCCode(config, code) {
  if (config.brokerUrl) {
    return exchangeOIDCCodeWithBroker(config, code);
  }
  if (!config.tokenUrl) {
    throw new Error("OIDC token endpoint is not configured");
  }
  const codeVerifier = window.sessionStorage?.getItem(OIDC_CODE_VERIFIER_KEY) || "";
  if (!codeVerifier) {
    throw new Error("OIDC PKCE verifier missing");
  }
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    client_id: config.clientId,
    code,
    redirect_uri: oidcRedirectUri(config),
    code_verifier: codeVerifier,
  });
  const response = await fetch(config.tokenUrl, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error_description || payload.error || `OIDC token exchange failed: ${response.status}`);
  }
  return payload;
}

async function exchangeOIDCCodeWithBroker(config, code) {
  const codeVerifier = window.sessionStorage?.getItem(OIDC_CODE_VERIFIER_KEY) || "";
  if (!codeVerifier) {
    throw new Error("OIDC PKCE verifier missing");
  }
  const response = await fetch(new URL(config.brokerUrl, API_URL).toString(), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      code,
      codeVerifier,
      redirectUri: oidcRedirectUri(config),
    }),
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error?.message || payload.error_description || payload.error || `OIDC broker failed: ${response.status}`);
  }
  return {
    access_token: payload.token || payload.access_token || payload.id_token || "",
    id_token: payload.id_token || "",
    role: payload.role || "",
  };
}

function cleanOIDCCallbackURL() {
  const params = new URLSearchParams(window.location.search);
  for (const key of ["code", "access_token", "id_token", "token_type", "expires_in", "state", "session_state"]) {
    params.delete(key);
  }
  const search = params.toString();
  window.history.replaceState({}, document.title, `${window.location.pathname}${search ? `?${search}` : ""}`);
}

async function request(path, options = {}) {
  const response = await fetchWithNetworkError(`${API_URL}${path}`, {
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(`${API_URL}${path}`),
      ...options.headers,
    },
    ...options,
  });

  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw toApiError(payload, response.status, `Request failed: ${response.status}`);
  }
  return payload;
}

export function listProducts() {
  return request("/api/products");
}

export function listBuilds() {
  return request("/api/builds");
}

export function getProductManifest(productId) {
  return request(`/api/products/${productId}/manifest`);
}

export function updateProductManifest(productId, manifest) {
  return request(`/api/products/${productId}/manifest`, {
    method: "PUT",
    body: JSON.stringify(manifest),
  });
}

export function dryRunProductManifest(productId, input) {
  return request(`/api/products/${productId}/manifest/dry-run`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function listProductManifestRevisions(productId) {
  return request(`/api/products/${productId}/manifest/revisions`);
}

export function restoreProductManifestRevision(productId, revisionId) {
  return request(`/api/products/${productId}/manifest/revisions/${revisionId}/restore`, {
    method: "POST",
  });
}

export function listProductOverlays(productId, kind) {
  return request(`/api/products/${productId}/overlays/${kind}`);
}

export function getProductOverlay(productId, kind, name) {
  return request(`/api/products/${productId}/overlays/${kind}/${name}`);
}

export function updateProductOverlay(productId, kind, name, data) {
  return request(`/api/products/${productId}/overlays/${kind}/${name}`, {
    method: "PUT",
    body: JSON.stringify(data),
  });
}

export function approveBuild(id) {
  return request(`/api/builds/${id}/approve`, { method: "POST" });
}

export function rejectBuild(id) {
  return request(`/api/builds/${id}/reject`, { method: "POST" });
}

export function validatePreviewBuild(id) {
  return request(`/api/builds/${id}/validate-preview`, { method: "POST" });
}

export function publishBuild(id, input = {}) {
  return request(`/api/builds/${id}/publish`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export function retryBuild(id) {
  return request(`/api/builds/${id}/retry`, { method: "POST" });
}

export function cancelBuild(id) {
  return request(`/api/builds/${id}/cancel`, { method: "POST" });
}

export function listUploads() {
  return request("/api/uploads");
}

export function listRunners() {
  return request("/api/runners");
}

export function listNotifications(options = {}) {
  const params = new URLSearchParams();
  if (options.limit) params.set("limit", String(options.limit));
  if (options.read) params.set("read", options.read);
  if (options.type) params.set("type", options.type);
  if (options.severity) params.set("severity", options.severity);
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return request(`/api/notifications${suffix}`);
}

export function markNotificationRead(id) {
  return request(`/api/notifications/${id}/read`, { method: "POST" });
}

export function markAllNotificationsRead() {
  return request("/api/notifications/read-all", { method: "POST" });
}

export function listAuditEvents(options = {}) {
  const params = new URLSearchParams();
  for (const key of ["resourceType", "resourceId", "buildId", "productId", "uploadId", "action", "limit"]) {
    if (options[key]) {
      params.set(key, String(options[key]));
    }
  }
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return request(`/api/audit-events${suffix}`);
}

export function exportAuditArchive(options = {}) {
  const params = new URLSearchParams();
  for (const key of ["resourceType", "resourceId", "buildId", "productId", "uploadId", "action", "limit"]) {
    if (options[key]) {
      params.set(key, String(options[key]));
    }
  }
  const suffix = params.toString() ? `?${params.toString()}` : "";
  return request(`/api/audit-events/archive${suffix}`);
}

export function getBuildLogs(id) {
  return request(`/api/builds/${id}/logs`);
}

export function getBuildLock(id) {
  return request(`/api/builds/${id}/lock`);
}

export function getBuildReleaseCheck(id) {
  return request(`/api/builds/${id}/release-check`);
}

export function getBuildReproducibilityReport(id) {
  return request(`/api/builds/${id}/reproducibility-report`);
}

export function exportBuildReleaseEvidenceArchive(id) {
  return request(`/api/builds/${id}/release-evidence/archive`);
}

export function buildDownloadUrl(id) {
  return `${API_URL}/api/builds/${id}/download`;
}

export function buildLogStreamUrl(id, follow = true) {
  return `${API_URL}/api/builds/${id}/logs/stream${follow ? "?follow=true" : ""}`;
}

export async function uploadArtifact(file, options = {}) {
  const form = new FormData();
  form.append("file", file);
  if (options.downloadPolicy) {
    form.append("downloadPolicy", options.downloadPolicy);
  }
  if (options.retentionDays !== undefined && options.retentionDays !== "") {
    form.append("retentionDays", String(options.retentionDays));
  }
  for (const [key, value] of Object.entries(options.metadata || {})) {
    if (value !== undefined && value !== "") {
      form.append(`metadata.${key}`, String(value));
    }
  }
  const response = await fetchWithNetworkError(`${API_URL}/api/uploads`, {
    method: "POST",
    body: form,
    headers: authHeaders(`${API_URL}/api/uploads`),
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw toApiError(payload, response.status, `Upload failed: ${response.status}`);
  }
  return payload;
}

export async function downloadFile(url, filename = "download") {
  const response = await fetchWithNetworkError(url, {
    headers: authHeaders(url),
  });
  const payload = await response.clone().json().catch(() => null);
  if (!response.ok) {
    throw toApiError(payload, response.status, `Download failed: ${response.status}`);
  }
  const blob = await response.blob();
  const objectUrl = window.URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = objectUrl;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.URL.revokeObjectURL(objectUrl);
}

export function createBuild(input) {
  return request("/api/builds", {
    method: "POST",
    body: JSON.stringify(input),
  });
}
