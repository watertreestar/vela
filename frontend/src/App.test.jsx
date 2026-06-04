import { Theme } from "@radix-ui/themes";
import "@radix-ui/themes/styles.css";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";

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
        message: "heartbeat: active",
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
    builds: [...builds],
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
      return jsonResponse([product]);
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
      return jsonResponse(product);
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
          message: "build request created and waiting for approval",
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
    <Theme appearance="light" accentColor="teal" grayColor="slate" radius="medium">
      <App />
    </Theme>,
  );
}

beforeEach(() => {
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
    expect(screen.getByText("build-pendin")).toBeInTheDocument();
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

  it("opens build detail and displays logs plus build lock data", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: /详情/ })[0]);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("构建详情")).toBeInTheDocument();
    expect(screen.getAllByText("build-pending-001").length).toBeGreaterThan(0);

    await user.click(screen.getByRole("tab", { name: /日志/ }));
    expect(screen.getByText("build request created and waiting for approval")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: /构建锁/ }));
    expect(screen.getByText(/minio:\/\/packaging\/location-guard.tar.gz/)).toBeInTheDocument();
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
  });

  it("submits a build request with structured config overrides", async () => {
    const state = setupFetch();
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getAllByRole("button", { name: /创建构建/ })[0]);

    await user.clear(screen.getByLabelText("客户标识"));
    await user.type(screen.getByLabelText("客户标识"), "qa-customer");
    await user.clear(screen.getByLabelText("Web 端口"));
    await user.type(screen.getByLabelText("Web 端口"), "49000");
    await user.clear(screen.getByLabelText("客户名称"));
    await user.type(screen.getByLabelText("客户名称"), "测试客户");
    const variablesEditor = screen
      .getAllByRole("textbox")
      .find((element) => element.tagName === "TEXTAREA" && element.value === "{}");
    fireEvent.change(variablesEditor, {
      target: { value: '{"ports":{"web":49999},"releaseNote":"nightly smoke"}' },
    });

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

  it("saves product manifest through the manifest editor", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await screen.findByText("Manifest 编辑器");
    await user.click(screen.getByRole("button", { name: "保存 Manifest" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/manifest",
        expect.objectContaining({ method: "PUT" }),
      );
    });
    expect(await screen.findByText("Manifest 已保存")).toBeInTheDocument();
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
    await user.click(screen.getByRole("button", { name: "保存 Manifest" }));
    expect(await screen.findByText(/Manifest 校验失败/)).toBeInTheDocument();
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
  });

  it("edits common manifest fields with the structured editor", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /产品配置/ }));
    await user.click(screen.getByRole("tab", { name: /结构化/ }));

    await user.clear(screen.getByLabelText("产品名称"));
    await user.type(screen.getByLabelText("产品名称"), "定位平台专业版");

    await user.click(screen.getByRole("tab", { name: /^编辑/ }));
    expect(screen.getByDisplayValue(/定位平台专业版/)).toBeInTheDocument();
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
    expect(await screen.findByText("20260528170000-aabbccddee")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "恢复" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/manifest/revisions/20260528170000-aabbccddee/restore",
        expect.objectContaining({ method: "POST" }),
      );
    });
    expect(await screen.findByText("Manifest 已恢复到历史版本")).toBeInTheDocument();
  });

  it("requires confirmation before canceling a build", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /构建记录/ }));
    await user.click(screen.getAllByRole("button", { name: "取消" })[0]);

    expect(await screen.findByRole("dialog")).toHaveTextContent("确认取消");
    await user.click(screen.getByRole("button", { name: "确认取消" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/builds/build-pending-001/cancel",
        expect.objectContaining({ method: "POST" }),
      );
    });
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

    expect(screen.queryByRole("button", { name: "Release" })).not.toBeInTheDocument();
    await user.click(screen.getAllByRole("button", { name: "验证" })[0]);
    expect(await screen.findByText("已验证 Preview")).toBeInTheDocument();

    await user.click((await screen.findAllByRole("button", { name: "Release" }))[0]);
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
    await user.click((await screen.findAllByRole("button", { name: "Release" }))[0]);
    await user.click(screen.getByRole("button", { name: "确认转 Release" }));

    expect(await screen.findByText("Release 门禁未通过")).toBeInTheDocument();
    expect(screen.getByText(/组件 URI 精确：组件 URI 不能包含通配符/)).toBeInTheDocument();
    expect(screen.getByText("请先修复阻塞项后再发布")).toBeInTheDocument();
  });

  it("uses backend build download endpoint when only artifact URI is available", async () => {
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await screen.getByRole("button", { name: /构建记录/ }).click();

    expect(screen.getByRole("link", { name: /下载/ })).toHaveAttribute(
      "href",
      "http://localhost:8080/api/builds/build-success-002/download",
    );
  });

  it("shows artifact metadata, retention and runner observability", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /制品上传/ }));

    expect(await screen.findByText("平台内")).toBeInTheDocument();
    expect(screen.getByText(/productId=lsap/)).toBeInTheDocument();
    expect(screen.getByText(/purpose=license/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /Runner 监控/ }));

    expect(await screen.findByText("linux-runner-01")).toBeInTheDocument();
    expect(screen.getByText("忙碌")).toBeInTheDocument();
    expect(screen.getByText("heartbeat: active")).toBeInTheDocument();
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

    expect(screen.getByText("已有上传")).toBeInTheDocument();
    expect(screen.getByText("组件总数")).toBeInTheDocument();
    expect(screen.getByText("来源就绪")).toBeInTheDocument();
    expect(screen.getAllByText("不复用上传记录").length).toBeGreaterThan(0);
  });

  it("edits and saves product overlays", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    expect(await screen.findByText("qa")).toBeInTheDocument();
    const editor = screen
      .getAllByRole("textbox")
      .find((element) => element.value.includes('"web": 48001'));

    fireEvent.change(editor, { target: { value: '{"config":{"ports":{"web":49001}}}' } });
    await user.click(screen.getByRole("button", { name: "保存覆盖" }));

    await waitFor(() => {
      expect(fetch).toHaveBeenCalledWith(
        "http://localhost:8080/api/products/lsap/overlays/profiles/qa",
        expect.objectContaining({ method: "PUT" }),
      );
    });
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

  it("blocks invalid overlay names before creating a draft", async () => {
    const user = userEvent.setup();
    renderApp();

    await screen.findByRole("heading", { name: "总览" });
    await user.click(screen.getByRole("button", { name: /覆盖配置/ }));

    await user.type(screen.getByPlaceholderText("新建覆盖名称"), "../bad");

    expect(screen.getByRole("button", { name: "新建" })).toBeDisabled();
    expect(screen.getByText("名称只能包含字母、数字、点、下划线和中划线")).toBeInTheDocument();
  });
});
