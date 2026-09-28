import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, copyFile, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const API_URL = process.env.PACKAGING_API_URL || "http://localhost:8080";
const RUNNER_ID = process.env.RUNNER_ID || `runner-${process.pid}`;
const TARGET = process.env.TARGET || "";
const RUNNER_DIR = path.dirname(fileURLToPath(import.meta.url));
let runnerBearer;
let executionCapability;

export function resolveRunnerTokenPath(secretsDir, tokenFile) {
  const name = String(tokenFile || "").trim();
  if (!name || path.isAbsolute(name) || name === "." || name === ".." || name.includes("/") || name.includes("\\")) {
    throw new Error("PACKAGING_API_TOKEN_FILE must be a safe relative file name");
  }
  return path.resolve(secretsDir, name);
}

export async function loadRunnerBearer({
  secretsDir = process.env.NOMAD_SECRETS_DIR || "",
  tokenFile = process.env.PACKAGING_API_TOKEN_FILE || "vela-runner-auth",
  required = process.env.RUNNER_AUTH_REQUIRED === "true",
} = {}) {
  if (!secretsDir) {
    if (required) throw new Error("runner bearer is required but NOMAD_SECRETS_DIR is unavailable");
    return "";
  }
  const tokenPath = resolveRunnerTokenPath(secretsDir, tokenFile);
  try {
    const token = (await readFile(tokenPath, "utf8")).trim();
    if (!token) throw new Error("runner bearer file is empty");
    return token;
  } catch (err) {
    if (required) throw new Error(`runner bearer is unavailable: ${err.message}`);
    return "";
  }
}

export function setRunnerBearerForTest(value) {
  runnerBearer = value || "";
}

export async function loadExecutionCapability({
  secretsDir = process.env.NOMAD_SECRETS_DIR || "",
  tokenFile = process.env.RUNNER_EXECUTION_FILE || "vela-runner-execution",
  required = process.env.RUNNER_AUTH_REQUIRED === "true",
} = {}) {
  if (!secretsDir) {
    if (required) throw new Error("runner execution capability is required but NOMAD_SECRETS_DIR is unavailable");
    return "";
  }
  const tokenPath = resolveRunnerTokenPath(secretsDir, tokenFile);
  try {
    const token = (await readFile(tokenPath, "utf8")).trim();
    if (!token) throw new Error("runner execution capability file is empty");
    return token;
  } catch (err) {
    if (required) throw new Error(`runner execution capability is unavailable: ${err.message}`);
    return "";
  }
}

export function setExecutionCapabilityForTest(value) {
  executionCapability = value || "";
}

function runnerHeaders(headers = {}) {
  const result = { ...headers };
  if (runnerBearer) result.Authorization = `Bearer ${runnerBearer}`;
  if (executionCapability) result["X-Runner-Execution"] = executionCapability;
  return result;
}

export async function request(pathname, options = {}) {
  const response = await fetch(`${API_URL}${pathname}`, {
    ...options,
    headers: runnerHeaders({
      "Content-Type": "application/json",
      ...options.headers,
    }),
  });
  if (response.status === 204) return null;
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload?.error || `request failed: ${response.status}`);
  }
  return payload;
}

export async function upload(filePath) {
  const form = new FormData();
  const data = await readFile(filePath);
  form.append("file", new Blob([data]), path.basename(filePath));
  const response = await fetch(`${API_URL}/api/uploads`, {
    method: "POST",
    body: form,
    headers: runnerHeaders(),
  });
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload?.error || `upload failed: ${response.status}`);
  }
  return payload;
}

function run(command, args, cwd) {
  const result = spawnSync(command, args, {
    cwd,
    encoding: "utf8",
    shell: process.platform === "win32",
    stdio: "pipe",
  });
  return {
    ok: result.status === 0,
    output: [result.stdout, result.stderr].filter(Boolean).join("\n").trim(),
  };
}

function runShell(command, cwd, env = {}) {
  const result = spawnSync(command, {
    cwd,
    encoding: "utf8",
    shell: true,
    stdio: "pipe",
    env: {
      ...process.env,
      ...env,
    },
  });
  return {
    ok: result.status === 0,
    output: [result.stdout, result.stderr].filter(Boolean).join("\n").trim(),
  };
}

async function log(build, message) {
  console.log(message);
  await request(`/api/builds/${build.id}/logs`, {
    method: "POST",
    body: JSON.stringify({ source: RUNNER_ID, message }),
  });
}

export function runnerTaskRequest({
  operation,
  buildID,
  runnerID = RUNNER_ID,
  target = TARGET,
  message,
  completion,
  authRequired = process.env.RUNNER_AUTH_REQUIRED === "true",
}) {
  if (authRequired && !buildID) throw new Error("BUILD_ID is required");
  if (operation === "claim") {
    return authRequired
      ? { pathname: `/api/runner/tasks/${buildID}/claim`, body: {} }
      : { pathname: "/api/runner/tasks/claim", body: { runnerId: runnerID, target } };
  }
  if (operation === "heartbeat") {
    return authRequired
      ? { pathname: `/api/runner/tasks/${buildID}/heartbeat`, body: { message } }
      : { pathname: "/api/runner/heartbeat", body: { runnerId: runnerID, buildId: buildID, target, message } };
  }
  if (operation === "complete") {
    return { pathname: `/api/runner/tasks/${buildID}/complete`, body: completion };
  }
  throw new Error(`unsupported runner task operation: ${operation}`);
}

async function heartbeat(build, message) {
  const taskRequest = runnerTaskRequest({ operation: "heartbeat", buildID: build.id, message });
  await request(taskRequest.pathname, {
    method: "POST",
    body: JSON.stringify(taskRequest.body),
  });
}

async function writeJSON(file, value) {
  await writeFile(file, JSON.stringify(value, null, 2));
}

function secretEnvName(name) {
  const prefix = process.env.SECRET_ENV_PREFIX || "PACKAGING_SECRET_";
  return `${prefix}${String(name || "")
    .trim()
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")}`;
}

function parseSecretRef(value) {
  const trimmed = String(value || "").trim();
  if (trimmed.startsWith("secret://")) return trimmed.slice("secret://".length).trim();
  if (trimmed.startsWith("secret:")) return trimmed.slice("secret:".length).trim();
  return "";
}

function resolveSecretRefs(value, stats = { count: 0 }) {
  if (typeof value === "string") {
    const name = parseSecretRef(value);
    if (!name) return value;
    const envName = secretEnvName(name);
    if (!(envName in process.env)) {
      throw new Error(`missing secret environment ${envName}`);
    }
    stats.count += 1;
    return process.env[envName];
  }
  if (Array.isArray(value)) {
    return value.map((item) => resolveSecretRefs(item, stats));
  }
  if (value && typeof value === "object") {
    return Object.fromEntries(
      Object.entries(value).map(([key, child]) => [key, resolveSecretRefs(child, stats)]),
    );
  }
  return value;
}

async function resolveBuildSecrets(build) {
  const stats = { count: 0 };
  const resolved = {
    ...build,
    finalConfig: resolveSecretRefs(build.finalConfig || {}, stats),
    variables: resolveSecretRefs(build.variables || {}, stats),
    workloads: resolveSecretRefs(build.workloads || build.buildLock?.workloads || [], stats),
  };
  await log(build, `secrets resolved: ${stats.count} reference(s)`);
  return resolved;
}

async function pathStats(root, limit) {
  const result = {
    path: root,
    exists: false,
    files: 0,
    directories: 0,
    bytes: 0,
    truncated: false,
    error: "",
  };
  if (!root) return result;
  try {
    const rootStat = await stat(root);
    result.exists = true;
    if (rootStat.isFile()) {
      result.files = 1;
      result.bytes = rootStat.size;
      return result;
    }
    if (!rootStat.isDirectory()) return result;
    const queue = [root];
    while (queue.length > 0) {
      const current = queue.shift();
      let entries;
      try {
        entries = await readdir(current, { withFileTypes: true });
      } catch (err) {
        result.error = err.message;
        continue;
      }
      for (const entry of entries) {
        const entryPath = path.join(current, entry.name);
        if (entry.isDirectory()) {
          result.directories += 1;
          queue.push(entryPath);
          continue;
        }
        if (!entry.isFile()) continue;
        const entryStat = await stat(entryPath).catch(() => null);
        result.files += 1;
        result.bytes += entryStat?.size || 0;
        if (result.files >= limit) {
          result.truncated = true;
          return result;
        }
      }
    }
    return result;
  } catch (err) {
    result.error = err.message;
    return result;
  }
}

async function collectCacheSnapshot(workspace) {
  const configuredLimit = Number(process.env.RUNNER_CACHE_SCAN_FILE_LIMIT || 5000);
  const limit = Number.isFinite(configuredLimit) && configuredLimit > 0 ? configuredLimit : 5000;
  const paths = [
    ["bun", process.env.BUN_INSTALL_CACHE_DIR],
    ["xdg", process.env.XDG_CACHE_HOME],
    ["dagger", process.env.DAGGER_CACHE_DIR],
    ["artifacts", process.env.ARTIFACT_ROOT],
    ["components", workspace ? path.join(workspace, "context", "components") : ""],
  ];
  const entries = [];
  for (const [name, root] of paths) {
    entries.push({ name, ...(await pathStats(root || "", limit)) });
  }
  return {
    runnerId: RUNNER_ID,
    target: TARGET,
    capturedAt: new Date().toISOString(),
    scanFileLimit: limit,
    entries,
  };
}

function formatBytes(bytes) {
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

function cacheSummary(snapshot) {
  return snapshot.entries
    .map((entry) => {
      if (!entry.path) return `${entry.name}=unconfigured`;
      if (!entry.exists) return `${entry.name}=missing`;
      const truncated = entry.truncated ? "+" : "";
      const error = entry.error ? ` error=${entry.error}` : "";
      return `${entry.name}=${formatBytes(entry.bytes)}/${entry.files}${truncated} files${error}`;
    })
    .join("; ");
}

async function reportCacheSnapshot(build, workspace, stage) {
  const snapshot = await collectCacheSnapshot(workspace);
  snapshot.stage = stage;
  await writeJSON(path.join(workspace, "context", "rendered", `runner-cache-${stage}.json`), snapshot);
  const summary = cacheSummary(snapshot);
  await log(build, `cache ${stage}: ${summary}`);
  await heartbeat(build, `cache ${stage}: ${summary}`);
  return snapshot;
}

async function sha256(file) {
  const hash = createHash("sha256");
  hash.update(await readFile(file));
  return hash.digest("hex");
}

async function prepareWorkspace(build) {
  const dir = path.join(os.tmpdir(), `packaging-${build.id}`);
  await rm(dir, { recursive: true, force: true });
  await mkdir(path.join(dir, "context"), { recursive: true });
  await mkdir(path.join(dir, "context", "components"), { recursive: true });
  await mkdir(path.join(dir, "context", "rendered"), { recursive: true });
  await mkdir(path.join(dir, "output"), { recursive: true });
  await copyFile(
    path.join(RUNNER_DIR, "fixtures", "linux_package_packager.py"),
    path.join(dir, "context", "rendered", "linux_package_packager.py"),
  ).catch(() => {});
  await writeJSON(path.join(dir, "context", "build-lock.json"), build.buildLock);
  await writeJSON(path.join(dir, "context", "final-config.json"), build.finalConfig);
  await writeJSON(path.join(dir, "context", "build-info.json"), {
    buildId: build.id,
    productId: build.productId,
    target: build.target,
    profile: build.profile,
    customer: build.customer,
    version: build.version,
    releaseChannel: build.releaseChannel || "preview",
    components: build.components,
    workloads: build.workloads || build.buildLock?.workloads || [],
    runnerId: RUNNER_ID,
    createdAt: new Date().toISOString(),
  });
  if (process.env.LSAPCTL_SOURCE_DIR) {
    await cp(process.env.LSAPCTL_SOURCE_DIR, path.join(dir, "context", "lsapctl-src"), {
      recursive: true,
      force: true,
      filter: (source) => !source.includes(`${path.sep}.git${path.sep}`),
    });
  }
  return dir;
}

function fileNameForComponent(component) {
  const uriName = decodeURIComponent((component.uri || "").split(/[/?#]/).pop() || "");
  if (uriName && !uriName.includes(":")) {
    return sanitizeFileName(`${component.id}-${uriName}`);
  }
  return `${component.id}.artifact`;
}

function sanitizeFileName(value) {
  return value.replace(/[<>:"/\\|?*]+/g, "_");
}

function localUploadDownloadURL(uri) {
  const [, rest = ""] = uri.split("local-upload://");
  const [id] = rest.split("/");
  return id ? `${API_URL}/api/uploads/${id}/download` : "";
}

function minioDownloadURL(uri) {
  const publicURL = process.env.MINIO_PUBLIC_URL || "";
  if (!publicURL) return "";
  return `${publicURL.replace(/\/$/, "")}/${uri.replace("minio://", "")}`;
}

function downloadHeaders(url) {
  const headers = url.startsWith(API_URL.replace(/\/$/, "")) ? runnerHeaders({}) : {};
  const gitlabBaseURL = process.env.GITLAB_PACKAGE_BASE_URL || "";
  const gitlabToken = process.env.GITLAB_PACKAGE_TOKEN || "";
  if (gitlabBaseURL && gitlabToken && url.startsWith(gitlabBaseURL.replace(/\/$/, ""))) {
    headers["PRIVATE-TOKEN"] = gitlabToken;
  }
  return headers;
}

export async function downloadToFile(url, target) {
  const response = await fetch(url, { headers: downloadHeaders(url) });
  if (!response.ok) {
    throw new Error(`download failed ${response.status}: ${url}`);
  }
  const data = Buffer.from(await response.arrayBuffer());
  await writeFile(target, data);
}

async function resolveLocalArtifact(uri, target) {
  const root = process.env.ARTIFACT_ROOT;
  if (!root) return false;
  const relative = uri.replace(/^[a-z0-9-]+:\/\//i, "");
  const source = path.join(root, relative);
  if (relative.includes("*")) {
    const directory = path.dirname(source);
    const pattern = new RegExp(
      `^${path.basename(source).split("*").map(escapeRegExp).join(".*")}$`,
    );
    const entries = await readdir(directory);
    const match = entries.find((entry) => pattern.test(entry));
    if (!match) {
      throw new Error(`artifact wildcard did not match: ${uri}`);
    }
    await copyFile(path.join(directory, match), target);
    return true;
  }
  await copyFile(source, target);
  return true;
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function downloadComponents(build, workspace) {
  const componentsDir = path.join(workspace, "context", "components");
  const lockedComponents = new Map((build.buildLock?.components || []).map((component) => [component.id, component]));
  const manifest = [];
  for (const component of build.components || []) {
    if (!component.uri) {
      await log(build, `component skipped: ${component.id} has no uri`);
      continue;
    }
    const target = path.join(componentsDir, fileNameForComponent(component));
    if (component.uri.startsWith("http://") || component.uri.startsWith("https://")) {
      await downloadToFile(component.uri, target);
    } else if (component.uri.startsWith("local-upload://")) {
      const locked = lockedComponents.get(component.id);
      if (!locked || locked.uri !== component.uri || !locked.sha256) {
        throw new Error(`component ${component.id} is not a locked local upload`);
      }
      await downloadToFile(`${API_URL}/api/runner/tasks/${build.id}/inputs/${locked.source?.uploadId || component.uri.split("//")[1].split("/")[0]}/download`, target);
      if ((await sha256(target)).toLowerCase() !== locked.sha256.toLowerCase()) {
        throw new Error(`component sha256 mismatch: ${component.id}`);
      }
    } else if (component.uri.startsWith("minio://") && process.env.MINIO_PUBLIC_URL) {
      await downloadToFile(minioDownloadURL(component.uri), target);
    } else {
      const copied = await resolveLocalArtifact(component.uri, target);
      if (!copied) {
        throw new Error(
          `cannot download ${component.id} from ${component.uri}; set MINIO_PUBLIC_URL or ARTIFACT_ROOT`,
        );
      }
    }
    manifest.push({ ...component, file: path.relative(path.join(workspace, "context"), target) });
    await log(build, `component downloaded: ${component.id}`);
  }
  await writeJSON(path.join(workspace, "context", "rendered", "components.json"), manifest);
}

async function renderConfigs(build, workspace) {
  const renderedDir = path.join(workspace, "context", "rendered");
  await writeJSON(path.join(renderedDir, "install-config.json"), build.finalConfig || {});
  await writeJSON(path.join(renderedDir, "build-lock.json"), build.buildLock || {});
  await writeJSON(path.join(renderedDir, "workloads.json"), build.workloads || build.buildLock?.workloads || []);
  await writeFile(
    path.join(renderedDir, "release.env"),
    [
      `BUILD_ID=${build.id}`,
      `PRODUCT_ID=${build.productId}`,
      `TARGET=${build.target}`,
      `VERSION=${build.version}`,
      `PROFILE=${build.profile || ""}`,
      `CUSTOMER=${build.customer || ""}`,
      `RELEASE_CHANNEL=${build.releaseChannel || build.profile || ""}`,
    ].join("\n") + "\n",
  );
}

async function writeDaggerPlan(build, workspace) {
  const plan = {
    schemaVersion: "packaging.dagger/v1",
    buildId: build.id,
    productId: build.productId,
    target: build.target,
    version: build.version,
    profile: build.profile || "",
    customer: build.customer || "",
    releaseChannel: build.releaseChannel || build.profile || "",
    packageName: `${build.productId}-${build.target}-${build.version}-${build.id}.tar.gz`,
    baseImageSource: process.env.DAGGER_LINUX_BASE_IMAGE_SOURCE || "registry",
    baseImage: process.env.DAGGER_LINUX_BASE_IMAGE || "alpine:3.20",
    baseImageTar: process.env.DAGGER_LINUX_BASE_IMAGE_TAR || "",
    baseImageTarTag: process.env.DAGGER_LINUX_BASE_IMAGE_TAR_TAG || "",
    installToolsCommand:
      process.env.DAGGER_LINUX_INSTALL_TOOLS ||
      "apk add --no-cache tar gzip coreutils bash",
    lsapctlBuildCommand: process.env.LSAPCTL_BUILD_CMD || "",
    lsapctlSourceDir: process.env.LSAPCTL_SOURCE_DIR ? "lsapctl-src" : "",
    packageCommand: process.env.DAGGER_LINUX_PACKAGE_CMD || process.env.LINUX_PACKAGER_CMD || "",
    contextFiles: [
      "build-lock.json",
      "final-config.json",
      "build-info.json",
      "components/",
      "rendered/",
    ],
    components: build.components || [],
    workloads: build.workloads || build.buildLock?.workloads || [],
    config: build.finalConfig || {},
  };
  const planPath = path.join(workspace, "context", "rendered", "pipeline-plan.json");
  await writeJSON(planPath, plan);
  return { plan, planPath };
}

async function compileLsapctl(build, workspace) {
  const command = process.env.LSAPCTL_BUILD_CMD;
  if (!command) {
    await log(build, "lsapctl compile skipped: LSAPCTL_BUILD_CMD is not set");
    return;
  }
  const result = runShell(command, workspace, {
    BUILD_ID: build.id,
    TARGET: build.target,
    VERSION: build.version,
    OUTPUT_DIR: path.join(workspace, "context"),
  });
  if (!result.ok) {
    throw new Error(result.output || "lsapctl compile failed");
  }
  await log(build, "lsapctl compiled");
}

function daggerPipelinePath() {
  return process.env.DAGGER_LINUX_PIPELINE || path.join(RUNNER_DIR, "dagger", "linux-package.mjs");
}

async function packageLinuxWithDagger(build, workspace, planPath) {
  const outputDir = path.join(workspace, "output");
  const command = [
    "dagger",
    "run",
    "--",
    "bun",
    shellArg(daggerPipelinePath()),
    "--context",
    shellArg(path.join(workspace, "context")),
    "--output-dir",
    shellArg(outputDir),
    "--plan",
    shellArg(planPath),
  ].join(" ");
  const result = runShell(command, workspace);
  if (!result.ok) {
    throw new Error(result.output || "dagger linux packaging failed");
  }
  return path.join(outputDir, `${build.productId}-${build.target}-${build.version}-${build.id}.tar.gz`);
}

async function packageLinux(build, workspace) {
  const name = `${build.productId}-${build.target}-${build.version}-${build.id}.tar.gz`;
  const output = path.join(workspace, "output", name);
  const custom = process.env.LINUX_PACKAGER_CMD;
  const result = custom
    ? runShell(custom, workspace, { OUTPUT: output })
    : run("tar", ["-czf", output, "-C", path.join(workspace, "context"), "."], workspace);
  if (!result.ok) {
    throw new Error(result.output || "linux packaging failed");
  }
  return output;
}

async function packageWindows(build, workspace) {
  const name = `${build.productId}-${build.target}-${build.version}-${build.id}-setup.exe`;
  const output = path.join(workspace, "output", name);
  const custom = process.env.WINDOWS_PACKAGER_CMD;
  if (custom) {
    const result = runShell(custom, workspace, { OUTPUT: output });
    if (!result.ok) {
      throw new Error(result.output || "windows packaging failed");
    }
    return output;
  }

  await writeFile(
    output,
    [
      "Vela Windows setup placeholder.",
      "Set WINDOWS_PACKAGER_CMD to run Inno Setup or WiX on a Windows runner.",
      `Build: ${build.id}`,
    ].join("\n"),
  );
  return output;
}

async function signWindowsArtifact(build, workspace, output) {
  const command = process.env.WINDOWS_SIGNING_CMD;
  if (!command) {
    await log(build, "windows signing skipped: WINDOWS_SIGNING_CMD is not set");
    return;
  }
  const result = runShell(command, workspace, {
    OUTPUT: output,
    ARTIFACT: output,
    BUILD_ID: build.id,
    PRODUCT_ID: build.productId,
    TARGET: build.target,
    VERSION: build.version,
  });
  if (!result.ok) {
    throw new Error(result.output || "windows signing failed");
  }
  await log(build, "windows artifact signed");
}

async function runPackaging(build) {
  const runtimeBuild = await resolveBuildSecrets(build);
  const workspace = await prepareWorkspace(runtimeBuild);
  await heartbeat(runtimeBuild, "workspace prepared");
  await log(runtimeBuild, `workspace prepared at ${workspace}`);
  await reportCacheSnapshot(runtimeBuild, workspace, "before");
  await downloadComponents(runtimeBuild, workspace);
  await reportCacheSnapshot(runtimeBuild, workspace, "components");
  await renderConfigs(runtimeBuild, workspace);

  const { planPath } = await writeDaggerPlan(runtimeBuild, workspace);
  const useDagger = process.env.USE_DAGGER_FOR_LINUX !== "false";
  let output;
  if (runtimeBuild.target === "windows") {
    await compileLsapctl(runtimeBuild, workspace);
    output = await packageWindows(runtimeBuild, workspace);
    await signWindowsArtifact(runtimeBuild, workspace, output);
  } else {
    output = useDagger
      ? await packageLinuxWithDagger(runtimeBuild, workspace, planPath)
      : await packageLinuxWithoutDagger(runtimeBuild, workspace);
  }
  const digest = await sha256(output);
  const checksumFile = `${output}.sha256`;
  await writeFile(checksumFile, `${digest}  ${path.basename(output)}\n`);
  await log(runtimeBuild, `package created: ${output}`);
  await reportCacheSnapshot(runtimeBuild, workspace, "after");

  const uploaded = await upload(output);
  const checksum = await upload(checksumFile);
  await log(runtimeBuild, `uploaded checksum: ${checksum.uri}`);
  await log(runtimeBuild, `uploaded artifact: ${uploaded.uri}`);
  return uploaded;
}

async function packageLinuxWithoutDagger(build, workspace) {
  await compileLsapctl(build, workspace);
  return packageLinux(build, workspace);
}

function shellArg(value) {
  return `"${String(value).replaceAll('"', '\\"')}"`;
}

export async function main() {
  const authRequired = process.env.RUNNER_AUTH_REQUIRED === "true";
  const buildID = process.env.BUILD_ID || "";
  if (authRequired && !buildID) throw new Error("BUILD_ID is required");
  runnerBearer = authRequired ? await loadRunnerBearer() : "";
  executionCapability = authRequired ? await loadExecutionCapability() : "";
  if (authRequired && !executionCapability) throw new Error("runner execution capability is required");
  const claim = runnerTaskRequest({ operation: "claim", buildID, authRequired });
  const build = await request(claim.pathname, {
    method: "POST",
    body: JSON.stringify(claim.body),
  });
  if (authRequired && (!build || build.id !== buildID || build.target !== TARGET)) {
    throw new Error("claim response does not match assigned build and target");
  }

  if (!build) {
    console.log("no queued build");
    return;
  }

  let heartbeatTimer;
  try {
    await log(build, `claimed ${build.productId}/${build.target}/${build.version}`);
    heartbeatTimer = setInterval(() => {
      heartbeat(build, "runner active").catch((err) => {
        console.warn(`heartbeat failed: ${err.message}`);
      });
    }, Number(process.env.HEARTBEAT_INTERVAL_MS || 30000));
    await runPackaging(build);
    clearInterval(heartbeatTimer);
    const completion = runnerTaskRequest({ operation: "complete", buildID: build.id, completion: { status: "succeeded" } });
    await request(completion.pathname, {
      method: "POST",
      body: JSON.stringify(completion.body),
    });
  } catch (err) {
    if (heartbeatTimer) {
      clearInterval(heartbeatTimer);
    }
    await log(build, `failed: ${err.message}`);
    const completion = runnerTaskRequest({
      operation: "complete",
      buildID: build.id,
      completion: { status: "failed", error: err.message },
    });
    await request(completion.pathname, {
      method: "POST",
      body: JSON.stringify(completion.body),
    });
    process.exit(1);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
