import { connect } from "@dagger.io/dagger";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";

const args = parseArgs(process.argv.slice(2));
const contextPath = required(args.context, "--context");
const outputDir = required(args.outputDir || args["output-dir"], "--output-dir");
const planPath = required(args.plan, "--plan");

const plan = JSON.parse(await readFile(planPath, "utf8"));
validatePlan(plan);
const packageName = plan.packageName || `${plan.productId}-${plan.target}-${plan.version}-${plan.buildId}.tar.gz`;
const baseImage = plan.baseImage || "alpine:3.20";

await mkdir(outputDir, { recursive: true });
await renderWorkloadFiles(plan, contextPath);

await connect(async (client) => {
  const contextDir = client.host().directory(contextPath);
  const script = renderLinuxScript(plan, packageName);
  const baseContainer = resolveBaseContainer(client, plan, baseImage);

  let container = baseContainer
    .withMountedDirectory("/workspace/context", contextDir)
    .withWorkdir("/workspace");

  if (shouldRunInstallTools(plan.installToolsCommand)) {
    container = container.withExec(["sh", "-lc", plan.installToolsCommand]);
  }

  container = container.withExec(["sh", "-lc", script]);

  await container.file(`/workspace/output/${packageName}`).export(path.join(outputDir, packageName));
});

function resolveBaseContainer(client, plan, fallbackImage) {
  if (plan.baseImageTar) {
    const opts = plan.baseImageTarTag ? { tag: plan.baseImageTarTag } : undefined;
    return client.container().import_(client.host().file(plan.baseImageTar), opts);
  }
  if (plan.baseImageSource === "host") {
    return client.host().containerImage(fallbackImage);
  }
  if (plan.baseImageSource && plan.baseImageSource !== "registry") {
    throw new Error(`unsupported base image source: ${plan.baseImageSource}`);
  }
  return client.container().from(fallbackImage);
}

function shouldRunInstallTools(command) {
  if (!command) return false;
  return !["false", "no", "none", "skip"].includes(String(command).trim().toLowerCase());
}

function renderLinuxScript(plan, packageName) {
  const commands = [
    "set -eu",
    "mkdir -p /workspace/output",
    `export BUILD_ID=${shellQuote(plan.buildId)}`,
    `export PRODUCT_ID=${shellQuote(plan.productId)}`,
    `export TARGET=${shellQuote(plan.target)}`,
    `export VERSION=${shellQuote(plan.version)}`,
    `export PROFILE=${shellQuote(plan.profile || "")}`,
    `export CUSTOMER=${shellQuote(plan.customer || "")}`,
    `export RELEASE_CHANNEL=${shellQuote(plan.releaseChannel || "")}`,
    "export CONTEXT_DIR=/workspace/context",
    "export OUTPUT_DIR=/workspace/context",
    `export OUTPUT=/workspace/output/${shellQuote(packageName)}`,
  ];

  if (plan.lsapctlBuildCommand) {
    commands.push(`cd /workspace && ${plan.lsapctlBuildCommand}`);
  }

  if (plan.packageCommand) {
    commands.push(`cd /workspace && ${plan.packageCommand}`);
  } else {
    commands.push(`tar -czf /workspace/output/${shellQuote(packageName)} -C /workspace/context .`);
  }

  return commands.join("\n");
}

async function renderWorkloadFiles(plan, contextPath) {
  const renderedDir = path.join(contextPath, "rendered");
  const systemdDir = path.join(contextPath, "systemd");
  await mkdir(renderedDir, { recursive: true });
  await mkdir(systemdDir, { recursive: true });

  const workloads = (plan.workloads || []).filter((workload) => workload.enabled !== false);
  const dockerWorkloads = workloads.filter((workload) => workload.runtime === "docker");
  const systemdWorkloads = workloads.filter((workload) => workload.runtime === "systemd");

  if (dockerWorkloads.length > 0) {
    await writeFile(path.join(renderedDir, "docker-compose.yml"), renderCompose(plan, dockerWorkloads));
  }
  for (const workload of systemdWorkloads) {
    const unitName = workload.systemd?.unitName || `${workload.id}.service`;
    await writeFile(path.join(systemdDir, unitName), renderSystemdUnit(plan, workload));
  }

  await writeFile(path.join(renderedDir, "lsapctl-runtime.json"), JSON.stringify(renderLsapctlRuntime(plan, dockerWorkloads, systemdWorkloads), null, 2));
}

function renderCompose(plan, workloads) {
  const lines = ["services:"];
  for (const workload of workloads) {
    const docker = workload.docker || {};
    lines.push(`  ${workload.id}:`);
    lines.push(`    image: ${templateValue(plan, docker.image || workload.component || workload.id)}`);
    if (docker.containerName) lines.push(`    container_name: ${templateValue(plan, docker.containerName)}`);
    if (docker.restart) lines.push(`    restart: ${docker.restart}`);
    appendList(lines, "ports", docker.ports, 4, plan);
    appendList(lines, "volumes", docker.volumes, 4, plan);
    appendList(lines, "depends_on", docker.dependsOn, 4, plan);
    if (docker.command) lines.push(`    command: ${templateValue(plan, docker.command)}`);
    if (docker.environment && Object.keys(docker.environment).length > 0) {
      lines.push("    environment:");
      for (const [key, value] of Object.entries(docker.environment)) {
        lines.push(`      ${key}: ${JSON.stringify(templateValue(plan, value))}`);
      }
    }
  }
  return lines.join("\n") + "\n";
}

function renderSystemdUnit(plan, workload) {
  const systemd = workload.systemd || {};
  const lines = [
    "[Unit]",
    `Description=${templateValue(plan, systemd.description || workload.id)}`,
  ];
  if (systemd.after?.length) lines.push(`After=${systemd.after.join(" ")}`);
  lines.push("", "[Service]");
  if (systemd.user) lines.push(`User=${templateValue(plan, systemd.user)}`);
  if (systemd.group) lines.push(`Group=${templateValue(plan, systemd.group)}`);
  if (systemd.workingDirectory) lines.push(`WorkingDirectory=${templateValue(plan, systemd.workingDirectory)}`);
  if (systemd.environmentFile) lines.push(`EnvironmentFile=-${templateValue(plan, systemd.environmentFile)}`);
  for (const [key, value] of Object.entries(systemd.environment || {})) {
    lines.push(`Environment="${key}=${templateValue(plan, value)}"`);
  }
  lines.push(`ExecStart=${templateValue(plan, required(systemd.execStart, `systemd.execStart for ${workload.id}`))}`);
  if (systemd.execStop) lines.push(`ExecStop=${templateValue(plan, systemd.execStop)}`);
  lines.push(`Restart=${systemd.restart || "always"}`);
  lines.push("", "[Install]", `WantedBy=${systemd.wantedBy || "multi-user.target"}`);
  return lines.join("\n") + "\n";
}

function renderLsapctlRuntime(plan, dockerWorkloads, systemdWorkloads) {
  return {
    schemaVersion: "lsapctl.runtime/v1",
    buildId: plan.buildId,
    productId: plan.productId,
    version: plan.version,
    installDir: plan.config?.installDir || "",
    files: {
      compose: dockerWorkloads.length > 0 ? "rendered/docker-compose.yml" : "",
      systemdDir: systemdWorkloads.length > 0 ? "systemd" : "",
      config: "rendered/install-config.json",
      buildLock: "rendered/build-lock.json",
      workloads: "rendered/workloads.json",
    },
    workloads: [
      ...dockerWorkloads.map((workload) => ({
        id: workload.id,
        runtime: "docker",
        component: workload.component,
        module: workload.module,
        composeService: workload.id,
      })),
      ...systemdWorkloads.map((workload) => ({
        id: workload.id,
        runtime: "systemd",
        component: workload.component,
        module: workload.module,
        unitName: workload.systemd?.unitName || `${workload.id}.service`,
      })),
    ],
    commands: {
      install: "lsapctl install",
      start: "lsapctl start",
      stop: "lsapctl stop",
      uninstall: "lsapctl uninstall",
      status: "lsapctl status",
    },
  };
}

function appendList(lines, key, values, indent, plan) {
  if (!Array.isArray(values) || values.length === 0) return;
  const pad = " ".repeat(indent);
  lines.push(`${pad}${key}:`);
  for (const value of values) {
    lines.push(`${pad}  - ${JSON.stringify(templateValue(plan, value))}`);
  }
}

function templateValue(plan, value) {
  if (typeof value !== "string") return value;
  return value.replace(/\{\{([^}]+)\}\}/g, (_, expression) => {
    const key = expression.trim();
    if (key === "version") return plan.version || "";
    if (key === "profile") return plan.profile || "";
    if (key === "customer") return plan.customer || "";
    if (key === "installDir") return plan.config?.installDir || "";
    const parts = key.split(".");
    let current = plan.config;
    for (const part of parts) {
      current = current?.[part];
    }
    return current === undefined || current === null ? "" : String(current);
  });
}

function parseArgs(values) {
  const parsed = {};
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index]?.replace(/^--/, "");
    const value = values[index + 1];
    if (!key || value === undefined) {
      throw new Error(`invalid argument list: ${values.join(" ")}`);
    }
    parsed[key] = value;
  }
  return parsed;
}

function required(value, name) {
  if (!value) throw new Error(`${name} is required`);
  return value;
}

function validatePlan(plan) {
  if (plan.schemaVersion !== "packaging.dagger/v1") {
    throw new Error(`unsupported plan schema: ${plan.schemaVersion || "missing"}`);
  }
  for (const field of ["buildId", "productId", "target", "version"]) {
    if (!plan[field]) throw new Error(`plan field ${field} is required`);
  }
  if (plan.target !== "linux") {
    throw new Error(`linux Dagger pipeline cannot build target ${plan.target}`);
  }
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}
