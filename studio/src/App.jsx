import { useEffect, useMemo, useState } from "react";
import {
  Alert, Badge, Button, Card, Col, Descriptions, Drawer, Dropdown, Empty, Form,
  Input, Layout, List, Menu, Modal, Popconfirm, Row, Select, Space, Spin,
  Table, Tabs, Tag, Typography, Upload, Steps, message,
} from "antd";
import {
  AuditOutlined, BellOutlined, BuildOutlined, CloudUploadOutlined, CodeOutlined,
  DashboardOutlined, DownloadOutlined, FileTextOutlined, HistoryOutlined,
  MenuFoldOutlined, MenuUnfoldOutlined, PlusOutlined, RobotOutlined,
  SettingOutlined, UploadOutlined,
} from "@ant-design/icons";
import {
  API_URL, approveBuild, buildDownloadUrl, buildLogStreamUrl, cancelBuild,
  completeOIDCRedirect, createBuild, downloadFile, dryRunProductManifest,
  exportAuditArchive, exportBuildReleaseEvidenceArchive, getAuthState, getBuildLock,
  oidcLoginConfigured, startOIDCLogin, logoutAuth, clearAuthState, setAuthState,
  getBuildLogs, getBuildReleaseCheck, getBuildReproducibilityReport,
  getProductManifest, getProductOverlay, listAuditEvents, listBuilds,
  listNotifications, listProductManifestRevisions, listProductOverlays, listProducts,
  listRunners, listUploads, markAllNotificationsRead, markNotificationRead,
  publishBuild, rejectBuild, restoreProductManifestRevision, retryBuild,
  updateProductManifest, updateProductOverlay, uploadArtifact, validatePreviewBuild,
} from "./lib/api";
import { translateError } from "./lib/i18n";
import { AgentCopilotPanel } from "./features/agent/AgentCopilot";
import { FadeUp } from "./components/amicro/FadeUp";

// JSDOM and older embedded webviews do not provide this browser API required by AntD.
if (typeof window !== "undefined" && !window.matchMedia) {
  window.matchMedia = (query) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent() { return false; } });
}

const { Header, Sider, Content } = Layout;
const { Title, Text } = Typography;
const pages = [
  ["overview", "总览", <DashboardOutlined />], ["create", "创建构建", <PlusOutlined />],
  ["builds", "构建记录", <HistoryOutlined />], ["runners", "Runner", <BuildOutlined />],
  ["uploads", "制品上传", <CloudUploadOutlined />], ["products", "产品配置", <FileTextOutlined />],
  ["overlays", "覆盖配置", <SettingOutlined />], ["audit", "审计", <AuditOutlined />],
  ["logs", "日志与锁定", <CodeOutlined />],
];
const statusMeta = {
  pending_approval: ["待审批", "orange"], queued: ["排队中", "blue"], running: ["构建中", "processing"],
  succeeded: ["Preview 已生成", "cyan"], preview_validated: ["Preview 已验证", "green"],
  published: ["Release 已发布", "success"], rejected: ["已拒绝", "default"], failed: ["失败", "error"], canceled: ["已取消", "warning"],
};
const permissions = {
  admin: ["*"],
  operator: ["build.create", "build.approve", "build.reject", "build.validate", "build.cancel", "build.retry", "manifest.write", "overlay.write", "upload.write", "download.platform", "download.restricted", "audit.read"],
  viewer: ["download.platform", "audit.read"],
};

export function apiEnvironmentLabel(url) {
  try {
    if (String(url).startsWith("/")) return "同源环境";
    const host = new URL(url).hostname;
    return ["localhost", "127.0.0.1"].includes(host) ? "本地环境" : "远程环境";
  } catch { return "自定义环境"; }
}
function can(auth, permission) {
  if (!auth?.token) return true;
  return permissions[auth.role]?.includes("*") || permissions[auth.role]?.includes(permission);
}
function errorText(error) { return translateError(error) || error?.message || "操作失败"; }
function asJson(value) { return JSON.stringify(value ?? {}, null, 2); }
function parseObject(text, label) {
  let value;
  try { value = JSON.parse(text || "{}"); } catch { throw new Error(`${label} JSON 解析错误`); }
  if (!value || Array.isArray(value) || typeof value !== "object") throw new Error(`${label} 必须是 JSON 对象`);
  return value;
}
function buildStatus(build) { const [label, color] = statusMeta[build?.status] || [build?.status || "未知", "default"]; return <Tag color={color}>{label}</Tag>; }
function buildActionItems(build, runAction, busy, releaseReady = false) {
  const running = Boolean(busy);
  const action = (key, label, fn, permission, danger = false, disabled = false) => ({
    key,
    label,
    danger,
    disabled: running || disabled,
    onClick: () => runAction(build, label, fn, permission, danger),
  });
  switch (build?.status) {
    case "pending_approval":
      return [action("approve", "审批通过", approveBuild, "build.approve"), action("reject", "拒绝", rejectBuild, "build.reject", true), action("cancel", "取消", cancelBuild, "build.cancel", true)];
    case "queued":
    case "running":
      return [action("cancel", "取消", cancelBuild, "build.cancel", true)];
    case "succeeded":
      return [action("validate", "验证 Preview", validatePreviewBuild, "build.validate")];
    case "preview_validated":
      return [action("publish", "发布 Release", publishBuild, "build.publish", true, !releaseReady)];
    case "failed":
    case "canceled":
      return [action("retry", "重试", retryBuild, "build.retry")];
    default:
      return [];
  }
}
function when(value) { return value ? new Date(value).toLocaleString("zh-CN", { hour12: false }) : "—"; }
function stripAnsi(value) { return String(value ?? "").replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, ""); }
function downloadJson(value, filename) { const url = URL.createObjectURL(new Blob([asJson(value)], { type: "application/json" })); const a = document.createElement("a"); a.href = url; a.download = filename; a.click(); URL.revokeObjectURL(url); }

export default function App() {
  const [screens, setScreens] = useState(() => ({ lg: typeof window === "undefined" || window.innerWidth >= 992, xs: typeof window !== "undefined" && window.innerWidth < 576 }));
  useEffect(() => {
    const resize = () => setScreens({ lg: window.innerWidth >= 992, xs: window.innerWidth < 576 });
    window.addEventListener("resize", resize);
    return () => window.removeEventListener("resize", resize);
  }, []);
  const [buildFilter, setBuildFilter] = useState("all");
  const [notice, holder] = message.useMessage();
  const [page, setPage] = useState("overview");
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem("packaging.sidebarCollapsed") === "true");
  const [mobileNav, setMobileNav] = useState(false);
  const [products, setProducts] = useState([]), [builds, setBuilds] = useState([]), [uploads, setUploads] = useState([]), [runners, setRunners] = useState([]), [notifications, setNotifications] = useState([]);
  const [audit, setAudit] = useState([]), [auditFilter, setAuditFilter] = useState({ action: "", resourceType: "", resourceId: "", buildId: "", productId: "", uploadId: "" });
  const [auth, setAuth] = useState(() => ({ ...getAuthState(), role: getAuthState().role || "operator" }));
  const [authOpen, setAuthOpen] = useState(false);
  const [loading, setLoading] = useState(true), [error, setError] = useState("");
  const [form, setForm] = useState({ productId: "", target: "linux", profile: "qa", customer: "", version: "latest", releaseChannel: "preview", installDir: "", webPort: "48000", serviceVersion: "latest", customerName: "", license: "community", passwordPolicy: "strong", modules: {}, variablesText: "{}", componentOverridesText: "{}" });
  const [manifest, setManifest] = useState("{}"), [savedManifest, setSavedManifest] = useState("{}"), [revisions, setRevisions] = useState([]), [dryRun, setDryRun] = useState(null);
  const [overlayKind, setOverlayKind] = useState("profiles"), [overlayName, setOverlayName] = useState(""), [overlayText, setOverlayText] = useState("{}"), [savedOverlay, setSavedOverlay] = useState("{}"), [overlays, setOverlays] = useState({ profiles: [], customers: [] });
  const [detail, setDetail] = useState(null), [detailData, setDetailData] = useState({ logs: [], lock: null, release: null, reproducibility: null }), [detailOpen, setDetailOpen] = useState(false);
  const [busy, setBusy] = useState(""), [search, setSearch] = useState(""), [agentOpen, setAgentOpen] = useState(false), [agentMessages, setAgentMessages] = useState([]);
  const product = products.find((p) => p.id === form.productId);
  const permitted = (permission) => can(auth, permission);

  const refresh = async (manual = false) => {
    setLoading(!manual); setError("");
    try {
      const [p, b, u, r, n] = await Promise.all([listProducts(), listBuilds(), listUploads(), listRunners(), listNotifications({ limit: 50 })]);
      setProducts(p); setBuilds(b); setUploads(u); setRunners(r); setNotifications(n);
      if (!form.productId && p[0]) await selectProduct(p[0], true);
      if (manual) notice.success("数据已刷新");
    } catch (e) { setError(errorText(e)); } finally { setLoading(false); }
  };
  useEffect(() => {
    refresh();
    completeOIDCRedirect()
      .then((result) => {
        if (!result) return;
        if (!result.ok) throw new Error(result.error || "OIDC 登录失败");
        setAuth({ ...getAuthState(), role: result.role || "viewer" });
        notice.success("OIDC 登录成功");
      })
      .catch((e) => setError(errorText(e)));
  }, []);
  useEffect(() => {
    const key = (e) => { if (e.ctrlKey && e.key.toLowerCase() === "k") { e.preventDefault(); document.getElementById("global-build-search")?.focus(); } };
    window.addEventListener("keydown", key); return () => window.removeEventListener("keydown", key);
  }, []);
  async function selectProduct(next, load = true) {
    if (load && manifest !== savedManifest && !window.confirm("Manifest 有未保存修改，确认丢弃草稿？")) return;
    const target = next.targets?.[0] || "linux";
    const config = next.targetConfig?.[target] || {};
    setForm((old) => ({ ...old, productId: next.id, target, profile: next.profiles?.[0] || "qa", version: next.versions?.[0] || "latest", installDir: config.installDir || "", webPort: String(next.config?.ports?.web || 48000), serviceVersion: next.versions?.[0] || "latest", customerName: next.config?.customer?.name || "", license: next.config?.customer?.license || "community", passwordPolicy: next.config?.database?.passwordPolicy || "strong", modules: next.config?.modules || {} }));
    if (!load) return;
    try {
      const [m, h, profiles, customers] = await Promise.all([getProductManifest(next.id), listProductManifestRevisions(next.id), listProductOverlays(next.id, "profiles"), listProductOverlays(next.id, "customers")]);
      const text = asJson(m); setManifest(text); setSavedManifest(text); setRevisions(h); setOverlays({ profiles, customers });
      if (!overlayName && profiles[0]) {
        const firstOverlay = await getProductOverlay(next.id, "profiles", profiles[0].name);
        const overlayValue = asJson(firstOverlay.data);
        setOverlayKind("profiles"); setOverlayName(profiles[0].name); setOverlayText(overlayValue); setSavedOverlay(overlayValue);
      }
    } catch (e) { setError(errorText(e)); }
  }
  async function downloadBuild(build) {
    const permission = build.downloadPolicy === "restricted" ? "download.restricted" : "download.platform";
    if (!permitted(permission)) return;
    try {
      const url = build.downloadUrl || buildDownloadUrl(build.id);
      const filename = String(build.artifactUri || build.downloadUrl || `${build.id}.zip`).split(/[/?#]/).pop() || `${build.id}.zip`;
      await downloadFile(url, filename);
      notice.success("下载已开始");
    } catch (e) { setError(errorText(e)); }
  }
  function payload() {
    const port = Number(form.webPort);
    if (!form.productId) throw new Error("请选择产品");
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("Web 端口必须在 1 到 65535 之间");
    return { productId: form.productId, target: form.target, profile: form.profile, customer: form.customer, version: form.version, releaseChannel: form.releaseChannel, variables: parseObject(form.variablesText, "构建变量"), componentOverrides: parseObject(form.componentOverridesText, "组件覆盖"), configOverrides: { installDir: form.installDir, serviceVersion: form.serviceVersion, ports: { web: port }, customer: { name: form.customerName, license: form.license }, database: { passwordPolicy: form.passwordPolicy }, modules: form.modules } };
  }
  async function create() { if (!permitted("build.create")) return; setBusy("create"); try { const created = await createBuild(payload()); await refresh(); await inspect(created); setPage("builds"); notice.success("构建请求已创建，等待审批"); } catch (e) { setError(errorText(e)); } finally { setBusy(""); } }
  async function inspect(build) {
    setDetail(build); setDetailOpen(true);
    try { const [logs, lock, release, reproducibility] = await Promise.all([getBuildLogs(build.id), getBuildLock(build.id), getBuildReleaseCheck(build.id), getBuildReproducibilityReport(build.id)]); setDetailData({ logs, lock, release, reproducibility }); } catch (e) { setError(errorText(e)); }
  }
  async function runAction(build, label, fn, permission, dangerous = false) {
    if (!permitted(permission) || busy) return;
    const execute = async () => {
      setBusy(`${label}-${build.id}`);
      try {
        const current = await fn(build.id);
        await refresh();
        if (detail?.id === build.id) await inspect(current || build);
        notice.success(`已${label}`);
      } catch (e) { setError(errorText(e)); } finally { setBusy(""); }
    };
    if (dangerous) {
      Modal.confirm({ title: `确认${label}`, content: `此操作将影响构建 ${build.id}，服务端会再次校验权限。`, okType: "danger", onOk: execute });
      return;
    }
    await execute();
  }
  async function saveManifest() { if (!permitted("manifest.write")) return; setBusy("manifest"); try { const parsed = validateManifest(parseObject(manifest, "Manifest")); await updateProductManifest(form.productId, parsed); const text = asJson(parsed); setManifest(text); setSavedManifest(text); setRevisions(await listProductManifestRevisions(form.productId)); notice.success("Manifest 已保存"); } catch (e) { setError(errorText(e)); } finally { setBusy(""); } }
  async function manifestDryRun() { setBusy("dryrun"); try { const output = await dryRunProductManifest(form.productId, { manifest: validateManifest(parseObject(manifest, "Manifest")), ...payload() }); setDryRun(output); } catch (e) { setError(errorText(e)); } finally { setBusy(""); } }
  async function loadOverlay(kind, name) { if (!name) return; if (overlayText !== savedOverlay && !window.confirm("覆盖配置有未保存修改，确认丢弃草稿？")) return; try { const value = await getProductOverlay(form.productId, kind, name); const text = asJson(value.data); setOverlayKind(kind); setOverlayName(name); setOverlayText(text); setSavedOverlay(text); } catch (e) { setError(errorText(e)); } }
  async function saveOverlay() { if (!permitted("overlay.write")) return; if (!/^[A-Za-z0-9._-]+$/.test(overlayName)) return setError("覆盖名称只能包含字母、数字、点、下划线或连字符"); const same = overlays[overlayKind]?.filter((x) => x.name === overlayName).length > 1; if (same) return setError("覆盖名称重复"); try { const data = parseObject(overlayText, "覆盖配置"); await updateProductOverlay(form.productId, overlayKind, overlayName, data); const text = asJson(data); setOverlayText(text); setSavedOverlay(text); const records = await listProductOverlays(form.productId, overlayKind); setOverlays((old) => ({ ...old, [overlayKind]: records })); notice.success("覆盖配置已保存"); } catch (e) { setError(errorText(e)); } }
  async function upload(file, componentId = "", options = {}) {
    if (!file || !permitted("upload.write")) return false;
    try {
      const result = await uploadArtifact(file, {
        downloadPolicy: options.downloadPolicy || "platform",
        retentionDays: Number(options.retentionDays || 30),
        metadata: { productId: form.productId, purpose: options.purpose || (componentId ? "component-override" : "manual-upload") },
      });
      if (componentId) {
        const current = parseObject(form.componentOverridesText, "组件覆盖");
        current[componentId] = { ...(current[componentId] || {}), kind: "manual-upload", uploadId: result.id, uri: result.uri };
        setForm((x) => ({ ...x, componentOverridesText: asJson(current) }));
      }
      await refresh(); notice.success("文件已上传");
    } catch (e) { setError(errorText(e)); }
    return false;
  }
  async function loadAudit() { if (!permitted("audit.read")) return; try { setAudit(await listAuditEvents({ ...auditFilter, limit: 100 })); } catch (e) { setError(errorText(e)); } }
  async function readNotification(n) { try { await markNotificationRead(n.id); setNotifications((old) => old.map((x) => x.id === n.id ? { ...x, readAt: new Date().toISOString() } : x)); if (n.buildId) { setPage("builds"); const build = builds.find((b) => b.id === n.buildId); if (build) inspect(build); } } catch (e) { setError(errorText(e)); } }
  const shownBuilds = useMemo(() => builds.filter((b) => {
    const matchesSearch = !search || [b.id, b.productId, b.status, b.version, b.target, b.profile, b.customer, b.runnerId].join(" ").toLowerCase().includes(search.toLowerCase());
    const ready = Boolean(b.releaseCheckSummary?.releaseReady || b.releaseReady);
    const matchesFilter = buildFilter === "all"
      || (buildFilter === "attention" && ["pending_approval", "failed", "canceled"].includes(b.status))
      || (buildFilter === "active" && ["queued", "running"].includes(b.status))
      || (buildFilter === "preview" && b.status === "succeeded")
      || (buildFilter === "validated" && b.status === "preview_validated")
      || (buildFilter === "releaseReady" && ready)
      || (buildFilter === "blocked" && b.status === "preview_validated" && !ready);
    return matchesSearch && matchesFilter;
  }), [builds, search, buildFilter]);
  const menu = <Menu className="vela-nav" id="admin-sidebar" mode="inline" selectedKeys={[page]} items={pages.map(([key, label, icon]) => ({ key, label, icon, "aria-label": `导航：${label}` }))} onClick={({ key }) => { setPage(key); setMobileNav(false); if (key === "audit") loadAudit(); }} />;
  const saveAuth = (values) => {
    const token = String(values.token || "").trim();
    if (!token) return setError("请输入访问令牌");
    setAuthState({ token, role: values.role, source: "manual" });
    setAuth({ token, role: values.role, source: "manual" });
    setAuthOpen(false); notice.success("访问令牌已保存");
  };
  const clearAuth = async () => {
    try { await logoutAuth(auth.token); } catch { /* local credentials must still be cleared */ }
    clearAuthState(); setAuth({ token: "", role: "operator", source: "" }); setAuthOpen(false); notice.success("本地认证已清除");
  };
  const changePage = (next) => setPage(next);
  return <Layout className={`vela-shell admin-shell ${collapsed ? "is-sidebar-collapsed" : ""}`}>{holder}
    {screens.lg && <Sider width={224} collapsed={collapsed} className="vela-sider"><div className="vela-brand"><BuildOutlined /><span>Vela Studio</span></div>{menu}<div className="vela-sider-footer" title={API_URL}><Badge status="default" /><Text type="secondary">{collapsed ? "" : apiEnvironmentLabel(API_URL)}</Text>{!collapsed && <div className="vela-footer-caption">打包与发布工作台</div>}</div></Sider>}
    <Drawer className="vela-nav-drawer" title="Vela Studio" placement="left" width={screens.xs ? "88vw" : 280} open={mobileNav} onClose={() => setMobileNav(false)}>{!screens.lg && menu}</Drawer>
    <Layout className="vela-main"><Header className="vela-header"><Space className="vela-header-search"><Button aria-label={!screens.lg ? "展开导航" : collapsed ? "展开导航" : "折叠导航"} aria-expanded={screens.lg ? !collapsed : mobileNav} type="text" icon={screens.lg && !collapsed ? <MenuFoldOutlined /> : <MenuUnfoldOutlined />} onClick={() => { if (!screens.lg) return setMobileNav(true); const next = !collapsed; setCollapsed(next); localStorage.setItem("packaging.sidebarCollapsed", String(next)); }} /><Input id="global-build-search" aria-label="搜索构建 ID、产品、状态" placeholder="搜索构建 ID、产品、状态" value={search} onChange={(e) => setSearch(e.target.value)} onPressEnter={() => setPage("builds")} suffix={search ? <Button type="link" size="small" aria-label="清空全局搜索" onClick={() => setSearch("")}>清空</Button> : !screens.xs && <kbd>Ctrl K</kbd>} /></Space><Space className="vela-header-tools"><Button aria-label="认证与角色" title="认证与角色" icon={<SettingOutlined />} onClick={() => setAuthOpen(true)}>{!screens.xs && (auth.token ? `${auth.role || "viewer"} 角色` : "本地开发访问")}</Button><Button icon={<RobotOutlined />} aria-label="打开构建助手" title="构建助手" onClick={() => setAgentOpen(true)}>{!screens.xs && "构建助手"}</Button><Dropdown trigger={["click"]} popupRender={() => <Card title="通知中心" size="small" style={{ width: "min(360px, calc(100vw - 24px))" }} extra={<Button type="link" onClick={async () => { try { await markAllNotificationsRead(); setNotifications((x) => x.map((n) => ({ ...n, readAt: new Date().toISOString() }))); } catch (e) { setError(errorText(e)); } }}>全部已读</Button>}><Text>{notifications.filter((n) => !n.readAt).length ? `${notifications.filter((n) => !n.readAt).length} 条未读` : "没有未读通知"}</Text><List dataSource={notifications} renderItem={(n) => <List.Item actions={[<Button key="read" onClick={() => readNotification(n)}>已读</Button>, <Button key="view" onClick={() => { if (n.buildId) { const b = builds.find((x) => x.id === n.buildId); if (b) inspect(b); } }}>查看</Button>]}><Badge dot={!n.readAt}>{n.title || n.message}</Badge></List.Item>} /></Card>}><Badge count={notifications.filter((n) => !n.readAt).length}><Button aria-label={`通知，${notifications.filter((n) => !n.readAt).length} 条未读`} icon={<BellOutlined />} /></Badge></Dropdown></Space></Header>
      <Content className="vela-content">{error && <Alert className="vela-alert" type="error" closable onClose={() => setError("")} message="操作未完成" description={error} />}<div className="vela-page-heading"><div><Title level={1}>{pages.find((x) => x[0] === page)?.[1]}</Title><Text type="secondary">{{ overview: "从待处理任务开始，跟进 Preview 与 Release 进度。", create: "配置打包目标与运行参数，提交后进入审批队列。", builds: "筛选构建状态，查看执行详情与发布门禁。", runners: "查看执行节点、当前任务与心跳状态。", uploads: "管理构建制品、下载策略与保留期限。", products: "编辑产品 Manifest，验证配置并管理历史版本。", overlays: "管理 Profile 与客户的配置覆盖。", audit: "检索操作记录并导出审计证据。", logs: "查看构建日志、发布门禁与锁定配置。" }[page]}</Text></div></div>{loading ? <div className="vela-loading" role="status" aria-label="正在加载工作台"><Space direction="vertical" align="center"><Spin /><Text type="secondary">加载工作台</Text></Space></div> : <Page page={page} products={products} product={product} builds={shownBuilds} allBuilds={builds} buildFilter={buildFilter} setBuildFilter={setBuildFilter} runners={runners} uploads={uploads} form={form} setForm={setForm} selectProduct={selectProduct} create={create} inspect={inspect} runAction={runAction} busy={busy} upload={upload} manifest={manifest} setManifest={setManifest} savedManifest={savedManifest} revisions={revisions} saveManifest={saveManifest} manifestDryRun={manifestDryRun} dryRun={dryRun} overlay={{ overlayKind, setOverlayKind, overlayName, setOverlayName, overlayText, setOverlayText, savedOverlay, overlays, loadOverlay, saveOverlay }} audit={{ audit, auditFilter, setAuditFilter, loadAudit }} auth={auth} download={downloadBuild} changePage={changePage} />}</Content></Layout>
    <BuildDrawer build={detail} data={detailData} open={detailOpen} onClose={() => setDetailOpen(false)} onDownload={downloadBuild} />
    <Modal title="访问令牌与认证" open={authOpen} onCancel={() => setAuthOpen(false)} footer={null} destroyOnHidden>
      <Alert type="info" showIcon message="令牌仅保存于此浏览器的本地存储，不会在界面中回显。服务端仍是最终授权方。" style={{ marginBottom: 16 }} />
      <Form layout="vertical" initialValues={{ role: auth.role || "operator" }} onFinish={saveAuth}>
        <Form.Item name="role" label="角色" rules={[{ required: true }]}><Select options={[{ value: "admin", label: "管理员" }, { value: "operator", label: "操作员" }, { value: "viewer", label: "只读" }]} /></Form.Item>
        <Form.Item name="token" label="Bearer Token" rules={[{ required: true, message: "请输入访问令牌" }]}><Input.Password autoComplete="off" aria-label="Bearer Token" placeholder="仅用于 API Bearer 认证" /></Form.Item>
        <Space><Button type="primary" htmlType="submit">保存令牌</Button><Button danger disabled={!auth.token} onClick={clearAuth}>清除认证</Button>{oidcLoginConfigured() && <Button onClick={() => startOIDCLogin().catch((e) => setError(errorText(e)))}>使用 OIDC 登录</Button>}</Space>
      </Form>
      {!oidcLoginConfigured() && <Text type="secondary">OIDC 未配置；可使用本地访问令牌。</Text>}
    </Modal>
    <AgentCopilotPanel open={agentOpen} onClose={() => setAgentOpen(false)} context={{ label: detail ? `构建 ${detail.id}` : "控制台" }} messages={agentMessages} onSend={(text) => {
      if (!import.meta.env.VITE_AGENT_API_URL) { setAgentMessages((x) => [...x, { id: Date.now(), role: "system", parts: [{ type: "text", text: "Agent 服务未配置；不会发起 API 请求或执行操作。" }] }]); return; }
      setAgentMessages((x) => [...x, { id: Date.now(), role: "user", parts: [{ type: "text", text }] }, { id: Date.now() + 1, role: "system", parts: [{ type: "text", text: "Agent BFF 集成等待服务端实现；不会模拟或执行特权操作。" }] }]);
    }} status="ready" error={import.meta.env.VITE_AGENT_API_URL ? "Agent BFF 传输层尚未接入；当前不会发送模型请求或执行工具。" : "Agent 服务未配置；不会发起 API 请求。"} />
  </Layout>;
}

function GuardedButton({ auth, permission, title, children, ...props }) { const allowed = can(auth, permission); const tip = allowed ? title : "需要操作员或管理员才能执行此操作"; return <Button {...props} disabled={!allowed || props.disabled} title={tip}>{children}</Button>; }
function Page(props) {
  const { page } = props;
  if (page === "overview") return <FadeUp key={page}><Overview {...props} /></FadeUp>;
  if (page === "create") return <FadeUp key={page}><CreateForm {...props} /></FadeUp>;
  if (page === "builds") return <FadeUp key={page}><BuildTable {...props} /></FadeUp>;
  if (page === "runners") return <FadeUp key={page}><RunnerTable runners={props.runners} builds={props.allBuilds || props.builds} inspect={props.inspect} /></FadeUp>;
  if (page === "uploads") return <FadeUp key={page}><Uploads {...props} /></FadeUp>;
  if (page === "products") return <FadeUp key={page}><Manifest {...props} /></FadeUp>;
  if (page === "overlays") return <FadeUp key={page}><Overlays {...props} /></FadeUp>;
  if (page === "audit") return <FadeUp key={page}><Audit {...props.audit} /></FadeUp>;
  return <FadeUp key={page}><LogsWorkspace builds={props.allBuilds || props.builds} inspect={props.inspect} /></FadeUp>;
}
function Overview({ products, builds, runners, uploads, inspect, setBuildFilter, allBuilds, changePage }) {
  const records = allBuilds || builds;
  const pending = records.filter((b) => b.status === "pending_approval");
  const failed = records.filter((b) => ["failed", "canceled"].includes(b.status));
  const attention = [...pending, ...failed];
  const active = records.filter((b) => ["queued", "running"].includes(b.status));
  const preview = records.filter((b) => b.status === "succeeded");
  const releaseReady = records.filter((b) => b.releaseCheckSummary?.releaseReady || b.releaseReady);
  const blocked = records.filter((b) => b.status === "preview_validated" && !(b.releaseCheckSummary?.releaseReady || b.releaseReady));
  const metrics = [["需要处理", attention.length, "attention"], ["执行中", active.length, "active"], ["待验证 Preview", preview.length, "preview"], ["可发布 Release", releaseReady.length, "releaseReady"], ["发布阻塞", blocked.length, "blocked"]];
  const focus = attention[0] || active[0] || preview[0] || blocked[0] || releaseReady[0] || records[0];
  const focusName = products.find((item) => item.id === focus?.productId)?.name || focus?.productId;
  const focusMeta = !focus ? {
    label: "工作台已就绪", title: "从一次构建开始", description: "选择产品与目标，提交后进入审批与发布流水线。", action: "创建构建",
  } : focus.status === "pending_approval" ? {
    label: "下一步：审批", title: `${focusName}等待审批`, description: "检查目标、版本与组件锁定，确认后即可进入构建队列。", action: "查看审批详情",
  } : ["failed", "canceled"].includes(focus.status) ? {
    label: "下一步：恢复", title: `${focusName}需要处理`, description: focus.status === "failed" ? "构建未完成。先查看失败日志，再决定是否重试。" : "构建已取消，可查看上下文后重新发起。", action: "查看构建详情",
  } : ["queued", "running"].includes(focus.status) ? {
    label: "正在执行", title: `${focusName}正在${focus.status === "queued" ? "等待 Runner" : "构建"}`, description: "构建完成后将进入 Preview 验证；当前无需人工操作。", action: "查看执行详情",
  } : focus.status === "succeeded" ? {
    label: "下一步：验证", title: `${focusName}的 Preview 已生成`, description: "验证安装与运行结果，通过后再检查 Release 门禁。", action: "查看 Preview",
  } : focus.status === "preview_validated" && !(focus.releaseCheckSummary?.releaseReady || focus.releaseReady) ? {
    label: "下一步：解锁", title: `${focusName}尚不能发布`, description: "Preview 已验证，但 Release 门禁仍有阻塞项需要处理。", action: "查看门禁详情",
  } : focus.releaseCheckSummary?.releaseReady || focus.releaseReady ? {
    label: "下一步：发布", title: `${focusName}已准备好发布`, description: "Release 门禁已通过，可在确认版本与证据后发布。", action: "查看发布详情",
  } : {
    label: "最近构建", title: `${focusName} / ${focus.version || "—"}`, description: "查看构建结果、锁定配置与发布证据。", action: "查看构建详情",
  };
  const queue = (filter) => { setBuildFilter(filter); changePage("builds"); };
  return <div className="vela-overview">
    <section className={`vela-command ${attention.length ? "has-attention" : "is-clear"}`} aria-labelledby="command-title">
      <div className="vela-command-focus">
        <Text className="vela-command-label">{focusMeta.label}</Text>
        <Title level={2} id="command-title">{focusMeta.title}</Title>
        <Text className="vela-command-description" type="secondary">{focusMeta.description}</Text>
        {focus && <div className="vela-command-meta"><span>{focus.id}</span><span>{focus.target || "—"} / {focus.profile || "—"}</span><span>{focus.version || "—"}</span></div>}
        <Space wrap className="vela-command-actions"><Button type="primary" onClick={() => focus ? inspect(focus) : changePage("create")}>{focusMeta.action}</Button>{focus && attention.length > 1 && <Button onClick={() => queue("attention")}>还有 {attention.length - 1} 项待处理</Button>}</Space>
      </div>
      <div className="vela-release-lane" aria-label="发布流水线">
        <div className="vela-release-lane-heading"><Text strong>发布流水线</Text><Text type="secondary">{records.length} 次构建</Text></div>
        <ol>{metrics.map(([title, value, filter], index) => <li key={title} className={value ? "has-value" : ""}><span className="vela-lane-marker" aria-hidden="true">{index + 1}</span><Button type="text" aria-label={`${title}：${value}，查看队列`} onClick={() => queue(filter)}><span>{title}</span><strong>{value}</strong></Button></li>)}</ol>
        <div className="vela-resource-line"><span>产品定义 <strong>{products.length}</strong></span><span>Runner <strong>{runners.length}</strong></span><span>上传制品 <strong>{uploads.length}</strong></span></div>
      </div>
    </section>
    <section className="vela-recent" aria-labelledby="recent-title"><div className="vela-section-heading"><div><Title level={2} id="recent-title">最近动态</Title><Text type="secondary">按最近更新时间排列</Text></div><Button type="link" onClick={() => queue("all")}>查看全部构建</Button></div>{builds.length ? <ul className="vela-build-stream">{builds.slice(0, 6).map((build) => <li key={build.id}><span className={`vela-build-pulse is-${build.status}`} aria-hidden="true" /><button type="button" className="vela-build-identity" onClick={() => inspect(build)} aria-label={`查看构建 ${build.id}`}><strong>{products.find((item) => item.id === build.productId)?.name || build.productId}</strong><span className="vela-build-meta"><span>{build.id}</span><span>{build.target || "—"} / {build.profile || "—"}</span><span>{build.version || "—"}</span></span></button><span className="vela-build-stream-status">{buildStatus(build)}</span><time dateTime={build.updatedAt || ""}>{when(build.updatedAt)}</time></li>)}</ul> : <Empty description="还没有构建记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />}</section>
  </div>;
}
function CreateForm({ form, setForm, products, product, selectProduct, upload, create, auth, busy }) {
  const [step, setStep] = useState(0);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const patch = (key, value) => setForm((x) => ({ ...x, [key]: value }));
  const components = product?.components || [];
  const target = <Card title="构建目标"><div className="vela-form-grid"><Form.Item label="产品"><Select value={form.productId || undefined} options={products.map((x) => ({ value: x.id, label: x.name || x.id }))} onChange={(id) => selectProduct(products.find((x) => x.id === id))} /></Form.Item><Form.Item label="目标平台"><Select value={form.target} options={(product?.targets || []).map((x) => ({ value: x }))} onChange={(v) => patch("target", v)} /></Form.Item><Form.Item label="Profile"><Select value={form.profile} options={(product?.profiles || []).map((x) => ({ value: x }))} onChange={(v) => patch("profile", v)} /></Form.Item><Form.Item label="客户标识"><Input placeholder="可选，例如 city-bank" value={form.customer} onChange={(e) => patch("customer", e.target.value)} /></Form.Item><Form.Item label="构建版本" className="is-wide"><Input value={form.version} onChange={(e) => patch("version", e.target.value)} /></Form.Item></div></Card>;
  const config = <Card title="运行配置"><div className="vela-form-grid"><Form.Item label="安装目录"><Input value={form.installDir} onChange={(e) => patch("installDir", e.target.value)} /></Form.Item><Form.Item label="Web 端口"><Input value={form.webPort} onChange={(e) => patch("webPort", e.target.value)} /></Form.Item><Form.Item label="服务版本"><Input value={form.serviceVersion} onChange={(e) => patch("serviceVersion", e.target.value)} /></Form.Item><Form.Item label="客户名称"><Input value={form.customerName} onChange={(e) => patch("customerName", e.target.value)} /></Form.Item><Form.Item label="启用模块" className="is-wide"><Select mode="multiple" placeholder="沿用产品默认模块" value={Object.keys(form.modules).filter((x) => form.modules[x])} options={Object.keys(product?.config?.modules || {}).map((x) => ({ value: x }))} onChange={(v) => patch("modules", Object.fromEntries(v.map((x) => [x, true])))} /></Form.Item></div></Card>;
  const component = <Card title="组件来源"><Text type="secondary">只替换本次构建需要变更的组件；其余组件继续使用产品定义中的锁定来源。</Text>{components.length ? <div className="vela-component-list">{components.map((c) => <div className="vela-component-row" key={c.id}><div><Text strong>{c.id}</Text><Text type="secondary">{c.required ? "必需组件" : "可选组件"} / {c.source?.kind || "默认来源"}</Text></div><Upload beforeUpload={(file) => upload(file, c.id)} showUploadList={false}><Button icon={<UploadOutlined />} aria-label={`上传 ${c.id} 组件文件`}>替换文件</Button></Upload></div>)}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该产品没有可替换组件" />}</Card>;
  const confirm = <Card title="提交前确认"><Descriptions column={1} size="small"><Descriptions.Item label="产品">{product?.name || form.productId || "未选择"}</Descriptions.Item><Descriptions.Item label="构建目标">{form.target} / {form.profile}</Descriptions.Item><Descriptions.Item label="版本">{form.version || "未填写"}</Descriptions.Item><Descriptions.Item label="安装目录">{form.installDir || "使用产品默认值"}</Descriptions.Item><Descriptions.Item label="组件覆盖">{form.componentOverridesText === "{}" ? "无" : "已配置，将在服务端校验"}</Descriptions.Item></Descriptions><Alert className="vela-inline-alert" type="info" showIcon message="提交后进入审批队列" description="审批、Preview 验证和 Release 门禁均由服务端继续校验。" /></Card>;
  const content = [target, config, component, confirm][step];
  return <div className="vela-create-workspace"><Form layout="vertical" className="vela-create-form"><Steps size="small" current={step} onChange={setStep} items={[{ title: "目标" }, { title: "配置" }, { title: "组件" }, { title: "确认" }]} /><div key={step} className="vela-form-step">{content}</div><div className="vela-step-tools"><Space wrap><Button disabled={step === 0} onClick={() => setStep(step - 1)}>上一步</Button><Button onClick={() => setAdvancedOpen(true)}>高级 JSON</Button></Space><Space wrap>{step < 3 && <Button type="primary" onClick={() => setStep(step + 1)}>下一步</Button>}<GuardedButton auth={auth} permission="build.create" title="提交构建申请" type={step === 3 ? "primary" : "default"} loading={busy === "create"} onClick={create}>提交构建申请</GuardedButton></Space></div><Drawer title="高级构建 JSON" open={advancedOpen} onClose={() => setAdvancedOpen(false)} width="min(560px, 100vw)"><Alert type="warning" showIcon message="高级配置会覆盖表单生成的同名字段" style={{ marginBottom: 16 }} /><Form.Item label="构建变量 JSON"><Input.TextArea aria-label="构建变量 JSON" rows={8} value={form.variablesText} onChange={(e) => patch("variablesText", e.target.value)} /></Form.Item><Form.Item label="组件覆盖 JSON"><Input.TextArea aria-label="组件覆盖 JSON" rows={10} value={form.componentOverridesText} onChange={(e) => patch("componentOverridesText", e.target.value)} /></Form.Item></Drawer></Form><aside className="vela-create-context" aria-label="当前构建配置"><div className="vela-context-heading"><Title level={2}>本次构建</Title><Tag>{step + 1} / 4</Tag></div><Descriptions column={1} size="small"><Descriptions.Item label="产品">{product?.name || form.productId || "未选择"}</Descriptions.Item><Descriptions.Item label="目标">{form.target} / {form.profile}</Descriptions.Item><Descriptions.Item label="版本">{form.version || "未填写"}</Descriptions.Item><Descriptions.Item label="发布通道">Preview</Descriptions.Item></Descriptions><div className="vela-create-guidance"><Text strong>{["选择打包目标", "检查运行配置", "按需替换组件", "准备提交审批"][step]}</Text><p>{["产品定义决定可用目标与 Profile。构建将使用所选产品的默认配置。", "配置仅作用于本次构建，不会更改产品 Manifest。Web 端口范围为 1–65535。", "上传组件后会写入当前构建的组件覆盖；未覆盖的组件沿用产品定义。", "提交后进入审批队列。审批与发布门禁仍由服务端校验。"][step]}</p></div></aside></div>;
}
function BuildTable({ builds, inspect, runAction, download, auth, allBuilds, buildFilter, setBuildFilter, busy, compact = false }) {
  const filterOptions = [
    ["all", "全部"], ["attention", "待处理"], ["active", "执行中"], ["preview", "待验证"], ["validated", "已验证"], ["releaseReady", "可发布"], ["blocked", "发布阻塞"],
  ];
  const count = (key) => (allBuilds || builds).filter((b) => {
    const ready = Boolean(b.releaseCheckSummary?.releaseReady || b.releaseReady);
    return key === "all" || (key === "attention" && ["pending_approval", "failed", "canceled"].includes(b.status)) || (key === "active" && ["queued", "running"].includes(b.status)) || (key === "preview" && b.status === "succeeded") || (key === "validated" && b.status === "preview_validated") || (key === "releaseReady" && ready) || (key === "blocked" && b.status === "preview_validated" && !ready);
  }).length;
  const columns = [
    { title: "构建", dataIndex: "id", render: (v, r) => <Button type="link" title={v} onClick={() => inspect(r)}>{v}</Button> },
    { title: "产品 / 版本", render: (_, r) => <Space direction="vertical" size={0}><Text strong>{r.productId}</Text><Text type="secondary">{r.version}</Text></Space> },
    { title: "目标", render: (_, r) => `${r.target || "—"} / ${r.profile || "—"}` },
    { title: "状态", render: (_, r) => buildStatus(r) },
    { title: "发布门禁", render: (_, r) => r.releaseCheckSummary?.releaseReady || r.releaseReady ? <Tag color="success">可发布</Tag> : r.status === "preview_validated" ? <Tag color="warning">{r.releaseCheckSummary?.blockingCount || 1} 阻塞</Tag> : "—" },
    { title: "Runner", dataIndex: "runnerId", render: (v) => v || "—" },
    { title: "更新时间", dataIndex: "updatedAt", render: when },
    { title: "操作", fixed: "right", width: 190, render: (_, r) => {
      const items = buildActionItems(r, runAction, Boolean(busy), Boolean(r.releaseCheckSummary?.releaseReady || r.releaseReady))
        .map((item) => ({ ...item, disabled: item.disabled || !can(auth, item.key === "publish" ? "build.publish" : item.key === "approve" ? "build.approve" : item.key === "reject" ? "build.reject" : item.key === "validate" ? "build.validate" : item.key === "cancel" ? "build.cancel" : "build.retry") }));
      return <Space><Button size="small" onClick={() => inspect(r)}>详情</Button>{items.length > 0 && <Dropdown menu={{ items }}><Button size="small">操作</Button></Dropdown>}{(r.downloadUrl || r.artifactUri) && <Button size="small" onClick={() => download(r)}>下载</Button>}</Space>;
    } },
  ];
  const activeLabel = filterOptions.find(([key]) => key === buildFilter)?.[1] || "全部";
  return <div className="vela-build-table">{setBuildFilter && <div className="vela-data-toolbar"><div><Text strong>构建队列</Text><Text type="secondary">当前显示 {builds.length} 个构建，筛选：{activeLabel}</Text></div><Space wrap className="vela-build-filters" role="group" aria-label="构建状态筛选">{filterOptions.map(([key, label]) => <Button key={key} aria-pressed={buildFilter === key} type={buildFilter === key ? "primary" : "text"} size="small" onClick={() => setBuildFilter(key)}>{label} <span>{count(key)}</span></Button>)}</Space></div>}<div className="vela-table-surface"><Table size="small" rowKey="id" scroll={{ x: 900 }} dataSource={builds} columns={columns} locale={{ emptyText: <Empty description="当前筛选下没有构建；可切换状态或清除搜索条件" image={Empty.PRESENTED_IMAGE_SIMPLE} /> }} pagination={compact ? false : { pageSize: 12, showSizeChanger: false, showTotal: (total) => `共 ${total} 个构建` }} /></div></div>;
}
function RunnerTable({ runners, builds, inspect }) {
  const online = runners.filter((runner) => !["offline", "disabled"].includes(runner.status)).length;
  const busyRunners = new Set(builds.filter((build) => ["queued", "running"].includes(build.status)).map((build) => build.runnerId).filter(Boolean));
  const columns = [
    { title: "Runner", dataIndex: "id", render: (value, runner) => <Space direction="vertical" size={0}><Text strong>{value}</Text><Text type="secondary">{runner.name || runner.hostname || "执行节点"}</Text></Space> },
    { title: "状态", dataIndex: "status", render: (value) => <Badge status={value === "offline" ? "error" : value === "disabled" ? "default" : "success"} text={value === "offline" ? "离线" : value === "disabled" ? "已停用" : "在线"} /> },
    { title: "当前构建", render: (_, runner) => { const build = builds.find((item) => item.runnerId === runner.id && ["queued", "running"].includes(item.status)); return build ? <Button type="link" onClick={() => inspect(build)}>{build.id}</Button> : <Text type="secondary">空闲</Text>; } },
    { title: "能力", dataIndex: "capabilities", render: (value) => <Space wrap size={[4, 4]}>{Array.isArray(value) && value.length ? value.map((item) => <Tag key={item}>{item}</Tag>) : <Text type="secondary">未上报</Text>}</Space> },
    { title: "最后心跳", dataIndex: "lastHeartbeatAt", render: when },
  ];
  return <div className="vela-resource-page"><section className="vela-plain-metrics" aria-label="Runner 概览"><div><Text type="secondary">已注册</Text><strong>{runners.length}</strong></div><div><Text type="secondary">在线</Text><strong>{online}</strong></div><div><Text type="secondary">执行任务</Text><strong>{busyRunners.size}</strong></div><div><Text type="secondary">可调度</Text><strong>{Math.max(online - busyRunners.size, 0)}</strong></div></section><div className="vela-section-heading"><div><Title level={2}>执行节点</Title><Text type="secondary">心跳与能力由 Runner 主动上报</Text></div></div><div className="vela-table-surface"><Table size="small" rowKey="id" dataSource={runners} columns={columns} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有 Runner 注册；启动 Runner 后将在这里显示心跳" /> }} pagination={false} /></div></div>;
}
function Uploads({ uploads, upload, form, auth }) {
  const [policy, setPolicy] = useState("platform"), [retention, setRetention] = useState(30), [purpose, setPurpose] = useState("manual-upload");
  const doUpload = async (file) => {
    if (!can(auth, "upload.write")) return false;
    return upload(file, "", { downloadPolicy: policy, retentionDays: retention, purpose });
  };
  const columns = [{ title: "制品", render: (_, record) => <Space direction="vertical" size={0}><Text strong>{record.fileName || record.name || record.id}</Text><Text type="secondary">{record.id}</Text></Space> }, { title: "SHA256", dataIndex: "sha256", render: (value) => <code className="vela-digest">{value || "—"}</code> }, { title: "保留期限", render: (_, record) => record.retentionStatus === "expired" ? <Tag color="error">已过期</Tag> : record.retentionDays ? `${record.retentionDays} 天` : record.retentionUntil ? when(record.retentionUntil) : "—" }, { title: "下载策略", dataIndex: "downloadPolicy", render: (value) => ({ platform: "平台内", restricted: "受限", public: "公开" }[value] || value || "—") }, { title: "创建时间", dataIndex: "createdAt", render: when }, { title: "操作", render: (_, record) => <Button disabled={record.retentionStatus === "expired" || !can(auth, record.downloadPolicy === "restricted" ? "download.restricted" : "download.platform")} onClick={() => downloadFile(record.downloadUrl || `${API_URL}/api/uploads/${record.id}/download`, record.fileName || record.name || record.id)}>下载</Button> }];
  return <div className="vela-resource-page"><section className="vela-upload-workbench"><div className="vela-upload-settings"><Title level={2}>上传设置</Title><Text type="secondary">上传前确定访问范围、保留期限和用途。策略会写入制品记录。</Text><Form layout="vertical"><div className="vela-form-grid"><Form.Item label="下载策略"><Select value={policy} onChange={setPolicy} options={[{ value: "platform", label: "平台内下载" }, { value: "restricted", label: "受限下载" }, { value: "public", label: "公开链接" }]} /></Form.Item><Form.Item label="保留天数"><Input type="number" min={1} aria-label="保留天数" value={retention} onChange={(e) => setRetention(e.target.value)} /></Form.Item><Form.Item className="is-wide" label="用途"><Input aria-label="用途" value={purpose} onChange={(e) => setPurpose(e.target.value)} /></Form.Item></div></Form></div><Upload.Dragger className="vela-upload-drop" beforeUpload={doUpload} showUploadList={false} disabled={!can(auth, "upload.write")}><UploadOutlined /><p>拖入安装包或组件制品</p><Text type="secondary">当前关联产品：{form.productId || "未选择"}</Text><div><Button icon={<UploadOutlined />} disabled={!can(auth, "upload.write")}>选择文件上传</Button></div></Upload.Dragger></section><div className="vela-section-heading"><div><Title level={2}>制品记录</Title><Text type="secondary">共 {uploads.length} 个上传记录；过期制品不可下载</Text></div></div><div className="vela-table-surface"><Table size="small" rowKey="id" scroll={{ x: 860 }} dataSource={uploads} columns={columns} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有上传制品" /> }} /></div></div>;
}
function validateManifest(data) { if (!data.id || !Array.isArray(data.targets) || !data.targetConfig || typeof data.targetConfig !== "object") throw new Error("Manifest 缺少 id、targets 或 targetConfig"); for (const target of data.targets) if (!data.targetConfig[target]) throw new Error(`Manifest 缺少 targetConfig.${target}`); return data; }
function Manifest({ products, product, selectProduct, manifest, setManifest, savedManifest, revisions, saveManifest, manifestDryRun, dryRun, busy, auth }) {
  const dirty = manifest !== savedManifest;
  return <div className="vela-editor-layout"><aside className="vela-resource-nav"><div className="vela-resource-nav-heading"><div><Text strong>产品定义</Text><Text type="secondary">{products.length} 个产品</Text></div></div><List dataSource={products} locale={{ emptyText: "没有产品定义" }} renderItem={(item) => <List.Item className={item.id === product?.id ? "is-active" : ""}><button type="button" onClick={() => selectProduct(item)}><strong>{item.name || item.id}</strong><span>{item.id} / {(item.targets || []).join(", ") || "无目标"}</span></button></List.Item>} /></aside><main className="vela-editor-main"><div className="vela-editor-toolbar"><div><Space><Title level={2}>Manifest</Title>{dirty && <Tag color="warning">未保存</Tag>}</Space><Text type="secondary">{product?.name || "请选择产品"} / JSON 产品定义</Text></div><Space wrap><Button loading={busy === "dryrun"} onClick={manifestDryRun}>试运行</Button><Button onClick={() => { if (dirty && !window.confirm("确认丢弃未保存修改并重新加载 Manifest")) return; selectProduct(product); }}>重新加载</Button><GuardedButton auth={auth} permission="manifest.write" type="primary" loading={busy === "manifest"} onClick={saveManifest}>保存 Manifest</GuardedButton></Space></div><Input.TextArea className="vela-json-editor" aria-label="Manifest JSON" rows={22} value={manifest} onChange={(e) => setManifest(e.target.value)} />{dryRun && <Alert className="vela-editor-result" type={dryRun.valid ? "success" : "warning"} showIcon message={dryRun.valid ? "试运行通过" : "试运行未通过"} description={dryRun.valid ? "当前定义可以生成构建锁。" : <pre>{asJson(dryRun)}</pre>} />}<section className="vela-revision-list"><div className="vela-section-heading"><div><Title level={2}>版本记录</Title><Text type="secondary">恢复操作会生成新的当前版本</Text></div></div><List dataSource={revisions} locale={{ emptyText: "还没有历史版本" }} renderItem={(revision) => <List.Item actions={[<Popconfirm key="restore" title="确认恢复此历史版本？" onConfirm={() => restoreProductManifestRevision(product.id, revision.id || revision.revisionId).then(() => selectProduct(product))}><Button size="small">恢复</Button></Popconfirm>]}><List.Item.Meta title={revision.id || revision.revision} description={when(revision.createdAt)} /></List.Item>} /></section></main></div>;
}
function Overlays({ product, overlay, auth }) {
  const names = overlay.overlays[overlay.overlayKind] || [];
  const dirty = overlay.overlayText !== overlay.savedOverlay;
  const startNew = () => { overlay.setOverlayName(""); overlay.setOverlayText("{}"); };
  const changeKind = (kind) => {
    const first = overlay.overlays[kind]?.[0];
    if (first) overlay.loadOverlay(kind, first.name);
    else { overlay.setOverlayKind(kind); overlay.setOverlayName(""); overlay.setOverlayText("{}"); }
  };
  return <div className="vela-editor-layout"><aside className="vela-resource-nav"><Tabs activeKey={overlay.overlayKind} onChange={changeKind} items={[{ key: "profiles", label: "Profile" }, { key: "customers", label: "客户" }]} /><Button block onClick={startNew}>新建覆盖</Button><List dataSource={names} locale={{ emptyText: "当前分类没有覆盖配置" }} renderItem={(item) => <List.Item className={item.name === overlay.overlayName ? "is-active" : ""}><button type="button" onClick={() => overlay.loadOverlay(overlay.overlayKind, item.name)}><strong>{item.name}</strong><span>{overlay.overlayKind === "profiles" ? "运行环境" : "客户配置"}</span></button></List.Item>} /></aside><main className="vela-editor-main"><div className="vela-editor-toolbar"><div><Space><Title level={2}>{overlay.overlayName || "新建覆盖"}</Title>{dirty && <Tag color="warning">未保存</Tag>}</Space><Text type="secondary">{product?.name || product?.id || "未选择产品"} / {overlay.overlayKind === "profiles" ? "Profile" : "客户"}</Text></div><GuardedButton auth={auth} permission="overlay.write" type="primary" onClick={overlay.saveOverlay}>保存覆盖</GuardedButton></div><Form layout="vertical"><Form.Item label="名称" extra="可使用字母、数字、点、下划线和连字符"><Input value={overlay.overlayName} onChange={(e) => overlay.setOverlayName(e.target.value)} /></Form.Item><Form.Item label="覆盖配置 JSON"><Input.TextArea className="vela-json-editor" aria-label="覆盖配置 JSON" rows={22} value={overlay.overlayText} onChange={(e) => overlay.setOverlayText(e.target.value)} /></Form.Item></Form><Alert type="info" showIcon message="覆盖只改变匹配项" description="未声明的字段继续继承产品 Manifest；最终配置会在创建构建时合并并校验。" /></main></div>;
}
function Audit({ audit: events, auditFilter, setAuditFilter, loadAudit }) {
  const set = (key, value) => setAuditFilter((current) => ({ ...current, [key]: value }));
  const clear = () => setAuditFilter({ action: "", resourceType: "", resourceId: "", buildId: "", productId: "", uploadId: "" });
  const actors = new Set(events.map((event) => event.actor).filter(Boolean)).size;
  const columns = [{ title: "时间", dataIndex: "createdAt", render: when }, { title: "操作", dataIndex: "action", render: (value) => <code>{value || "—"}</code> }, { title: "资源", render: (_, record) => <Space direction="vertical" size={0}><Text>{record.resourceType || "—"}</Text><Text type="secondary">{record.resourceId || "—"}</Text></Space> }, { title: "操作者", dataIndex: "actor", render: (value) => value || "—" }, { title: "证据摘要", dataIndex: "evidenceDigest", render: (value) => <code className="vela-digest">{value || "—"}</code> }];
  return <div className="vela-audit-page"><section className="vela-query-workbench"><div className="vela-query-heading"><div><Title level={2}>定位审计事件</Title><Text type="secondary">可组合多个条件；空条件返回最近 100 条记录。</Text></div><Space><Button onClick={clear}>清空筛选</Button><Button type="primary" onClick={loadAudit}>查询事件</Button></Space></div><div className="vela-query-grid"><Form.Item label="动作"><Input placeholder="例如 build.download" value={auditFilter.action} onChange={(e) => set("action", e.target.value)} /></Form.Item><Form.Item label="资源类型"><Input placeholder="build / upload" value={auditFilter.resourceType} onChange={(e) => set("resourceType", e.target.value)} /></Form.Item><Form.Item label="资源 ID"><Input placeholder="精确资源 ID" value={auditFilter.resourceId} onChange={(e) => set("resourceId", e.target.value)} /></Form.Item><Form.Item label="构建 ID"><Input placeholder="build..." value={auditFilter.buildId} onChange={(e) => set("buildId", e.target.value)} /></Form.Item><Form.Item label="产品 ID"><Input placeholder="例如 lsap" value={auditFilter.productId} onChange={(e) => set("productId", e.target.value)} /></Form.Item><Form.Item label="上传 ID"><Input placeholder="upload..." value={auditFilter.uploadId} onChange={(e) => set("uploadId", e.target.value)} /></Form.Item></div></section><section className="vela-audit-summary"><div><Text type="secondary">当前事件</Text><strong>{events.length}</strong></div><div><Text type="secondary">涉及操作者</Text><strong>{actors}</strong></div><div><Text type="secondary">查询范围</Text><strong>最近 100 条</strong></div><Button icon={<DownloadOutlined />} onClick={async () => downloadJson(await exportAuditArchive({ ...auditFilter, limit: 100 }), "audit-evidence.json")}>导出审计归档</Button></section><div className="vela-section-heading"><div><Title level={2}>审计事件</Title><Text type="secondary">按服务端返回顺序展示</Text></div></div><div className="vela-table-surface"><Table size="small" rowKey="id" scroll={{ x: 820 }} dataSource={events} columns={columns} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有匹配的审计事件；调整条件后重新查询" /> }} /></div></div>;
}
function LogsWorkspace({ builds }) {
  const [selectedId, setSelectedId] = useState(builds[0]?.id || "");
  const [data, setData] = useState({ logs: [], lock: null, release: null, reproducibility: null });
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState("");
  const build = builds.find((item) => item.id === selectedId) || builds[0];
  useEffect(() => {
    if (!build?.id) return;
    let active = true;
    setLoading(true); setLoadError("");
    Promise.all([getBuildLogs(build.id), getBuildLock(build.id), getBuildReleaseCheck(build.id), getBuildReproducibilityReport(build.id)])
      .then(([logs, lock, release, reproducibility]) => { if (active) setData({ logs, lock, release, reproducibility }); })
      .catch((error) => { if (active) setLoadError(errorText(error)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [build?.id]);
  if (!builds.length) return <Empty description="还没有构建，创建构建后可在这里查看日志与锁定配置" />;
  return <div className="vela-logs-workspace"><aside className="vela-log-builds"><div><Text strong>选择构建</Text><Text type="secondary">{builds.length} 个构建记录</Text></div><Select value={build?.id} onChange={setSelectedId} options={builds.map((item) => ({ value: item.id, label: `${item.productId} / ${item.id}` }))} /><List dataSource={builds.slice(0, 10)} renderItem={(item) => <List.Item className={item.id === build?.id ? "is-active" : ""}><button type="button" onClick={() => setSelectedId(item.id)}><span>{buildStatus(item)}</span><strong>{item.id}</strong><small>{item.target || "—"} / {item.profile || "—"}</small></button></List.Item>} /></aside><main className="vela-log-main"><div className="vela-log-heading"><div><Space>{buildStatus(build)}<Title level={2}>{build.id}</Title></Space><Text type="secondary">{build.productId} / {build.target || "—"} / {build.profile || "—"} / {build.version || "—"}</Text></div><Button icon={<DownloadOutlined />} onClick={async () => downloadJson(await exportBuildReleaseEvidenceArchive(build.id), `${build.id}-evidence.json`)}>导出证据</Button></div>{loadError && <Alert type="error" showIcon message="构建上下文加载失败" description={loadError} />}{loading ? <div className="vela-loading"><Spin /><Text type="secondary">加载构建上下文</Text></div> : <LogPanel build={build} data={data} />}</main></div>;
}
function LogPanel({ build, data }) {
  const logs = Array.isArray(data.logs) ? data.logs : data.logs?.items || [];
  const [filter, setFilter] = useState("");
  const lines = logs.map((item) => typeof item === "string" ? item : item.message || asJson(item)).map(stripAnsi).filter((line) => line.toLowerCase().includes(filter.toLowerCase()));
  const code = (value, label) => <pre className="vela-code" aria-label={label}>{asJson(value)}</pre>;
  const items = [
    { key: "logs", label: `日志 ${lines.length}`, children: <div className="vela-log-panel"><div className="vela-log-tools"><Input allowClear placeholder="过滤当前日志" value={filter} onChange={(e) => setFilter(e.target.value)} /><Button title="复制当前筛选后的日志" onClick={() => navigator.clipboard?.writeText(lines.join("\n"))}>复制日志</Button><a href={build ? buildLogStreamUrl(build.id) : "#"} target="_blank" rel="noreferrer">打开实时日志流</a></div>{lines.length ? <pre className="vela-code vela-log-output">{lines.map((line, index) => <div key={index} title={line}>{line}</div>)}</pre> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={filter ? "没有匹配的日志行" : "当前构建还没有日志"} />}</div> },
    { key: "lock", label: "构建锁", children: code(data.lock, "构建锁 JSON") },
    { key: "components", label: "组件", children: code(data.lock?.components || [], "组件锁定 JSON") },
    { key: "workloads", label: "工作负载", children: code(data.lock?.workloads || [], "工作负载 JSON") },
    { key: "release", label: "发布门禁", children: code(data.release, "发布门禁 JSON") },
    { key: "reproducibility", label: "可复现报告", children: code(data.reproducibility, "可复现报告 JSON") },
  ];
  return <Tabs className="vela-log-tabs" items={items} />;
}
function BuildDrawer({ build, data, open, onClose, onDownload }) {
  const release = data.release || {};
  const releaseReady = Boolean(release.releaseReady || build?.releaseCheckSummary?.releaseReady || build?.releaseReady);
  const releaseItems = (release.checks || []).map((item) => ({
    key: item.id,
    label: item.stage === "release" ? "Release 门禁" : "Preview 门禁",
    children: <div className="vela-gate-row"><Tag color={item.status === "blocked" ? "error" : "success"}>{item.status === "blocked" ? "阻塞" : "通过"}</Tag><div><Text strong>{item.title}</Text><Text type="secondary">{item.reason || "检查通过"}</Text></div></div>,
  }));
  return <Drawer title="构建详情" width="min(820px, 100vw)" open={open} onClose={onClose} extra={build && <Button icon={<DownloadOutlined />} disabled={!build.downloadUrl && !build.artifactUri} onClick={() => onDownload(build)}>下载制品</Button>}>{build && <><section className="vela-drawer-summary"><div><Space>{buildStatus(build)}<Title level={2}>{build.id}</Title></Space><Text type="secondary">{build.productId} / {build.version || "—"}</Text></div><Descriptions column={2} size="small"><Descriptions.Item label="构建 ID">{build.id}</Descriptions.Item><Descriptions.Item label="目标">{build.target} / {build.profile}</Descriptions.Item><Descriptions.Item label="Runner">{build.runnerId || "尚未分配"}</Descriptions.Item><Descriptions.Item label="更新时间">{when(build.updatedAt)}</Descriptions.Item></Descriptions></section><Tabs className="vela-detail-tabs" items={[{ key: "logs", label: "日志", children: <LogPanel build={build} data={data} /> }, { key: "release", label: releaseReady ? "发布门禁（通过）" : "发布门禁", children: releaseItems.length ? <div className="vela-gate-list">{releaseItems.map((item) => <div key={item.key}>{item.children}</div>)}</div> : <pre className="vela-code">{asJson(release)}</pre> }, { key: "components", label: "组件锁定", children: <Table size="small" rowKey={(item) => item.id} dataSource={data.lock?.components || []} pagination={false} columns={[{ title: "组件", dataIndex: "id" }, { title: "版本", dataIndex: "version" }, { title: "URI", dataIndex: "uri", render: (v) => <code>{v || "—"}</code> }, { title: "SHA256", dataIndex: "sha256", render: (v) => <code>{v || "—"}</code> }]} /> }, { key: "workloads", label: "工作负载", children: <pre className="vela-code">{asJson(data.lock?.workloads || [])}</pre> }, { key: "config", label: "最终配置", children: <pre className="vela-code">{asJson(build.finalConfig || data.lock?.config || {})}</pre> }, { key: "lock", label: "构建锁", children: <pre className="vela-code">{asJson(data.lock)}</pre> }, { key: "repro", label: "可复现报告", children: <pre className="vela-code">{asJson(data.reproducibility)}</pre> }, { key: "evidence", label: "证据", children: <div className="vela-evidence-action"><Text>导出包含门禁、锁定配置和可复现报告的 Release 证据。</Text><Button type="primary" onClick={() => exportBuildReleaseEvidenceArchive(build.id).then((value) => downloadJson(value, `${build.id}-evidence.json`))}>导出 Release 证据</Button></div> }]} /></>}</Drawer>;
}
