import { expect, test } from "@playwright/test";

const product = {
  id: "lsap",
  name: "智能定位平台",
  description: "Linux and Windows installer packaging definition for LSAP.",
  targets: ["linux", "windows"],
  targetConfig: {
    linux: { installDir: "/opt/lsap", packageName: "lsap-linux-x86_64" },
    windows: { installDir: "C:\\Program Files\\LSAP", packageName: "lsap-windows-x64-setup" },
  },
  profiles: ["qa", "prod", "preview"],
  versions: ["latest", "1.0.0", "1.1.0", "2026.05.28"],
  components: [
    {
      id: "location-guard",
      type: "docker-image",
      required: true,
      source: {
        kind: "artifact-repository",
        repository: "minio",
        path: "artifact/Publish/location-guard/main/{{version}}/*.tar.gz",
      },
    },
    {
      id: "location-ui",
      type: "archive",
      required: true,
      source: {
        kind: "fixed-url",
        url: "https://artifacts.example.test/location-ui.zip",
      },
    },
    {
      id: "license",
      type: "license",
      required: false,
      source: { kind: "manual-upload", accept: [".lic"] },
    },
  ],
  workloads: [
    { id: "guard-api", runtime: "docker", component: "location-guard", module: "map" },
    {
      id: "guard-worker",
      runtime: "systemd",
      component: "location-guard",
      module: "push",
      systemd: { unitName: "lsap-guard-worker.service", execStart: "/opt/lsap/guard-worker" },
    },
  ],
  config: {
    ports: { web: 48000 },
    database: { passwordPolicy: "strong" },
    customer: { name: "Default Customer", license: "community" },
    modules: { camera: true, push: true, map: true },
  },
};

const baseLock = {
  buildId: "build-preview-ready",
  productId: "lsap",
  target: "windows",
  profile: "prod",
  version: "1.0.0",
  releaseChannel: "preview",
  config: product.config,
  components: [
    {
      id: "location-guard",
      type: "docker-image",
      version: "1.0.0",
      source: { kind: "artifact-repository" },
      uri: "minio://packaging/location-guard/1.0.0/location-guard.tar.gz",
      sha256: "1111222233334444555566667777888899990000aaaabbbbccccdddd",
      required: true,
    },
    {
      id: "location-ui",
      type: "archive",
      version: "1.0.0",
      source: { kind: "fixed-url" },
      uri: "https://artifacts.example.test/location-ui-1.0.0.zip",
      sha256: "99990000aaaabbbbccccdddd11112222333344445555666677778888",
      required: true,
    },
  ],
  workloads: product.workloads.map((workload) => ({ ...workload, enabled: true })),
};

const builds = [
  {
    id: "build-pending-001",
    productId: "lsap",
    target: "linux",
    profile: "qa",
    customer: "acme",
    version: "latest",
    releaseChannel: "preview",
    status: "pending_approval",
    finalConfig: product.config,
    components: baseLock.components,
    workloads: baseLock.workloads,
    buildLock: { ...baseLock, buildId: "build-pending-001", target: "linux", profile: "qa", version: "latest" },
    createdAt: "2026-05-28T05:00:00Z",
    updatedAt: "2026-05-28T05:05:00Z",
    logs: [],
  },
  {
    id: "build-preview-ready",
    productId: "lsap",
    target: "windows",
    profile: "prod",
    customer: "city-bank",
    version: "1.0.0",
    releaseChannel: "preview",
    status: "preview_validated",
    artifactUri: "minio://packaging/lsap/1.0.0/windows/setup.exe",
    downloadUrl: "https://minio.example.test/lsap/1.0.0/windows/setup.exe",
    finalConfig: product.config,
    components: baseLock.components,
    workloads: baseLock.workloads,
    buildLock: baseLock,
    createdAt: "2026-05-28T04:00:00Z",
    updatedAt: "2026-05-28T04:45:00Z",
    validatedAt: "2026-05-28T04:44:00Z",
    logs: [],
  },
  {
    id: "build-preview-blocked",
    productId: "lsap",
    target: "linux",
    profile: "preview",
    customer: "trial",
    version: "latest",
    releaseChannel: "preview",
    status: "preview_validated",
    artifactUri: "local-upload://preview/lsap.tar.gz",
    finalConfig: product.config,
    components: [
      { ...baseLock.components[0], version: "latest", uri: "minio://packaging/location-guard/latest/*.tar.gz" },
    ],
    workloads: baseLock.workloads,
    buildLock: {
      ...baseLock,
      buildId: "build-preview-blocked",
      target: "linux",
      profile: "preview",
      version: "latest",
      components: [
        { ...baseLock.components[0], version: "latest", uri: "minio://packaging/location-guard/latest/*.tar.gz" },
      ],
    },
    createdAt: "2026-05-28T03:00:00Z",
    updatedAt: "2026-05-28T03:30:00Z",
    logs: [],
  },
  {
    id: "build-running-001",
    productId: "lsap",
    target: "linux",
    profile: "qa",
    customer: "demo",
    version: "1.1.0",
    releaseChannel: "preview",
    status: "running",
    runnerId: "linux-runner-01",
    finalConfig: product.config,
    components: baseLock.components,
    workloads: baseLock.workloads,
    buildLock: { ...baseLock, buildId: "build-running-001", target: "linux", profile: "qa", version: "1.1.0" },
    createdAt: "2026-05-28T06:00:00Z",
    updatedAt: "2026-05-28T06:10:00Z",
    logs: [],
  },
  {
    id: "build-failed-001",
    productId: "lsap",
    target: "windows",
    profile: "qa",
    customer: "demo",
    version: "2026.05.28",
    releaseChannel: "preview",
    status: "failed",
    error: "windows packaging failed",
    finalConfig: product.config,
    components: baseLock.components,
    workloads: baseLock.workloads,
    buildLock: { ...baseLock, buildId: "build-failed-001", target: "windows", profile: "qa", version: "2026.05.28" },
    createdAt: "2026-05-28T02:00:00Z",
    updatedAt: "2026-05-28T02:20:00Z",
    logs: [],
  },
];

const uploads = [
  {
    id: "upload-license",
    fileName: "city-bank.lic",
    uri: "local-upload://upload-license/city-bank.lic",
    downloadUrl: "http://localhost:8080/api/uploads/upload-license/download",
    sha256: "abcdef0123456789abcdef0123456789",
    size: 2048,
    contentType: "application/octet-stream",
    downloadPolicy: "platform",
    retentionDays: 30,
    retentionStatus: "active",
    metadata: { productId: "lsap", purpose: "license" },
    createdAt: "2026-05-28T06:05:00Z",
  },
  {
    id: "upload-ui-hotfix",
    fileName: "location-ui-hotfix.zip",
    uri: "local-upload://upload-ui-hotfix/location-ui-hotfix.zip",
    downloadUrl: "http://localhost:8080/api/uploads/upload-ui-hotfix/download",
    sha256: "fedcba9876543210fedcba9876543210",
    size: 8_388_608,
    contentType: "application/zip",
    downloadPolicy: "restricted",
    retentionDays: 7,
    retentionStatus: "active",
    metadata: { productId: "lsap", purpose: "hotfix" },
    createdAt: "2026-05-28T06:08:00Z",
  },
  ...Array.from({ length: 26 }, (_, index) => {
    const sequence = String(index + 1).padStart(2, "0");
    return {
      id: `upload-acceptance-${sequence}`,
      fileName: `acceptance-package-${sequence}.tar.gz`,
      uri: `local-upload://upload-acceptance-${sequence}/acceptance-package-${sequence}.tar.gz`,
      downloadUrl: `http://localhost:8080/api/uploads/upload-acceptance-${sequence}/download`,
      sha256: `${sequence.repeat(8)}abcdef0123456789abcdef0123456789`,
      size: 1024 + index,
      contentType: "application/gzip",
      downloadPolicy: index % 2 === 0 ? "platform" : "restricted",
      retentionDays: index % 3 === 0 ? 7 : 30,
      retentionStatus: "active",
      metadata: { productId: "lsap", purpose: `acceptance-${sequence}` },
      createdAt: `2026-05-28T06:${String(10 + index).padStart(2, "0")}:00Z`,
    };
  }),
];

const runners = [
  {
    runnerId: "linux-runner-01",
    target: "linux",
    status: "busy",
    currentBuildId: "build-running-001",
    currentBuildState: "running",
    lastHeartbeatAt: "2026-05-28T06:12:00Z",
    lastLogAt: "2026-05-28T06:12:00Z",
    runningBuilds: 1,
    completedBuilds: 3,
    failedBuilds: 1,
    recentMessages: [{ sequence: 3, time: "2026-05-28T06:12:00Z", source: "linux-runner-01", message: "heartbeat: active" }],
  },
];

const auditEvents = [
  {
    id: "aud-build-download",
    time: "2026-05-28T06:55:00Z",
    action: "build.download",
    actorId: "operator",
    actorRole: "operator",
    resourceType: "build",
    resourceId: "build-preview-ready",
    productId: "lsap",
    buildId: "build-preview-ready",
    uploadId: "upload-license",
    requestId: "req-build-download",
    correlationId: "corr-release-flow",
    status: "success",
    summary: "build artifact download requested",
    metadata: { downloadPolicy: "restricted", retention: "active" },
  },
  {
    id: "aud-archive-export",
    time: "2026-05-28T06:58:00Z",
    action: "audit.archive.export",
    actorId: "viewer",
    actorRole: "viewer",
    resourceType: "audit_archive",
    resourceId: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    productId: "lsap",
    requestId: "req-archive",
    correlationId: "corr-release-flow",
    status: "success",
    summary: "audit archive exported",
    metadata: { digestAlgorithm: "sha256", retentionMode: "COMPLIANCE" },
  },
];

function jwtWithClaims(claims) {
  const encode = (payload) => Buffer.from(JSON.stringify(payload)).toString("base64url");
  return `${encode({ alg: "RS256" })}.${encode(claims)}.signature`;
}

function releaseCheckFor(build) {
  const fixedVersion = build.version && build.version !== "latest";
  const exactUris = (build.buildLock?.components || build.components || []).every(
    (component) => component.uri && !component.uri.includes("*"),
  );
  const fixedComponents = (build.buildLock?.components || build.components || []).every(
    (component) => component.version !== "latest",
  );
  const releaseReady = build.status === "preview_validated" && fixedVersion && exactUris && fixedComponents && !build.error;
  return {
    buildId: build.id,
    stage: build.status === "preview_validated" ? "release" : "preview",
    previewReady: ["succeeded", "preview_validated", "published"].includes(build.status),
    releaseReady,
    blockingCount: releaseReady ? 0 : 1,
    checks: [
      { id: "preview.artifact", stage: "preview", title: "Preview 制品可下载", status: build.artifactUri || build.downloadUrl ? "pass" : "blocked", reason: "" },
      { id: "release.fixed-version", stage: "release", title: "构建版本固定", status: fixedVersion ? "pass" : "blocked", reason: fixedVersion ? "" : "release requires a fixed build version" },
      { id: "release.exact-component-uris", stage: "release", title: "组件 URI 精确", status: exactUris ? "pass" : "blocked", reason: exactUris ? "" : "release requires exact component URIs without wildcards" },
      { id: "release.fixed-component-versions", stage: "release", title: "组件版本固定", status: fixedComponents ? "pass" : "blocked", reason: fixedComponents ? "" : "release requires fixed component versions" },
    ],
  };
}

function withReleaseSummary(build) {
  const check = releaseCheckFor(build);
  const firstBlocked = check.checks.find((item) => item.status === "blocked");
  return {
    ...build,
    releaseCheckSummary: {
      buildId: build.id,
      stage: check.stage,
      previewReady: check.previewReady,
      releaseReady: check.releaseReady,
      blockingCount: check.blockingCount,
      firstBlockedId: firstBlocked?.id || "",
      firstBlockedStage: firstBlocked?.stage || "",
      firstBlockedTitle: firstBlocked?.title || "",
      firstBlockedReason: firstBlocked?.reason || "",
    },
  };
}

async function mockApi(page) {
  const state = {
    builds: [...builds],
    createPayloads: [],
    authHeaders: [],
    overlays: {
      profiles: [{ name: "qa", kind: "profiles", data: { config: { ports: { web: 48001 } } } }],
      customers: [{ name: "city-bank", kind: "customers", data: { config: { customer: { name: "City Bank" } } } }],
    },
  };
  await page.route("http://localhost:8080/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const method = request.method();
    state.authHeaders.push({ method, path, authorization: request.headers().authorization || "" });
    const json = (payload, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(payload) });

    if (method === "GET" && path === "/api/products") return json([product]);
    if (method === "GET" && path === "/api/builds") return json(state.builds.map(withReleaseSummary));
    if (method === "GET" && path === "/api/uploads") return json(uploads);
    if (method === "GET" && path === "/api/runners") return json(runners);
    if (method === "GET" && path === "/api/notifications") return json([]);
    if (method === "GET" && path === "/api/audit-events") return json(auditEvents);
    if (method === "GET" && path === "/api/audit-events/archive") {
      return json({
        schemaVersion: "packaging.audit.archive/v1",
        generatedAt: "2026-05-28T07:00:00Z",
        archiveUri: "minio://packaging-audit/audit-archives/sha256/012345.json",
        archiveStoredAt: "2026-05-28T07:00:00Z",
        archiveRetentionMode: "COMPLIANCE",
        archiveRetainUntil: "2036-05-28T07:00:00Z",
        archiveLegalHold: "ON",
        eventCount: auditEvents.length,
        filter: Object.fromEntries(url.searchParams.entries()),
        events: auditEvents,
        digestAlgorithm: "sha256",
        digest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        signatureAlgorithm: "hmac-sha256",
        signatureKeyId: "audit-key-2026",
        signature: "signature",
      });
    }
    if (method === "POST" && path === "/api/notifications/read-all") return json([]);
    if (method === "GET" && path === "/api/products/lsap/manifest") return json(product);
    if (method === "GET" && path === "/api/products/lsap/manifest/revisions") return json([]);
    if (method === "GET" && path === "/api/products/lsap/overlays/profiles") return json(state.overlays.profiles);
    if (method === "GET" && path === "/api/products/lsap/overlays/customers") return json(state.overlays.customers);
    const overlayMatch = path.match(/^\/api\/products\/lsap\/overlays\/(profiles|customers)\/([^/]+)$/);
    if (method === "GET" && overlayMatch) {
      const [, kind, name] = overlayMatch;
      const record = state.overlays[kind].find((item) => item.name === name);
      return record ? json(record) : json({ error: "not found" }, 404);
    }
    if (method === "POST" && path === "/api/builds") {
      const payload = JSON.parse(request.postData() || "{}");
      state.createPayloads.push(payload);
      const created = {
        ...state.builds[0],
        id: "build-created-e2e",
        status: "pending_approval",
        target: payload.target,
        version: payload.version,
        customer: payload.customer,
        finalConfig: payload.configOverrides,
        buildLock: { ...baseLock, buildId: "build-created-e2e", target: payload.target, version: payload.version, config: payload.configOverrides },
      };
      state.builds = [created, ...state.builds];
      return json(withReleaseSummary(created), 202);
    }
    const buildMatch = path.match(/^\/api\/builds\/([^/]+)(?:\/([^/]+))?$/);
    if (buildMatch) {
      const [, buildId, action] = buildMatch;
      const build = state.builds.find((item) => item.id === buildId);
      if (!build) return json({ error: "not found" }, 404);
      if (method === "GET" && !action) return json(withReleaseSummary(build));
      if (method === "GET" && action === "logs") {
        return json([{ time: "2026-05-28T06:10:00Z", source: "runner", message: `logs for ${buildId}` }]);
      }
      if (method === "GET" && action === "lock") return json(build.buildLock);
      if (method === "GET" && action === "release-check") return json(releaseCheckFor(build));
      if (method === "POST" && action === "validate-preview") {
        const updated = { ...build, status: "preview_validated", validatedAt: "2026-05-28T07:00:00Z" };
        state.builds = state.builds.map((item) => (item.id === build.id ? updated : item));
        return json(withReleaseSummary(updated));
      }
      if (method === "POST" && action === "publish") {
        const updated = { ...build, status: "published", releaseChannel: "release", publishedAt: "2026-05-28T07:05:00Z" };
        state.builds = state.builds.map((item) => (item.id === build.id ? updated : item));
        return json(withReleaseSummary(updated));
      }
    }
    return json({ error: `Unhandled ${method} ${path}` }, 500);
  });
  return state;
}

test("dashboard renders production-like release data and opens release gate details", async ({ page }) => {
  await mockApi(page);
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  await expect(page.getByText("待验证 Preview")).toBeVisible();
  await expect(page.getByText("可发布 Release")).toBeVisible();
  await expect(page.getByText("发布阻塞")).toBeVisible();

  await page.getByRole("button", { name: /构建记录/ }).click();
  await page.getByRole("button", { name: /筛选：可发布/ }).click();
  await expect(page.getByText("build-previe").filter({ visible: true })).toHaveCount(1);
  await page.getByPlaceholder("搜索 ID / 产品 / 状态").fill("preview-ready");
  await expect(page.getByText("build-previe").filter({ visible: true })).toBeVisible();
  await page.getByRole("button", { name: "详情" }).focus();
  await page.keyboard.press("Enter");
  await page.getByRole("tab", { name: /发布门禁/ }).click();

  await expect(page.getByText("Release 可发布")).toBeVisible();
  await expect(page.getByText("Preview 制品可下载")).toBeVisible();
  await expect(page.getByText("组件 URI 精确")).toBeVisible();
  await expect(page.getByText("0 阻塞")).toHaveCount(2);
});

test("build list exposes release gate blockers without opening details", async ({ page }) => {
  await mockApi(page);
  await page.goto("/");

  await page.getByRole("button", { name: /构建记录/ }).click();
  await page.getByRole("button", { name: /筛选：发布阻塞/ }).click();

  await expect(page.getByText("build-previe").filter({ visible: true })).toHaveCount(1);
  await expect(page.getByText("1 阻塞").filter({ visible: true })).toHaveCount(1);
  await expect(page.getByText("Release 需要固定版本，不能使用 latest").filter({ visible: true })).toHaveCount(1);
});

test("upload records stay usable with a large artifact history", async ({ page }) => {
  await mockApi(page);
  await page.goto("/");

  await page.getByRole("button", { name: /制品上传/ }).click();
  const uploadTableScroll = page.locator(".upload-table-scroll");
  const lastUploadFile = page.getByText("acceptance-package-26.tar.gz", { exact: true });
  await expect(uploadTableScroll).toBeVisible();
  await uploadTableScroll.scrollIntoViewIfNeeded();
  await expect(lastUploadFile).not.toBeInViewport();

  const scrollMetrics = await uploadTableScroll.evaluate((element) => ({
    clientHeight: element.clientHeight,
    scrollHeight: element.scrollHeight,
    tableTop: element.getBoundingClientRect().top,
    headerTop: element.querySelector("thead")?.getBoundingClientRect().top,
  }));
  expect(scrollMetrics.scrollHeight).toBeGreaterThan(scrollMetrics.clientHeight);
  expect(Math.abs((scrollMetrics.headerTop || 0) - scrollMetrics.tableTop)).toBeLessThan(2);

  await uploadTableScroll.evaluate((element) => {
    element.scrollTop = element.scrollHeight;
  });
  await expect(lastUploadFile).toBeInViewport();
  await expect(page.getByRole("columnheader", { name: "文件" })).toBeInViewport();
});

test("audit center lists events and shows archive evidence", async ({ page }) => {
  await mockApi(page);
  await page.goto("/");

  await page.getByRole("button", { name: /审计中心/ }).click();

  await expect(page.getByRole("heading", { name: "审计中心" }).first()).toBeVisible();
  await expect(page.getByText("build.download")).toBeVisible();
  await expect(page.getByText("audit.archive.export")).toBeVisible();
  await expect(page.getByText("corr-release")).toHaveCount(2);

  await page.getByRole("button", { name: "导出归档" }).click();

  await expect(page.getByText("最近归档证据")).toBeVisible();
  await expect(page.getByText("audit-key-2026")).toBeVisible();
  await expect(page.getByText("COMPLIANCE", { exact: true })).toBeVisible();
  await expect(page.getByText("ON", { exact: true })).toBeVisible();
});

test("create build form accepts richer component and customer data", async ({ page }) => {
  const state = await mockApi(page);
  await page.goto("/");

  await page.getByRole("button", { name: /创建构建/ }).first().click();
  await page.getByLabel("客户标识").fill("city-bank");
  await page.getByRole("button", { name: /下一步/ }).focus();
  await page.keyboard.press("Enter");
  await page.getByLabel("Web 端口").fill("49100");
  await page.getByLabel("客户名称").fill("城市银行");
  await page.getByRole("button", { name: /组件/ }).focus();
  await page.keyboard.press("Enter");
  await page.getByText("组件总数").first().waitFor();
  await page.getByText("已有上传").first().waitFor();
  await page.getByRole("button", { name: /确认/ }).focus();
  await page.keyboard.press("Enter");
  const submitButton = page.getByRole("button", { name: /提交构建申请/ });
  await submitButton.focus();
  await page.keyboard.press("Enter");

  await expect(page.getByText("构建请求已创建，等待审批")).toBeVisible();
  expect(state.createPayloads).toHaveLength(1);
  expect(state.createPayloads[0].customer).toBe("city-bank");
  expect(state.createPayloads[0].configOverrides.ports.web).toBe(49100);
  expect(state.createPayloads[0].configOverrides.customer.name).toBe("城市银行");
});

test("auth dialog saves bearer token and browser requests include authorization", async ({ page }) => {
  const state = await mockApi(page);
  await page.goto("/");

  await page.getByRole("button", { name: /免登录/ }).click({ force: true });
  await page.getByLabel("Bearer Token").fill("operator-token-e2e");
  await page.getByRole("button", { name: "保存" }).click({ force: true });

  await expect(page.getByText("访问令牌已保存")).toBeVisible();
  await expect(page.getByRole("button", { name: /操作员/ })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => window.localStorage.getItem("packaging.auth")))
    .toContain("operator-token-e2e");
  await expect
    .poll(() => state.authHeaders.filter((entry) => entry.authorization === "Bearer operator-token-e2e").length)
    .toBeGreaterThan(0);
});

test("OIDC redirect callback stores access token and derived role", async ({ page }) => {
  const state = await mockApi(page);
  const token = jwtWithClaims({ roles: ["operator"], sub: "alice" });
  await page.goto(`/#access_token=${token}&token_type=Bearer&state=oidc-state`);

  await expect(page.getByText("OIDC 登录已完成")).toBeVisible();
  await expect(page.getByRole("button", { name: /操作员/ })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => window.localStorage.getItem("packaging.auth")))
    .toContain(token);
  await expect
    .poll(() => state.authHeaders.filter((entry) => entry.authorization === `Bearer ${token}`).length)
    .toBeGreaterThan(0);
});

test("viewer auth state disables protected dashboard actions", async ({ page }) => {
  await mockApi(page);
  await page.addInitScript(() => {
    window.localStorage.setItem("packaging.auth", JSON.stringify({ token: "viewer-token-e2e", role: "viewer" }));
  });
  await page.goto("/");

  await expect(page.getByRole("button", { name: /只读/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "新建构建" })).toBeDisabled();
  await page.getByRole("button", { name: /构建记录/ }).click();
  await expect(page.getByRole("button", { name: "通过", exact: true }).first()).toBeDisabled();
  await expect(page.getByRole("button", { name: "发布", exact: true }).first()).toBeDisabled();
  await expect(page.getByRole("button", { name: "重试", exact: true }).first()).toBeDisabled();
  await expect(page.getByRole("button", { name: "详情", exact: true }).first()).toBeEnabled();
});

test.describe("desktop visual regression", () => {
  test("captures key admin pages", async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== "chromium", "desktop screenshot gate only");

    await mockApi(page);
    await page.goto("/");
    const main = page.locator(".admin-main");

    await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
    await expect(main).toHaveScreenshot("admin-overview.png", {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.01,
    });

    await page.getByRole("button", { name: /构建记录/ }).click();
    await expect(page.getByRole("heading", { name: "构建记录", level: 1 })).toBeVisible();
    await expect(main).toHaveScreenshot("admin-builds.png", {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.01,
    });

    await page.getByRole("button", { name: /创建构建/ }).click();
    await page.getByRole("button", { name: /步骤 3：组件/ }).click();
    await expect(page.getByRole("heading", { name: "组件来源" })).toBeVisible();
    await expect(main).toHaveScreenshot("admin-create-components.png", {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.01,
    });

    await page.getByRole("button", { name: /审计中心/ }).click();
    await expect(page.getByRole("heading", { name: "审计中心" }).first()).toBeVisible();
    await page.getByRole("button", { name: "导出归档" }).click();
    await expect(page.getByText("最近归档证据")).toBeVisible();
    await expect(main).toHaveScreenshot("admin-audit-center.png", {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.01,
    });
  });
});
