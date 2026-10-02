import { useEffect, useMemo, useState } from "react";
import {
  Alert, Badge, Button, Card, Col, Descriptions, Drawer, Dropdown, Empty, Form,
  Input, Layout, List, Menu, Modal, Popconfirm, Row, Select, Space, Spin,
  Table, Tabs, Tag, Tooltip, Typography, Upload, Steps, message,
} from "antd";
import {
  AuditOutlined, BellOutlined, BuildOutlined, CloudUploadOutlined, CodeOutlined,
  DashboardOutlined, DownloadOutlined, FileTextOutlined, HistoryOutlined,
  InfoCircleOutlined, QuestionCircleOutlined, SafetyCertificateOutlined,
  MenuFoldOutlined, MenuUnfoldOutlined, PlusOutlined, RobotOutlined,
  SettingOutlined, UploadOutlined, WarningOutlined,
} from "@ant-design/icons";
import {
  API_URL, approveBuild, buildDownloadUrl, buildLogStreamUrl, cancelBuild,
  completeOIDCRedirect, createBuild, downloadFile, dryRunProductManifest,
  exportAuditArchive, exportBuildReleaseEvidenceArchive, getAuthState, getBuildLock, getHealth,
  oidcLoginConfigured, startOIDCLogin, logoutAuth, clearAuthState, setAuthState,
  getBuildLogs, getBuildReleaseCheck, getBuildReproducibilityReport,
  getProductManifest, getProductOverlay, listAuditEvents, listBuilds,
  listNotifications, listProductManifestRevisions, listProductOverlays, listProducts,
  listRunners, listUploads, markAllNotificationsRead, markNotificationRead,
  publishBuild, rejectBuild, restoreProductManifestRevision, retryBuild,
  updateProductManifest, updateProductOverlay, uploadArtifact, validatePreviewBuild,
} from "./lib/api";
import { translateError, translateErrorDetail, translateUserAction } from "./lib/i18n";
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
const statusDescriptions = {
  pending_approval: "构建输入已解析并生成 BuildLock，等待操作员审批。",
  queued: "后端已接受审批并提交调度，正在等待 Runner 领取任务。",
  running: "Runner 已领取任务并持续回传日志与心跳。",
  succeeded: "Preview 制品已生成，下一步需要验证安装与运行结果。",
  preview_validated: "Preview 已验证；是否能够发布仍以 Release 门禁结果为准。",
  published: "服务端已将经过验证的 Preview 提升为 Release。",
  rejected: "审批已拒绝；服务端允许重新审批后再次进入队列。",
  failed: "构建、调度或 Runner 执行失败；查看日志后可重试。",
  canceled: "构建已取消；服务端允许重新排队。",
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
function Hint({ title, label = "查看说明" }) { return <Tooltip title={title}><span className="vela-hint" role="img" tabIndex={0} aria-label={label}><QuestionCircleOutlined /></span></Tooltip>; }
function FieldLabel({ children, tip }) { return <span className="vela-field-label">{children}{tip && <Hint title={tip} label="查看字段说明" />}</span>; }
function ContextNote({ title, children, tone = "info" }) { return <div className={`vela-context-note is-${tone}`}><span aria-hidden="true">{tone === "warning" ? <WarningOutlined /> : tone === "secure" ? <SafetyCertificateOutlined /> : <InfoCircleOutlined />}</span><div><Text strong>{title}</Text><Text type="secondary">{children}</Text></div></div>; }
function ErrorBanner({ error, onClose }) {
  if (!error) return null;
  const isStructured = typeof error === "object";
  const messageText = isStructured ? errorText(error) : String(error);
  const action = isStructured ? translateUserAction(error) : "";
  const details = isStructured && Array.isArray(error.details) ? error.details.map(translateErrorDetail) : [];
  const metadata = isStructured ? [error.code, error.retryable ? "可重试" : "不可自动重试", error.requestId && `Request ${error.requestId}`, error.correlationId && `Correlation ${error.correlationId}`].filter(Boolean) : [];
  return <Alert className="vela-alert vela-error-banner" type={error?.severity === "warning" ? "warning" : "error"} showIcon closable onClose={onClose} message={messageText} description={<div className="vela-error-detail">{action && <Text strong>{action}</Text>}{details.length > 0 && <ul>{details.map((detail, index) => <li key={`${detail.code || detail.field || index}`}><strong>{detail.title || detail.field || "检查项"}</strong>{detail.message ? `：${detail.message}` : ""}</li>)}</ul>}{metadata.length > 0 && <div className="vela-error-meta">{metadata.map((item) => <code key={item}>{item}</code>)}</div>}</div>} />;
}
function asJson(value) { return JSON.stringify(value ?? {}, null, 2); }
function parseObject(text, label) {
  let value;
  try { value = JSON.parse(text || "{}"); } catch { throw new Error(`${label} JSON 解析错误`); }
  if (!value || Array.isArray(value) || typeof value !== "object") throw new Error(`${label} 必须是 JSON 对象`);
  return value;
}
function buildStatus(build) { const [label, color] = statusMeta[build?.status] || [build?.status || "未知", "default"]; return <Tooltip title={statusDescriptions[build?.status] || "服务端返回的当前构建状态。"}><Tag color={color}>{label}</Tag></Tooltip>; }
function releaseGateStatus(build) {
  const summary = build?.releaseCheckSummary || {};
  if (summary.releaseReady || build?.releaseReady) return <Tooltip title="服务端 Release 门禁已通过；发布操作仍需要管理员权限。"><Tag color="success">可发布</Tag></Tooltip>;
  if (build?.status !== "preview_validated") return <Text type="secondary">尚未进入 Release 检查</Text>;
  const reason = summary.firstBlockedReason || summary.firstBlockedTitle || "打开详情查看服务端 checks[] 返回的阻塞项。";
  return <Tooltip title={reason}><Tag color="warning">{summary.blockingCount || 1} 项阻塞</Tag></Tooltip>;
}
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
function formatBytes(value) { const bytes = Number(value); if (!Number.isFinite(bytes) || bytes < 0) return "—"; if (bytes < 1024) return `${bytes} B`; const units = ["KB", "MB", "GB", "TB"]; let size = bytes / 1024; let index = 0; while (size >= 1024 && index < units.length - 1) { size /= 1024; index += 1; } return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[index]}`; }
function stripAnsi(value) { return String(value ?? "").replace(/\x1B\[[0-?]*[ -/]*[@-~]/g, ""); }
function downloadJson(value, filename) { const url = URL.createObjectURL(new Blob([asJson(value)], { type: "application/json" })); const a = document.createElement("a"); a.href = url; a.download = filename; a.click(); URL.revokeObjectURL(url); }
function notificationStatus(severity) { return ({ critical: "error", error: "error", warning: "warning", info: "processing", success: "success" }[severity] || "default"); }

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
  const [health, setHealth] = useState({ state: "checking", checkedAt: "" });
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
    } catch (e) { setError(e); } finally { setLoading(false); }
  };
  useEffect(() => {
    let active = true;
    const check = async () => {
      try { const value = await getHealth(); if (active) setHealth({ state: "reachable", data: value, checkedAt: new Date().toISOString() }); }
      catch (healthError) { if (active) setHealth({ state: "unreachable", error: healthError, checkedAt: new Date().toISOString() }); }
    };
    check();
    const timer = window.setInterval(check, 30000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);
  useEffect(() => {
    refresh();
    completeOIDCRedirect()
      .then((result) => {
        if (!result) return;
        if (!result.ok) throw new Error(result.error || "OIDC 登录失败");
        setAuth({ ...getAuthState(), role: result.role || "viewer" });
        notice.success("OIDC 登录成功");
      })
      .catch((e) => setError(e));
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
    } catch (e) { setError(e); }
  }
  async function downloadBuild(build) {
    const permission = build.downloadPolicy === "restricted" ? "download.restricted" : "download.platform";
    if (!permitted(permission)) return;
    try {
      const url = build.downloadUrl || buildDownloadUrl(build.id);
      const filename = String(build.artifactUri || build.downloadUrl || `${build.id}.zip`).split(/[/?#]/).pop() || `${build.id}.zip`;
      await downloadFile(url, filename);
      notice.success("下载已开始");
    } catch (e) { setError(e); }
  }
  function payload() {
    const port = Number(form.webPort);
    if (!form.productId) throw new Error("请选择产品");
    if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error("Web 端口必须在 1 到 65535 之间");
    return { productId: form.productId, target: form.target, profile: form.profile, customer: form.customer, version: form.version, releaseChannel: form.releaseChannel, variables: parseObject(form.variablesText, "构建变量"), componentOverrides: parseObject(form.componentOverridesText, "组件覆盖"), configOverrides: { installDir: form.installDir, serviceVersion: form.serviceVersion, ports: { web: port }, customer: { name: form.customerName, license: form.license }, database: { passwordPolicy: form.passwordPolicy }, modules: form.modules } };
  }
  async function create() { if (!permitted("build.create")) return; setBusy("create"); try { const created = await createBuild(payload()); await refresh(); await inspect(created); setPage("builds"); notice.success("构建请求已创建，等待审批"); } catch (e) { setError(e); } finally { setBusy(""); } }
  async function inspect(build) {
    setDetail(build); setDetailOpen(true);
    try { const [logs, lock, release, reproducibility] = await Promise.all([getBuildLogs(build.id), getBuildLock(build.id), getBuildReleaseCheck(build.id), getBuildReproducibilityReport(build.id)]); setDetailData({ logs, lock, release, reproducibility }); } catch (e) { setError(e); }
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
      } catch (e) { setError(e); } finally { setBusy(""); }
    };
    if (dangerous) {
      Modal.confirm({ title: `确认${label}`, content: `此操作将影响构建 ${build.id}，服务端会再次校验权限。`, okType: "danger", onOk: execute });
      return;
    }
    await execute();
  }
  async function saveManifest() { if (!permitted("manifest.write")) return; setBusy("manifest"); try { const parsed = validateManifest(parseObject(manifest, "Manifest")); await updateProductManifest(form.productId, parsed); const text = asJson(parsed); setManifest(text); setSavedManifest(text); setRevisions(await listProductManifestRevisions(form.productId)); notice.success("Manifest 已保存"); } catch (e) { setError(e); } finally { setBusy(""); } }
  async function manifestDryRun() { setBusy("dryrun"); try { const output = await dryRunProductManifest(form.productId, { manifest: validateManifest(parseObject(manifest, "Manifest")), ...payload() }); setDryRun(output); } catch (e) { setError(e); } finally { setBusy(""); } }
  async function loadOverlay(kind, name) { if (!name) return; if (overlayText !== savedOverlay && !window.confirm("覆盖配置有未保存修改，确认丢弃草稿？")) return; try { const value = await getProductOverlay(form.productId, kind, name); const text = asJson(value.data); setOverlayKind(kind); setOverlayName(name); setOverlayText(text); setSavedOverlay(text); } catch (e) { setError(e); } }
  async function saveOverlay() { if (!permitted("overlay.write")) return; if (!/^[A-Za-z0-9._-]+$/.test(overlayName)) return setError("覆盖名称只能包含字母、数字、点、下划线或连字符"); const same = overlays[overlayKind]?.filter((x) => x.name === overlayName).length > 1; if (same) return setError("覆盖名称重复"); try { const data = parseObject(overlayText, "覆盖配置"); await updateProductOverlay(form.productId, overlayKind, overlayName, data); const text = asJson(data); setOverlayText(text); setSavedOverlay(text); const records = await listProductOverlays(form.productId, overlayKind); setOverlays((old) => ({ ...old, [overlayKind]: records })); notice.success("覆盖配置已保存"); } catch (e) { setError(e); } }
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
    } catch (e) { setError(e); }
    return false;
  }
  async function loadAudit() { if (!permitted("audit.read")) return; try { setAudit(await listAuditEvents({ ...auditFilter, limit: 100 })); } catch (e) { setError(e); } }
  async function readNotification(n) { try { await markNotificationRead(n.id); setNotifications((old) => old.map((x) => x.id === n.id ? { ...x, readAt: new Date().toISOString() } : x)); if (n.buildId) { setPage("builds"); const build = builds.find((b) => b.id === n.buildId); if (build) inspect(build); } } catch (e) { setError(e); } }
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
  const healthStatus = health.state === "reachable" ? "success" : health.state === "unreachable" ? "error" : "processing";
  const healthLabel = health.state === "reachable" ? "API 可达" : health.state === "unreachable" ? "API 不可达" : "检查 API";
  const healthTip = health.state === "reachable" ? "仅表示 /api/health HTTP handler 可达，不代表 PostgreSQL、Nomad、对象存储或 OIDC 正常。" : health.state === "unreachable" ? "无法访问 /api/health；业务请求可能失败。" : "正在检查 API handler 存活状态。";
  return <Layout className={`vela-shell admin-shell ${collapsed ? "is-sidebar-collapsed" : ""}`}>{holder}
    {screens.lg && <Sider width={224} collapsed={collapsed} className="vela-sider"><div className="vela-brand"><BuildOutlined /><span>Vela Studio</span></div>{menu}<Tooltip title={healthTip} placement="right"><div className="vela-sider-footer"><Badge status={healthStatus} /><Text type="secondary">{collapsed ? "" : `${apiEnvironmentLabel(API_URL)} · ${healthLabel}`}</Text>{!collapsed && <div className="vela-footer-caption">打包与发布控制平面</div>}</div></Tooltip></Sider>}
    <Drawer className="vela-nav-drawer" title="Vela Studio" placement="left" width={screens.xs ? "88vw" : 280} open={mobileNav} onClose={() => setMobileNav(false)}>{!screens.lg && menu}</Drawer>
    <Layout className="vela-main"><Header className="vela-header"><Space className="vela-header-search"><Button aria-label={!screens.lg ? "展开导航" : collapsed ? "展开导航" : "折叠导航"} aria-expanded={screens.lg ? !collapsed : mobileNav} type="text" icon={screens.lg && !collapsed ? <MenuFoldOutlined /> : <MenuUnfoldOutlined />} onClick={() => { if (!screens.lg) return setMobileNav(true); const next = !collapsed; setCollapsed(next); localStorage.setItem("packaging.sidebarCollapsed", String(next)); }} /><Input id="global-build-search" aria-label="搜索构建 ID、产品、状态" placeholder="搜索构建 ID、产品、状态" value={search} onChange={(e) => setSearch(e.target.value)} onPressEnter={() => setPage("builds")} suffix={<span className="vela-search-suffix">{search ? <Button type="link" size="small" aria-label="清空全局搜索" onClick={() => setSearch("")}>清空</Button> : !screens.xs && <kbd>Ctrl K</kbd>}</span>} /></Space><Space className="vela-header-tools"><Tooltip title="界面角色用于提前禁用操作，服务端仍会再次鉴权。"><Button aria-label="认证与角色" icon={<SettingOutlined />} onClick={() => setAuthOpen(true)}>{!screens.xs && (auth.token ? `${auth.role || "viewer"} 角色` : "本地开发访问")}</Button></Tooltip><Tooltip title="助手会区分建议、工具调用与待审批操作；生产操作仍需服务端授权。"><Button icon={<RobotOutlined />} aria-label="打开构建助手" onClick={() => setAgentOpen(true)}>{!screens.xs && "构建助手"}</Button></Tooltip><Dropdown trigger={["click"]} popupRender={() => <Card className="vela-notification-card" title="通知中心" size="small" style={{ width: "min(380px, calc(100vw - 24px))" }} extra={<Button type="link" onClick={async () => { try { await markAllNotificationsRead(); setNotifications((x) => x.map((n) => ({ ...n, readAt: new Date().toISOString() }))); } catch (e) { setError(e); } }}>全部已读</Button>}><Text type="secondary">{notifications.filter((n) => !n.readAt).length ? `${notifications.filter((n) => !n.readAt).length} 条未读` : "没有未读通知"}</Text><List dataSource={notifications} locale={{ emptyText: "暂无平台通知" }} renderItem={(n) => <List.Item className={!n.readAt ? "is-unread" : ""} actions={[!n.readAt && <Button key="read" size="small" onClick={() => readNotification(n)}>已读</Button>, n.buildId && <Button key="view" size="small" onClick={() => { const b = builds.find((x) => x.id === n.buildId); if (b) inspect(b); }}>查看</Button>].filter(Boolean)}><List.Item.Meta avatar={<Badge status={notificationStatus(n.severity)} />} title={n.title || n.type || "平台通知"} description={<div className="vela-notification-copy"><span>{n.message || "没有详细说明"}</span><time>{when(n.createdAt)}</time></div>} /></List.Item>} /></Card>}><Badge count={notifications.filter((n) => !n.readAt).length}><Button aria-label={`通知，${notifications.filter((n) => !n.readAt).length} 条未读`} icon={<BellOutlined />} /></Badge></Dropdown></Space></Header>
      <Content className="vela-content"><ErrorBanner error={error} onClose={() => setError("")} /><div className="vela-page-heading"><div><Title level={1}>{pages.find((x) => x[0] === page)?.[1]}</Title><Text type="secondary">{{ overview: "从待处理任务开始，跟进 Preview 与 Release 进度。", create: "配置打包目标与运行参数，提交后进入审批队列。", builds: "筛选构建状态，查看执行详情与发布门禁。", runners: "查看执行节点、当前任务与心跳状态。", uploads: "管理构建制品、下载策略与保留期限。", products: "编辑产品 Manifest，验证配置并管理历史版本。", overlays: "管理 Profile 与客户的配置覆盖。", audit: "检索操作记录并导出审计证据。", logs: "查看构建日志、发布门禁与锁定配置。" }[page]}</Text></div></div>{loading ? <div className="vela-loading" role="status" aria-label="正在加载工作台"><Space direction="vertical" align="center"><Spin /><Text type="secondary">加载工作台</Text></Space></div> : <Page page={page} products={products} product={product} builds={shownBuilds} allBuilds={builds} buildFilter={buildFilter} setBuildFilter={setBuildFilter} runners={runners} uploads={uploads} form={form} setForm={setForm} selectProduct={selectProduct} create={create} inspect={inspect} runAction={runAction} busy={busy} upload={upload} manifest={manifest} setManifest={setManifest} savedManifest={savedManifest} revisions={revisions} saveManifest={saveManifest} manifestDryRun={manifestDryRun} dryRun={dryRun} overlay={{ overlayKind, setOverlayKind, overlayName, setOverlayName, overlayText, setOverlayText, savedOverlay, overlays, loadOverlay, saveOverlay }} audit={{ audit, auditFilter, setAuditFilter, loadAudit }} auth={auth} download={downloadBuild} changePage={changePage} />}</Content></Layout>
    <BuildDrawer build={detail} data={detailData} open={detailOpen} onClose={() => setDetailOpen(false)} onDownload={downloadBuild} />
    <Modal title="访问令牌与认证" open={authOpen} onCancel={() => setAuthOpen(false)} footer={null} destroyOnHidden>
      <Alert type="warning" showIcon message="Bearer Token 属于敏感凭据" description="令牌只保存在当前浏览器本地存储，不会写入产品、BuildLock、日志或审计样例。界面角色只用于预判按钮权限，服务端仍是最终授权方。" style={{ marginBottom: 16 }} />
      <Form layout="vertical" initialValues={{ role: auth.role || "operator" }} onFinish={saveAuth}>
        <Form.Item name="role" label={<FieldLabel tip="管理员可发布 Release；操作员可创建、审批、重试和编辑配置；只读角色可查看审计并下载平台内制品。">界面角色</FieldLabel>} rules={[{ required: true }]}><Select options={[{ value: "admin", label: "管理员" }, { value: "operator", label: "操作员" }, { value: "viewer", label: "只读" }]} /></Form.Item>
        <Form.Item name="token" label="Bearer Token" rules={[{ required: true, message: "请输入访问令牌" }]}><Input.Password autoComplete="off" aria-label="Bearer Token" placeholder="仅用于 API Bearer 认证" /></Form.Item>
        <Space><Button type="primary" htmlType="submit">保存令牌</Button><Button danger disabled={!auth.token} onClick={clearAuth}>清除认证</Button>{oidcLoginConfigured() && <Button onClick={() => startOIDCLogin().catch((e) => setError(e))}>使用 OIDC 登录</Button>}</Space>
      </Form>
      {!oidcLoginConfigured() && <Text type="secondary">OIDC 未配置；可使用本地访问令牌。</Text>}
    </Modal>
    <AgentCopilotPanel open={agentOpen} onClose={() => setAgentOpen(false)} context={{ label: detail ? `构建 ${detail.id}` : "控制台" }} messages={agentMessages} onSend={(text) => {
      if (!import.meta.env.VITE_AGENT_API_URL) { setAgentMessages((x) => [...x, { id: Date.now(), role: "system", parts: [{ type: "text", text: "Agent 服务未配置；不会发起 API 请求或执行操作。" }] }]); return; }
      setAgentMessages((x) => [...x, { id: Date.now(), role: "user", parts: [{ type: "text", text }] }, { id: Date.now() + 1, role: "system", parts: [{ type: "text", text: "Agent BFF 集成等待服务端实现；不会模拟或执行特权操作。" }] }]);
    }} status="ready" notice={import.meta.env.VITE_AGENT_API_URL ? "Agent BFF 传输层尚未接入；当前不会发送模型请求或执行工具。" : "Agent 服务未配置；不会发起 API 请求。"} />
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
  const target = <Card title="构建目标"><div className="vela-form-grid"><Form.Item label={<FieldLabel tip="产品 Manifest 决定可选平台、Profile、组件和默认配置。">产品</FieldLabel>}><Select value={form.productId || undefined} options={products.map((x) => ({ value: x.id, label: x.name || x.id }))} onChange={(id) => selectProduct(products.find((x) => x.id === id))} /></Form.Item><Form.Item label={<FieldLabel tip="目标平台同时决定安装目录、打包工具和 Runner 调度约束。">目标平台</FieldLabel>}><Select value={form.target} options={(product?.targets || []).map((x) => ({ value: x }))} onChange={(v) => patch("target", v)} /></Form.Item><Form.Item label={<FieldLabel tip="Profile overlay 会覆盖目标配置，优先级低于本次请求参数。">Profile</FieldLabel>}><Select value={form.profile} options={(product?.profiles || []).map((x) => ({ value: x }))} onChange={(v) => patch("profile", v)} /></Form.Item><Form.Item label={<FieldLabel tip="填写后会尝试合并同名客户 overlay；留空则不应用客户覆盖。">客户标识</FieldLabel>}><Input placeholder="可选，例如 city-bank" value={form.customer} onChange={(e) => patch("customer", e.target.value)} /></Form.Item><Form.Item label={<FieldLabel tip="Release 发布要求固定版本；latest 可以用于 Preview，但会阻塞 Release 门禁。">构建版本</FieldLabel>} className="is-wide"><Input value={form.version} onChange={(e) => patch("version", e.target.value)} /></Form.Item></div></Card>;
  const config = <Card title="运行配置"><div className="vela-form-grid"><Form.Item label="安装目录"><Input value={form.installDir} onChange={(e) => patch("installDir", e.target.value)} /></Form.Item><Form.Item label="Web 端口"><Input value={form.webPort} onChange={(e) => patch("webPort", e.target.value)} /></Form.Item><Form.Item label="服务版本"><Input value={form.serviceVersion} onChange={(e) => patch("serviceVersion", e.target.value)} /></Form.Item><Form.Item label="客户名称"><Input value={form.customerName} onChange={(e) => patch("customerName", e.target.value)} /></Form.Item><Form.Item label="启用模块" className="is-wide"><Select mode="multiple" placeholder="沿用产品默认模块" value={Object.keys(form.modules).filter((x) => form.modules[x])} options={Object.keys(product?.config?.modules || {}).map((x) => ({ value: x }))} onChange={(v) => patch("modules", Object.fromEntries(v.map((x) => [x, true])))} /></Form.Item></div></Card>;
  const component = <Card title="组件来源"><Text type="secondary">只替换本次构建需要变更的组件；其余组件继续使用产品定义中的锁定来源。</Text>{components.length ? <div className="vela-component-list">{components.map((c) => <div className="vela-component-row" key={c.id}><div><Text strong>{c.id}</Text><Text type="secondary">{c.required ? "必需组件" : "可选组件"} / {c.source?.kind || "默认来源"}</Text></div><Upload beforeUpload={(file) => upload(file, c.id)} showUploadList={false}><Button icon={<UploadOutlined />} aria-label={`上传 ${c.id} 组件文件`}>替换文件</Button></Upload></div>)}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该产品没有可替换组件" />}</Card>;
  const confirm = <Card title="提交前确认"><Descriptions column={1} size="small"><Descriptions.Item label="产品">{product?.name || form.productId || "未选择"}</Descriptions.Item><Descriptions.Item label="构建目标">{form.target} / {form.profile}</Descriptions.Item><Descriptions.Item label="版本">{form.version || "未填写"}</Descriptions.Item><Descriptions.Item label="安装目录">{form.installDir || "使用产品默认值"}</Descriptions.Item><Descriptions.Item label="组件覆盖">{form.componentOverridesText === "{}" ? "无" : "已配置，将在服务端校验"}</Descriptions.Item></Descriptions><Alert className="vela-inline-alert" type="info" showIcon message="提交后由服务端生成不可变 BuildLock" description="构建会以 pending_approval 状态保存。前端不推导锁定结果，也不会直接创建 Release。" /></Card>;
  const content = [target, config, component, confirm][step];
  return <div className="vela-create-workspace"><Form layout="vertical" className="vela-create-form"><ContextNote title="构建始终从 Preview 开始">Release 只能由已验证的 Preview 通过服务端发布操作提升，不能在创建时直接选择。</ContextNote><Steps size="small" current={step} onChange={setStep} items={[{ title: "目标" }, { title: "配置" }, { title: "组件" }, { title: "确认" }]} /><div key={step} className="vela-form-step">{content}</div><div className="vela-step-tools"><Space wrap><Button disabled={step === 0} onClick={() => setStep(step - 1)}>上一步</Button><Button onClick={() => setAdvancedOpen(true)}>高级 JSON</Button></Space><Space wrap>{step < 3 && <Button type="primary" onClick={() => setStep(step + 1)}>下一步</Button>}<GuardedButton auth={auth} permission="build.create" title="提交构建申请" type={step === 3 ? "primary" : "default"} loading={busy === "create"} onClick={create}>提交构建申请</GuardedButton></Space></div><Drawer title="高级构建 JSON" open={advancedOpen} onClose={() => setAdvancedOpen(false)} width="min(560px, 100vw)"><Alert type="warning" showIcon message="高级配置会覆盖表单生成的同名字段" description="不要在变量、覆盖配置或组件 URI 中填写 token、密码、私钥或对象存储凭据。" style={{ marginBottom: 16 }} /><Form.Item label="构建变量 JSON"><Input.TextArea aria-label="构建变量 JSON" rows={8} value={form.variablesText} onChange={(e) => patch("variablesText", e.target.value)} /></Form.Item><Form.Item label="组件覆盖 JSON"><Input.TextArea aria-label="组件覆盖 JSON" rows={10} value={form.componentOverridesText} onChange={(e) => patch("componentOverridesText", e.target.value)} /></Form.Item></Drawer></Form><aside className="vela-create-context" aria-label="当前构建配置"><div className="vela-context-heading"><Title level={2}>本次构建</Title><Tag>{step + 1} / 4</Tag></div><Descriptions column={1} size="small"><Descriptions.Item label="产品">{product?.name || form.productId || "未选择"}</Descriptions.Item><Descriptions.Item label="目标">{form.target} / {form.profile}</Descriptions.Item><Descriptions.Item label="版本">{form.version || "未填写"}</Descriptions.Item><Descriptions.Item label="发布通道"><Tooltip title="创建接口只接受 preview；发布需在 Preview 验证通过后由管理员执行。"><Tag color="blue">Preview</Tag></Tooltip></Descriptions.Item></Descriptions><div className="vela-create-guidance"><Text strong>{["选择打包目标", "检查运行配置", "按需替换组件", "准备提交审批"][step]}</Text><p>{["产品定义决定可用目标与 Profile。构建将使用所选产品的默认配置。", "配置仅作用于本次构建，不会更改产品 Manifest。Web 端口范围为 1–65535。", "上传组件后会写入当前构建的组件覆盖；未覆盖的组件沿用产品定义。", "提交后进入审批队列。审批与发布门禁仍由服务端校验。"][step]}</p></div></aside></div>;
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
    { title: <FieldLabel tip="列表只展示服务端计算的 releaseCheckSummary；打开详情查看 checks[] 的具体检查项。">发布门禁</FieldLabel>, render: (_, r) => releaseGateStatus(r) },
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
  const runnerMeta = {
    busy: ["processing", "忙碌", "Runner 正在执行构建。"],
    online: ["success", "在线", "Runner 最近有活动，但未明确声明为空闲。"],
    idle: ["success", "空闲", "Runner 在线且当前没有运行构建。"],
    stale: ["warning", "心跳过期", "Runner 最近有记录，但心跳已超过服务端新鲜度阈值。"],
    offline: ["error", "离线", "Runner 已被服务端判定为离线。"],
    unknown: ["default", "未知", "服务端尚不能确认 Runner 当前状态。"],
  };
  const idOf = (runner) => runner.runnerId || runner.id || "unknown-runner";
  const available = runners.filter((runner) => ["online", "idle"].includes(runner.status)).length;
  const busyCount = runners.filter((runner) => runner.status === "busy" || Number(runner.runningBuilds) > 0).length;
  const attentionCount = runners.filter((runner) => ["stale", "offline", "unknown"].includes(runner.status)).length;
  const columns = [
    { title: "Runner", render: (_, runner) => <Space direction="vertical" size={0}><Text strong>{idOf(runner)}</Text><Text type="secondary">{runner.target || runner.name || runner.hostname || "未声明目标"}</Text></Space> },
    { title: <FieldLabel tip="状态由后端根据 Build、日志和心跳派生；浏览器不会直接调用内部 Runner 协议。">状态</FieldLabel>, dataIndex: "status", render: (value = "unknown") => { const [status, label, tip] = runnerMeta[value] || runnerMeta.unknown; return <Tooltip title={tip}><Badge status={status} text={label} /></Tooltip>; } },
    { title: "当前构建", render: (_, runner) => { const runnerId = idOf(runner); const buildId = runner.currentBuildId || builds.find((item) => item.runnerId === runnerId && ["queued", "running"].includes(item.status))?.id; const build = builds.find((item) => item.id === buildId); return buildId ? <Button type="link" onClick={() => build && inspect(build)}>{buildId}</Button> : <Text type="secondary">无运行任务</Text>; } },
    { title: "运行统计", render: (_, runner) => <Space size={12}><Tooltip title="当前运行构建"><span>运行 {runner.runningBuilds ?? (runner.status === "busy" ? 1 : 0)}</span></Tooltip><Tooltip title="历史完成构建"><span>完成 {runner.completedBuilds ?? "—"}</span></Tooltip><Tooltip title="历史失败构建"><span>失败 {runner.failedBuilds ?? "—"}</span></Tooltip></Space> },
    { title: <FieldLabel tip="心跳只反映 Runner 上报时间；平台依赖健康需要结合调度与构建结果判断。">最后心跳</FieldLabel>, dataIndex: "lastHeartbeatAt", render: (value, runner) => <Tooltip title={runner.recentMessages?.at?.(-1)?.message || "没有最近消息摘要"}><span>{when(value)}</span></Tooltip> },
  ];
  return <div className="vela-resource-page"><ContextNote title="Runner 是只读运行态">此页面展示服务端派生状态；浏览器不会领取任务、发送心跳或直接控制 Nomad。</ContextNote><section className="vela-plain-metrics" aria-label="Runner 概览"><div><Text type="secondary">已发现</Text><strong>{runners.length}</strong></div><div><Text type="secondary">可用</Text><strong>{available}</strong></div><div><Text type="secondary">忙碌</Text><strong>{busyCount}</strong></div><div><Text type="secondary">需关注</Text><strong>{attentionCount}</strong></div></section><div className="vela-section-heading"><div><Title level={2}>执行节点</Title><Text type="secondary">状态由 Builds、日志和心跳记录派生</Text></div></div><div className="vela-table-surface"><Table size="small" rowKey={idOf} dataSource={runners} columns={columns} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有 Runner 状态；Runner 开始上报后会在这里出现" /> }} pagination={false} /></div></div>;
}
function Uploads({ uploads, upload, form, auth }) {
  const [policy, setPolicy] = useState("platform"), [retention, setRetention] = useState(30), [purpose, setPurpose] = useState("manual-upload");
  const doUpload = async (file) => {
    if (!can(auth, "upload.write")) return false;
    return upload(file, "", { downloadPolicy: policy, retentionDays: retention, purpose });
  };
  const policyMeta = { public: ["公开", "允许匿名下载；仅在明确需要公开分发时使用。"], platform: ["平台内", "需要 viewer 或更高角色。"], restricted: ["受限", "需要 operator 或管理员角色。"] };
  const renderRetention = (record) => {
    if (record.retentionStatus === "expired") return <Tooltip title="下载接口会返回 410 ARTIFACT_EXPIRED。"><Tag color="error">已过期</Tag></Tooltip>;
    if (record.retentionStatus === "permanent") return <Tooltip title="该记录没有到期时间；实际持久性仍取决于存储后端配置。"><Tag color="blue">永久</Tag></Tooltip>;
    const expires = record.expiresAt || record.retentionUntil;
    return <Tooltip title={expires ? `预计到期：${when(expires)}` : "服务端会根据 retentionDays 计算保留状态。"}><span>{record.retentionDays ? `${record.retentionDays} 天` : expires ? when(expires) : "活动"}</span></Tooltip>;
  };
  const columns = [{ title: "制品", render: (_, record) => <Space direction="vertical" size={0}><Text strong>{record.fileName || record.name || record.id}</Text><Text type="secondary">{record.contentType || record.id} / {formatBytes(record.size)}</Text></Space> }, { title: <FieldLabel tip="上传完成后由服务端计算；可用于核对来源制品和证据归档。">SHA256</FieldLabel>, dataIndex: "sha256", render: (value) => <Tooltip title={value || "没有摘要"}><code className="vela-digest">{value || "—"}</code></Tooltip> }, { title: "保留状态", render: (_, record) => renderRetention(record) }, { title: <FieldLabel tip="public 可匿名下载；platform 需要 viewer+；restricted 需要 operator+。">下载策略</FieldLabel>, dataIndex: "downloadPolicy", render: (value) => { const [label, tip] = policyMeta[value] || [value || "—", "服务端返回的下载策略。"] ; return <Tooltip title={tip}><span>{label}</span></Tooltip>; } }, { title: "创建时间", dataIndex: "createdAt", render: when }, { title: "操作", render: (_, record) => <Tooltip title={record.retentionStatus === "expired" ? "制品已过期，无法下载。" : "下载权限与保留期会由服务端再次校验。"}><Button disabled={record.retentionStatus === "expired" || !can(auth, record.downloadPolicy === "restricted" ? "download.restricted" : "download.platform")} onClick={() => downloadFile(record.downloadUrl || `${API_URL}/api/uploads/${record.id}/download`, record.fileName || record.name || record.id)}>下载</Button></Tooltip> }];
  return <div className="vela-resource-page"><ContextNote title="浏览器不直接访问对象存储" tone="secure">上传与下载都经过受控 API；不要在页面配置 S3、RustFS 或 MinIO 凭据。</ContextNote><section className="vela-upload-workbench"><div className="vela-upload-settings"><Title level={2}>上传设置</Title><Text type="secondary">上传前确定访问范围、保留期限和用途。策略会写入制品记录。</Text><Form layout="vertical"><div className="vela-form-grid"><Form.Item label={<FieldLabel tip="策略决定下载端点的最低角色要求。">下载策略</FieldLabel>}><Select value={policy} onChange={setPolicy} options={[{ value: "platform", label: "平台内下载（viewer+）" }, { value: "restricted", label: "受限下载（operator+）" }, { value: "public", label: "公开下载（匿名）" }]} /></Form.Item><Form.Item label={<FieldLabel tip="允许范围为 0–3650 天；最终值可能由服务端默认或限制。">保留天数</FieldLabel>}><Input type="number" min={0} max={3650} aria-label="保留天数" value={retention} onChange={(e) => setRetention(e.target.value)} /></Form.Item><Form.Item className="is-wide" label={<FieldLabel tip="用途会写入 metadata.purpose，便于审计与后续检索。">用途</FieldLabel>}><Input aria-label="用途" value={purpose} onChange={(e) => setPurpose(e.target.value)} /></Form.Item></div></Form></div><Upload.Dragger className="vela-upload-drop" beforeUpload={doUpload} showUploadList={false} disabled={!can(auth, "upload.write")}><UploadOutlined /><p>拖入安装包或组件制品</p><Text type="secondary">当前关联产品：{form.productId || "未选择"}</Text><div><Button icon={<UploadOutlined />} disabled={!can(auth, "upload.write")}>选择文件上传</Button></div></Upload.Dragger></section><div className="vela-section-heading"><div><Title level={2}>制品记录</Title><Text type="secondary">共 {uploads.length} 个上传记录；保留状态由服务端读取时计算</Text></div></div><div className="vela-table-surface"><Table size="small" rowKey="id" scroll={{ x: 920 }} dataSource={uploads} columns={columns} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有上传制品" /> }} /></div></div>;
}
function validateManifest(data) { if (!data.id || !Array.isArray(data.targets) || !data.targetConfig || typeof data.targetConfig !== "object") throw new Error("Manifest 缺少 id、targets 或 targetConfig"); for (const target of data.targets) if (!data.targetConfig[target]) throw new Error(`Manifest 缺少 targetConfig.${target}`); return data; }
function Manifest({ products, product, selectProduct, manifest, setManifest, savedManifest, revisions, saveManifest, manifestDryRun, dryRun, busy, auth }) {
  const dirty = manifest !== savedManifest;
  const dryRunErrors = Array.isArray(dryRun?.errors) ? dryRun.errors : [];
  return <div className="vela-editor-layout"><aside className="vela-resource-nav"><div className="vela-resource-nav-heading"><div><Text strong>产品定义</Text><Text type="secondary">{products.length} 个产品</Text></div></div><List dataSource={products} locale={{ emptyText: "没有产品定义" }} renderItem={(item) => <List.Item className={item.id === product?.id ? "is-active" : ""}><button type="button" onClick={() => selectProduct(item)}><strong>{item.name || item.id}</strong><span>{item.id} / {(item.targets || []).join(", ") || "无目标"}</span></button></List.Item>} /></aside><main className="vela-editor-main"><ContextNote title="先验证，再保存">试运行返回 HTTP 200 并不代表配置有效；请以响应中的 valid 与 errors[] 为准。通过后生成的 BuildLock 是构建输入的不可变快照。</ContextNote><div className="vela-editor-toolbar"><div><Space><Title level={2}>Manifest</Title>{dirty && <Tag color="warning">未保存</Tag>}</Space><Text type="secondary">{product?.name || "请选择产品"} / JSON 产品定义</Text></div><Space wrap><Button loading={busy === "dryrun"} onClick={manifestDryRun}>试运行</Button><Button onClick={() => { if (dirty && !window.confirm("确认丢弃未保存修改并重新加载 Manifest")) return; selectProduct(product); }}>重新加载</Button><GuardedButton auth={auth} permission="manifest.write" type="primary" loading={busy === "manifest"} onClick={saveManifest}>保存 Manifest</GuardedButton></Space></div><Input.TextArea className="vela-json-editor" aria-label="Manifest JSON" rows={22} value={manifest} onChange={(e) => setManifest(e.target.value)} />{dryRun && <Alert className="vela-editor-result" type={dryRun.valid ? "success" : "warning"} showIcon message={dryRun.valid ? "试运行通过" : `试运行未通过${dryRunErrors.length ? `（${dryRunErrors.length} 项）` : ""}`} description={dryRun.valid ? <div><Text>当前定义可以解析为不可变 BuildLock。</Text>{dryRun.buildLock && <div className="vela-result-meta"><Tag color="success">BuildLock 已生成</Tag><code>{dryRun.buildLock.buildId || "预览锁"}</code></div>}</div> : dryRunErrors.length ? <ul className="vela-validation-list">{dryRunErrors.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}</ul> : <Text>服务端未返回具体错误；请检查 Manifest 结构和所选目标。</Text>} />}<section className="vela-revision-list"><div className="vela-section-heading"><div><Title level={2}>版本记录</Title><Text type="secondary">每次保存或恢复都会形成新修订；已生成的构建锁不会随修订变化</Text></div></div><List dataSource={revisions} locale={{ emptyText: "还没有历史版本" }} renderItem={(revision) => <List.Item actions={[<Popconfirm key="restore" title="确认恢复此历史版本？" description="恢复会创建新的当前修订，不会改写既有 BuildLock。" onConfirm={() => restoreProductManifestRevision(product.id, revision.id || revision.revisionId).then(() => selectProduct(product))}><Button size="small">恢复为新版本</Button></Popconfirm>]}><List.Item.Meta title={revision.id || revision.revision} description={<Space size={8}><span>{when(revision.createdAt)}</span>{revision.actorId && <span>{revision.actorId}</span>}</Space>} /></List.Item>} /></section></main></div>;
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
  return <div className="vela-editor-layout"><aside className="vela-resource-nav"><Tabs activeKey={overlay.overlayKind} onChange={changeKind} items={[{ key: "profiles", label: "Profile" }, { key: "customers", label: "客户" }]} /><Button block onClick={startNew}>新建覆盖</Button><List dataSource={names} locale={{ emptyText: "当前分类没有覆盖配置" }} renderItem={(item) => <List.Item className={item.name === overlay.overlayName ? "is-active" : ""}><button type="button" onClick={() => overlay.loadOverlay(overlay.overlayKind, item.name)}><strong>{item.name}</strong><span>{overlay.overlayKind === "profiles" ? "运行环境" : "客户配置"}</span></button></List.Item>} /></aside><main className="vela-editor-main"><ContextNote title="最终值按优先级覆盖">请求 overrides ＞ variables ＞ customer ＞ profile ＞ target ＞ base。靠左的来源覆盖靠右的同名字段，未声明字段继续继承。</ContextNote><div className="vela-editor-toolbar"><div><Space><Title level={2}>{overlay.overlayName || "新建覆盖"}</Title>{dirty && <Tag color="warning">未保存</Tag>}</Space><Text type="secondary">{product?.name || product?.id || "未选择产品"} / {overlay.overlayKind === "profiles" ? "Profile" : "客户"}</Text></div><GuardedButton auth={auth} permission="overlay.write" type="primary" onClick={overlay.saveOverlay}>保存覆盖</GuardedButton></div><Form layout="vertical"><Form.Item label={<FieldLabel tip="名称会成为构建请求中的 profile 或 customer 标识。">名称</FieldLabel>} extra="可使用字母、数字、点、下划线和连字符"><Input value={overlay.overlayName} onChange={(e) => overlay.setOverlayName(e.target.value)} /></Form.Item><Form.Item label={<FieldLabel tip="只写需要覆盖的字段；空对象表示不改变上游配置。">覆盖配置 JSON</FieldLabel>}><Input.TextArea className="vela-json-editor" aria-label="覆盖配置 JSON" rows={22} value={overlay.overlayText} onChange={(e) => overlay.setOverlayText(e.target.value)} /></Form.Item></Form><Alert type="info" showIcon message="保存覆盖不会改写历史构建" description="新配置只会在后续创建构建时参与合并；既有构建继续使用各自的不可变 BuildLock。" /></main></div>;
}
function Audit({ audit: events, auditFilter, setAuditFilter, loadAudit }) {
  const set = (key, value) => setAuditFilter((current) => ({ ...current, [key]: value }));
  const clear = () => setAuditFilter({ action: "", resourceType: "", resourceId: "", buildId: "", productId: "", uploadId: "" });
  const actors = new Set(events.map((event) => event.actorId || event.actor).filter(Boolean)).size;
  const columns = [{ title: "时间", render: (_, record) => when(record.time || record.createdAt) }, { title: "操作", dataIndex: "action", render: (value, record) => <Space direction="vertical" size={0}><code>{value || "—"}</code><Text type="secondary">{record.summary || "没有摘要"}</Text></Space> }, { title: "资源", render: (_, record) => <Space direction="vertical" size={0}><Text>{record.resourceType || "—"}</Text><Text type="secondary">{record.resourceId || "—"}</Text></Space> }, { title: "操作者", render: (_, record) => <Space direction="vertical" size={0}><Text>{record.actorId || record.actor || "—"}</Text><Text type="secondary">{record.actorRole || "角色未记录"}</Text></Space> }, { title: "结果", dataIndex: "status", render: (value) => <Tag color={value === "success" || value === "succeeded" ? "success" : value === "failed" || value === "denied" ? "error" : "default"}>{value || "未声明"}</Tag> }, { title: <FieldLabel tip="用于跨服务定位同一次请求；事件本身不保证包含 evidenceDigest。">追踪标识</FieldLabel>, render: (_, record) => <Space direction="vertical" size={0}><code className="vela-digest">{record.requestId || "—"}</code><Text type="secondary">{record.correlationId || "无 correlationId"}</Text></Space> }];
  return <div className="vela-audit-page"><ContextNote title="事件与归档分层">列表展示独立审计事件；导出归档时服务端才生成整体 digest，并可附带签名、保留策略与法律保全信息。</ContextNote><section className="vela-query-workbench"><div className="vela-query-heading"><div><Title level={2}>定位审计事件</Title><Text type="secondary">可组合多个条件；空条件返回最近 100 条记录。</Text></div><Space><Button onClick={clear}>清空筛选</Button><Button type="primary" onClick={loadAudit}>查询事件</Button></Space></div><div className="vela-query-grid"><Form.Item label="动作"><Input placeholder="例如 build.download" value={auditFilter.action} onChange={(e) => set("action", e.target.value)} /></Form.Item><Form.Item label="资源类型"><Input placeholder="build / upload" value={auditFilter.resourceType} onChange={(e) => set("resourceType", e.target.value)} /></Form.Item><Form.Item label="资源 ID"><Input placeholder="精确资源 ID" value={auditFilter.resourceId} onChange={(e) => set("resourceId", e.target.value)} /></Form.Item><Form.Item label="构建 ID"><Input placeholder="build..." value={auditFilter.buildId} onChange={(e) => set("buildId", e.target.value)} /></Form.Item><Form.Item label="产品 ID"><Input placeholder="例如 lsap" value={auditFilter.productId} onChange={(e) => set("productId", e.target.value)} /></Form.Item><Form.Item label="上传 ID"><Input placeholder="upload..." value={auditFilter.uploadId} onChange={(e) => set("uploadId", e.target.value)} /></Form.Item></div></section><section className="vela-audit-summary"><div><Text type="secondary">当前事件</Text><strong>{events.length}</strong></div><div><Text type="secondary">涉及操作者</Text><strong>{actors}</strong></div><div><Text type="secondary">查询范围</Text><strong>最近 100 条</strong></div><Button icon={<DownloadOutlined />} onClick={async () => downloadJson(await exportAuditArchive({ ...auditFilter, limit: 100 }), "audit-evidence.json")}>导出审计归档</Button></section><div className="vela-section-heading"><div><Title level={2}>审计事件</Title><Text type="secondary">按服务端返回顺序展示；requestId 与 correlationId 可用于故障追踪</Text></div></div><div className="vela-table-surface"><Table size="small" rowKey="id" scroll={{ x: 980 }} dataSource={events} columns={columns} locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有匹配的审计事件；调整条件后重新查询" /> }} /></div></div>;
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
      .catch((error) => { if (active) setLoadError(error); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [build?.id]);
  if (!builds.length) return <Empty description="还没有构建，创建构建后可在这里查看日志与锁定配置" />;
  return <div className="vela-logs-workspace"><aside className="vela-log-builds"><div><Text strong>选择构建</Text><Text type="secondary">{builds.length} 个构建记录</Text></div><Select value={build?.id} onChange={setSelectedId} options={builds.map((item) => ({ value: item.id, label: `${item.productId} / ${item.id}` }))} /><List dataSource={builds.slice(0, 10)} renderItem={(item) => <List.Item className={item.id === build?.id ? "is-active" : ""}><button type="button" onClick={() => setSelectedId(item.id)}><span>{buildStatus(item)}</span><strong>{item.id}</strong><small>{item.target || "—"} / {item.profile || "—"}</small></button></List.Item>} /></aside><main className="vela-log-main"><div className="vela-log-heading"><div><Space>{buildStatus(build)}<Title level={2}>{build.id}</Title></Space><Text type="secondary">{build.productId} / {build.target || "—"} / {build.profile || "—"} / {build.version || "—"}</Text></div><Button icon={<DownloadOutlined />} onClick={async () => downloadJson(await exportBuildReleaseEvidenceArchive(build.id), `${build.id}-evidence.json`)}>导出证据</Button></div>{loadError && <ErrorBanner error={loadError} onClose={() => setLoadError("")} />}{loading ? <div className="vela-loading"><Spin /><Text type="secondary">加载构建上下文</Text></div> : <LogPanel build={build} data={data} />}</main></div>;
}
function LogPanel({ build, data }) {
  const logs = Array.isArray(data.logs) ? data.logs : data.logs?.items || [];
  const [filter, setFilter] = useState("");
  const uniqueLogs = Array.from(new Map(logs.map((item, index) => [typeof item === "object" && item?.sequence ? `sequence-${item.sequence}` : `index-${index}`, item])).values());
  const lines = uniqueLogs.map((item) => typeof item === "string" ? item : `${item.sequence ? `#${item.sequence} ` : ""}${item.time ? `${when(item.time)} ` : ""}${item.source ? `[${item.source}] ` : ""}${item.message || asJson(item)}`).map(stripAnsi).filter((line) => line.toLowerCase().includes(filter.toLowerCase()));
  const code = (value, label) => <pre className="vela-code" aria-label={label}>{asJson(value)}</pre>;
  const items = [
    { key: "logs", label: `日志 ${lines.length}`, children: <div className="vela-log-panel"><ContextNote title="快照与实时流用途不同">此处是可重复加载的持久日志快照。实时 SSE 端点由服务端轮询日志存储实现；重连可能收到重复事件，客户端应按 sequence 去重。</ContextNote><div className="vela-log-tools"><Input allowClear placeholder="过滤当前日志" value={filter} onChange={(e) => setFilter(e.target.value)} /><Button title="复制当前筛选后的日志" onClick={() => navigator.clipboard?.writeText(lines.join("\n"))}>复制日志</Button><a href={build ? buildLogStreamUrl(build.id) : "#"} target="_blank" rel="noreferrer">打开实时日志流</a></div>{lines.length ? <pre className="vela-code vela-log-output">{lines.map((line, index) => <div key={index} title={line}>{line}</div>)}</pre> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={filter ? "没有匹配的日志行" : "当前构建还没有日志"} />}</div> },
    { key: "lock", label: "构建锁", children: <div><ContextNote title="BuildLock 是不可变输入快照" tone="secure">Manifest 或覆盖配置之后发生变化，不会回写到这个构建。复现和发布证据应以此处锁定的配置、组件与工作负载为准。</ContextNote>{code(data.lock, "构建锁 JSON")}</div> },
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
  return <Drawer title="构建详情" width="min(820px, 100vw)" open={open} onClose={onClose} extra={build && <Button icon={<DownloadOutlined />} disabled={!build.downloadUrl && !build.artifactUri} onClick={() => onDownload(build)}>下载制品</Button>}>{build && <><section className="vela-drawer-summary"><div><Space>{buildStatus(build)}<Title level={2}>{build.id}</Title></Space><Text type="secondary">{build.productId} / {build.version || "—"}</Text></div><Descriptions column={2} size="small"><Descriptions.Item label="构建 ID">{build.id}</Descriptions.Item><Descriptions.Item label="目标">{build.target} / {build.profile}</Descriptions.Item><Descriptions.Item label="Runner">{build.runnerId || "尚未分配"}</Descriptions.Item><Descriptions.Item label="更新时间">{when(build.updatedAt)}</Descriptions.Item></Descriptions><ContextNote title="详情以 BuildLock 为准" tone="secure">构建创建后，锁定配置不会被之后的 Manifest、Profile 或客户覆盖修改。界面中的最终配置用于阅读，BuildLock 才是复现依据。</ContextNote></section><Tabs className="vela-detail-tabs" items={[{ key: "logs", label: "日志", children: <LogPanel build={build} data={data} /> }, { key: "release", label: releaseReady ? "发布门禁（通过）" : "发布门禁", children: <div><ContextNote title="门禁由服务端裁决">每个 checks[] 项包含稳定 id、stage 与 pass/blocked 状态；前端只解释结果，不自行推导发布权限。</ContextNote>{releaseItems.length ? <div className="vela-gate-list">{releaseItems.map((item) => <div key={item.key}>{item.children}</div>)}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="服务端尚未返回门禁检查项" />}</div> }, { key: "components", label: "组件锁定", children: <Table size="small" rowKey={(item) => item.id} dataSource={data.lock?.components || []} pagination={false} columns={[{ title: "组件", dataIndex: "id" }, { title: "版本", dataIndex: "version" }, { title: "URI", dataIndex: "uri", render: (v) => <code>{v || "—"}</code> }, { title: "SHA256", dataIndex: "sha256", render: (v) => <code>{v || "—"}</code> }]} /> }, { key: "workloads", label: "工作负载", children: <pre className="vela-code">{asJson(data.lock?.workloads || [])}</pre> }, { key: "config", label: "最终配置", children: <pre className="vela-code">{asJson(build.finalConfig || data.lock?.config || {})}</pre> }, { key: "lock", label: "构建锁", children: <div><ContextNote title="不可变构建输入" tone="secure">下载、复现和 Release 证据都应关联这个 BuildLock，而不是读取当前产品配置。</ContextNote><pre className="vela-code">{asJson(data.lock)}</pre></div> }, { key: "repro", label: "可复现报告", children: <pre className="vela-code">{asJson(data.reproducibility)}</pre> }, { key: "evidence", label: "证据", children: <div className="vela-evidence-action"><Text>导出包含门禁、锁定配置和可复现报告的 Release 证据。</Text><Button type="primary" onClick={() => exportBuildReleaseEvidenceArchive(build.id).then((value) => downloadJson(value, `${build.id}-evidence.json`))}>导出 Release 证据</Button></div> }]} /></>}</Drawer>;
}
