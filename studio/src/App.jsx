import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert, Badge, Button, Card, Col, Descriptions, Drawer, Dropdown, Empty, Form,
  Input, Layout, List, Menu, Modal, Popconfirm, Radio, Row, Select, Space, Spin,
  Switch, Table, Tabs, Tag, Tooltip, Typography, Upload, Steps, message,
} from "antd";
import {
  ApartmentOutlined, AuditOutlined, BellOutlined, BuildOutlined, CheckCircleOutlined,
  CloudServerOutlined, CloudUploadOutlined, CodeOutlined, DatabaseOutlined,
  DashboardOutlined, DownloadOutlined, FileTextOutlined, HistoryOutlined,
  InboxOutlined, InfoCircleOutlined, LinkOutlined, CopyOutlined, QuestionCircleOutlined, SafetyCertificateOutlined,
  MenuFoldOutlined, MenuUnfoldOutlined, PlusOutlined, ReloadOutlined, RobotOutlined,
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
const pageGroups = [
  ["构建交付", ["overview", "create", "builds", "logs"]],
  ["配置", ["products", "overlays", "uploads"]],
  ["平台", ["runners", "audit"]],
];
const pageDescriptions = {
  overview: "从待处理任务开始，跟进 Preview 与 Release 进度。",
  create: "配置打包目标与运行参数，提交后进入审批队列。",
  builds: "筛选构建状态，查看执行详情与发布门禁。",
  runners: "查看执行节点、当前任务与心跳状态。",
  uploads: "管理构建制品、下载策略与保留期限。",
  products: "编辑产品 Manifest，验证配置并管理历史版本。",
  overlays: "管理 Profile 与客户的配置覆盖。",
  audit: "检索操作记录并导出审计证据。",
  logs: "查看构建日志、发布门禁与锁定配置。",
};
const agentPrompts = {
  overview: ["检查当前工作台的首要风险", "解释下一步为什么优先", "给出今天的构建处理顺序"],
  create: ["检查当前 BuildLock 蓝图", "找出构建输入中的风险", "解释这些 Workload 会如何运行"],
  builds: ["分析当前构建队列", "找出最值得先处理的失败", "总结发布门禁状态"],
  runners: ["检查 Runner 容量与异常", "解释当前调度状态", "给出扩容前检查清单"],
  uploads: ["检查制品保留与下载风险", "找出即将过期的制品", "解释上传策略"],
  products: ["检查 Manifest 的结构风险", "解释当前产品定义", "给出发布前配置检查项"],
  overlays: ["检查覆盖配置冲突", "解释配置合并优先级", "找出可能覆盖错误的字段"],
  audit: ["总结最近的高风险操作", "按关联 ID 串联审计事件", "生成审计检查清单"],
  logs: ["定位当前日志中的失败线索", "解释 BuildLock 与最终配置差异", "总结发布证据"],
};
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
function inspectJson(text, requireObject = true) {
  try {
    const value = JSON.parse(text || "");
    if (requireObject && (!value || Array.isArray(value) || typeof value !== "object")) {
      return { valid: false, message: "顶层必须是 JSON 对象", line: 1, column: 1 };
    }
    return { valid: true, value };
  } catch (error) {
    const source = String(text || "");
    const lineMatch = error.message.match(/line\s+(\d+)/i);
    const columnMatch = error.message.match(/column\s+(\d+)/i);
    const positionMatch = error.message.match(/position\s+(\d+)/i);
    const position = positionMatch ? Number(positionMatch[1]) : source.length;
    const before = source.slice(0, Math.max(0, position));
    const line = lineMatch ? Number(lineMatch[1]) : before.split("\n").length;
    const column = columnMatch ? Number(columnMatch[1]) : before.length - before.lastIndexOf("\n");
    return { valid: false, message: `第 ${line} 行，第 ${column} 列附近无法解析`, line, column };
  }
}
function highlightJson(text) {
  const source = String(text || "");
  const tokenPattern = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/g;
  const nodes = [];
  let cursor = 0;
  let match;
  while ((match = tokenPattern.exec(source))) {
    if (match.index > cursor) nodes.push(source.slice(cursor, match.index));
    const token = match[0];
    const className = match[1] ? (match[2] ? "token-key" : "token-string") : /^(true|false)$/.test(token) ? "token-boolean" : token === "null" ? "token-null" : "token-number";
    nodes.push(<span className={className} key={`${match.index}-${token}`}>{token}</span>);
    cursor = match.index + token.length;
  }
  if (cursor < source.length) nodes.push(source.slice(cursor));
  return nodes;
}
function JsonEditor({ label, value, onChange, rows = 16, requireObject = true }) {
  const inspection = useMemo(() => inspectJson(value, requireObject), [value, requireObject]);
  const highlightRef = useRef(null);
  const gutterRef = useRef(null);
  const lineCount = Math.max(1, String(value || "").split("\n").length);
  const height = Math.max(150, Math.min(560, rows * 21 + 30));
  const syncScroll = (event) => {
    if (highlightRef.current) highlightRef.current.style.transform = `translate(${-event.currentTarget.scrollLeft}px, ${-event.currentTarget.scrollTop}px)`;
    if (gutterRef.current) gutterRef.current.style.transform = `translateY(${-event.currentTarget.scrollTop}px)`;
  };
  const insertIndent = (event) => {
    if (event.key !== "Tab") return;
    event.preventDefault();
    const input = event.currentTarget;
    const next = `${value.slice(0, input.selectionStart)}  ${value.slice(input.selectionEnd)}`;
    const cursor = input.selectionStart + 2;
    onChange(next);
    window.requestAnimationFrame(() => { input.selectionStart = cursor; input.selectionEnd = cursor; });
  };
  const format = () => { if (inspection.valid) onChange(JSON.stringify(inspection.value, null, 2)); };
  return <div className={`vela-json-workbench ${inspection.valid ? "is-valid" : "is-invalid"}`} style={{ "--editor-height": `${height}px` }}><div className="vela-json-toolbar"><div className="vela-json-status" role={inspection.valid ? "status" : "alert"}><span aria-hidden="true" /> <strong>{inspection.valid ? "JSON 语法有效" : "JSON 语法错误"}</strong><small>{inspection.valid ? `${lineCount} 行 · 本地即时检查` : inspection.message}</small></div><Button size="small" disabled={!inspection.valid} onClick={format}>格式化</Button></div><div className="vela-json-editor-shell"><div className="vela-json-gutter" aria-hidden="true"><div ref={gutterRef}>{Array.from({ length: lineCount }, (_, index) => <span key={index}>{index + 1}</span>)}</div></div><div className="vela-json-input"><pre ref={highlightRef} aria-hidden="true"><code>{highlightJson(value)}{"\n"}</code></pre><textarea aria-label={label} aria-invalid={!inspection.valid} spellCheck="false" value={value} onChange={(event) => onChange(event.target.value)} onScroll={syncScroll} onKeyDown={insertIndent} /></div></div></div>;
}
function JsonCodeBlock({ value, label }) {
  const [copied, setCopied] = useState(false);
  const text = asJson(value);
  const copy = async () => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error("Clipboard API unavailable");
      await navigator.clipboard.writeText(text);
    } catch {
      const input = document.createElement("textarea");
      input.value = text;
      input.style.position = "fixed";
      input.style.opacity = "0";
      document.body.appendChild(input);
      input.select();
      document.execCommand?.("copy");
      input.remove();
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 5000);
  };
  return <div className="vela-code-block"><div className="vela-code-toolbar"><span><i aria-hidden="true" />只读 JSON</span><Button type="text" size="small" icon={<CopyOutlined />} className={copied ? "is-copied" : ""} onClick={copy}>{copied ? "已复制" : "复制"}</Button></div><pre className="vela-code vela-json-code" aria-label={label}><code>{highlightJson(text)}</code></pre></div>;
}
function parseObject(text, label) {
  let value;
  try { value = JSON.parse(text || "{}"); } catch { throw new Error(`${label} JSON 解析错误`); }
  if (!value || Array.isArray(value) || typeof value !== "object") throw new Error(`${label} 必须是 JSON 对象`);
  return value;
}
function buildStatus(build) { const [label, color] = statusMeta[build?.status] || [build?.status || "未知", "default"]; return <Tooltip title={statusDescriptions[build?.status] || "服务端返回的当前构建状态。"}><Tag color={color}>{label}</Tag></Tooltip>; }
function BuildLifecycle({ build }) {
  const stages = [
    ["pending_approval", "待审批", "BuildLock 已生成"],
    ["queued", "排队", "等待 Runner 领取"],
    ["running", "执行", "持续回传状态与日志"],
    ["succeeded", "Preview", "制品已生成"],
    ["preview_validated", "已验证", "Release 门禁检查"],
    ["published", "已发布", "Release 可分发"],
  ];
  const indexByStatus = Object.fromEntries(stages.map((stage, index) => [stage[0], index]));
  const exceptional = ["failed", "rejected", "canceled"].includes(build?.status);
  let current = indexByStatus[build?.status] ?? 0;
  if (exceptional) {
    if (build?.status === "rejected") current = 0;
    else if (build?.runnerId || build?.status === "failed") current = 2;
    else current = 1;
  }
  return <ol className="vela-build-lifecycle" aria-label="构建生命周期">{stages.map(([key, label, detail], index) => { const state = exceptional && index === current ? "error" : index < current ? "done" : index === current ? "current" : "future"; return <li className={`is-${state}`} key={key}><span>{state === "done" ? <CheckCircleOutlined /> : index + 1}</span><div><strong>{label}</strong><small>{state === "error" ? statusMeta[build.status]?.[0] || build.status : detail}</small></div></li>; })}</ol>;
}
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
  const [busy, setBusy] = useState(""), [search, setSearch] = useState(""), [agentOpen, setAgentOpen] = useState(false), [agentMessages, setAgentMessages] = useState([]), [agentDraftSeed, setAgentDraftSeed] = useState(null);
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
    setForm((old) => ({ ...old, productId: next.id, target, profile: next.profiles?.[0] || "qa", version: next.versions?.[0] || "latest", installDir: config.installDir || "", webPort: String(next.config?.ports?.web || 48000), serviceVersion: next.versions?.[0] || "latest", customerName: next.config?.customer?.name || "", license: next.config?.customer?.license || "community", passwordPolicy: next.config?.database?.passwordPolicy || "strong", modules: next.config?.modules || {}, componentOverridesText: "{}" }));
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
        current[componentId] = { kind: "manual-upload", uploadId: result.id, uri: result.uri };
        setForm((x) => ({ ...x, componentOverridesText: asJson(current) }));
      }
      setUploads((current) => [{ ...result, fileName: result.fileName || file.name }, ...current.filter((item) => item.id !== result.id)]);
      notice.success("文件已上传");
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
  const pageItem = (key) => { const [, label, icon] = pages.find((item) => item[0] === key); return { key, label, icon, "aria-label": `导航：${label}` }; };
  const menu = <Menu className="vela-nav" id="admin-sidebar" mode="inline" selectedKeys={[page]} items={pageGroups.map(([label, keys]) => ({ type: "group", label, children: keys.map(pageItem) }))} onClick={({ key }) => { setPage(key); setMobileNav(false); if (key === "audit") loadAudit(); }} />;
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
  const openAgent = (prompt = "") => {
    if (prompt) setAgentDraftSeed({ id: Date.now(), text: prompt });
    setAgentOpen(true);
  };
  const agentContext = {
    label: detail ? `构建 ${detail.id}` : `${pages.find((item) => item[0] === page)?.[1]}工作区`,
    details: [
      `${builds.filter((build) => ["pending_approval", "failed", "canceled"].includes(build.status)).length} 项需要处理`,
      `${builds.filter((build) => ["queued", "running"].includes(build.status)).length} 项正在执行`,
      detail ? `${detail.target || "—"} / ${detail.profile || "—"} / ${detail.version || "—"}` : `${products.length} 个产品定义`,
    ],
  };
  const healthStatus = health.state === "reachable" ? "success" : health.state === "unreachable" ? "error" : "processing";
  const healthLabel = health.state === "reachable" ? "API 可达" : health.state === "unreachable" ? "API 不可达" : "检查 API";
  const healthTip = health.state === "reachable" ? "仅表示 /api/health HTTP handler 可达，不代表 PostgreSQL、Nomad、对象存储或 OIDC 正常。" : health.state === "unreachable" ? "无法访问 /api/health；业务请求可能失败。" : "正在检查 API handler 存活状态。";
  return <Layout className={`vela-shell admin-shell ${collapsed ? "is-sidebar-collapsed" : ""}`}>{holder}
    {screens.lg && <Sider width={236} collapsed={collapsed} className="vela-sider"><div className="vela-brand"><BuildOutlined /><div className="vela-brand-copy"><span>Vela</span><small>Packaging workspace</small></div></div>{menu}<Tooltip title={healthTip} placement="right"><div className="vela-sider-footer"><Badge status={healthStatus} /><Text type="secondary">{collapsed ? "" : `${apiEnvironmentLabel(API_URL)} · ${healthLabel}`}</Text>{!collapsed && <div className="vela-footer-caption">打包与发布控制平面</div>}</div></Tooltip></Sider>}
    <Drawer className="vela-nav-drawer" title="Vela Studio" placement="left" width={screens.xs ? "88vw" : 280} open={mobileNav} onClose={() => setMobileNav(false)}>{!screens.lg && menu}</Drawer>
    <Layout className="vela-main"><Header className="vela-header"><Space className="vela-header-search"><Button aria-label={!screens.lg ? "展开导航" : collapsed ? "展开导航" : "折叠导航"} aria-expanded={screens.lg ? !collapsed : mobileNav} type="text" icon={screens.lg && !collapsed ? <MenuFoldOutlined /> : <MenuUnfoldOutlined />} onClick={() => { if (!screens.lg) return setMobileNav(true); const next = !collapsed; setCollapsed(next); localStorage.setItem("packaging.sidebarCollapsed", String(next)); }} /><Input id="global-build-search" aria-label="搜索构建 ID、产品、状态" placeholder="搜索构建 ID、产品、状态" value={search} onChange={(e) => setSearch(e.target.value)} onPressEnter={() => setPage("builds")} suffix={<span className="vela-search-suffix">{search ? <Button type="link" size="small" aria-label="清空全局搜索" onClick={() => setSearch("")}>清空</Button> : !screens.xs && <kbd>Ctrl K</kbd>}</span>} /></Space><Space className="vela-header-tools"><Tooltip title="界面角色用于提前禁用操作，服务端仍会再次鉴权。"><Button aria-label="认证与角色" icon={<SettingOutlined />} onClick={() => setAuthOpen(true)}>{!screens.xs && (auth.token ? `${auth.role || "viewer"} 角色` : "本地开发访问")}</Button></Tooltip><Tooltip title="助手会携带当前页面与构建状态；生产操作仍需明确确认和服务端授权。"><Button className="vela-agent-trigger" icon={<RobotOutlined />} aria-label="打开构建助手" onClick={() => openAgent()}>{!screens.xs && "Ask Vela"}</Button></Tooltip><Dropdown trigger={["click"]} popupRender={() => <Card className="vela-notification-card" title="通知中心" size="small" style={{ width: "min(380px, calc(100vw - 24px))" }} extra={<Button type="link" onClick={async () => { try { await markAllNotificationsRead(); setNotifications((x) => x.map((n) => ({ ...n, readAt: new Date().toISOString() }))); } catch (e) { setError(e); } }}>全部已读</Button>}><Text type="secondary">{notifications.filter((n) => !n.readAt).length ? `${notifications.filter((n) => !n.readAt).length} 条未读` : "没有未读通知"}</Text><List dataSource={notifications} locale={{ emptyText: "暂无平台通知" }} renderItem={(n) => <List.Item className={!n.readAt ? "is-unread" : ""} actions={[!n.readAt && <Button key="read" size="small" onClick={() => readNotification(n)}>已读</Button>, n.buildId && <Button key="view" size="small" onClick={() => { const b = builds.find((x) => x.id === n.buildId); if (b) inspect(b); }}>查看</Button>].filter(Boolean)}><List.Item.Meta avatar={<Badge status={notificationStatus(n.severity)} />} title={n.title || n.type || "平台通知"} description={<div className="vela-notification-copy"><span>{n.message || "没有详细说明"}</span><time>{when(n.createdAt)}</time></div>} /></List.Item>} /></Card>}><Badge count={notifications.filter((n) => !n.readAt).length}><Button aria-label={`通知，${notifications.filter((n) => !n.readAt).length} 条未读`} icon={<BellOutlined />} /></Badge></Dropdown></Space></Header>
      <Content className="vela-content"><div className="vela-content-inner"><ErrorBanner error={error} onClose={() => setError("")} /><div className="vela-page-heading"><div><Title level={1}>{pages.find((x) => x[0] === page)?.[1]}</Title><Text type="secondary">{pageDescriptions[page]}</Text></div><div className="vela-page-agent"><span><RobotOutlined /> Agent 已读取本页上下文</span><Button size="small" onClick={() => openAgent(agentPrompts[page][0])}>检查本页</Button></div></div>{loading ? <div className="vela-loading" role="status" aria-label="正在加载工作台"><Space direction="vertical" align="center"><Spin /><Text type="secondary">加载工作台</Text></Space></div> : <Page page={page} products={products} product={product} builds={shownBuilds} allBuilds={builds} buildFilter={buildFilter} setBuildFilter={setBuildFilter} runners={runners} uploads={uploads} form={form} setForm={setForm} selectProduct={selectProduct} create={create} inspect={inspect} runAction={runAction} busy={busy} upload={upload} manifest={manifest} setManifest={setManifest} savedManifest={savedManifest} revisions={revisions} saveManifest={saveManifest} manifestDryRun={manifestDryRun} dryRun={dryRun} overlay={{ overlayKind, setOverlayKind, overlayName, setOverlayName, overlayText, setOverlayText, savedOverlay, overlays, loadOverlay, saveOverlay }} audit={{ audit, auditFilter, setAuditFilter, loadAudit }} auth={auth} download={downloadBuild} changePage={changePage} askAgent={openAgent} />}</div></Content></Layout>
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
    <AgentCopilotPanel open={agentOpen} onClose={() => setAgentOpen(false)} context={agentContext} messages={agentMessages} draftSeed={agentDraftSeed} starterPrompts={agentPrompts[page]} onSend={(text) => {
      const userMessage = { id: Date.now(), role: "user", parts: [{ type: "text", text }] };
      if (!import.meta.env.VITE_AGENT_API_URL) { setAgentMessages((x) => [...x, userMessage, { id: Date.now() + 1, role: "system", parts: [{ type: "text", text: "Agent 服务未配置；已保留问题和当前页面上下文，但不会发起 API 请求或执行操作。" }] }]); return; }
      setAgentMessages((x) => [...x, userMessage, { id: Date.now() + 1, role: "system", parts: [{ type: "text", text: "Agent BFF 集成等待服务端实现；不会模拟或执行特权操作。" }] }]);
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
function Overview({ products, builds, runners, uploads, inspect, setBuildFilter, allBuilds, changePage, askAgent }) {
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
  const agentBrief = attention.length ? {
    title: `先处理 ${focus?.id || "待办队列"}`,
    summary: pending.length ? "审批是当前吞吐瓶颈。先核对 BuildLock 与版本，再让任务进入 Runner 队列。" : "当前存在失败或已取消的任务。建议先读取最后一段日志，再决定重试或重新配置。",
    prompt: `请分析工作台中最优先的构建 ${focus?.id || ""}，列出判断依据、风险和下一步；不要直接执行任何操作。`,
  } : blocked.length ? {
    title: "先解释 Release 门禁",
    summary: "Preview 已验证，但发布条件尚未全部满足。建议逐项解释服务端 checks[]，避免盲目重建。",
    prompt: "请分析当前 Release 门禁阻塞项，按影响范围排序并给出不会改变生产状态的排查步骤。",
  } : {
    title: "当前无需人工救火",
    summary: active.length ? "构建正在执行。Agent 可以先整理完成后的验证清单，而不是打断运行中的任务。" : "工作台状态稳定。可以让 Agent 生成下一次构建前的配置检查清单。",
    prompt: "请根据当前工作台状态生成下一步检查清单，区分只读检查、需要确认的操作和生产变更。",
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
    <section className="vela-agent-brief" aria-labelledby="agent-brief-title">
      <div className="vela-agent-brief-mark" aria-hidden="true"><RobotOutlined /></div>
      <div className="vela-agent-brief-copy"><div><Text className="vela-agent-kicker">Vela Agent 建议</Text><Title level={2} id="agent-brief-title">{agentBrief.title}</Title></div><Text type="secondary">{agentBrief.summary}</Text></div>
      <div className="vela-agent-signals" aria-label="建议依据"><span><strong>{attention.length}</strong> 需要处理</span><span><strong>{active.length}</strong> 正在执行</span><span><strong>{blocked.length}</strong> 发布阻塞</span></div>
      <Button onClick={() => askAgent(agentBrief.prompt)}>查看分析计划</Button>
    </section>
    <section className="vela-recent" aria-labelledby="recent-title"><div className="vela-section-heading"><div><Title level={2} id="recent-title">最近动态</Title><Text type="secondary">按最近更新时间排列</Text></div><Button type="link" onClick={() => queue("all")}>查看全部构建</Button></div>{builds.length ? <ul className="vela-build-stream">{builds.slice(0, 6).map((build) => <li key={build.id}><span className={`vela-build-pulse is-${build.status}`} aria-hidden="true" /><button type="button" className="vela-build-identity" onClick={() => inspect(build)} aria-label={`查看构建 ${build.id}`}><strong>{products.find((item) => item.id === build.productId)?.name || build.productId}</strong><span className="vela-build-meta"><span>{build.id}</span><span>{build.target || "—"} / {build.profile || "—"}</span><span>{build.version || "—"}</span></span></button><span className="vela-build-stream-status">{buildStatus(build)}</span><time dateTime={build.updatedAt || ""}>{when(build.updatedAt)}</time></li>)}</ul> : <Empty description="还没有构建记录" image={Empty.PRESENTED_IMAGE_SIMPLE} />}</section>
  </div>;
}
function parseDraftObject(text) {
  try { const value = JSON.parse(text || "{}"); return value && !Array.isArray(value) && typeof value === "object" ? value : {}; }
  catch { return {}; }
}
function resolveWorkloadText(value, form) {
  return String(value || "")
    .replaceAll("{{installDir}}", form.installDir || "{installDir}")
    .replaceAll("{{version}}", form.version || "{version}")
    .replaceAll("{{profile}}", form.profile || "{profile}")
    .replaceAll("{{customer.name}}", form.customerName || form.customer || "{customer}")
    .replaceAll("{{ports.web}}", form.webPort || "{port}");
}
function componentSourceLabel(source) {
  return ({ "artifact-repository": "制品仓库", "manual-upload": "上传文件", "fixed-url": "固定链接", "previous-build": "历史构建" }[source?.kind] || "产品默认");
}
function CreateForm({ form, setForm, products, product, selectProduct, upload, create, auth, busy, askAgent }) {
  const [step, setStep] = useState(0);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [stepError, setStepError] = useState("");
  const patch = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  const components = product?.components || [];
  const workloads = product?.workloads || [];
  const overrides = useMemo(() => parseDraftObject(form.componentOverridesText), [form.componentOverridesText]);
  const variablesInspection = useMemo(() => inspectJson(form.variablesText), [form.variablesText]);
  const overridesInspection = useMemo(() => inspectJson(form.componentOverridesText), [form.componentOverridesText]);
  const advancedReady = variablesInspection.valid && overridesInspection.valid;
  const enabledWorkloads = workloads.filter((workload) => workload.enabled !== false && form.modules?.[workload.module] !== false);
  const overrideCount = Object.keys(overrides).length;
  const targetReady = Boolean(form.productId && form.target && form.profile && form.version);
  const sourceReady = (component) => {
    const source = overrides[component.id] || component.source || {};
    if (!component.required) return true;
    if (source.kind === "manual-upload") return Boolean(source.uploadId || source.uri);
    if (source.kind === "fixed-url") return Boolean(source.url || source.uri);
    if (source.kind === "previous-build") return Boolean(source.uri);
    if (source.kind === "artifact-repository") return Boolean(source.path || source.uri);
    return false;
  };
  const componentsReady = components.every(sourceReady);
  const workloadsReady = workloads.length === 0 || enabledWorkloads.length > 0;
  const port = Number(form.webPort);
  const runtimeReady = Boolean(form.installDir && Number.isInteger(port) && port > 0 && port <= 65535);
  const stepReady = [targetReady, componentsReady, workloadsReady, runtimeReady, targetReady && componentsReady && workloadsReady && runtimeReady && advancedReady][step];
  const completion = [targetReady, componentsReady, workloadsReady, runtimeReady].filter(Boolean).length * 25;
  const sourceModes = [
    { label: "产品默认", value: "manifest" },
    { label: "上传文件", value: "manual-upload" },
    { label: "固定链接", value: "fixed-url" },
    { label: "仓库路径", value: "artifact-repository" },
    { label: "历史构建", value: "previous-build" },
  ];
  const updateOverride = (id, next) => {
    const value = { ...overrides };
    if (!next) delete value[id]; else value[id] = next;
    patch("componentOverridesText", asJson(value));
  };
  const changeSourceMode = (component, kind) => {
    if (kind === "manifest") return updateOverride(component.id, null);
    const previous = overrides[component.id];
    if (previous?.kind === kind) return;
    const base = component.source?.kind === kind ? component.source : {};
    updateOverride(component.id, { ...base, kind });
  };
  const changeTarget = (target) => {
    const targetConfig = product?.targetConfig?.[target] || {};
    setForm((current) => ({ ...current, target, installDir: targetConfig.installDir || current.installDir }));
  };
  const toggleModule = (module, enabled) => patch("modules", { ...form.modules, [module]: enabled });
  const nextStep = () => {
    if (!stepReady) {
      setStepError(["请选择完整的产品、目标、Profile 和版本。", "必需组件还没有可解析的来源。", "至少需要启用一个可执行 Workload。", "请填写安装目录，并使用 1–65535 范围内的 Web 端口。", "构建配置还不完整。"][step]);
      return;
    }
    setStepError(""); setStep((current) => Math.min(current + 1, 4));
  };
  const sourceEditor = (component) => {
    const override = overrides[component.id];
    const source = override || component.source || {};
    const mode = override ? override.kind : "manifest";
    const setSource = (next) => updateOverride(component.id, { ...source, ...next });
    if (mode === "manual-upload") return <Upload.Dragger className="vela-component-upload" accept={(source.accept || component.source?.accept || []).join(",")} showUploadList={false} beforeUpload={async (file) => { updateOverride(component.id, { kind: "manual-upload" }); return upload(file, component.id); }}><InboxOutlined /><p>拖入文件或点击上传</p><Text type="secondary">{source.uploadId ? `已关联 ${source.uploadId}` : "上传后写入 uploadId 与受控制品 URI"}</Text></Upload.Dragger>;
    if (mode === "fixed-url") return <Form.Item label={<FieldLabel tip="后端会下载该 URL、计算 SHA256 并写入 BuildLock。不要把带凭据的临时签名放入长期配置。">下载链接</FieldLabel>}><Input prefix={<LinkOutlined />} placeholder="https://downloads.example.com/component.tar.gz" value={source.url || ""} onChange={(event) => setSource({ kind: "fixed-url", url: event.target.value })} /></Form.Item>;
    if (mode === "artifact-repository") return <div className="vela-source-grid"><Form.Item label="仓库"><Input prefix={<DatabaseOutlined />} placeholder="gitlab 或 minio" value={source.repository || ""} onChange={(event) => setSource({ kind: "artifact-repository", repository: event.target.value })} /></Form.Item><Form.Item label="精确包路径"><Input placeholder="artifact/Publish/app/main/{{version}}/app.tar.gz" value={source.path || ""} onChange={(event) => setSource({ kind: "artifact-repository", path: event.target.value })} /></Form.Item></div>;
    if (mode === "previous-build") return <Form.Item label={<FieldLabel tip="填写已批准的历史构建制品 URI；服务端仍会重新解析和校验。">历史制品 URI</FieldLabel>}><Input prefix={<HistoryOutlined />} placeholder="artifact://builds/build-123/output" value={source.uri || ""} onChange={(event) => setSource({ kind: "previous-build", uri: event.target.value })} /></Form.Item>;
    return <div className="vela-source-default"><CheckCircleOutlined /><div><Text strong>{componentSourceLabel(component.source)}</Text><Text type="secondary">{component.source?.path || component.source?.url || component.source?.uri || "由产品 Manifest 在服务端解析"}</Text></div></div>;
  };
  const targetPanel = <section className="vela-create-panel"><div className="vela-panel-heading"><div><Title level={2}>选择交付目标</Title><Text type="secondary">产品定义决定平台、Profile、组件目录与打包模板。</Text></div><Tag color="blue">Preview</Tag></div><Form.Item label={<FieldLabel tip="切换产品会重新加载 Manifest 默认值，并清理不兼容的组件覆盖。">产品</FieldLabel>}><Select size="large" value={form.productId || undefined} placeholder="选择产品定义" options={products.map((item) => ({ value: item.id, label: item.name || item.id, title: item.description }))} onChange={(id) => selectProduct(products.find((item) => item.id === id))} /></Form.Item>{product && <div className="vela-product-choice"><div><Text strong>{product.name || product.id}</Text><Text type="secondary">{product.description || `${components.length} 个组件，${workloads.length} 个 Workload`}</Text></div><span>{components.length} 组件 / {workloads.length} Workload</span></div>}<div className="vela-form-grid"><Form.Item className="is-wide" label={<FieldLabel tip="目标平台会改变安装目录、模板和 Runner 调度约束。">目标平台</FieldLabel>}><Radio.Group className="vela-target-choice" optionType="button" buttonStyle="solid" value={form.target} onChange={(event) => changeTarget(event.target.value)} options={(product?.targets || []).map((value) => ({ value, label: value === "windows" ? "Windows Installer" : "Linux Package" }))} /></Form.Item><Form.Item label={<FieldLabel tip="Profile overlay 在 target 之后合并，本次请求参数仍拥有更高优先级。">Profile</FieldLabel>}><Select value={form.profile} options={(product?.profiles || []).map((value) => ({ value }))} onChange={(value) => patch("profile", value)} /></Form.Item><Form.Item label={<FieldLabel tip="填写后会合并同名客户 overlay；留空表示不应用客户覆盖。">客户标识</FieldLabel>}><Input placeholder="可选，例如 city-bank" value={form.customer} onChange={(event) => patch("customer", event.target.value)} /></Form.Item><Form.Item className="is-wide" label={<FieldLabel tip="latest 可用于 Preview，但 Release 门禁要求固定版本。">构建版本</FieldLabel>}><Input list="vela-version-options" value={form.version} onChange={(event) => patch("version", event.target.value)} placeholder="例如 1.4.2 或 latest" /><datalist id="vela-version-options">{(product?.versions || []).map((value) => <option key={value} value={value} />)}</datalist></Form.Item></div></section>;
  const componentsPanel = <section className="vela-create-panel"><div className="vela-panel-heading"><div><Title level={2}>选择组件来源</Title><Text type="secondary">默认来源不产生 override；只为本次构建替换的组件写入 ComponentSource。</Text></div><Tag>{overrideCount} 个覆盖</Tag></div>{components.length ? <div className="vela-component-studio">{components.map((component) => { const source = overrides[component.id] || component.source || {}; const mode = overrides[component.id] ? source.kind : "manifest"; return <article className={`vela-component-editor ${sourceReady(component) ? "is-ready" : "has-error"}`} key={component.id}><div className="vela-component-heading"><div className="vela-component-identity"><span><CloudServerOutlined /></span><div><Space size={6}><Text strong>{component.id}</Text>{component.required ? <Tag color="orange">必需</Tag> : <Tag>可选</Tag>}</Space><Text type="secondary">{component.type || "artifact"} / 当前：{mode === "manifest" ? componentSourceLabel(component.source) : componentSourceLabel(source)}</Text></div></div><Radio.Group size="small" value={mode} onChange={(event) => changeSourceMode(component, event.target.value)} options={sourceModes} optionType="button" /></div><div className="vela-component-source">{sourceEditor(component)}</div></article>; })}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="该产品没有外部组件；可直接进入 Workload 配置" />}</section>;
  const workloadsPanel = <section className="vela-create-panel"><div className="vela-panel-heading"><div><Title level={2}>编排 Workload</Title><Text type="secondary">Workload 结构来自 Manifest；模块开关决定哪些服务进入最终 BuildLock。</Text></div><Tag color={workloadsReady ? "success" : "warning"}>{enabledWorkloads.length} / {workloads.length} 启用</Tag></div><ContextNote title="这里配置的是解析结果">创建接口没有 workloadOverrides。页面通过模块、变量和运行配置预览服务端将解析出的 docker/systemd 工作负载。</ContextNote>{workloads.length ? <div className="vela-workload-map">{workloads.map((workload, index) => { const enabled = workload.enabled !== false && form.modules?.[workload.module] !== false; const runtime = workload.runtime === "systemd" ? workload.systemd || {} : workload.docker || {}; const primary = workload.runtime === "systemd" ? resolveWorkloadText(runtime.execStart, form) : resolveWorkloadText(runtime.image, form); const secondary = workload.runtime === "systemd" ? runtime.unitName : (runtime.ports || []).map((value) => resolveWorkloadText(value, form)).join(", "); return <article className={`vela-workload-node ${enabled ? "is-enabled" : "is-disabled"}`} key={workload.id}><span className="vela-workload-index">{index + 1}</span><div className="vela-workload-copy"><div><Space size={7}><Text strong>{workload.id}</Text><Tag color={workload.runtime === "docker" ? "cyan" : "purple"}>{workload.runtime}</Tag></Space><Text type="secondary">组件 {workload.component} / 模块 {workload.module}</Text></div><code>{primary || "运行入口由服务端模板生成"}</code>{secondary && <Text type="secondary">{secondary}</Text>}</div><div className="vela-workload-switch"><Switch checked={enabled} onChange={(checked) => toggleModule(workload.module, checked)} aria-label={`${enabled ? "停用" : "启用"} ${workload.id}`} /><Text type="secondary">{enabled ? "进入 BuildLock" : "本次不部署"}</Text></div></article>; })}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="Manifest 没有 Workload；构建只生成安装制品" />}</section>;
  const runtimePanel = <section className="vela-create-panel"><div className="vela-panel-heading"><div><Title level={2}>运行与安装参数</Title><Text type="secondary">这些值进入 configOverrides，并参与 Workload 模板渲染。</Text></div><Button onClick={() => setAdvancedOpen(true)}>高级变量</Button></div><div className="vela-form-grid"><Form.Item label={<FieldLabel tip="随目标平台自动切换，可在本次构建覆盖。">安装目录</FieldLabel>}><Input value={form.installDir} onChange={(event) => patch("installDir", event.target.value)} /></Form.Item><Form.Item label={<FieldLabel tip="进入 ports.web，并替换 Workload 中的 {{ports.web}}。">Web 端口</FieldLabel>}><Input type="number" min={1} max={65535} value={form.webPort} onChange={(event) => patch("webPort", event.target.value)} /></Form.Item><Form.Item label="服务版本"><Input value={form.serviceVersion} onChange={(event) => patch("serviceVersion", event.target.value)} /></Form.Item><Form.Item label="客户显示名称"><Input value={form.customerName} onChange={(event) => patch("customerName", event.target.value)} /></Form.Item><Form.Item label="许可类型"><Select value={form.license} onChange={(value) => patch("license", value)} options={[{ value: "community" }, { value: "enterprise" }, { value: "trial" }]} /></Form.Item><Form.Item label={<FieldLabel tip="这里只提交策略名称，密码和 secret value 不得进入构建请求。">密码策略</FieldLabel>}><Select value={form.passwordPolicy} onChange={(value) => patch("passwordPolicy", value)} options={[{ value: "strong" }, { value: "generated" }, { value: "external-secret" }]} /></Form.Item></div><div className="vela-runtime-preview"><div><Text type="secondary">安装根目录</Text><code>{form.installDir || "未设置"}</code></div><div><Text type="secondary">启用服务</Text><strong>{enabledWorkloads.length}</strong></div><div><Text type="secondary">对外端口</Text><strong>{form.webPort || "—"}</strong></div></div></section>;
  const confirmPanel = <section className="vela-create-panel"><div className="vela-panel-heading"><div><Title level={2}>检查并发起构建</Title><Text type="secondary">服务端会重新解析全部来源，生成不可变 BuildLock，再保存为待审批构建。</Text></div><Tag color="gold">pending_approval</Tag></div><div className="vela-review-grid"><div><Text type="secondary">交付目标</Text><strong>{product?.name || form.productId}</strong><span>{form.target} / {form.profile} / {form.version}</span></div><div><Text type="secondary">组件来源</Text><strong>{components.length} 个组件</strong><span>{overrideCount ? `${overrideCount} 个使用本次覆盖` : "全部使用 Manifest 默认来源"}</span></div><div><Text type="secondary">Workload</Text><strong>{enabledWorkloads.length} 个启用</strong><span>{enabledWorkloads.map((item) => item.id).join("、") || "无运行服务"}</span></div><div><Text type="secondary">运行参数</Text><strong>{form.installDir}</strong><span>Web {form.webPort} / {form.license}</span></div></div><div className="vela-preflight-list">{[[targetReady, "目标与版本完整"], [componentsReady, "必需组件来源可解析"], [workloadsReady, "Workload 编排有效"], [runtimeReady, "运行参数有效"], [advancedReady, "高级 JSON 语法有效"]].map(([ok, label]) => <div className={ok ? "is-pass" : "is-blocked"} key={label}>{ok ? <CheckCircleOutlined /> : <WarningOutlined />}<span>{label}</span><Tag color={ok ? "success" : "warning"}>{ok ? "通过" : "需处理"}</Tag></div>)}</div><Alert className="vela-inline-alert" type="info" showIcon message="提交不会立即执行" description="构建先进入 pending_approval。审批后才会调度 Runner；成功后仍需验证 Preview，Release 发布由服务端门禁裁决。" /></section>;
  const panels = [targetPanel, componentsPanel, workloadsPanel, runtimePanel, confirmPanel];
  const stepItems = [{ title: "目标" }, { title: "组件" }, { title: "Workload" }, { title: "运行参数" }, { title: "发起" }];
  const guidance = [
    ["锁定交付坐标", "选择产品、目标、Profile 与版本，右侧蓝图会立即更新。"],
    ["解析组件来源", "保留默认来源，或使用上传、固定链接、仓库路径与历史构建制品。"],
    ["形成服务拓扑", "模块开关直接影响进入 BuildLock 的 Workload；运行定义仍以 Manifest 为准。"],
    ["渲染运行模板", "安装目录、端口与客户配置会替换 docker/systemd 模板中的变量。"],
    ["等待服务端锁定", "发起后进入审批队列；页面不会乐观伪造状态或发布结果。"],
  ][step];
  const agentAssessment = !targetReady ? "还缺少完整的交付坐标，Agent 会先检查产品、目标、Profile 与版本。" : !componentsReady ? "存在无法解析的必需组件来源，建议先检查 URI、上传结果与固定版本。" : !workloadsReady ? "当前没有可执行 Workload；需要确认这是纯制品构建还是模块开关误操作。" : !runtimeReady ? "运行参数未通过检查，Agent 会重点核对安装目录与端口冲突。" : !advancedReady ? "高级 JSON 存在语法错误，当前蓝图不能安全提交。" : form.version === "latest" ? "Preview 可以使用 latest；正式发布前应固定产品与组件版本。" : "当前输入已通过本地预检，可以让 Agent 解释影响范围，再提交服务端生成 BuildLock。";
  const askBlueprint = () => askAgent(`请检查当前构建蓝图：产品 ${product?.name || form.productId || "未选择"}，目标 ${form.target}，Profile ${form.profile}，版本 ${form.version}，${components.length} 个组件，${enabledWorkloads.length} 个 Workload，安装目录 ${form.installDir || "未设置"}，端口 ${form.webPort || "未设置"}。先列出依据和风险，不要直接发起构建。`);
  return <div className="vela-create-workspace"><Form layout="vertical" className="vela-create-form"><ContextNote title="从构建输入到可复现蓝图">选择会实时组成组件、Workload 与运行参数预览；最终结果仍以服务端生成的 BuildLock 为准。</ContextNote><Steps className="vela-create-steps" size="small" current={step} items={stepItems} onChange={(next) => { if (next < step) { setStepError(""); setStep(next); } }} /><div key={step} className="vela-form-step">{panels[step]}</div>{stepError && <Alert className="vela-inline-alert" type="warning" showIcon message="当前步骤还不能继续" description={stepError} />}<div className="vela-step-tools"><Space wrap><Button disabled={step === 0} onClick={() => { setStepError(""); setStep(step - 1); }}>上一步</Button><Button onClick={() => setAdvancedOpen(true)}>查看高级 JSON</Button></Space><Space wrap>{step < 4 && <Button type="primary" onClick={nextStep}>保存并继续</Button>}{step === 4 && <GuardedButton auth={auth} permission="build.create" title="发起 Preview 构建" type="primary" loading={busy === "create"} disabled={!stepReady} onClick={create}>发起 Preview 构建</GuardedButton>}</Space></div><Drawer title="高级构建输入" open={advancedOpen} onClose={() => setAdvancedOpen(false)} width="min(680px, 100vw)"><Alert type="warning" showIcon message="高级输入会参与服务端合并" description="优先级为 request overrides ＞ variables ＞ customer ＞ profile ＞ target ＞ base。不要填写 token、密码、私钥或对象存储凭据。" style={{ marginBottom: 16 }} /><Form.Item label={<FieldLabel tip="可用于 Manifest 模板解析；必须是 JSON 对象。">构建变量 JSON</FieldLabel>}><JsonEditor label="构建变量 JSON" rows={9} value={form.variablesText} onChange={(value) => patch("variablesText", value)} /></Form.Item><Form.Item label={<FieldLabel tip="每个键是组件 ID，值必须符合 OpenAPI ComponentSource。">组件覆盖 JSON</FieldLabel>}><JsonEditor label="组件覆盖 JSON" rows={13} value={form.componentOverridesText} onChange={(value) => patch("componentOverridesText", value)} /></Form.Item></Drawer></Form><aside className="vela-create-context vela-build-blueprint" aria-label="BuildLock 实时蓝图"><div className="vela-context-heading"><div><Title level={2}>BuildLock 蓝图</Title><Text type="secondary">提交前预览</Text></div><Tag color={completion === 100 ? "success" : "processing"}>{completion}%</Tag></div><div className="vela-blueprint-progress"><span style={{ width: `${completion}%` }} /></div><div className="vela-blueprint-agent"><div><span><RobotOutlined /></span><div><Text strong>Agent 配置诊断</Text><Text type="secondary">{agentAssessment}</Text></div></div><Button size="small" onClick={askBlueprint}>检查蓝图</Button></div><div className="vela-blueprint-target"><span className="vela-blueprint-icon"><ApartmentOutlined /></span><div><Text strong>{product?.name || "等待选择产品"}</Text><Text type="secondary">{form.target || "target"} / {form.profile || "profile"} / {form.version || "version"}</Text></div></div><div className="vela-blueprint-flow"><div><span><DatabaseOutlined /></span><div><Text strong>{components.length} 个组件</Text><Text type="secondary">{overrideCount} 个请求覆盖</Text></div></div><div><span><BuildOutlined /></span><div><Text strong>{enabledWorkloads.length} 个 Workload</Text><Text type="secondary">{workloads.filter((item) => form.modules?.[item.module] === false).length} 个已停用</Text></div></div><div><span><CloudServerOutlined /></span><div><Text strong>{form.target === "windows" ? "Windows Runner" : "Linux Runner"}</Text><Text type="secondary">审批后由调度器分配</Text></div></div></div>{enabledWorkloads.length > 0 && <div className="vela-blueprint-workloads"><Text type="secondary">将进入锁定配置</Text>{enabledWorkloads.slice(0, 5).map((workload) => <div key={workload.id}><span className={`is-${workload.runtime}`} /><strong>{workload.id}</strong><small>{workload.runtime}</small></div>)}</div>}<div className="vela-create-guidance"><Text strong>{guidance[0]}</Text><p>{guidance[1]}</p></div><ol className="vela-build-lifecycle-preview"><li className="is-current"><span>1</span><div><strong>待审批</strong><small>BuildLock 已生成</small></div></li><li><span>2</span><div><strong>排队与执行</strong><small>Runner 回传状态和日志</small></div></li><li><span>3</span><div><strong>验证 Preview</strong><small>安装与运行验收</small></div></li><li><span>4</span><div><strong>发布 Release</strong><small>固定版本与门禁检查</small></div></li></ol></aside></div>;
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
  const syntax = useMemo(() => inspectJson(manifest), [manifest]);
  const dryRunErrors = Array.isArray(dryRun?.errors) ? dryRun.errors : [];
  return <div className="vela-editor-layout"><aside className="vela-resource-nav"><div className="vela-resource-nav-heading"><div><Text strong>产品定义</Text><Text type="secondary">{products.length} 个产品</Text></div></div><List dataSource={products} locale={{ emptyText: "没有产品定义" }} renderItem={(item) => <List.Item className={item.id === product?.id ? "is-active" : ""}><button type="button" onClick={() => selectProduct(item)}><strong>{item.name || item.id}</strong><span>{item.id} / {(item.targets || []).join(", ") || "无目标"}</span></button></List.Item>} /></aside><main className="vela-editor-main"><ContextNote title="先验证，再保存">浏览器先检查 JSON 语法，试运行再验证 Manifest 语义；最终仍以服务端的 valid、errors[] 与 BuildLock 为准。</ContextNote><div className="vela-editor-toolbar"><div><Space><Title level={2}>Manifest</Title>{dirty && <Tag color="warning">未保存</Tag>}</Space><Text type="secondary">{product?.name || "请选择产品"} / JSON 产品定义</Text></div><Space wrap><Button disabled={!syntax.valid} loading={busy === "dryrun"} onClick={manifestDryRun}>试运行</Button><Button onClick={() => { if (dirty && !window.confirm("确认丢弃未保存修改并重新加载 Manifest")) return; selectProduct(product); }}>重新加载</Button><GuardedButton auth={auth} permission="manifest.write" type="primary" disabled={!syntax.valid} loading={busy === "manifest"} onClick={saveManifest}>保存 Manifest</GuardedButton></Space></div><JsonEditor label="Manifest JSON" rows={22} value={manifest} onChange={setManifest} />{dryRun && <Alert className="vela-editor-result" type={dryRun.valid ? "success" : "warning"} showIcon message={dryRun.valid ? "试运行通过" : `试运行未通过${dryRunErrors.length ? `（${dryRunErrors.length} 项）` : ""}`} description={dryRun.valid ? <div><Text>当前定义可以解析为不可变 BuildLock。</Text>{dryRun.buildLock && <div className="vela-result-meta"><Tag color="success">BuildLock 已生成</Tag><code>{dryRun.buildLock.buildId || "预览锁"}</code></div>}</div> : dryRunErrors.length ? <ul className="vela-validation-list">{dryRunErrors.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}</ul> : <Text>服务端未返回具体错误；请检查 Manifest 结构和所选目标。</Text>} />}<section className="vela-revision-list"><div className="vela-section-heading"><div><Title level={2}>版本记录</Title><Text type="secondary">每次保存或恢复都会形成新修订；已生成的构建锁不会随修订变化</Text></div></div><List dataSource={revisions} locale={{ emptyText: "还没有历史版本" }} renderItem={(revision) => <List.Item actions={[<Popconfirm key="restore" title="确认恢复此历史版本？" description="恢复会创建新的当前修订，不会改写既有 BuildLock。" onConfirm={() => restoreProductManifestRevision(product.id, revision.id || revision.revisionId).then(() => selectProduct(product))}><Button size="small">恢复为新版本</Button></Popconfirm>]}><List.Item.Meta title={revision.id || revision.revision} description={<Space size={8}><span>{when(revision.createdAt)}</span>{revision.actorId && <span>{revision.actorId}</span>}</Space>} /></List.Item>} /></section></main></div>;
}
function Overlays({ product, overlay, auth }) {
  const names = overlay.overlays[overlay.overlayKind] || [];
  const dirty = overlay.overlayText !== overlay.savedOverlay;
  const syntax = useMemo(() => inspectJson(overlay.overlayText), [overlay.overlayText]);
  const startNew = () => { overlay.setOverlayName(""); overlay.setOverlayText("{}"); };
  const changeKind = (kind) => {
    const first = overlay.overlays[kind]?.[0];
    if (first) overlay.loadOverlay(kind, first.name);
    else { overlay.setOverlayKind(kind); overlay.setOverlayName(""); overlay.setOverlayText("{}"); }
  };
  return <div className="vela-editor-layout"><aside className="vela-resource-nav"><Tabs activeKey={overlay.overlayKind} onChange={changeKind} items={[{ key: "profiles", label: "Profile" }, { key: "customers", label: "客户" }]} /><Button block onClick={startNew}>新建覆盖</Button><List dataSource={names} locale={{ emptyText: "当前分类没有覆盖配置" }} renderItem={(item) => <List.Item className={item.name === overlay.overlayName ? "is-active" : ""}><button type="button" onClick={() => overlay.loadOverlay(overlay.overlayKind, item.name)}><strong>{item.name}</strong><span>{overlay.overlayKind === "profiles" ? "运行环境" : "客户配置"}</span></button></List.Item>} /></aside><main className="vela-editor-main"><ContextNote title="最终值按优先级覆盖">请求 overrides ＞ variables ＞ customer ＞ profile ＞ target ＞ base。靠左的来源覆盖靠右的同名字段，未声明字段继续继承。</ContextNote><div className="vela-editor-toolbar"><div><Space><Title level={2}>{overlay.overlayName || "新建覆盖"}</Title>{dirty && <Tag color="warning">未保存</Tag>}</Space><Text type="secondary">{product?.name || product?.id || "未选择产品"} / {overlay.overlayKind === "profiles" ? "Profile" : "客户"}</Text></div><GuardedButton auth={auth} permission="overlay.write" type="primary" disabled={!syntax.valid} onClick={overlay.saveOverlay}>保存覆盖</GuardedButton></div><Form layout="vertical"><Form.Item label={<FieldLabel tip="名称会成为构建请求中的 profile 或 customer 标识。">名称</FieldLabel>} extra="可使用字母、数字、点、下划线和连字符"><Input aria-label="覆盖名称" value={overlay.overlayName} onChange={(e) => overlay.setOverlayName(e.target.value)} /></Form.Item><Form.Item label={<FieldLabel tip="只写需要覆盖的字段；空对象表示不改变上游配置。">覆盖配置 JSON</FieldLabel>}><JsonEditor label="覆盖配置 JSON" rows={22} value={overlay.overlayText} onChange={overlay.setOverlayText} /></Form.Item></Form><Alert type="info" showIcon message="保存覆盖不会改写历史构建" description="新配置只会在后续创建构建时参与合并；既有构建继续使用各自的不可变 BuildLock。" /></main></div>;
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
  const [refreshing, setRefreshing] = useState(false);
  const [updatedAt, setUpdatedAt] = useState("");
  const [loadError, setLoadError] = useState("");
  const build = builds.find((item) => item.id === selectedId) || builds[0];
  const live = ["queued", "running"].includes(build?.status);
  const loadContext = async (buildId, background = false) => {
    if (!buildId) return;
    if (background) setRefreshing(true); else setLoading(true);
    setLoadError("");
    try {
      const [logs, lock, release, reproducibility] = await Promise.all([getBuildLogs(buildId), getBuildLock(buildId), getBuildReleaseCheck(buildId), getBuildReproducibilityReport(buildId)]);
      setData({ logs, lock, release, reproducibility });
      setUpdatedAt(new Date().toISOString());
    } catch (error) { setLoadError(error); }
    finally { if (background) setRefreshing(false); else setLoading(false); }
  };
  useEffect(() => {
    if (!build?.id) return;
    loadContext(build.id);
  }, [build?.id]);
  useEffect(() => {
    if (!build?.id || !live) return undefined;
    const timer = window.setInterval(() => loadContext(build.id, true), 5000);
    return () => window.clearInterval(timer);
  }, [build?.id, live]);
  if (!builds.length) return <Empty description="还没有构建，创建构建后可在这里查看日志与锁定配置" />;
  return <div className="vela-logs-workspace"><aside className="vela-log-builds"><div><Text strong>选择构建</Text><Text type="secondary">{builds.length} 个构建记录</Text></div><Select value={build?.id} onChange={setSelectedId} options={builds.map((item) => ({ value: item.id, label: `${item.productId} / ${item.id}` }))} /><List dataSource={builds.slice(0, 10)} renderItem={(item) => <List.Item className={item.id === build?.id ? "is-active" : ""}><button type="button" onClick={() => setSelectedId(item.id)}><span>{buildStatus(item)}</span><strong>{item.id}</strong><small>{item.target || "—"} / {item.profile || "—"}</small></button></List.Item>} /></aside><main className="vela-log-main"><div className="vela-log-heading"><div><Space>{buildStatus(build)}<Title level={2}>{build.id}</Title></Space><Text type="secondary">{build.productId} / {build.target || "—"} / {build.profile || "—"} / {build.version || "—"}</Text><div className="vela-log-freshness" aria-live="polite">{live && <Tag color="processing">运行中每 5 秒刷新</Tag>}<Text type="secondary">{updatedAt ? `上下文更新于 ${when(updatedAt)}` : "正在读取构建上下文"}</Text></div></div><Space wrap><Button icon={<ReloadOutlined />} loading={refreshing} onClick={() => loadContext(build.id, true)}>刷新上下文</Button><Button icon={<DownloadOutlined />} onClick={async () => downloadJson(await exportBuildReleaseEvidenceArchive(build.id), `${build.id}-evidence.json`)}>导出证据</Button></Space></div>{loadError && <ErrorBanner error={loadError} onClose={() => setLoadError("")} />}{loading ? <div className="vela-loading"><Spin /><Text type="secondary">加载构建上下文</Text></div> : <LogPanel build={build} data={data} />}</main></div>;
}
function LogPanel({ build, data }) {
  const logs = Array.isArray(data.logs) ? data.logs : data.logs?.items || [];
  const [filter, setFilter] = useState("");
  const uniqueLogs = Array.from(new Map(logs.map((item, index) => [typeof item === "object" && item?.sequence ? `sequence-${item.sequence}` : `index-${index}`, item])).values());
  const lines = uniqueLogs.map((item) => typeof item === "string" ? item : `${item.sequence ? `#${item.sequence} ` : ""}${item.time ? `${when(item.time)} ` : ""}${item.source ? `[${item.source}] ` : ""}${item.message || asJson(item)}`).map(stripAnsi).filter((line) => line.toLowerCase().includes(filter.toLowerCase()));
  const code = (value, label) => <JsonCodeBlock value={value} label={label} />;
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
  return <Drawer title="构建详情" width="min(900px, 100vw)" open={open} onClose={onClose} extra={build && <Button icon={<DownloadOutlined />} disabled={!build.downloadUrl && !build.artifactUri} onClick={() => onDownload(build)}>下载制品</Button>}>{build && <><section className="vela-drawer-summary"><div><Space>{buildStatus(build)}<Title level={2}>{build.id}</Title></Space><Text type="secondary">{build.productId} / {build.version || "—"}</Text></div><Descriptions column={2} size="small"><Descriptions.Item label="构建 ID">{build.id}</Descriptions.Item><Descriptions.Item label="目标">{build.target} / {build.profile}</Descriptions.Item><Descriptions.Item label="Runner">{build.runnerId || "尚未分配"}</Descriptions.Item><Descriptions.Item label="更新时间">{when(build.updatedAt)}</Descriptions.Item><Descriptions.Item label="执行尝试">第 {Number(build.attempt || 0) + 1} 次</Descriptions.Item><Descriptions.Item label="Nomad Job">{build.nomadJobId || "尚未调度"}</Descriptions.Item></Descriptions><BuildLifecycle build={build} /><ContextNote title="详情以 BuildLock 为准" tone="secure">构建创建后，锁定配置不会被之后的 Manifest、Profile 或客户覆盖修改。界面中的最终配置用于阅读，BuildLock 才是复现依据。</ContextNote></section><Tabs className="vela-detail-tabs" items={[{ key: "logs", label: "日志", children: <LogPanel build={build} data={data} /> }, { key: "release", label: releaseReady ? "发布门禁（通过）" : "发布门禁", children: <div><ContextNote title="门禁由服务端裁决">每个 checks[] 项包含稳定 id、stage 与 pass/blocked 状态；前端只解释结果，不自行推导发布权限。</ContextNote>{releaseItems.length ? <div className="vela-gate-list">{releaseItems.map((item) => <div key={item.key}>{item.children}</div>)}</div> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="服务端尚未返回门禁检查项" />}</div> }, { key: "components", label: "组件锁定", children: <Table size="small" rowKey={(item) => item.id} dataSource={data.lock?.components || []} pagination={false} columns={[{ title: "组件", dataIndex: "id" }, { title: "版本", dataIndex: "version" }, { title: "URI", dataIndex: "uri", render: (v) => <code>{v || "—"}</code> }, { title: "SHA256", dataIndex: "sha256", render: (v) => <code>{v || "—"}</code> }]} /> }, { key: "workloads", label: `工作负载 ${data.lock?.workloads?.length || 0}`, children: <div className="vela-locked-workloads">{(data.lock?.workloads || []).map((workload) => <div key={workload.id}><Tag color={workload.enabled === false ? "default" : workload.runtime === "docker" ? "cyan" : "purple"}>{workload.runtime}</Tag><div><Text strong>{workload.id}</Text><Text type="secondary">{workload.component} / {workload.module} / {workload.enabled === false ? "未启用" : "已锁定"}</Text></div></div>)}</div> }, { key: "config", label: "最终配置", children: <JsonCodeBlock value={build.finalConfig || data.lock?.config || {}} label="最终配置 JSON" /> }, { key: "lock", label: "构建锁", children: <div><ContextNote title="不可变构建输入" tone="secure">下载、复现和 Release 证据都应关联这个 BuildLock，而不是读取当前产品配置。</ContextNote><JsonCodeBlock value={data.lock} label="构建锁 JSON" /></div> }, { key: "repro", label: "可复现报告", children: <JsonCodeBlock value={data.reproducibility} label="可复现报告 JSON" /> }, { key: "evidence", label: "证据", children: <div className="vela-evidence-action"><Text>导出包含门禁、锁定配置和可复现报告的 Release 证据。</Text><Button type="primary" onClick={() => exportBuildReleaseEvidenceArchive(build.id).then((value) => downloadJson(value, `${build.id}-evidence.json`))}>导出 Release 证据</Button></div> }]} /></>}</Drawer>;
}
