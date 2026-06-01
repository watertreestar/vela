const API_URL = import.meta.env.VITE_API_URL || "http://localhost:8080";

async function request(path, options = {}) {
  const response = await fetch(`${API_URL}${path}`, {
    headers: {
      "Content-Type": "application/json",
      ...options.headers,
    },
    ...options,
  });

  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload?.error || `Request failed: ${response.status}`);
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

export function getBuildLogs(id) {
  return request(`/api/builds/${id}/logs`);
}

export function getBuildLock(id) {
  return request(`/api/builds/${id}/lock`);
}

export function getBuildReleaseCheck(id) {
  return request(`/api/builds/${id}/release-check`);
}

export function buildDownloadUrl(id) {
  return `${API_URL}/api/builds/${id}/download`;
}

export async function uploadArtifact(file) {
  const form = new FormData();
  form.append("file", file);
  const response = await fetch(`${API_URL}/api/uploads`, {
    method: "POST",
    body: form,
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload?.error || `Upload failed: ${response.status}`);
  }
  return payload;
}

export function createBuild(input) {
  return request("/api/builds", {
    method: "POST",
    body: JSON.stringify(input),
  });
}
