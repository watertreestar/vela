import { expect, test } from "@playwright/test";

const product = { id: "lsap", name: "智能定位平台", targets: ["linux", "windows"], targetConfig: { linux: { installDir: "/opt/lsap" }, windows: { installDir: "C:\\Program Files\\LSAP" } }, profiles: ["qa", "prod"], versions: ["latest", "1.0.0"], components: [{ id: "guard", required: true, source: { kind: "artifact-repository", repository: "minio", path: "guard/{{version}}/guard.tar.gz" } }], workloads: [{ id: "location-guard", component: "guard", module: "map", runtime: "docker", docker: { image: "registry.local/guard:{{version}}", ports: ["{{ports.web}}:8080"] } }], config: { ports: { web: 48000 }, customer: { name: "默认客户", license: "community" }, database: { passwordPolicy: "strong" }, modules: { map: true } } };
const lock = { productId: "lsap", config: product.config, components: [{ id: "guard", version: "1.0.0", uri: "minio://guard/1.0.0.tar.gz", sha256: "abc" }], workloads: product.workloads };
const builds = [
  { id: "build-pending", productId: "lsap", target: "linux", profile: "qa", version: "latest", customer: "acme", status: "pending_approval", finalConfig: product.config, buildLock: { ...lock, buildId: "build-pending" }, updatedAt: "2026-05-28T06:00:00Z" },
  { id: "build-success", productId: "lsap", target: "windows", profile: "prod", version: "1.0.0", customer: "bank", status: "succeeded", artifactUri: "minio://setup.exe", finalConfig: product.config, buildLock: { ...lock, buildId: "build-success" }, updatedAt: "2026-05-28T06:10:00Z" },
  { id: "build-gated", productId: "lsap", target: "linux", profile: "qa", version: "latest", customer: "trial", status: "preview_validated", artifactUri: "minio://latest.tar.gz", finalConfig: product.config, buildLock: { ...lock, buildId: "build-gated", components: [{ id: "guard", version: "latest", uri: "minio://guard/latest/*.tar.gz" }] }, updatedAt: "2026-05-28T06:20:00Z" },
];
function withRelease(build) { const ready = build.status === "preview_validated" && build.version !== "latest"; return { ...build, releaseCheckSummary: { releaseReady: ready, blockingCount: ready ? 0 : 1 } }; }
async function mockApi(page) {
  const state = { builds: structuredClone(builds), creates: [], requests: [] };
  await page.route("http://localhost:8080/api/**", async (route) => {
    const request = route.request(); const url = new URL(request.url()); const { pathname: path } = url; const method = request.method(); state.requests.push({ path, method });
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    if (method === "GET" && path === "/api/health") return json({ status: "ok" });
    if (method === "GET" && path === "/api/products") return json([product]);
    if (method === "GET" && path === "/api/builds") return json(state.builds.map(withRelease));
    if (method === "GET" && path === "/api/uploads") return json(Array.from({ length: 16 }, (_, i) => ({ id: `upload-${i}`, fileName: `artifact-${i}.tar.gz`, retentionStatus: i === 0 ? "expired" : "active", downloadPolicy: "platform" })));
    if (method === "POST" && path === "/api/uploads") return json({ id: "upload-component-1", uri: "artifact://uploads/upload-component-1" }, 201);
    if (method === "GET" && path === "/api/runners") return json([]);
    if (method === "GET" && path === "/api/notifications") return json([]);
    if (method === "GET" && path === "/api/audit-events") return json([{ id: "audit-1", time: "2026-05-28T06:00:00Z", action: "build.create", resourceType: "build", resourceId: "build-pending", actorId: "operator", status: "success", summary: "创建 Preview 构建", requestId: "req-1" }]);
    if (method === "GET" && path === "/api/products/lsap/manifest") return json(product);
    if (method === "GET" && /\/(manifest\/revisions|overlays\/profiles|overlays\/customers)$/.test(path)) return json([]);
    if (method === "POST" && path === "/api/builds") { const payload = JSON.parse(request.postData() || "{}"); state.creates.push(payload); const created = { ...state.builds[0], id: "build-created", customer: payload.customer, finalConfig: payload.configOverrides, buildLock: { ...lock, buildId: "build-created", config: payload.configOverrides } }; state.builds.unshift(created); return json(withRelease(created), 202); }
    const match = path.match(/^\/api\/builds\/([^/]+)\/(logs|lock|release-check|reproducibility-report)$/); if (match) { const [, id, part] = match; const build = state.builds.find((b) => b.id === id); if (part === "logs") return json([{ message: `logs for ${id}` }]); if (part === "lock") return json(build.buildLock); if (part === "release-check") return json({ releaseReady: withRelease(build).releaseCheckSummary.releaseReady, checks: [] }); return json({ reproducible: true }); }
    return json({ error: `Unhandled ${method} ${path}` }, 500);
  });
  return state;
}
async function nav(page, name, projectName) {
  if (projectName === "mobile-chrome") {
    await page.locator("button[aria-label*='导航']").first().click();
    await page.getByRole("dialog").getByRole("menuitem", { name: `导航：${name}` }).click();
    await expect(page.getByRole("dialog")).toBeHidden();
    return;
  }
  await page.getByRole("menuitem", { name: `导航：${name}` }).click();
}

test("总览和 AntD 菜单可进入构建记录并过滤、打开详情 Drawer", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "mobile-chrome", "mobile drawer flow has dedicated coverage");
  await mockApi(page); await page.goto("/");
  await expect(page.getByRole("heading", { name: "总览" })).toBeVisible();
  await nav(page, "构建记录", testInfo.project.name); await expect(page.getByRole("button", { name: /发布阻塞 1/ })).toBeVisible(); await page.waitForTimeout(250); await page.getByRole("button", { name: /发布阻塞 1/ }).click();
  await expect(page.getByRole("row", { name: /build-gated/ })).toBeVisible();
  await page.getByRole("button", { name: "build-gated" }).click();
  await expect(page.getByText("构建详情")).toBeVisible(); await expect(page.getByText("构建 ID")).toBeVisible();
});

test("创建向导提交 configOverrides 而非法端口不发请求", async ({ page }, testInfo) => {
  const state = await mockApi(page); await page.goto("/"); await nav(page, "创建构建", testInfo.project.name);
  await page.getByPlaceholder("可选，例如 city-bank").fill("city-bank");
  for (let index = 0; index < 3; index += 1) await page.getByRole("button", { name: "保存并继续" }).click();
  await page.getByRole("spinbutton").fill("49100");
  await page.getByRole("button", { name: "保存并继续" }).click();
  await page.getByRole("button", { name: "发起 Preview 构建" }).click();
  await expect(page.getByText("构建请求已创建，等待审批")).toBeVisible(); expect(state.creates[0]).toMatchObject({ customer: "city-bank", configOverrides: { ports: { web: 49100 } } });
});

test("组件可切换固定链接和上传来源并实时更新 BuildLock 蓝图", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "mobile-chrome", "desktop source composition coverage");
  await mockApi(page); await page.goto("/"); await nav(page, "创建构建", testInfo.project.name);
  await page.getByRole("button", { name: "保存并继续" }).click();
  await page.locator("label").filter({ hasText: "固定链接" }).click();
  await page.getByPlaceholder("https://downloads.example.com/component.tar.gz").fill("https://downloads.example.com/guard.zip");
  await expect(page.getByLabel("BuildLock 实时蓝图")).toContainText("1 个请求覆盖");
  await page.locator("label").filter({ hasText: "上传文件" }).click();
  await page.locator("input[type=file]").setInputFiles({ name: "guard.tar.gz", mimeType: "application/gzip", buffer: Buffer.from("guard") });
  await expect(page.getByText("文件已上传")).toBeVisible();
  await expect(page.getByText("已关联 upload-component-1")).toBeVisible();
});

test("高密度制品表保留过期下载禁用", async ({ page }, testInfo) => {
  await mockApi(page); await page.goto("/"); await nav(page, "制品上传", testInfo.project.name);
  await expect(page.getByRole("row", { name: /artifact-0.tar.gz/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /artifact-0.tar.gz/ }).getByRole("button", { name: /下\s*载/ })).toBeDisabled();
});

test("移动端通过导航 Drawer 使用 menuitem，详情 Drawer 适配视口", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile-chrome", "mobile-specific navigation");
  await mockApi(page); await page.goto("/"); await page.locator("button[aria-label*='导航']").first().click();
  await expect(page.getByRole("dialog")).toBeVisible(); await page.getByRole("dialog").getByRole("menuitem", { name: "导航：构建记录" }).click();
  await page.getByRole("button", { name: "build-success" }).click(); await expect(page.getByText("构建详情")).toBeVisible();
});

test("构建助手未配置时展示安全本地状态而不请求 Agent", async ({ page }) => {
  const state = await mockApi(page); await page.goto("/"); await expect(page.getByRole("heading", { name: "总览" })).toBeVisible(); await page.waitForLoadState("networkidle"); const count = state.requests.length;
  await page.getByRole("button", { name: "打开构建助手" }).click(); await expect(page.getByText(/Agent 服务未配置；不会发起 API 请求/)).toBeVisible(); expect(state.requests).toHaveLength(count);
});

test("审计和日志工作区可定位证据上下文", async ({ page }, testInfo) => {
  await mockApi(page); await page.goto("/");
  await nav(page, "审计", testInfo.project.name); await expect(page.getByRole("heading", { name: "定位审计事件" })).toBeVisible(); await expect(page.getByText("build.create")).toBeVisible();
  await nav(page, "日志与锁定", testInfo.project.name); await expect(page.getByRole("heading", { name: "build-pending" })).toBeVisible(); await expect(page.getByText("logs for build-pending")).toBeVisible();
  await page.getByRole("tab", { name: "构建锁" }).click(); await expect(page.getByLabel("构建锁 JSON")).toContainText("lsap");
});

test("总览待办指标直接进入对应构建队列", async ({ page }) => {
  await mockApi(page); await page.goto("/");
  await page.getByRole("button", { name: "需要处理：1，查看队列", exact: true }).click();
  await expect(page.getByRole("heading", { name: "构建记录", exact: true })).toBeVisible();
  await expect(page.getByRole("row", { name: /build-pending/ })).toBeVisible();
  await expect(page.getByRole("row", { name: /build-success/ })).toHaveCount(0);
});

test("窗口缩小后导航切换为移动抽屉且无页面横向溢出", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "chromium", "desktop resize coverage");
  await mockApi(page); await page.goto("/");
  await expect(page.getByRole("heading", { name: "总览", exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator("button[aria-label*='导航']").first().click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByRole("dialog").getByRole("menuitem", { name: "导航：创建构建" }).click();
  await expect(page.getByRole("heading", { name: "创建构建", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await expect(page.getByRole("button", { name: "保存并继续" })).toBeEnabled();
  for (let index = 0; index < 3; index += 1) {
    await page.getByRole("button", { name: "保存并继续" }).click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
  await expect(page.getByText("Web 端口", { exact: true })).toBeVisible();
});

test("移动端所有工作区保持在视口内", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile-chrome", "mobile responsive coverage");
  await mockApi(page); await page.goto("/");
  for (const name of ["总览", "创建构建", "构建记录", "Runner", "制品上传", "产品配置", "覆盖配置", "审计", "日志与锁定"]) {
    if (name !== "总览") await nav(page, name, testInfo.project.name);
    await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  }
});
