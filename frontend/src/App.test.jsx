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
    releaseChannel: "prod",
    status: "succeeded",
    artifactUri: "minio://packaging/lsap-windows.exe",
    downloadUrl: "http://localhost:8080/api/builds/build-success-002/download",
    variables: {},
    finalConfig: baseBuildLock.config,
    components: baseBuildLock.components,
    workloads: baseBuildLock.workloads,
    buildLock: { ...baseBuildLock, buildId: "build-success-002", target: "windows" },
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
    createdAt: "2026-05-28T06:05:00Z",
  },
];

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
  };

  global.fetch = vi.fn((url, options = {}) => {
    const parsed = new URL(String(url));
    const path = parsed.pathname;
    const method = options.method || "GET";

    if (method === "GET" && path === "/api/products") {
      return jsonResponse([product]);
    }
    if (method === "GET" && path === "/api/builds") {
      return jsonResponse(state.builds);
    }
    if (method === "GET" && path === "/api/uploads") {
      return jsonResponse(uploads);
    }
    if (method === "GET" && path === "/api/products/lsap/manifest") {
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
      return jsonResponse({ ...baseBuildLock, buildId: "build-success-002", target: "windows" });
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
      return jsonResponse(created, 202);
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
    if (method === "POST" && path === "/api/builds/build-pending-001/approve") {
      const approved = { ...state.builds.find((build) => build.id === "build-pending-001"), status: "queued" };
      state.builds = state.builds.map((build) => (build.id === approved.id ? approved : build));
      return jsonResponse(approved);
    }
    if (method === "POST" && path === "/api/builds/build-pending-001/cancel") {
      const canceled = { ...state.builds.find((build) => build.id === "build-pending-001"), status: "canceled" };
      state.builds = state.builds.map((build) => (build.id === canceled.id ? canceled : build));
      return jsonResponse(canceled);
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
    expect(screen.getByText("build-pendin")).toBeInTheDocument();
    expect(screen.getAllByText("待审批").length).toBeGreaterThan(0);
    expect(screen.getAllByText("构建成功").length).toBeGreaterThan(0);
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

    await user.click(screen.getByRole("tab", { name: /Build Lock/ }));
    expect(screen.getByText(/minio:\/\/packaging\/location-guard.tar.gz/)).toBeInTheDocument();
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

    await user.click(screen.getByRole("button", { name: /提交构建申请/ }));

    await waitFor(() => expect(state.createPayloads).toHaveLength(1));
    expect(state.createPayloads[0]).toMatchObject({
      productId: "lsap",
      target: "linux",
      profile: "qa",
      customer: "qa-customer",
      releaseChannel: "qa",
      configOverrides: {
        installDir: "/opt/lsap",
        ports: { web: 49000 },
        customer: {
          name: "测试客户",
          license: "community",
        },
      },
    });
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

    expect(screen.getByText("选择已有上传记录")).toBeInTheDocument();
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

    await user.type(screen.getByPlaceholderText("新建 overlay 名称"), "dev");
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

    await user.type(screen.getByPlaceholderText("新建 overlay 名称"), "../bad");

    expect(screen.getByRole("button", { name: "新建" })).toBeDisabled();
    expect(screen.getByText("名称只能包含字母、数字、点、下划线和中划线")).toBeInTheDocument();
  });
});
