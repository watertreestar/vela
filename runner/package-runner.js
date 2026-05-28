import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cp, copyFile, mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const API_URL = process.env.PACKAGING_API_URL || "http://localhost:8080";
const RUNNER_ID = process.env.RUNNER_ID || `runner-${process.pid}`;
const TARGET = process.env.TARGET || "";
const RUNNER_DIR = path.dirname(fileURLToPath(import.meta.url));

async function request(pathname, options = {}) {
  const response = await fetch(`${API_URL}${pathname}`, {
    headers: {
      "Content-Type": "application/json",
      ...options.headers,
    },
    ...options,
  });
  if (response.status === 204) return null;
  const payload = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(payload?.error || `request failed: ${response.status}`);
  }
  return payload;
}

async function upload(filePath) {
  const form = new FormData();
  const data = await readFile(filePath);
  form.append("file", new Blob([data]), path.basename(filePath));
  const response = await fetch(`${API_URL}/api/uploads`, {
    method: "POST",
    body: form,
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

async function heartbeat(build, message) {
  await request("/api/runner/heartbeat", {
    method: "POST",
    body: JSON.stringify({
      runnerId: RUNNER_ID,
      buildId: build.id,
      target: build.target,
      message,
    }),
  });
}

async function writeJSON(file, value) {
  await writeFile(file, JSON.stringify(value, null, 2));
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
  await writeJSON(path.join(dir, "context", "build-lock.json"), build.buildLock);
  await writeJSON(path.join(dir, "context", "final-config.json"), build.finalConfig);
  await writeJSON(path.join(dir, "context", "build-info.json"), {
    buildId: build.id,
    productId: build.productId,
    target: build.target,
    profile: build.profile,
    customer: build.customer,
    version: build.version,
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

async function downloadToFile(url, target) {
  const response = await fetch(url);
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
      await downloadToFile(localUploadDownloadURL(component.uri), target);
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
    baseImage: process.env.DAGGER_LINUX_BASE_IMAGE || "alpine:3.20",
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
      "Packaging Platform Windows setup placeholder.",
      "Set WINDOWS_PACKAGER_CMD to run Inno Setup or WiX on a Windows runner.",
      `Build: ${build.id}`,
    ].join("\n"),
  );
  return output;
}

async function runPackaging(build) {
  const workspace = await prepareWorkspace(build);
  await heartbeat(build, "workspace prepared");
  await log(build, `workspace prepared at ${workspace}`);
  await downloadComponents(build, workspace);
  await renderConfigs(build, workspace);

  const { planPath } = await writeDaggerPlan(build, workspace);
  const useDagger = process.env.USE_DAGGER_FOR_LINUX !== "false";
  let output;
  if (build.target === "windows") {
    await compileLsapctl(build, workspace);
    output = await packageWindows(build, workspace);
  } else {
    output = useDagger
      ? await packageLinuxWithDagger(build, workspace, planPath)
      : await packageLinuxWithoutDagger(build, workspace);
  }
  const digest = await sha256(output);
  const checksumFile = `${output}.sha256`;
  await writeFile(checksumFile, `${digest}  ${path.basename(output)}\n`);
  await log(build, `package created: ${output}`);

  const uploaded = await upload(output);
  const checksum = await upload(checksumFile);
  await log(build, `uploaded checksum: ${checksum.uri}`);
  await log(build, `uploaded artifact: ${uploaded.uri}`);
  return uploaded;
}

async function packageLinuxWithoutDagger(build, workspace) {
  await compileLsapctl(build, workspace);
  return packageLinux(build, workspace);
}

function shellArg(value) {
  return `"${String(value).replaceAll('"', '\\"')}"`;
}

async function main() {
  const build = await request("/api/runner/tasks/claim", {
    method: "POST",
    body: JSON.stringify({ runnerId: RUNNER_ID, target: TARGET }),
  });

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
    const uploaded = await runPackaging(build);
    clearInterval(heartbeatTimer);
    await request(`/api/runner/tasks/${build.id}/complete`, {
      method: "POST",
      body: JSON.stringify({
        status: "succeeded",
        artifactUri: uploaded.uri,
        downloadUrl: uploaded.downloadUrl,
      }),
    });
  } catch (err) {
    if (heartbeatTimer) {
      clearInterval(heartbeatTimer);
    }
    await log(build, `failed: ${err.message}`);
    await request(`/api/runner/tasks/${build.id}/complete`, {
      method: "POST",
      body: JSON.stringify({
        status: "failed",
        error: err.message,
      }),
    });
    process.exit(1);
  }
}

main().catch((err) => {
  console.error(err.message);
  process.exit(1);
});
