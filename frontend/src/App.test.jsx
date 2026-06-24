import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App, { apiEnvironmentLabel } from "./App";
import { TooltipProvider } from "./components/admin/primitives";

const product = {
  id: "lsap",
  name: "智能定位平台",
  description: "Linux and Windows installer packaging definition for LSAP.",
  targets: ["linux", "windows"],
  targetConfig: {
    linux: { installDir: "/opt/lsap", packageName: "lsap-linux-x86_64" },
    windows: { installDir: "C:\\Program Files\\LSAP", packageName: "lsap-windows-x64-setup" },
  },
  profiles: ["qa", "prod"],
  versions: ["latest", "1.0.0"],
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
      id: "license",
      type: "license",
      required: false,
      source: {
        kind: "manual-upload",
        accept: [".lic"],
      },
    },
  ],
  workloads: [
    {
      id: "location-guard",
      runtime: "docker",
      component: "location-guard",
      module: "map",
    },
  ],
  config: {
    ports: { web: 48000 },
    database: { passwordPolicy: "strong" },
    customer: { name: "Default Customer", license: "community" },
    modules: { camera: true, push: true, map: true },
  },
};

const secondaryProduct = {
  ...product,
  id: "edge",
  name: "边缘计算平台",
  description: "Edge packaging definition for product switching validation.",
  targetConfig: {
    linux: { installDir: "/opt/edge", packageName: "edge-linux-x86_64" },
    windows: { installDir: "C:\\Program Files\\Edge", packageName: "edge-windows-x64-setup" },
  },
  config: {
    ...product.config,
    ports: { web: 49000 },
    customer: { name: "Edge Customer", license: "enterprise" },
  },
};

const baseBuildLock = {
  buildId: "build-pending-001",
  productId: "lsap",
  target: "linux",
  profile: "qa",
  customer: "example-customer",
  version: "latest",
  config: {
    installDir: "/opt/lsap",
    ports: { web: 48000 },
    customer: { name: "Default Customer", license: "community" },
  },
  components: [
    {
      id: "location-guard",
      type: "docker-image",
      version: "latest",
      source: { kind: "artifact-repository" },
      uri: "minio://packaging/location-guard.tar.gz",
      sha256: "0123456789abcdef0123456789abcdef",
      required: true,
    },
  ],
  workloads: [
    {
      id: "location-guard",
      runtime: "docker",
      component: "location-guard",
      module: "map",
      enabled: true,
    },
  ],
};

const releaseReadyBuildLock = {
  ...baseBuildLock,
  buildId: "build-success-002",
  target: "windows",
  profile: "prod",
  version: "1.0.0",
  releaseChannel: "preview",
  components: baseBuildLock.components.map((component) => ({
    ...component,
    version: "1.0.0",
    uri: component.uri.replace("latest", "1.0.0"),
  })),
};

const builds = [
  {
    id: "build-pending-001",
    productId: "lsap",
    target: "linux",
    profile: "qa",
    customer: "example-customer",
    version: "latest",
    releaseChannel: "qa",
    status: "pending_approval",
    variables: {},
    finalConfig: baseBuildLock.config,
    components: baseBuildLock.components,
    workloads: baseBuildLock.workloads,
    buildLock: baseBuildLock,
    createdAt: "2026-05-28T06:00:00Z",
    updatedAt: "2026-05-28T06:10:00Z",
    logs: [],
  },
  {
    id: "build-success-002",
    productId: "lsap",
    target: "windows",
    profile: "prod",
    version: "1.0.0",
    releaseChannel: "preview",
    status: "succeeded",
    artifactUri: "minio://packaging/lsap-windows.exe",
    variables: {},
    finalConfig: baseBuildLock.config,
    components: releaseReadyBuildLock.components,
    workloads: baseBuildLock.workloads,
    buildLock: releaseReadyBuildLock,
    createdAt: "2026-05-28T05:00:00Z",
    updatedAt: "2026-05-28T05:30:00Z",
    logs: [],
  },
];

const uploads = [
  {
    id: "upload-license",
    fileName: "customer.lic",
    uri: "local-upload://upload-license/customer.lic",
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
    id: "upload-expired",
    fileName: "expired-preview.tar.gz",
    uri: "local-upload://upload-expired/expired-preview.tar.gz",
    sha256: "11112222333344445555666677778888",
    size: 4096,
    contentType: "application/gzip",
    downloadPolicy: "restricted",
    retentionDays: 7,
    retentionStatus: "expired",
    metadata: { productId: "lsap", purpose: "old-preview" },
    createdAt: "2026-05-20T06:05:00Z",
  },
];

const runners = [
  {
    runnerId: "linux-runner-01",
    target: "linux",
    status: "busy",
    currentBuildId: "build-pending-001",
    currentBuildState: "running",
    lastHeartbeatAt: "2026-05-28T06:12:00Z",
    lastLogAt: "2026-05-28T06:12:00Z",
    runningBuilds: 1,
    completedBuilds: 2,
    failedBuilds: 1,
    recentMessages: [
      {
        sequence: 2,
        time: "2026-05-28T06:12:00Z",
        source: "linux-runner-01",
        message: "\u001b[32mheartbeat: active\u001b[0m",
      },
    ],
  },
];

function releaseCheckFor(build) {
  const fixedVersion = build.version && build.version !== "latest";
  const previewReady = build.status === "succeeded" || build.status === "preview_validated" || build.status === "published";
  const releaseReady = build.status === "preview_validated" && fixedVersion;
  return {
    buildId: build.id,
    stage: build.status === "preview_validated" ? "release" : "preview",
    previewReady,
    releaseReady,
    blockingCount: releaseReady ? 0 : 1,
    checks: [
      {
        id: "preview.artifact",
        stage: "preview",
        title: "Preview 制品可下载",
        status: build.artifactUri || build.downloadUrl ? "pass" : "blocked",
        reason: build.artifactUri || build.downloadUrl ? "" : "preview artifact is not available",
      },
      {
        id: "release.fixed-version",
        stage: "release",
        title: "构建版本固定",
        status: fixedVersion ? "pass" : "blocked",
        reason: fixedVersion ? "" : "release requires a fixed build version",
      },
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

function jsonResponse(payload, status = 200) {
  return Promise.resolve({
    ok: status >= 200 && status < 300,
    status,
    json: () => Promise.resolve(payload),
  });
}

function setupFetch() {
  const state = {
    products: [product],
    builds: [...builds],
    manifests: {
      lsap: product,
      edge: secondaryProduct,
    },
    overlays: {
      profiles: [
        {
          name: "qa",
          kind: "profiles",
          data: { config: { ports: { web: 48001 } } },
        },
      ],
      customers: [
        {
          name: "example-customer",
          kind: "customers",
          data: { config: { customer: { name: "Example Customer" } } },
        },
      ],
    },
    createPayloads: [],
    revisions: [
      {
        id: "20260528170000-aabbccddee",
        productId: "lsap",
        hash: "aabbccddeeff001122334455",
        summary: "manifest saved",
        createdAt: "2026-05-28T07:00:00Z",
      },
    ],
  };

  global.fetch = vi.fn((url, options = {}) => {
    const parsed = new URL(String(url));
    const path = parsed.pathname;
    const method = options.method || "GET";

    if (method === "GET" && path === "/api/products") {
      return jsonResponse(state.products);
    }
    if (method === "GET" && path === "/api/builds") {
      return jsonResponse(state.builds.map(withReleaseSummary));
    }
    if (method === "GET" && path === "/api/uploads") {
      return jsonResponse(uploads);
    }
    if (method === "GET" && path === "/api/runners") {
      return jsonResponse(runners);
    }
    if (method === "GET" && path === "/api/products/lsap/manifest") {
      return jsonResponse(state.manifests.lsap);
    }
    if (method === "GET" && path === "/api/products/edge/manifest") {
      return jsonResponse(state.manifests.edge);
    }
    if (method === "GET" && path === "/api/products/edge/manifest/revisions") {
      return jsonResponse([]);
    }
    if (method === "GET" && path === "/api/products/edge/overlays/profiles") {
      return jsonResponse([]);
    }
    if (method === "GET" && path === "/api/products/edge/overlays/customers") {
      return jsonResponse([]);
    }
    if (method === "POST" && path === "/api/products/lsap/manifest/dry-run") {
      const payload = JSON.parse(options.body);
      return jsonResponse({
        valid: true,
        errors: [],
        config: { installDir: "/opt/lsap", ports: { web: 48000 } },
        components: releaseReadyBuildLock.components,
        buildLock: {
          ...releaseReadyBuildLock,
          buildId: "dry-run",
          target: payload.target,
          profile: payload.profile,
          version: payload.version,
        },
      });
    }
    if (method === "GET" && path === "/api/products/lsap/manifest/revisions") {
      return jsonResponse(state.revisions);
    }
    if (method === "POST" && path === "/api/products/lsap/manifest/revisions/20260528170000-aabbccddee/restore") {
      return jsonResponse(product);
    }
    if (method === "GET" && path === "/api/products/lsap/overlays/profiles") {
      return jsonResponse(state.overlays.profiles);
    }
    if (method === "GET" && path === "/api/products/lsap/overlays/customers") {
      return jsonResponse(state.overlays.customers);
    }
    const overlayMatch = path.match(/^\/api\/products\/lsap\/overlays\/(profiles|customers)\/([^/]+)$/);
    if (method === "GET" && overlayMatch) {
      const [, kind, name] = overlayMatch;
      const record = state.overlays[kind].find((item) => item.name === name);
      return record ? jsonResponse(record) : jsonResponse({ error: "not found" }, 404);
    }
    if (method === "PUT" && path.startsWith("/api/products/lsap/overlays/")) {
      const [, , , , , kind, name] = path.split("/");
      const record = { name, kind, data: JSON.parse(options.body) };
      state.overlays[kind] = [
        record,
        ...(state.overlays[kind] || []).filter((item) => item.name !== name),
      ];
      return jsonResponse(record);
    }
    if (method === "PUT" && path === "/api/products/lsap/manifest") {
      state.revisions = [
        {
          id: "20260528171000-bbccddeeff",
          productId: "lsap",
          hash: "bbccddeeff00112233445566",
          summary: "manifest saved",
          createdAt: "2026-05-28T07:10:00Z",
        },
        ...state.revisions,
      ];
      return jsonResponse(product);
    }
    if (method === "GET" && path === "/api/builds/build-pending-001/logs") {
      return jsonResponse([
        {
          time: "2026-05-28T06:00:00Z",
          source: "system",
          message: "\u001b[95mbuild request created\u001b[0m and waiting for approval",
        },
      ]);
    }
    if (method === "GET" && path === "/api/builds/build-pending-001/lock") {
      return jsonResponse(baseBuildLock);
    }
    if (method === "GET" && path === "/api/builds/build-pending-001/release-check") {
      return jsonResponse(releaseCheckFor(state.builds.find((build) => build.id === "build-pending-001")));
    }
    if (method === "GET" && path === "/api/builds/build-success-002/logs") {
      return jsonResponse([
        {
          time: "2026-05-28T05:30:00Z",
          source: "runner",
          message: "build completed successfully",
        },
      ]);
    }
    if (method === "GET" && path === "/api/builds/build-success-002/lock") {
      return jsonResponse(releaseReadyBuildLock);
    }
    if (method === "GET" && path === "/api/builds/build-success-002/release-check") {
      return jsonResponse(releaseCheckFor(state.builds.find((build) => build.id === "build-success-002")));
    }
    if (method === "POST" && path === "/api/builds") {
      const payload = JSON.parse(options.body);
      state.createPayloads.push(payload);
      const created = {
        ...builds[0],
        id: "build-created-003",
        customer: payload.customer,
        target: payload.target,
        profile: payload.profile,
        version: payload.version,
        releaseChannel: payload.releaseChannel,
        finalConfig: payload.configOverrides,
        buildLock: { ...baseBuildLock, buildId: "build-created-003", config: payload.configOverrides },
      };
      state.builds = [created, ...state.builds];
      return jsonResponse(withReleaseSummary(created), 202);
    }
    if (method === "GET" && path === "/api/builds/build-created-003/logs") {
      return jsonResponse([
        {
          time: "2026-05-28T06:30:00Z",
          source: "system",
          message: "build request created and waiting for approval",
        },
      ]);
    }
    if (method === "GET" && path === "/api/builds/build-created-003/lock") {
      return jsonResponse({ ...baseBuildLock, buildId: "build-created-003" });
    }
    if (method === "GET" && path === "/api/builds/build-created-003/release-check") {
      return jsonResponse(releaseCheckFor(state.builds.find((build) => build.id === "build-created-003")));
    }
    if (method === "POST" && path === "/api/builds/build-pending-001/approve") {
      const approved = { ...state.builds.find((build) => build.id === "build-pending-001"), status: "queued" };
      state.builds = state.builds.map((build) => (build.id === approved.id ? approved : build));
      return jsonResponse(withReleaseSummary(approved));
    }
    if (method === "POST" && path === "/api/builds/build-pending-001/cancel") {
      const canceled = { ...state.builds.find((build) => build.id === "build-pending-001"), status: "canceled" };
      state.builds = state.builds.map((build) => (build.id === canceled.id ? canceled : build));
      return jsonResponse(withReleaseSummary(canceled));
    }
    if (method === "POST" && path === "/api/builds/build-success-002/validate-preview") {
      const validated = {
        ...state.builds.find((build) => build.id === "build-success-002"),
        status: "preview_validated",
        releaseChannel: "preview",
        validatedAt: "2026-05-28T06:40:00Z",
      };
      state.builds = state.builds.map((build) => (build.id === validated.id ? validated : build));
      return jsonResponse(withReleaseSummary(validated));
    }
    if (method === "POST" && path === "/api/builds/build-success-002/publish") {
      const published = {
        ...state.builds.find((build) => build.id === "build-success-002"),
        status: "published",
        releaseChannel: "release",
        publishedAt: "2026-05-28T06:45:00Z",
      };
      state.builds = state.builds.map((build) => (build.id === published.id ? published : build));
      return jsonResponse(withReleaseSummary(published));
    }

    return jsonResponse({ error: `Unhandled ${method} ${path}` }, 500);
  });

  return state;
}

function renderApp() {
  return render(
    <TooltipProvider>
      <App />
    </TooltipProvider>,
  );
}

beforeEach(() => {
  window.localStorage.clear();
  setupFetch();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("Packaging dashboard", () => {
  it("renders dashboard metrics from mocked API data", async () => {
    renderApp();

    expect(await screen.findByRole("heading", { name: "总览" })).toBeInTheDocument();
    expect(screen.getByText("产品定义")).toBeInTheDocument();
    expect(screen.getByText("活跃任务")).toBeInTheDocument();
    expect(screen.getByText("需要处理")).toBeInTheDocument();
    expect(screen.getByText("待验证 Preview")).toBeInTheDocument();
    expect(screen.getByText("可发布 Release")).toBeInTheDocument();
    expect(screen.getByText("发布阻塞")).toBeInTheDocument();
    expect(screen.getByText("build-pendin")).toHaveAttribute("title", expect.stringContaining("build-pending-001"));
    expect(screen.getByText(/build-succes/)).toHaveAttribute("title", expect.stringContaining("build-success-002"));
    expect(screen.getAllByText("待审批").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Preview 已生成").length).toBeGreaterThan(0);
  });

  it("only shows refresh success feedback for manual refresh", async () => {
    const user = userEvent.setup();
    renderApp();

    expect(await screen.findByRole("heading", { name: "总览" })).toBeInTheDocument();
    expect(screen.queryByText("数据已刷新")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /^刷新$/ }));

    expect(await screen.findByText("数据已刷新")).toBeInTheDocument();
  });

  it("supports global build search keyboard flow", async () => {
    const user = userEvent.setup();
    renderApp();

    expect(await screen.findByRole("heading", { name: "总览" })).toBeInTheDocument();
    const globalSearch = screen.getByLabelText("搜索构建、产品、Runner");

    fireEvent.keyDown(window, { key: "k", ctrlKey: true });
    expect(globalSearch).toHaveFocus();

    await user.type(globalSearch, "success{Enter}");

    expect(await screen.findByRole("heading", { level: 1, name: "构建记录" })).toBeInTheDocument();
    expect(globalSearch).toHaveValue("success");
    expect(screen.getAllByText("build-succes").length).toBeGreaterThan(0);
    expect(screen.queryByText("build-pendin")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "清空全局搜索" }));

    expect(globalSearch).toHaveValue("");
    expect(screen.getAllByText("build-pendin").length).toBeGreaterThan(0);
  });

  it("collapses and expands the desktop sidebar from the toolbar", async () => {
    const user = userEvent.setup();
    const { container } = renderApp();

    expect(await screen.findByRole("heading", { name: "总览" })).toBeInTheDocument();
    const shell = container.querySelector(".admin-shell");

    expect(shell).not.toHaveClass("is-sidebar-collapsed");
    await user.click(screen.getByRole("button", { name: "折叠导航" }));

    expect(shell).toHaveClass("is-sidebar-collapsed");
    expect(window.localStorage.getItem("packaging.sidebarCollapsed")).toBe("true");
    expect(screen.getByRole("button", { name: "展开导航" })).toHaveAttribute("aria-expanded", "false");
    expect(screen.getByRole("button", { name: "构建记录" })).toHaveAttribute("title", "构建记录");
    await user.click(screen.getByRole("button", { name: "构建记录" }));
    expect(await screen.findByRole("heading", { level: 1, name: "构建记录" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "展开导航" }));

    expect(shell).not.toHaveClass("is-sidebar-collapsed");
    expect(window.localStorage.getItem("packaging.sidebarCollapsed")).toBe("false");
    expect(screen.getByRole("button", { name: "折叠导航" })).toHaveAttribute("aria-expanded", "true");
  });

  it("keeps the toolbar free of placeholder action buttons", async () => {
    renderApp();

    expect(await screen.findByRole("heading", { name: "总览" })).toBeInTheDocument();
    const environment = screen.getByLabelText("当前环境，本地环境，API http://localhost:8080");
    expect(environment).toHaveTextContent("本地环境");
    expect(environment).toHaveTextContent("localhost:8080");
    expect(environment).toHaveAttribute("title", "http://localhost:8080");
    expect(screen.getByText("http://localhost:8080")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "本地环境" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "日期范围" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "通知" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "主题" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "当前用户" })).not.toBeInTheDocument();
  });

  it("derives environment labels from API URLs", () => {
    expect(apiEnvironmentLabel("http://localhost:8080")).toBe("本地环境");
    expect(apiEnvironmentLabel("http://127.0.0.1:8080")).toBe("本地环境");
    expect(apiEnvironmentLabel("https://packaging.example.com")).toBe("远程环境");
    expect(apiEnvironmentLabel("/api")).toBe("同源环境");
    expect(apiEnvironmentLabel("not a url")).toBe("自定义环境");
  });

  it("opens build detail and displays logs plus build lock data", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: /详情/ })[0]);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("构建详情")).toBeInTheDocument();
    expect(screen.getAllByText("build-pending-001").length).toBeGreaterThan(0);
    expect(screen.getAllByText("构建 ID").some((element) => element.closest(".detail-item")?.getAttribute("title") === "build-pending-001")).toBe(true);

    await user.click(screen.getByRole("tab", { name: /发布门禁/ }));
    expect(screen.getByText("构建版本固定").closest(".release-check-row")).toHaveAttribute(
      "title",
      "构建版本固定（release.fixed-version）：阻塞，Release 需要选择固定版本，不能使用 latest",
    );
    expect(screen.getByText("Release 需要选择固定版本，不能使用 latest")).toHaveAttribute(
      "title",
      "Release 需要选择固定版本，不能使用 latest",
    );

    await user.click(screen.getByRole("tab", { name: /日志/ }));
    expect(screen.getByText("build request created and waiting for approval")).toBeInTheDocument();
    expect(screen.getByText("build request created and waiting for approval")).toHaveAttribute(
      "title",
      "build request created and waiting for approval",
    );
    expect(screen.getByRole("button", { name: "复制" })).toHaveAttribute("title", "复制当前筛选后的日志");
    expect(screen.getByRole("dialog")).not.toHaveTextContent(/\u001b\[/);

    await user.click(screen.getByRole("tab", { name: /构建锁/ }));
    expect(screen.getByText(/minio:\/\/packaging\/location-guard.tar.gz/)).toBeInTheDocument();
    expect(screen.getByLabelText("JSON 内容")).toHaveAttribute("title", expect.stringContaining("build-pending-001"));
  });

  it("loads backend release checks in the build detail gate tab", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: /详情/ })[1]);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: /发布门禁/ }));

    expect(await screen.findByText("Preview 制品可下载")).toBeInTheDocument();
    expect(screen.getByText("构建版本固定")).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledWith(
      "http://localhost:8080/api/builds/build-success-002/release-check",
      expect.objectContaining({ headers: expect.objectContaining({ "Content-Type": "application/json" }) }),
    );

    await user.click(screen.getByRole("tab", { name: /组件锁定/ }));
    expect(screen.getByText("minio://packaging/location-guard.tar.gz")).toHaveAttribute(
      "title",
      "minio://packaging/location-guard.tar.gz",
    );
    expect(screen.getByText("0123456789abcdef01...")).toHaveAttribute(
      "title",
      "0123456789abcdef0123456789abcdef",
    );
  });

  it("submits a build request with structured config overrides", async () => {
    const state = setupFetch();
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);

    await user.clear(screen.getByLabelText("客户标识"));
    await user.type(screen.getByLabelText("客户标识"), "qa-customer");
    await user.click(screen.getByRole("button", { name: /下一步/ }));
    await user.clear(screen.getByLabelText("Web 端口"));
    await user.type(screen.getByLabelText("Web 端口"), "49000");
    await user.clear(screen.getByLabelText("客户名称"));
    await user.type(screen.getByLabelText("客户名称"), "测试客户");
    await user.click(screen.getByRole("button", { name: /高级 JSON/ }));
    const variablesEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value === "{}");
    fireEvent.change(variablesEditor, {
      target: { value: '{"ports":{"web":49999},"releaseNote":"nightly smoke"}' },
    });
    await user.click(screen.getByRole("button", { name: "关闭" }));
    await user.click(screen.getByRole("button", { name: /下一步/ }));
    await user.click(screen.getByRole("button", { name: /下一步/ }));

    await user.click(screen.getByRole("button", { name: /提交构建申请/ }));

    await waitFor(() => expect(state.createPayloads).toHaveLength(1));
    expect(state.createPayloads[0]).toMatchObject({
      productId: "lsap",
      target: "linux",
      profile: "qa",
      customer: "qa-customer",
      releaseChannel: "preview",
      configOverrides: {
        installDir: "/opt/lsap",
        ports: { web: 49000 },
        customer: {
          name: "测试客户",
          license: "community",
        },
      },
      variables: {
        ports: { web: 49999 },
        releaseNote: "nightly smoke",
      },
    });
    expect(state.createPayloads[0].configOverrides.ports.web).toBe(49000);
    expect(await screen.findByText("构建请求已创建，等待审批")).toBeInTheDocument();
  });

  it("blocks build submission when advanced JSON is invalid", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);
    await user.click(screen.getByRole("button", { name: /高级 JSON/ }));

    fireEvent.change(screen.getByLabelText("构建变量 JSON"), { target: { value: "{" } });

    expect(screen.getAllByText(/构建变量 JSON 解析错误/).length).toBeGreaterThanOrEqual(2);
    expect(screen.getByText(/高级 JSON 无效/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "关闭" }));
    await user.click(screen.getByRole("button", { name: /步骤 4：确认/ }));

    const submitButton = screen.getByRole("button", { name: /提交构建申请/ });
    expect(submitButton).toBeDisabled();
    expect(submitButton).toHaveAttribute("title", "请先修复高级 JSON 后再提交构建申请");
    fireEvent.submit(submitButton.closest("form"));
    expect(fetch).not.toHaveBeenCalledWith(
      "http://localhost:8080/api/builds",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("does not upload component files when component override JSON is invalid", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);
    await user.click(screen.getByRole("button", { name: /高级 JSON/ }));
    await user.click(screen.getByRole("tab", { name: "组件覆盖" }));

    fireEvent.change(screen.getByLabelText("组件覆盖 JSON"), { target: { value: "[]" } });

    expect(screen.getAllByText(/组件覆盖 必须是 JSON 对象/).length).toBeGreaterThanOrEqual(2);
    await user.click(screen.getByRole("button", { name: "关闭" }));
    await user.click(screen.getByRole("button", { name: /步骤 3：组件/ }));

    fireEvent.change(screen.getByLabelText("上传 license 组件文件"), {
      target: {
        files: [new File(["license"], "customer.lic", { type: "application/octet-stream" })],
      },
    });

    await waitFor(() => {
      expect(fetch).not.toHaveBeenCalledWith(
        "http://localhost:8080/api/uploads",
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("marks the current create-build step and supports direct step navigation", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);

    const targetStep = screen.getByRole("button", { name: /步骤 1：目标，当前步骤/ });
    expect(targetStep).toHaveAttribute("aria-current", "step");

    await user.click(screen.getByRole("button", { name: /步骤 2：配置/ }));

    const configStep = screen.getByRole("button", { name: /步骤 2：配置，当前步骤/ });
    expect(configStep).toHaveAttribute("aria-current", "step");
    expect(screen.getByLabelText("Web 端口")).toBeInTheDocument();
    expect(targetStep).not.toHaveAttribute("aria-current");
  });

  it("saves product manifest through the manifest editor", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await screen.findByText("Manifest 编辑器");
    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value.includes('"id": "lsap"'));
    fireEvent.change(manifestEditor, { target: { value: JSON.stringify(product) } });
    expect(screen.getByTitle("Manifest 有未保存修改")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存 Manifest" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/manifest",
        expect.objectContaining({ method: "PUT" }),
      );
    });
    await waitFor(() => {
      expect(manifestEditor).toHaveValue(JSON.stringify(product, null, 2));
    });
    expect(screen.queryByTitle("Manifest 有未保存修改")).not.toBeInTheDocument();
    expect(await screen.findByText("Manifest 已保存")).toBeInTheDocument();
  });

  it("requires confirmation before reloading a dirty manifest", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    const currentProduct = await screen.findByRole("button", { name: "当前产品 智能定位平台 lsap" });
    expect(currentProduct).toHaveAttribute("aria-current", "true");
    expect(currentProduct).toHaveAttribute("title", "当前产品：智能定位平台");

    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value.includes('"id": "lsap"'));
    const editedManifest = JSON.stringify({ ...product, name: "未保存产品名" }, null, 2);
    const manifestGetCalls = () =>
      fetch.mock.calls.filter(([url, options = {}]) => {
        const parsed = new URL(String(url));
        return (options.method || "GET") === "GET" && parsed.pathname === "/api/products/lsap/manifest";
      }).length;
    const beforeReloadCount = manifestGetCalls();

    fireEvent.change(manifestEditor, { target: { value: editedManifest } });
    expect(screen.getByTitle("Manifest 有未保存修改")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "重新加载" }));

    expect(screen.getByRole("button", { name: "确认重载" })).toHaveAttribute(
      "title",
      "确认丢弃未保存修改并重新加载 Manifest",
    );
    expect(manifestEditor).toHaveValue(editedManifest);
    expect(manifestGetCalls()).toBe(beforeReloadCount);

    await user.click(screen.getByRole("button", { name: "确认重载" }));

    await waitFor(() => {
      expect(manifestGetCalls()).toBeGreaterThan(beforeReloadCount);
    });
    await waitFor(() => {
      expect(manifestEditor).toHaveValue(JSON.stringify(product, null, 2));
    });
    expect(screen.queryByTitle("Manifest 有未保存修改")).not.toBeInTheDocument();
  });

  it("requires confirmation before switching products with a dirty manifest", async () => {
    const state = setupFetch();
    state.products = [product, secondaryProduct];
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));

    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value.includes('"id": "lsap"'));
    const editedManifest = JSON.stringify({ ...product, name: "未保存产品名" }, null, 2);
    fireEvent.change(manifestEditor, { target: { value: editedManifest } });

    await user.click(screen.getByRole("button", { name: "选择产品 边缘计算平台 edge" }));

    expect(screen.getByText(/确认丢弃草稿后将切换到 边缘计算平台/)).toBeInTheDocument();
    expect(manifestEditor).toHaveValue(editedManifest);
    expect(screen.getByRole("button", { name: "当前产品 智能定位平台 lsap" })).toHaveAttribute("aria-current", "true");

    await user.click(screen.getByRole("button", { name: "继续编辑" }));
    expect(screen.queryByText(/确认丢弃草稿后将切换到 边缘计算平台/)).not.toBeInTheDocument();
    expect(manifestEditor).toHaveValue(editedManifest);

    await user.click(screen.getByRole("button", { name: "选择产品 边缘计算平台 edge" }));
    await user.click(screen.getByRole("button", { name: "确认切换" }));

    expect(await screen.findByRole("button", { name: "当前产品 边缘计算平台 edge" })).toHaveAttribute("aria-current", "true");
    expect(await screen.findByDisplayValue(/"id": "edge"/)).toBeInTheDocument();
    expect(screen.queryByTitle("Manifest 有未保存修改")).not.toBeInTheDocument();
  });

  it("renders structured API error details in the page alert", async () => {
    const originalFetch = global.fetch;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if ((options.method || "GET") === "PUT" && parsed.pathname === "/api/products/lsap/manifest") {
        return jsonResponse(
          {
            error: {
              code: "MANIFEST_SCHEMA_INVALID",
              message: "Manifest 校验失败",
              severity: "error",
              category: "validation",
              userAction: "请修复 Manifest 校验错误后再保存",
              details: [{ code: "manifest.targetConfig", message: "缺少 targetConfig.windows" }],
            },
          },
          400,
        );
      }
      return originalFetch(url, options);
    });

    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await screen.findByText("Manifest 编辑器");
    await user.click(screen.getByRole("button", { name: "保存 Manifest" }));

    expect(await screen.findByText("Manifest 校验失败")).toBeInTheDocument();
    expect(screen.getByText("缺少 targetConfig.windows")).toBeInTheDocument();
    expect(screen.getByText("请修复 Manifest 校验错误后再保存")).toBeInTheDocument();
  });

  it("shows a retryable API unavailable alert and reloads after retry", async () => {
    const originalFetch = global.fetch;
    let failedOnce = false;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if (!failedOnce && (options.method || "GET") === "GET" && parsed.pathname === "/api/products") {
        failedOnce = true;
        return Promise.reject(new TypeError("Failed to fetch"));
      }
      return originalFetch(url, options);
    });

    const user = userEvent.setup();
    renderApp();

    expect(await screen.findByText("无法连接后端 API")).toBeInTheDocument();
    expect(screen.getByText(/API 地址：http:\/\/localhost:8080/)).toBeInTheDocument();
    expect(screen.getByText("请确认后端服务、PostgreSQL 和对象存储依赖已启动后重试")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /重试连接/ }));

    expect((await screen.findAllByText("build-pendin")).length).toBeGreaterThan(0);
    expect(screen.queryByText("无法连接后端 API")).not.toBeInTheDocument();
  });

  it("keeps the dashboard usable when initial optional config data fails", async () => {
    const originalFetch = global.fetch;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if ((options.method || "GET") === "GET" && parsed.pathname === "/api/products/lsap/overlays/profiles/qa") {
        return jsonResponse({ error: "overlay detail failed" }, 500);
      }
      return originalFetch(url, options);
    });

    const { container } = renderApp();

    expect(await screen.findByRole("heading", { name: "总览" })).toBeInTheDocument();
    expect(screen.getByText("活跃任务")).toBeInTheDocument();
    expect(await screen.findByText("覆盖配置未加载：请求失败")).toBeInTheDocument();
    expect(container.querySelector(".message-bar")).toBeNull();
  });

  it("shows manifest validation errors before saving invalid JSON structure", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.value.includes('"id": "lsap"'));

    fireEvent.change(manifestEditor, { target: { value: '{"id":"broken"}' } });
    await user.click(screen.getByRole("tab", { name: /校验/ }));

    expect(screen.getByText(/缺少必填字段 name/)).toBeInTheDocument();
    expect(screen.getByText("请先修复 Manifest 校验错误，保存和试运行已暂停。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存 Manifest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "试运行" })).toBeDisabled();
    expect(fetch).not.toHaveBeenCalledWith(
      "http://localhost:8080/api/products/lsap/manifest",
      expect.objectContaining({ method: "PUT" }),
    );
  });

  it("shows JSON parse errors in the manifest validation tab", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.value.includes('"id": "lsap"'));

    fireEvent.change(manifestEditor, { target: { value: '{"id":"broken"' } });
    await user.click(screen.getByRole("tab", { name: /校验/ }));

    expect(screen.getByText(/JSON 解析错误/)).toBeInTheDocument();
    expect(screen.queryByText(/缺少必填字段 name/)).not.toBeInTheDocument();
    expect(screen.getByText("请先修复 Manifest 校验错误，保存和试运行已暂停。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存 Manifest" })).toBeDisabled();
  });

  it("blocks manifest saving when enabled targets are missing packageName", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.value.includes('"id": "lsap"'));
    const manifestWithoutPackageName = {
      ...product,
      targetConfig: {
        linux: { installDir: "/opt/lsap" },
        windows: { installDir: "C:\\Program Files\\LSAP" },
      },
    };

    fireEvent.change(manifestEditor, {
      target: { value: JSON.stringify(manifestWithoutPackageName, null, 2) },
    });

    await user.click(screen.getByRole("tab", { name: /校验/ }));
    expect(screen.getByText(/targetConfig\.linux\.packageName 缺失/)).toBeInTheDocument();
    expect(screen.getByText(/targetConfig\.windows\.packageName 缺失/)).toBeInTheDocument();
    expect(screen.getByText("请先修复 Manifest 校验错误，保存和试运行已暂停。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存 Manifest" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "试运行" })).toBeDisabled();

    await user.click(screen.getByRole("tab", { name: /功能检查/ }));
    expect(screen.getByText("安装包命名")).toBeInTheDocument();
    expect(screen.getByText(/linux, windows 未配置 packageName；保存 Manifest 和试运行前必须显式固定/)).toBeInTheDocument();
    expect(screen.getAllByText("需确认").length).toBeGreaterThan(0);
  });

  it("blocks manifest dry run but not manifest saving when advanced JSON is invalid", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);
    await user.click(screen.getByRole("button", { name: /高级 JSON/ }));
    fireEvent.change(screen.getByLabelText("构建变量 JSON"), { target: { value: "[]" } });
    await user.click(screen.getByRole("button", { name: "关闭" }));

    await user.click(screen.getByRole("button", { name: /产品配置/ }));

    expect(screen.getByText("请先修复高级 JSON，试运行已暂停。保存 Manifest 不受影响。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "试运行" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "保存 Manifest" })).toBeEnabled();
    expect(fetch).not.toHaveBeenCalledWith(
      "http://localhost:8080/api/products/lsap/manifest/dry-run",
      expect.objectContaining({ method: "POST" }),
    );
  });

  it("blocks manifest dry run rerun when advanced JSON becomes invalid", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await user.click(screen.getByRole("tab", { name: /试运行/ }));
    await user.click(screen.getByRole("button", { name: "试运行" }));

    expect(await screen.findByText("试运行通过，构建锁可生成。")).toBeInTheDocument();
    const dryRunCallsBeforeInvalidJson = fetch.mock.calls.filter(([url, options = {}]) => {
      const parsed = new URL(String(url));
      return (options.method || "GET") === "POST" && parsed.pathname === "/api/products/lsap/manifest/dry-run";
    }).length;

    await user.click(screen.getByRole("button", { name: /创建构建/ }));
    await user.click(screen.getByRole("button", { name: /高级 JSON/ }));
    fireEvent.change(screen.getByLabelText("构建变量 JSON"), { target: { value: "[]" } });
    await user.click(screen.getByRole("button", { name: "关闭" }));
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await user.click(screen.getByRole("tab", { name: /试运行/ }));

    const rerunButton = screen.getByRole("button", { name: "重新运行" });
    expect(rerunButton).toBeDisabled();
    expect(rerunButton).toHaveAttribute("title", "请先修复高级 JSON");
    expect(fetch.mock.calls.filter(([url, options = {}]) => {
      const parsed = new URL(String(url));
      return (options.method || "GET") === "POST" && parsed.pathname === "/api/products/lsap/manifest/dry-run";
    })).toHaveLength(dryRunCallsBeforeInvalidJson);
  });

  it("edits common manifest fields with the structured editor", async () => {
    const user = userEvent.setup();
    const { container } = renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await user.click(screen.getByRole("tab", { name: /结构化/ }));

    await user.clear(screen.getByLabelText("产品名称"));
    await user.type(screen.getByLabelText("产品名称"), "定位平台专业版");

    const componentCards = container.querySelectorAll(".component-card");
    const artifactComponent = componentCards[0];
    await user.clear(within(artifactComponent).getByLabelText("仓库名称"));
    await user.type(within(artifactComponent).getByLabelText("仓库名称"), "rustfs");
    await user.clear(within(artifactComponent).getByLabelText("仓库路径"));
    await user.type(within(artifactComponent).getByLabelText("仓库路径"), "artifact/stable/location-guard.tar.gz");

    const manualUploadComponent = componentCards[1];
    fireEvent.change(within(manualUploadComponent).getByLabelText("上传记录 ID"), { target: { value: "upload-license" } });
    fireEvent.change(within(manualUploadComponent).getByLabelText("上传 URI"), {
      target: { value: "local-upload://upload-license/customer.lic" },
    });
    fireEvent.change(within(manualUploadComponent).getByLabelText("允许后缀"), { target: { value: ".lic, .license" } });

    await user.click(screen.getByRole("tab", { name: /^编辑/ }));
    expect(screen.getByDisplayValue(/定位平台专业版/)).toBeInTheDocument();
    expect(screen.getByDisplayValue(/"repository": "rustfs"/)).toBeInTheDocument();
    expect(screen.getByDisplayValue(/"path": "artifact\/stable\/location-guard.tar.gz"/)).toBeInTheDocument();
    expect(screen.getByDisplayValue(/"uploadId": "upload-license"/)).toBeInTheDocument();
    expect(screen.getByDisplayValue(/"uri": "local-upload:\/\/upload-license\/customer.lic"/)).toBeInTheDocument();
    expect(screen.getByDisplayValue((value) => value.includes('".license"'))).toBeInTheDocument();
  });

  it("requires inline confirmation before removing structured manifest items", async () => {
    const user = userEvent.setup();
    const { container } = renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await user.click(screen.getByRole("tab", { name: /结构化/ }));

    let componentCards = container.querySelectorAll(".component-card");
    const firstComponent = componentCards[0];
    expect(within(firstComponent).getByDisplayValue("location-guard")).toBeInTheDocument();

    await user.click(within(firstComponent).getByRole("button", { name: "删除组件 location-guard" }));

    expect(within(firstComponent).getByRole("group", { name: "确认删除组件 location-guard" })).toBeInTheDocument();
    expect(within(firstComponent).getByDisplayValue("location-guard")).toBeInTheDocument();

    await user.click(within(firstComponent).getByRole("button", { name: "取消" }));
    expect(within(firstComponent).queryByRole("group", { name: "确认删除组件 location-guard" })).not.toBeInTheDocument();

    await user.click(within(firstComponent).getByRole("button", { name: "删除组件 location-guard" }));
    await user.click(within(firstComponent).getByRole("button", { name: "确认删除" }));

    componentCards = container.querySelectorAll(".component-card");
    expect(within(componentCards[0]).queryByDisplayValue("location-guard")).not.toBeInTheDocument();
    expect(within(componentCards[0]).getAllByDisplayValue("license").length).toBeGreaterThan(0);

    const firstWorkload = container.querySelectorAll(".workload-card")[0];
    await user.click(within(firstWorkload).getByRole("button", { name: "删除 Workload location-guard" }));

    expect(within(firstWorkload).getByRole("group", { name: "确认删除 Workload location-guard" })).toBeInTheDocument();
    expect(within(firstWorkload).getByDisplayValue("location-guard")).toBeInTheDocument();
  });

  it("runs manifest dry run and restores a manifest revision", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));

    await user.click(screen.getByRole("tab", { name: /试运行/ }));
    await user.click(screen.getByRole("button", { name: /试运行/ }));

    expect(await screen.findByText("试运行通过，构建锁可生成。")).toBeInTheDocument();
    expect(screen.getByText("构建锁预览")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /历史/ }));
    const revisionId = await screen.findByText("20260528170000-aabbccddee");
    expect(revisionId).toHaveAttribute("title", "20260528170000-aabbccddee");
    expect(screen.getByText("manifest saved")).toHaveAttribute("title", "manifest saved");
    expect(screen.getByRole("button", { name: "恢复" })).toHaveAttribute("title", "恢复 Manifest 历史 20260528170000-aabbccddee");
    await user.click(screen.getByRole("button", { name: "恢复" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/manifest/revisions/20260528170000-aabbccddee/restore",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByText("Manifest 已恢复到历史版本")).toBeInTheDocument();
  });

  it("requires confirmation before restoring a revision with a dirty manifest", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));

    const manifestEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value.includes('"id": "lsap"'));
    const editedManifest = JSON.stringify({ ...product, name: "未保存产品名" }, null, 2);
    fireEvent.change(manifestEditor, { target: { value: editedManifest } });

    await user.click(screen.getByRole("tab", { name: /历史/ }));
    await user.click(screen.getByRole("button", { name: "恢复" }));

    expect(screen.getByText(/确认丢弃草稿后将恢复历史版本 20260528170000-aabbccddee/)).toBeInTheDocument();
    expect(manifestEditor).toHaveValue(editedManifest);
    expect(fetch).not.toHaveBeenCalledWith(
      "http://localhost:8080/api/products/lsap/manifest/revisions/20260528170000-aabbccddee/restore",
      expect.objectContaining({ method: "POST" }),
    );

    await user.click(screen.getByRole("button", { name: "继续编辑" }));
    expect(screen.queryByText(/确认丢弃草稿后将恢复历史版本/)).not.toBeInTheDocument();
    expect(manifestEditor).toHaveValue(editedManifest);

    await user.click(screen.getByRole("button", { name: "恢复" }));
    await user.click(screen.getByRole("button", { name: "确认恢复" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/manifest/revisions/20260528170000-aabbccddee/restore",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByText("Manifest 已恢复到历史版本")).toBeInTheDocument();
    expect(screen.queryByTitle("Manifest 有未保存修改")).not.toBeInTheDocument();
  });

  it("requires confirmation before canceling a build", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: "取消" })[0]);

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("确认取消");
    expect(within(dialog).getByText("lsap")).toHaveAttribute("title", "lsap");
    expect(within(dialog).getByText("build-pending-001")).toHaveAttribute("title", "build-pending-001");
    expect(within(dialog).getByRole("button", { name: "返回" })).toHaveAttribute("title", "返回，不执行操作");
    expect(within(dialog).getByRole("button", { name: "确认取消" })).toHaveAttribute("title", "确认取消 build-pending-001");
    await user.click(within(dialog).getByRole("button", { name: "确认取消" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/builds/build-pending-001/cancel",
        expect.objectContaining({ method: "POST" }),
      );
    });
  });

  it("keeps confirm dialog busy while a build action is running", async () => {
    const originalFetch = global.fetch;
    let resolveCancel;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if ((options.method || "GET") === "POST" && parsed.pathname === "/api/builds/build-pending-001/cancel") {
        return new Promise((resolve) => {
          resolveCancel = () => resolve(originalFetch(url, options));
        });
      }
      return originalFetch(url, options);
    });

    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: "取消" })[0]);

    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "确认取消" }));

    expect(await within(dialog).findByRole("button", { name: "确认取消" })).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "确认取消" })).toHaveAttribute("title", "取消处理中");
    expect(within(dialog).getByRole("button", { name: "返回" })).toBeDisabled();

    await user.click(within(dialog).getByRole("button", { name: "确认取消" }));
    expect(
      fetch.mock.calls.filter(([url, options = {}]) => {
        const parsed = new URL(String(url));
        return (options.method || "GET") === "POST" && parsed.pathname === "/api/builds/build-pending-001/cancel";
      }),
    ).toHaveLength(1);

    resolveCancel();
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    expect((await screen.findAllByText("已取消")).length).toBeGreaterThan(0);
  });

  it("prevents duplicate direct build actions while one is running", async () => {
    const originalFetch = global.fetch;
    let resolveApprove;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if ((options.method || "GET") === "POST" && parsed.pathname === "/api/builds/build-pending-001/approve") {
        return new Promise((resolve) => {
          resolveApprove = () => resolve(originalFetch(url, options));
        });
      }
      return originalFetch(url, options);
    });

    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    fireEvent.click(screen.getByRole("button", { name: /构建记录/ }));

    const approveButton = screen.getAllByRole("button", { name: "通过" })[0];
    fireEvent.click(approveButton);
    fireEvent.click(approveButton);

    await waitFor(() => {
      expect(approveButton).toBeDisabled();
    });
    expect(approveButton).toHaveAttribute("title", "审批通过处理中");
    expect(
      fetch.mock.calls.filter(([url, options = {}]) => {
        const parsed = new URL(String(url));
        return (options.method || "GET") === "POST" && parsed.pathname === "/api/builds/build-pending-001/approve";
      }),
    ).toHaveLength(1);

    resolveApprove();
    expect(await screen.findByText("已审批通过")).toBeInTheDocument();
  });

  it("shows build state conflicts as toast feedback", async () => {
    const originalFetch = global.fetch;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if ((options.method || "GET") === "POST" && parsed.pathname === "/api/builds/build-pending-001/approve") {
        return jsonResponse(
          {
            error: {
              code: "BUILD_STATE_CONFLICT",
              message: "only pending or rejected builds can be approved",
              severity: "warning",
              category: "business",
            },
          },
          409,
        );
      }
      return originalFetch(url, options);
    });

    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: "通过" })[0]);

    expect(await screen.findByText("当前构建状态不允许执行该操作")).toBeInTheDocument();
  });

  it("requires preview validation before promoting a build to release", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));

    expect(screen.queryByRole("button", { name: "发布" })).not.toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "验证" })[0]);
    expect(await screen.findByText("已验证 Preview")).toBeInTheDocument();

    await user.click((await screen.findAllByRole("button", { name: "发布" }))[0]);
    expect(await screen.findByRole("dialog")).toHaveTextContent("确认转 Release");
    await user.click(screen.getByRole("button", { name: "确认转 Release" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/builds/build-success-002/publish",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByText("已转 Release")).toBeInTheDocument();
  });

  it("renders backend release gate failures when promotion is rejected", async () => {
    const originalFetch = global.fetch;
    global.fetch = vi.fn((url, options = {}) => {
      const parsed = new URL(String(url));
      if ((options.method || "GET") === "POST" && parsed.pathname === "/api/builds/build-success-002/publish") {
        return jsonResponse(
          {
            error: {
              code: "BUILD_RELEASE_GATE_BLOCKED",
              messageKey: "errors.build.releaseGateBlocked",
              message: "release gate blocked",
              severity: "warning",
              category: "business",
              userActionKey: "actions.fixReleaseGate",
              userAction: "fix release gate blockers before publishing",
              details: [
                {
                  code: "release.exact-component-uris",
                  stage: "release",
                  titleKey: "release.check.exactComponentUris.title",
                  messageKey: "release.check.exactComponentUris.reason",
                  message: "release requires exact component URIs without wildcards",
                },
              ],
            },
          },
          422,
        );
      }
      return originalFetch(url, options);
    });

    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: "验证" })[0]);
    await user.click((await screen.findAllByRole("button", { name: "发布" }))[0]);
    await user.click(screen.getByRole("button", { name: "确认转 Release" }));

    expect(await screen.findByText("Release 门禁未通过")).toBeInTheDocument();
    expect(screen.getByText(/组件 URI 精确：组件 URI 不能包含通配符/)).toBeInTheDocument();
    expect(screen.getByText("请先修复阻塞项后再发布")).toBeInTheDocument();
  });

  it("uses backend build download endpoint when only artifact URI is available", async () => {
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await screen.getByRole("button", { name: /构建记录/ }).click();

    expect(screen.getAllByText("build-succes").some((element) => element.getAttribute("title") === "build-success-002")).toBe(true);
    expect(screen.getAllByText("build-pendin").some((element) => element.getAttribute("title") === "build-pending-001")).toBe(true);
    expect(screen.getAllByText("等待构建成功").some((element) => element.parentElement?.getAttribute("title")?.includes("build-pending-001"))).toBe(true);
    expect(screen.getAllByText("待验证 Preview").some((element) => element.parentElement?.getAttribute("title")?.includes("build-success-002"))).toBe(true);
    expect(screen.getAllByRole("button", { name: "详情" }).some((element) => element.getAttribute("title") === "查看详情 build-success-002")).toBe(true);
    const downloadLink = screen.getByRole("link", { name: /下载/ });
    expect(downloadLink).toHaveAttribute(
      "href",
      "http://localhost:8080/api/builds/build-success-002/download",
    );
    expect(downloadLink).toHaveAttribute("title", "minio://packaging/lsap-windows.exe");
  });

  it("shows artifact metadata, retention and runner observability", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /制品上传/ }));

    expect(await screen.findByText("平台内")).toBeInTheDocument();
    expect(screen.getAllByText(/productId=lsap/).length).toBeGreaterThan(0);
    expect(screen.getByText(/purpose=license/)).toHaveAttribute("title", "productId=lsap / purpose=license");
    expect(screen.getByText("abcdef0123456789ab...")).toHaveAttribute("title", "abcdef0123456789abcdef0123456789");
    expect(screen.getByText("expired-preview.tar.gz")).toBeInTheDocument();
    expect(screen.getAllByText("已过期").length).toBeGreaterThan(0);
    expect(screen.getAllByRole("link", { name: /下载/ })).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: /Runner 监控/ }));

    expect(await screen.findByText("linux-runner-01")).toBeInTheDocument();
    expect(screen.getByText("忙碌")).toBeInTheDocument();
    expect(screen.getByText("heartbeat: active")).toBeInTheDocument();
    expect(screen.getByText("build-pendin")).toHaveAttribute("title", "build-pending-001");
    expect(screen.getAllByText("2026/5/28 14:12:00").some((element) => element.getAttribute("title") === "2026-05-28T06:12:00Z")).toBe(true);
    expect(screen.getByText("heartbeat: active")).toHaveAttribute("title", "heartbeat: active");
    expect(screen.getByRole("button", { name: /查看当前构建/ })).toHaveAttribute("title", "查看当前构建 build-pending-001");
    expect(screen.getByText("linux-runner-01").closest(".runner-card")).not.toHaveTextContent(/\u001b\[/);
  });

  it("filters build records with operator status chips", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));

    expect(screen.getAllByText("build-pendin").length).toBeGreaterThan(0);
    expect(screen.getAllByText("build-succes").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("button", { name: /筛选：待处理/ }));
    expect(screen.getAllByText("build-pendin").length).toBeGreaterThan(0);
    expect(screen.queryByText("build-succes")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /筛选：待验证/ }));
    expect(screen.queryByText("build-pendin")).not.toBeInTheDocument();
    expect(screen.getAllByText("build-succes").length).toBeGreaterThan(0);
  });

  it("filters build logs in the detail panel", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: /详情/ })[0]);
    await user.click(screen.getByRole("tab", { name: /日志/ }));

    expect(screen.getByText("build request created and waiting for approval")).toBeInTheDocument();
    await user.type(screen.getByPlaceholderText("过滤时间、来源或日志内容"), "runner");
    expect(screen.getByText("没有匹配的日志。")).toBeInTheDocument();
  });

  it("shows existing upload records as reusable component sources", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);

    await user.click(screen.getByRole("button", { name: /组件/ }));
    expect(screen.getByText("已有上传")).toBeInTheDocument();
    expect(screen.getByText("组件总数")).toBeInTheDocument();
    expect(screen.getByText("来源就绪")).toBeInTheDocument();
    expect(screen.getAllByText("license").some((element) => element.getAttribute("title") === "license")).toBe(true);
    expect(screen.getByText("等待选择上传记录")).toHaveAttribute("title", "等待选择上传记录");
    expect(screen.getByText("可选组件未绑定上传").closest(".component-health")).toHaveAttribute("title", "license：可选组件未绑定上传");
    expect(screen.getAllByText("不复用上传记录").length).toBeGreaterThan(0);
  });

  it("edits and saves product overlays", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    expect(await screen.findByText("qa")).toBeInTheDocument();
    const currentOverlayButton = screen.getByRole("button", { name: "当前覆盖 profiles qa" });
    expect(currentOverlayButton).toHaveAttribute("aria-current", "true");
    expect(currentOverlayButton).toHaveAttribute("title", "当前覆盖：qa");
    const editor = screen
      .getAllByRole("textbox")
      .find((element) => element.value.includes('"web": 48001'));

    fireEvent.change(editor, { target: { value: '{"config":{"ports":{"web":49001}}}' } });
    expect(screen.getByTitle("覆盖配置有未保存修改")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "保存覆盖" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/overlays/profiles/qa",
        expect.objectContaining({ method: "PUT" }),
      );
    });
    await waitFor(() => {
      expect(editor).toHaveValue(JSON.stringify({ config: { ports: { web: 49001 } } }, null, 2));
    });
    expect(screen.queryByTitle("覆盖配置有未保存修改")).not.toBeInTheDocument();
    expect(await screen.findByText("覆盖配置已保存")).toBeInTheDocument();
  });

  it("creates a new product overlay and saves it", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    await user.type(screen.getByPlaceholderText("新建覆盖名称"), "dev");
    await user.click(screen.getByRole("button", { name: "新建" }));

    const editor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value === "{}");
    fireEvent.change(editor, { target: { value: '{"config":{"ports":{"web":48002}}}' } });
    await user.click(screen.getByRole("button", { name: "保存覆盖" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/overlays/profiles/dev",
        expect.objectContaining({
          method: "PUT",
          body: JSON.stringify({ config: { ports: { web: 48002 } } }),
        }),
      );
    });
    expect(await screen.findByText("dev")).toBeInTheDocument();
  });

  it("requires confirmation before leaving a dirty overlay", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    const editor = await screen.findByDisplayValue(/"web": 48001/);
    const editedOverlay = '{"config":{"ports":{"web":49001}}}';
    fireEvent.change(editor, { target: { value: editedOverlay } });

    await user.click(screen.getByRole("tab", { name: "客户覆盖" }));

    expect(screen.getByText(/确认丢弃草稿后将切换到客户覆盖/)).toBeInTheDocument();
    expect(editor).toHaveValue(editedOverlay);
    expect(screen.getByRole("button", { name: "当前覆盖 profiles qa" })).toHaveAttribute("aria-current", "true");

    await user.click(screen.getByRole("button", { name: "继续编辑" }));
    expect(screen.queryByText(/确认丢弃草稿后将切换到客户覆盖/)).not.toBeInTheDocument();
    expect(editor).toHaveValue(editedOverlay);

    await user.click(screen.getByRole("tab", { name: "客户覆盖" }));
    await user.click(screen.getByRole("button", { name: "确认丢弃" }));

    expect(await screen.findByDisplayValue(/Example Customer/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "当前覆盖 customers example-customer" })).toHaveAttribute("aria-current", "true");
  });

  it("requires confirmation before creating an overlay from a dirty draft", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    const editor = await screen.findByDisplayValue(/"web": 48001/);
    const editedOverlay = '{"config":{"ports":{"web":49001}}}';
    fireEvent.change(editor, { target: { value: editedOverlay } });

    await user.type(screen.getByPlaceholderText("新建覆盖名称"), "dev");
    await user.click(screen.getByRole("button", { name: "新建" }));

    expect(screen.getByText(/确认丢弃草稿后将新建覆盖 dev/)).toBeInTheDocument();
    expect(editor).toHaveValue(editedOverlay);

    await user.click(screen.getByRole("button", { name: "确认丢弃" }));

    expect(screen.getByDisplayValue("{}")).toBeInTheDocument();
    expect(screen.getByTitle("覆盖配置有未保存修改")).toBeInTheDocument();
  });

  it("blocks invalid overlay names before creating a draft", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    await user.type(screen.getByPlaceholderText("新建覆盖名称"), "../bad");

    expect(screen.getByRole("button", { name: "新建" })).toBeDisabled();
    expect(screen.getByText("名称只能包含字母、数字、点、下划线和中划线")).toBeInTheDocument();
  });

  it("blocks duplicate overlay names before creating a draft", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    await user.type(screen.getByPlaceholderText("新建覆盖名称"), "qa");

    const createButton = screen.getByRole("button", { name: "新建" });
    expect(createButton).toBeDisabled();
    expect(createButton).toHaveAttribute("title", "覆盖配置 qa 已存在");
    expect(screen.getByText("覆盖配置 qa 已存在")).toBeInTheDocument();
  });

  it("shows why overlay saving is disabled for invalid JSON", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    const editor = await screen.findByDisplayValue(/"web": 48001/);
    fireEvent.change(editor, { target: { value: "{" } });

    const saveButton = screen.getByRole("button", { name: "保存覆盖" });
    expect(screen.getByText("请先修复覆盖 JSON，保存覆盖已暂停。")).toBeInTheDocument();
    expect(saveButton).toBeDisabled();
    expect(saveButton).toHaveAttribute("title", "请先修复覆盖 JSON");
    expect(fetch).not.toHaveBeenCalledWith(
      "http://localhost:8080/api/products/lsap/overlays/profiles/qa",
      expect.objectContaining({ method: "PUT" }),
    );
  });
});
