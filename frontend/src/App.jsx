import {
  Badge,
  Box,
  Button,
  Callout,
  Card,
  Checkbox,
  Code,
  Dialog,
  Flex,
  Grid,
  Heading,
  Link,
  Select,
  Separator,
  Spinner,
  Table,
  Tabs,
  Text,
  TextArea,
  TextField,
  Tooltip,
} from "@radix-ui/themes";
import {
  AlertCircle,
  Ban,
  Boxes,
  Check,
  ChevronRight,
  CircleDot,
  ClipboardList,
  Code2,
  Copy,
  Database,
  Download,
  FileDown,
  FileJson,
  FileUp,
  Gauge,
  GitCompare,
  History,
  LayoutDashboard,
  ListChecks,
  PackageCheck,
  Play,
  RefreshCcw,
  RotateCcw,
  Rocket,
  ScrollText,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Upload,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  approveBuild,
  buildDownloadUrl,
  buildLogStreamUrl,
  cancelBuild,
  createBuild,
  dryRunProductManifest,
  getBuildLock,
  getBuildLogs,
  getBuildReleaseCheck,
  getProductManifest,
  getProductOverlay,
  listRunners,
  listProductManifestRevisions,
  listBuilds,
  listProductOverlays,
  listProducts,
  listUploads,
  publishBuild,
  rejectBuild,
  restoreProductManifestRevision,
  retryBuild,
  updateProductManifest,
  updateProductOverlay,
  uploadArtifact,
  validatePreviewBuild,
} from "./lib/api";
import {
  errorSurface,
  translateError,
  translateErrorDetail,
  translateLegacyReason,
  translateUserAction,
} from "./lib/i18n";
import productManifestSchema from "../../docs/product-manifest.schema.json";

const statusMeta = {
  pending_approval: { label: "待审批", color: "orange" },
  queued: { label: "排队中", color: "blue" },
  running: { label: "构建中", color: "purple" },
  succeeded: { label: "Preview 已生成", color: "green" },
  preview_validated: { label: "Preview 已验证", color: "teal" },
  published: { label: "Release 已发布", color: "green" },
  rejected: { label: "已拒绝", color: "gray" },
  failed: { label: "失败", color: "red" },
  canceled: { label: "已取消", color: "amber" },
};

const navItems = [
  { key: "overview", label: "总览", icon: LayoutDashboard },
  { key: "create", label: "创建构建", icon: Play },
  { key: "builds", label: "构建记录", icon: History },
  { key: "runners", label: "Runner 监控", icon: Server },
  { key: "products", label: "产品配置", icon: FileJson },
  { key: "overlays", label: "覆盖配置", icon: GitCompare },
  { key: "uploads", label: "制品上传", icon: FileUp },
  { key: "logs", label: "日志与锁定", icon: ScrollText },
];

const navGroups = [
  { label: "运行", keys: ["overview", "create", "builds"] },
  { label: "交付", keys: ["runners", "uploads", "logs"] },
  { label: "配置", keys: ["products", "overlays"] },
];

const buildFilterOptions = [
  { key: "all", label: "全部", icon: History, match: () => true },
  {
    key: "attention",
    label: "待处理",
    icon: AlertCircle,
    match: (build) => ["pending_approval", "failed", "canceled"].includes(build.status),
  },
  {
    key: "active",
    label: "执行中",
    icon: Gauge,
    match: (build) => ["queued", "running"].includes(build.status),
  },
  { key: "preview", label: "待验证", icon: PackageCheck, match: (build) => build.status === "succeeded" },
  {
    key: "validated",
    label: "已验证",
    icon: ShieldCheck,
    match: (build) => build.status === "preview_validated",
  },
  { key: "releaseReady", label: "可发布", icon: Rocket, match: (build) => getReleaseReadiness(build).ready },
  {
    key: "blocked",
    label: "发布阻塞",
    icon: Ban,
    match: (build) => build.status === "preview_validated" && !getReleaseReadiness(build).ready,
  },
];

const sourceKindOptions = [
  { key: "artifact-repository", label: "制品仓库" },
  { key: "manual-upload", label: "手动上传" },
  { key: "fixed-url", label: "固定 URL" },
  { key: "previous-build", label: "历史构建" },
];

const sourceKindLabels = Object.fromEntries(
  sourceKindOptions.map((item) => [item.key, item.label]),
);

const overlayNamePattern = /^[A-Za-z0-9._-]+$/;

export default function App() {
  const [activeSection, setActiveSection] = useState("overview");
  const [products, setProducts] = useState([]);
  const [builds, setBuilds] = useState([]);
  const [uploads, setUploads] = useState([]);
  const [runners, setRunners] = useState([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [savingManifest, setSavingManifest] = useState(false);
  const [uploadingStandalone, setUploadingStandalone] = useState(false);
  const [busyAction, setBusyAction] = useState("");
  const [pageAlert, setPageAlert] = useState(null);
  const [toasts, setToasts] = useState([]);
  const [selectedBuild, setSelectedBuild] = useState(null);
  const [logs, setLogs] = useState([]);
  const [buildLock, setBuildLock] = useState(null);
  const [releaseCheck, setReleaseCheck] = useState(null);
  const [manifestText, setManifestText] = useState("");
  const [originalManifestText, setOriginalManifestText] = useState("");
  const [manifestDryRun, setManifestDryRun] = useState(null);
  const [dryRunningManifest, setDryRunningManifest] = useState(false);
  const [manifestRevisions, setManifestRevisions] = useState([]);
  const [loadingManifestRevisions, setLoadingManifestRevisions] = useState(false);
  const [profileOverlays, setProfileOverlays] = useState([]);
  const [customerOverlays, setCustomerOverlays] = useState([]);
  const [overlayKind, setOverlayKind] = useState("profiles");
  const [overlayName, setOverlayName] = useState("");
  const [overlayText, setOverlayText] = useState("{}");
  const [originalOverlayText, setOriginalOverlayText] = useState("{}");
  const [savingOverlay, setSavingOverlay] = useState(false);
  const [searchText, setSearchText] = useState("");
  const [buildFilter, setBuildFilter] = useState("all");
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [confirmAction, setConfirmAction] = useState(null);
  const [form, setForm] = useState({
    productId: "",
    target: "linux",
    profile: "qa",
    customer: "",
    version: "latest",
    releaseChannel: "preview",
    installDir: "",
    webPort: "48000",
    serviceVersion: "latest",
    customerName: "",
    license: "",
    passwordPolicy: "strong",
    modules: {
      camera: true,
      push: true,
      map: true,
    },
    variablesText: "{}",
    componentOverridesText: "{}",
  });
  const [uploadForm, setUploadForm] = useState({
    downloadPolicy: "platform",
    retentionDays: "30",
    productId: "",
    purpose: "manual-upload",
  });

  function dismissToast(id) {
    setToasts((current) => current.filter((toast) => toast.id !== id));
  }

  function setNotice(message, tone = "success") {
    if (!message) return;
    const id = `${Date.now()}-${Math.random().toString(16).slice(2)}`;
    setToasts((current) => [...current.slice(-3), { id, message, tone }]);
    window.setTimeout(() => dismissToast(id), 4200);
  }

  function setError(error) {
    if (!error) {
      setPageAlert(null);
      return;
    }
    const alert = normalizeError(error);
    if (alert.surface === "toast") {
      setNotice(alert.message, alert.tone);
      return;
    }
    setPageAlert(alert);
  }

  const selectedProduct = useMemo(
    () => products.find((product) => product.id === form.productId),
    [products, form.productId],
  );

  const filteredBuilds = useMemo(() => {
    const keyword = searchText.trim().toLowerCase();
    const filter = buildFilterOptions.find((item) => item.key === buildFilter) || buildFilterOptions[0];
    return builds.filter((build) => {
      if (!filter.match(build)) return false;
      if (!keyword) return true;
      return [
        build.id,
        build.productId,
        build.target,
        build.profile,
        build.version,
        statusMeta[build.status]?.label,
        build.status,
        build.runnerId,
        build.releaseChannel,
        build.customer,
      ]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(keyword));
    });
  }, [builds, searchText, buildFilter]);

  const buildFilterCounts = useMemo(() => {
    return Object.fromEntries(
      buildFilterOptions.map((option) => [
        option.key,
        builds.filter((build) => option.match(build)).length,
      ]),
    );
  }, [builds]);

  const hasBuildSearch = searchText.trim().length > 0;
  const hasBuildFilter = buildFilter !== "all";
  const visibleBuildTotal = hasBuildSearch || hasBuildFilter ? filteredBuilds.length : builds.length;

  const dashboard = useMemo(() => {
    const counts = Object.fromEntries(Object.keys(statusMeta).map((key) => [key, 0]));
    for (const build of builds) {
      counts[build.status] = (counts[build.status] || 0) + 1;
    }
    const active = (counts.queued || 0) + (counts.running || 0);
    const attention =
      (counts.pending_approval || 0) + (counts.failed || 0) + (counts.canceled || 0);
    const latest = [...builds].sort((a, b) => dateValue(b.updatedAt) - dateValue(a.updatedAt))[0];
    const successful =
      (counts.succeeded || 0) + (counts.preview_validated || 0) + (counts.published || 0);
    const previewPending = builds.filter((build) => build.status === "succeeded").length;
    const releaseReady = builds.filter((build) => getReleaseReadiness(build).ready).length;
    const releaseBlocked = builds.filter((build) => {
      const readiness = getReleaseReadiness(build);
      return build.status === "preview_validated" && !readiness.ready;
    }).length;
    return {
      counts,
      active,
      attention,
      latest,
      products: products.length,
      uploads: uploads.length,
      successRate: builds.length === 0 ? 0 : Math.round((successful / builds.length) * 100),
      previewPending,
      releaseReady,
      releaseBlocked,
    };
  }, [builds, products.length, uploads.length]);

  const topbarMetrics = useMemo(
    () => [
      { label: "运行中", value: dashboard.active },
      { label: "待验证", value: dashboard.previewPending },
      { label: "可发布", value: dashboard.releaseReady },
      { label: "阻塞", value: dashboard.releaseBlocked },
    ],
    [dashboard.active, dashboard.previewPending, dashboard.releaseReady, dashboard.releaseBlocked],
  );

  useEffect(() => {
    refresh();
  }, []);

  useEffect(() => {
    if (!autoRefresh) return undefined;
    const timer = window.setInterval(() => refresh({ silent: true }), 10000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, selectedBuild?.id, form.productId]);

  async function refresh(options = {}) {
    if (!options.silent) {
      setLoading(true);
    }
    setError("");
    try {
      const [nextProducts, nextBuilds, nextUploads, nextRunners] = await Promise.all([
        listProducts(),
        listBuilds(),
        listUploads(),
        listRunners(),
      ]);
      setProducts(nextProducts);
      setBuilds(nextBuilds);
      setUploads(nextUploads);
      setRunners(nextRunners);
      if (!form.productId && nextProducts[0]) {
        applyProductDefaults(nextProducts[0], false);
        const manifest = JSON.stringify(await getProductManifest(nextProducts[0].id), null, 2);
        setManifestText(manifest);
        setOriginalManifestText(manifest);
        await loadManifestRevisions(nextProducts[0].id);
        await loadOverlays(nextProducts[0].id);
      }
      if (selectedBuild) {
        const fresh = nextBuilds.find((build) => build.id === selectedBuild.id);
        if (fresh) {
          setSelectedBuild(fresh);
        }
      }
      if (options.notify) {
        setNotice("数据已刷新");
      }
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  function productDefaults(product, target = product?.targets?.[0] || "linux") {
    const config = product?.config || {};
    return {
      installDir: product?.targetConfig?.[target]?.installDir || "",
      webPort: String(config.ports?.web ?? 48000),
      serviceVersion: product?.versions?.[0] || "latest",
      customerName: config.customer?.name || "",
      license: config.customer?.license || "",
      passwordPolicy: config.database?.passwordPolicy || "strong",
      modules: {
        camera: config.modules?.camera ?? true,
        push: config.modules?.push ?? true,
        map: config.modules?.map ?? true,
      },
    };
  }

  function applyProductDefaults(product, resetAdvanced = true) {
    const target = product.targets?.[0] || "linux";
    const defaults = productDefaults(product, target);
    setForm((current) => ({
      ...current,
      productId: product.id,
      target,
      profile: product.profiles?.[0] || "qa",
      version: product.versions?.[0] || "latest",
      releaseChannel: "preview",
      ...defaults,
      variablesText: resetAdvanced ? "{}" : current.variablesText,
      componentOverridesText: resetAdvanced ? "{}" : current.componentOverridesText,
    }));
  }

  async function chooseProduct(product) {
    applyProductDefaults(product);
    await loadManifest(product.id);
    await loadManifestRevisions(product.id);
    await loadOverlays(product.id);
  }

  async function loadOverlays(productId = form.productId) {
    if (!productId) return;
    try {
      const [profiles, customers] = await Promise.all([
        listProductOverlays(productId, "profiles"),
        listProductOverlays(productId, "customers"),
      ]);
      setProfileOverlays(profiles);
      setCustomerOverlays(customers);
      const currentList = overlayKind === "profiles" ? profiles : customers;
      const nextName = currentList.find((item) => item.name === overlayName)?.name || currentList[0]?.name || "";
      if (nextName) {
        await loadOverlay(productId, overlayKind, nextName);
      } else {
        setOverlayName("");
        setOverlayText("{}");
        setOriginalOverlayText("{}");
      }
    } catch (err) {
      setError(err);
    }
  }

  async function loadOverlay(productId = form.productId, kind = overlayKind, name = overlayName) {
    if (!productId || !name) return;
    setError("");
    try {
      const record = await getProductOverlay(productId, kind, name);
      const text = JSON.stringify(record.data || {}, null, 2);
      setOverlayKind(kind);
      setOverlayName(name);
      setOverlayText(text);
      setOriginalOverlayText(text);
    } catch (err) {
      setError(err);
    }
  }

  async function saveOverlay() {
    if (!form.productId || !overlayName) return;
    setError("");
    setSavingOverlay(true);
    try {
      if (!isValidOverlayName(overlayName)) {
        throw new Error("覆盖配置名称只能包含字母、数字、点、下划线和中划线");
      }
      const parsed = JSON.parse(overlayText);
      await updateProductOverlay(form.productId, overlayKind, overlayName, parsed);
      setOriginalOverlayText(JSON.stringify(parsed, null, 2));
      await loadOverlays(form.productId);
      setNotice("覆盖配置已保存");
    } catch (err) {
      setError(err);
    } finally {
      setSavingOverlay(false);
    }
  }

  async function loadManifest(productId = form.productId) {
    if (!productId) return;
    setError("");
    try {
      const manifest = await getProductManifest(productId);
      const text = JSON.stringify(manifest, null, 2);
      setManifestText(text);
      setOriginalManifestText(text);
      setManifestDryRun(null);
      setNotice("Manifest 已加载");
    } catch (err) {
      setError(err);
    }
  }

  async function saveManifest() {
    if (!form.productId) return;
    setError("");
    setSavingManifest(true);
    try {
      const parsed = JSON.parse(manifestText);
      const validation = validateProductManifest(parsed);
      if (!validation.valid) {
        throw new Error(`Manifest 校验失败：${validation.errors.join("；")}`);
      }
      await updateProductManifest(form.productId, parsed);
      setOriginalManifestText(JSON.stringify(parsed, null, 2));
      await loadManifestRevisions(form.productId);
      await refresh({ silent: true });
      setNotice("Manifest 已保存");
    } catch (err) {
      setError(err);
    } finally {
      setSavingManifest(false);
    }
  }

  async function runManifestDryRun() {
    if (!form.productId) return;
    setError("");
    setDryRunningManifest(true);
    try {
      const manifest = JSON.parse(manifestText || "{}");
      const result = await dryRunProductManifest(form.productId, {
        manifest,
        target: form.target,
        profile: form.profile,
        customer: form.customer,
        version: form.version,
        releaseChannel: form.releaseChannel,
        variables: JSON.parse(form.variablesText || "{}"),
        configOverrides: buildConfigOverrides(),
        componentOverrides: JSON.parse(form.componentOverridesText || "{}"),
      });
      setManifestDryRun(result);
      setNotice(result.valid ? "Manifest 试运行通过" : "Manifest 试运行未通过");
    } catch (err) {
      setError(err);
    } finally {
      setDryRunningManifest(false);
    }
  }

  async function loadManifestRevisions(productId = form.productId) {
    if (!productId) return;
    setLoadingManifestRevisions(true);
    try {
      const revisions = await listProductManifestRevisions(productId);
      setManifestRevisions(revisions);
    } catch (err) {
      setError(err);
    } finally {
      setLoadingManifestRevisions(false);
    }
  }

  async function restoreManifestRevision(revision) {
    if (!form.productId || !revision?.id) return;
    setError("");
    try {
      await restoreProductManifestRevision(form.productId, revision.id);
      await loadManifest(form.productId);
      await loadManifestRevisions(form.productId);
      await refresh({ silent: true });
      setNotice("Manifest 已恢复到历史版本");
    } catch (err) {
      setError(err);
    }
  }

  function buildConfigOverrides() {
    return {
      installDir: form.installDir,
      serviceVersion: form.serviceVersion,
      ports: {
        web: Number(form.webPort),
      },
      customer: {
        name: form.customerName,
        license: form.license,
      },
      database: {
        passwordPolicy: form.passwordPolicy,
      },
      modules: form.modules,
    };
  }

  async function submitBuild(event) {
    event.preventDefault();
    setError("");
    setNotice("");
    setSubmitting(true);
    try {
      const variables = JSON.parse(form.variablesText || "{}");
      const componentOverrides = JSON.parse(form.componentOverridesText || "{}");
      const configOverrides = buildConfigOverrides();
      const build = await createBuild({
        productId: form.productId,
        target: form.target,
        profile: form.profile,
        customer: form.customer,
        version: form.version,
        releaseChannel: form.releaseChannel,
        variables,
        configOverrides,
        componentOverrides,
      });
      await refresh({ silent: true });
      await inspectBuild(build, { openDialog: true });
      setActiveSection("builds");
      setNotice("构建请求已创建，等待审批");
    } catch (err) {
      setError(err);
    } finally {
      setSubmitting(false);
    }
  }

  async function uploadComponent(componentId, file) {
    if (!file) return;
    setError("");
    try {
      const record = await uploadArtifact(file);
      const overrides = JSON.parse(form.componentOverridesText || "{}");
      overrides[componentId] = {
        kind: "manual-upload",
        uploadId: record.id,
        uri: record.uri,
      };
      setForm({
        ...form,
        componentOverridesText: JSON.stringify(overrides, null, 2),
      });
      await refresh({ silent: true });
      setNotice(`${componentId} 已绑定上传文件`);
    } catch (err) {
      setError(err);
    }
  }

  async function uploadStandalone(file) {
    if (!file) return;
    setUploadingStandalone(true);
    setError("");
    try {
      await uploadArtifact(file, {
        downloadPolicy: uploadForm.downloadPolicy,
        retentionDays: uploadForm.retentionDays,
        metadata: {
          productId: uploadForm.productId,
          purpose: uploadForm.purpose,
        },
      });
      await refresh({ silent: true });
      setNotice("文件已上传");
    } catch (err) {
      setError(err);
    } finally {
      setUploadingStandalone(false);
    }
  }

  function updateComponentOverride(componentId, patch) {
    setError("");
    try {
      const overrides = JSON.parse(form.componentOverridesText || "{}");
      overrides[componentId] = {
        ...(overrides[componentId] || {}),
        ...patch,
      };
      setForm({
        ...form,
        componentOverridesText: JSON.stringify(overrides, null, 2),
      });
    } catch (err) {
      setError(err);
    }
  }

  function setModule(name, value) {
    setForm({
      ...form,
      modules: {
        ...form.modules,
        [name]: value === true,
      },
    });
  }

  async function runBuildAction(fn, build, label) {
    const key = `${label}:${build.id}`;
    setBusyAction(key);
    setError("");
    setNotice("");
    try {
      const updated = await fn(build.id);
      await refresh({ silent: true });
      if (selectedBuild?.id === build.id) {
        await inspectBuild(updated || build, { openDialog: false, keepSection: true });
      }
      setNotice(`已${label}`);
    } catch (err) {
      setError(err);
    } finally {
      setBusyAction("");
    }
  }

  function requestBuildAction(fn, build, label, description) {
    setConfirmAction({
      build,
      label,
      description,
      run: () => runBuildAction(fn, build, label),
    });
  }

  async function inspectBuild(build, options = { openDialog: true }) {
    setSelectedBuild(build);
    if (!options.keepSection) {
      setActiveSection("logs");
    }
    setError("");
    try {
      const [nextLogs, nextLock, nextReleaseCheck] = await Promise.all([
        getBuildLogs(build.id),
        getBuildLock(build.id),
        getBuildReleaseCheck(build.id),
      ]);
      setLogs(nextLogs);
      setBuildLock(nextLock);
      setReleaseCheck(nextReleaseCheck);
      if (options.openDialog) {
        setDetailOpen(true);
      }
    } catch (err) {
      setError(err);
    }
  }

  const mainContent = {
    overview: (
      <OverviewSection
        dashboard={dashboard}
        builds={builds}
        products={products}
        uploads={uploads}
        onCreate={() => setActiveSection("create")}
        onInspect={inspectBuild}
      />
    ),
    create: (
      <CreateBuildSection
        form={form}
        setForm={setForm}
        selectedProduct={selectedProduct}
        uploads={uploads}
        products={products}
        chooseProduct={chooseProduct}
        submitBuild={submitBuild}
        submitting={submitting}
        setModule={setModule}
        updateComponentOverride={updateComponentOverride}
        uploadComponent={uploadComponent}
      />
    ),
    builds: (
      <BuildsSection
        builds={filteredBuilds}
        totalBuilds={builds.length}
        visibleBuildTotal={visibleBuildTotal}
        loading={loading}
        searchText={searchText}
        setSearchText={setSearchText}
        buildFilter={buildFilter}
        setBuildFilter={setBuildFilter}
        buildFilterCounts={buildFilterCounts}
        busyAction={busyAction}
        onApprove={(build) => runBuildAction(approveBuild, build, "审批通过")}
        onReject={(build) =>
          requestBuildAction(rejectBuild, build, "拒绝", "拒绝后该构建不会进入队列，后续只能重新审批或重新创建。")
        }
        onValidate={(build) => runBuildAction(validatePreviewBuild, build, "验证 Preview")}
        onPublish={(build) =>
          requestBuildAction(publishBuild, build, "转 Release", "只有 Preview 验证通过后才能转为 Release 发布。")
        }
        onCancel={(build) =>
          requestBuildAction(cancelBuild, build, "取消", "取消会终止待审批、排队中或运行中的构建。")
        }
        onRetry={(build) => runBuildAction(retryBuild, build, "重试")}
        onInspect={inspectBuild}
      />
    ),
    products: (
      <ProductsSection
        products={products}
        selectedProduct={selectedProduct}
        form={form}
        manifestText={manifestText}
        originalManifestText={originalManifestText}
        setManifestText={setManifestText}
        chooseProduct={chooseProduct}
        loadManifest={loadManifest}
        saveManifest={saveManifest}
        savingManifest={savingManifest}
        runManifestDryRun={runManifestDryRun}
        dryRunningManifest={dryRunningManifest}
        manifestDryRun={manifestDryRun}
        manifestRevisions={manifestRevisions}
        loadingManifestRevisions={loadingManifestRevisions}
        loadManifestRevisions={loadManifestRevisions}
        restoreManifestRevision={restoreManifestRevision}
      />
    ),
    overlays: (
      <OverlaysSection
        productId={form.productId}
        profileOverlays={profileOverlays}
        customerOverlays={customerOverlays}
        overlayKind={overlayKind}
        overlayName={overlayName}
        overlayText={overlayText}
        originalOverlayText={originalOverlayText}
        setOverlayKind={setOverlayKind}
        setOverlayName={setOverlayName}
        setOverlayText={setOverlayText}
        setOriginalOverlayText={setOriginalOverlayText}
        loadOverlay={loadOverlay}
        saveOverlay={saveOverlay}
        savingOverlay={savingOverlay}
      />
    ),
    uploads: (
      <UploadsSection
        uploads={uploads}
        uploadForm={uploadForm}
        setUploadForm={setUploadForm}
        uploading={uploadingStandalone}
        onUpload={uploadStandalone}
      />
    ),
    runners: (
      <RunnersSection runners={runners} builds={builds} onInspect={inspectBuild} />
    ),
    logs: (
      <LogsAndLockSection
        build={selectedBuild}
        logs={logs}
        buildLock={buildLock}
        releaseCheck={releaseCheck}
        onRefresh={() =>
          selectedBuild && inspectBuild(selectedBuild, { openDialog: false, keepSection: true })
        }
      />
    ),
  }[activeSection];

  return (
    <div className="admin-shell">
      <aside className="admin-sidebar">
        <div className="brand-block">
          <div className="brand-mark">
            <PackageCheck size={22} />
          </div>
          <div>
            <div className="brand-title">Packaging</div>
            <div className="brand-subtitle">构建控制台</div>
          </div>
        </div>

        <nav className="sidebar-nav">
          {navGroups.map((group) => (
            <div key={group.label} className="nav-group">
              <div className="nav-group-label">{group.label}</div>
              <div className="nav-group-items">
                {group.keys.map((key) => {
                  const item = navItems.find((candidate) => candidate.key === key);
                  if (!item) return null;
                  const Icon = item.icon;
                  const active = activeSection === item.key;
                  return (
                    <button
                      key={item.key}
                      className={`nav-item ${active ? "is-active" : ""}`}
                      aria-current={active ? "page" : undefined}
                      onClick={() => setActiveSection(item.key)}
                    >
                      <span className="nav-item-icon">
                        <Icon size={17} />
                      </span>
                      <span className="nav-item-label">{item.label}</span>
                      <ChevronRight className="nav-item-arrow" size={15} />
                    </button>
                  );
                })}
              </div>
            </div>
          ))}
        </nav>

        <div className="sidebar-status">
          <div className="status-dot" />
          <div>
            <Text size="2" weight="medium">
              本地 API
            </Text>
            <Text as="div" size="1" color="gray">
              默认 http://localhost:8080
            </Text>
          </div>
        </div>
      </aside>

      <main className="admin-main">
        <header className="topbar">
          <Box>
            <Text size="1" color="gray" weight="bold">
              内部安装包构建平台
            </Text>
            <Heading as="h1" size="7">
              {navItems.find((item) => item.key === activeSection)?.label}
            </Heading>
            <div className="topbar-metrics" aria-label="构建运行摘要">
              {topbarMetrics.map((item) => (
                <div key={item.label} className="topbar-metric">
                  <Text size="1" color="gray">
                    {item.label}
                  </Text>
                  <Text size="3" weight="bold">
                    {item.value}
                  </Text>
                </div>
              ))}
            </div>
          </Box>
          <Flex className="topbar-actions" gap="3" align="center" wrap="wrap">
            <label className="inline-check">
              <Checkbox checked={autoRefresh} onCheckedChange={(value) => setAutoRefresh(value === true)} />
              <Text size="2">10 秒自动刷新</Text>
            </label>
            <Button variant="soft" onClick={() => refresh({ notify: true })} disabled={loading}>
              {loading ? <Spinner /> : <RefreshCcw size={16} />}
              刷新
            </Button>
            <Button onClick={() => setActiveSection("create")}>
              <Rocket size={16} />
              新建构建
            </Button>
          </Flex>
        </header>

        {pageAlert && (
          <Callout.Root
            className="message-bar"
            color={pageAlert.color}
            variant="soft"
          >
            <Callout.Icon>
              <AlertCircle size={18} />
            </Callout.Icon>
            <div className="message-bar-content">
              <Text as="div" weight="bold">
                {pageAlert.message}
              </Text>
              {pageAlert.details.length > 0 && (
                <ul className="alert-detail-list">
                  {pageAlert.details.map((detail, index) => (
                    <li key={`${detail.code || detail.field || "detail"}-${index}`}>
                      {detail.title ? `${detail.title}：` : ""}{detail.message}
                    </li>
                  ))}
                </ul>
              )}
              {pageAlert.userAction && (
                <Text as="div" size="1">
                  {pageAlert.userAction}
                </Text>
              )}
            </div>
            <Button size="1" variant="ghost" onClick={() => setError("")}>
              关闭
            </Button>
          </Callout.Root>
        )}

        <div className="toast-stack" aria-live="polite" aria-relevant="additions">
          {toasts.map((toast) => (
            <div key={toast.id} className={`toast-item is-${toast.tone}`}>
              {toast.tone === "success" ? <Check size={16} /> : <CircleDot size={16} />}
              <Text size="2" weight="medium">{toast.message}</Text>
              <button type="button" onClick={() => dismissToast(toast.id)} aria-label="关闭提示">
                <X size={14} />
              </button>
            </div>
          ))}
        </div>

        {loading && builds.length === 0 ? (
          <div className="loading-panel">
            <Spinner size="3" />
            <Text color="gray">正在加载平台数据</Text>
          </div>
        ) : (
          mainContent
        )}
      </main>

      <BuildDetailDialog
        open={detailOpen}
        onOpenChange={setDetailOpen}
        build={selectedBuild}
        logs={logs}
        buildLock={buildLock}
        onApprove={(build) => runBuildAction(approveBuild, build, "审批通过")}
        onReject={(build) =>
          requestBuildAction(rejectBuild, build, "拒绝", "拒绝后该构建不会进入队列，后续只能重新审批或重新创建。")
        }
        onValidate={(build) => runBuildAction(validatePreviewBuild, build, "验证 Preview")}
        onPublish={(build) =>
          requestBuildAction(publishBuild, build, "转 Release", "只有 Preview 验证通过后才能转为 Release 发布。")
        }
        onCancel={(build) =>
          requestBuildAction(cancelBuild, build, "取消", "取消会终止待审批、排队中或运行中的构建。")
        }
        onRetry={(build) => runBuildAction(retryBuild, build, "重试")}
        busyAction={busyAction}
      />
      <ConfirmActionDialog
        action={confirmAction}
        onCancel={() => setConfirmAction(null)}
        onConfirm={async () => {
          const action = confirmAction;
          setConfirmAction(null);
          await action?.run();
        }}
      />
    </div>
  );
}

function OverviewSection({ dashboard, builds, products, uploads, onCreate, onInspect }) {
  const recentBuilds = [...builds]
    .sort((a, b) => dateValue(b.updatedAt) - dateValue(a.updatedAt))
    .slice(0, 6);

  return (
    <div className="section-stack">
      <Grid className="metric-grid" gap="4">
        <MetricCard
          icon={Boxes}
          label="产品定义"
          value={dashboard.products}
          hint={`${products.reduce((sum, item) => sum + (item.components?.length || 0), 0)} 个组件`}
          color="teal"
        />
        <MetricCard icon={Gauge} label="活跃任务" value={dashboard.active} hint="排队中 + 构建中" color="blue" />
        <MetricCard icon={ShieldCheck} label="成功率" value={`${dashboard.successRate}%`} hint={`${builds.length} 条构建记录`} color="green" />
        <MetricCard icon={AlertCircle} label="需要处理" value={dashboard.attention} hint="待审批 / 失败 / 已取消" color="amber" />
        <MetricCard icon={PackageCheck} label="待验证 Preview" value={dashboard.previewPending} hint="成功但未验证" color="teal" />
        <MetricCard icon={Rocket} label="可发布 Release" value={dashboard.releaseReady} hint="门禁检查通过" color="green" />
        <MetricCard icon={Ban} label="发布阻塞" value={dashboard.releaseBlocked} hint="需修复复现性" color="amber" />
      </Grid>

      <div className="dashboard-grid">
        <Panel
          title="构建队列"
          description="按状态聚合当前构建任务"
          icon={ClipboardList}
          action={
            <Button onClick={onCreate}>
              <Play size={16} />
              创建构建
            </Button>
          }
        >
          <div className="status-grid">
            {Object.entries(statusMeta).map(([status, meta]) => (
              <div key={status} className="status-tile">
                <Text size="2" color="gray">
                  {meta.label}
                </Text>
                <Text size="6" weight="bold">
                  {dashboard.counts[status] || 0}
                </Text>
              </div>
            ))}
          </div>
        </Panel>

        <Panel title="平台资产" description="产品、上传文件和最近构建" icon={Database}>
          <div className="asset-list">
            <AssetLine label="产品 Manifest" value={`${products.length} 个`} />
            <AssetLine label="上传制品" value={`${uploads.length} 个`} />
            <AssetLine label="最近更新" value={dashboard.latest ? formatDate(dashboard.latest.updatedAt) : "暂无"} />
            <AssetLine label="最近构建" value={dashboard.latest ? shortId(dashboard.latest.id) : "暂无"} />
          </div>
        </Panel>
      </div>

      <Panel title="最近构建" description="快速查看和进入构建详情" icon={History}>
        <BuildTimeline builds={recentBuilds} onInspect={onInspect} />
      </Panel>
    </div>
  );
}

function CreateBuildSection({
  form,
  setForm,
  selectedProduct,
  uploads,
  products,
  chooseProduct,
  submitBuild,
  submitting,
  setModule,
  updateComponentOverride,
  uploadComponent,
}) {
  const releasePrecheck = useMemo(
    () => getReleasePrecheck(form, selectedProduct),
    [form.version, form.serviceVersion, form.componentOverridesText, selectedProduct],
  );

  return (
    <form className="section-stack" onSubmit={submitBuild}>
      <div className="form-grid">
        <Panel title="构建目标" description="选择产品、版本、环境和发布通道" icon={Server}>
          <div className="field-grid two">
            <FieldSelect
              label="产品"
              value={form.productId}
              onValueChange={(productId) => {
                const product = products.find((item) => item.id === productId);
                if (product) chooseProduct(product);
              }}
              options={products.map((product) => ({ value: product.id, label: product.name }))}
            />
            <FieldSelect
              label="目标平台"
              value={form.target}
              onValueChange={(target) =>
                setForm({
                  ...form,
                  target,
                  installDir: selectedProduct?.targetConfig?.[target]?.installDir || form.installDir,
                })
              }
              options={(selectedProduct?.targets || ["linux"]).map((target) => ({
                value: target,
                label: target,
              }))}
            />
            <FieldSelect
              label="版本"
              value={form.version}
              onValueChange={(version) => setForm({ ...form, version })}
              options={(selectedProduct?.versions || ["latest"]).map((version) => ({
                value: version,
                label: version,
              }))}
            />
            <FieldSelect
              label="配置 Profile"
              value={form.profile}
              onValueChange={(profile) => setForm({ ...form, profile })}
              options={(selectedProduct?.profiles || ["qa"]).map((profile) => ({
                value: profile,
                label: profile,
              }))}
            />
            <LabeledInput
              label="客户标识"
              value={form.customer}
              onChange={(customer) => setForm({ ...form, customer })}
              placeholder="例如 example-customer"
            />
            <FieldSelect
              label="构建通道"
              value={form.releaseChannel}
              onValueChange={(releaseChannel) => setForm({ ...form, releaseChannel })}
              options={[{ value: "preview", label: "Preview 安装包" }]}
            />
          </div>
        </Panel>

        <Panel title="核心覆盖项" description="这些配置会写入构建锁" icon={Settings2}>
          <div className="field-grid two">
            <LabeledInput label="安装目录" value={form.installDir} onChange={(installDir) => setForm({ ...form, installDir })} />
            <LabeledInput label="Web 端口" type="number" value={form.webPort} onChange={(webPort) => setForm({ ...form, webPort })} />
            <LabeledInput label="服务版本" value={form.serviceVersion} onChange={(serviceVersion) => setForm({ ...form, serviceVersion })} />
            <FieldSelect
              label="数据库密码策略"
              value={form.passwordPolicy}
              onValueChange={(passwordPolicy) => setForm({ ...form, passwordPolicy })}
              options={[
                { value: "strong", label: "强策略" },
                { value: "relaxed", label: "宽松策略" },
                { value: "custom", label: "自定义" },
              ]}
            />
            <LabeledInput label="客户名称" value={form.customerName} onChange={(customerName) => setForm({ ...form, customerName })} />
            <LabeledInput label="许可证类型" value={form.license} onChange={(license) => setForm({ ...form, license })} />
          </div>
          <Flex mt="4" gap="4" wrap="wrap">
            {Object.entries(form.modules).map(([name, enabled]) => (
              <label key={name} className="inline-check">
                <Checkbox checked={enabled} onCheckedChange={(value) => setModule(name, value)} />
                <Text size="2">模块 {name}</Text>
              </label>
            ))}
          </Flex>
        </Panel>
      </div>

      <ReleasePrecheckPanel result={releasePrecheck} />

      <Panel title="组件来源" description="为每个组件选择仓库、上传文件、固定 URL 或历史构建" icon={Boxes}>
        <ComponentSourceEditor
          components={selectedProduct?.components || []}
          uploads={uploads}
          overridesText={form.componentOverridesText}
          onUpdate={updateComponentOverride}
          onUpload={uploadComponent}
        />
      </Panel>

      <Panel title="高级 JSON" description="保留原始变量和组件覆盖 JSON，便于调试与审计" icon={Code2}>
        <Tabs.Root defaultValue="variables">
          <Tabs.List>
            <Tabs.Trigger value="variables">构建变量</Tabs.Trigger>
            <Tabs.Trigger value="components">组件覆盖</Tabs.Trigger>
          </Tabs.List>
          <Box pt="3">
            <Tabs.Content value="variables">
              <TextArea
                className="code-textarea"
                resize="vertical"
                value={form.variablesText}
                onChange={(event) => setForm({ ...form, variablesText: event.target.value })}
              />
            </Tabs.Content>
            <Tabs.Content value="components">
              <TextArea
                className="code-textarea"
                resize="vertical"
                value={form.componentOverridesText}
                onChange={(event) =>
                  setForm({ ...form, componentOverridesText: event.target.value })
                }
              />
            </Tabs.Content>
          </Box>
        </Tabs.Root>
      </Panel>

      <Flex className="form-actions" justify="end">
        <Button type="submit" size="3" disabled={!form.productId || submitting}>
          {submitting ? <Spinner /> : <Rocket size={18} />}
          提交构建申请
        </Button>
      </Flex>
    </form>
  );
}

function BuildsSection({
  builds,
  totalBuilds,
  visibleBuildTotal,
  loading,
  searchText,
  setSearchText,
  buildFilter,
  setBuildFilter,
  buildFilterCounts,
  busyAction,
  onApprove,
  onReject,
  onValidate,
  onPublish,
  onCancel,
  onRetry,
  onInspect,
}) {
  return (
    <Panel
      title="构建记录"
      description={`显示 ${visibleBuildTotal} / ${totalBuilds} 条构建`}
      icon={History}
      action={
        <TextField.Root
          className="search-input"
          placeholder="搜索 ID / 产品 / 状态"
          value={searchText}
          onChange={(event) => setSearchText(event.target.value)}
        >
          <TextField.Slot>
            <Search size={16} />
          </TextField.Slot>
        </TextField.Root>
      }
    >
      <div className="build-filter-bar" aria-label="构建筛选">
        {buildFilterOptions.map((option) => {
          const Icon = option.icon;
          const count = buildFilterCounts[option.key] || 0;
          return (
            <button
              key={option.key}
              type="button"
              className={`build-filter-chip ${buildFilter === option.key ? "is-active" : ""}`}
              aria-pressed={buildFilter === option.key}
              aria-label={`筛选：${option.label}，${count} 条`}
              onClick={() => setBuildFilter(option.key)}
            >
              <Icon size={15} />
              <span>{option.label}</span>
              <strong>{count}</strong>
            </button>
          );
        })}
      </div>
      <BuildsTable
        builds={builds}
        loading={loading}
        busyAction={busyAction}
        onApprove={onApprove}
        onReject={onReject}
        onValidate={onValidate}
        onPublish={onPublish}
        onCancel={onCancel}
        onRetry={onRetry}
        onInspect={onInspect}
      />
    </Panel>
  );
}

function ReleasePrecheckPanel({ result }) {
  return (
    <Callout.Root color={result.ready ? "green" : "amber"} variant="soft">
      <Callout.Icon>
        {result.ready ? <ShieldCheck size={18} /> : <AlertCircle size={18} />}
      </Callout.Icon>
      <Callout.Text>
        {result.ready
          ? "Release 预检通过：当前配置具备转 Release 的基础条件。"
          : `Release 预检提示：${result.warnings.join("；")}。该构建仍可用于 Preview，但通过 Preview 后可能不能转 Release。`}
      </Callout.Text>
    </Callout.Root>
  );
}

function getReleasePrecheck(form, product) {
  const warnings = [];
  if (!form.productId) {
    return { ready: false, warnings: ["请选择产品"] };
  }
  if (!form.version || form.version === "latest") {
    warnings.push("构建版本需要固定值，不能使用 latest");
  }
  if (!form.serviceVersion || form.serviceVersion === "latest") {
    warnings.push("服务版本需要固定值，不能使用 latest");
  }

  let overrides = {};
  try {
    overrides = JSON.parse(form.componentOverridesText || "{}");
  } catch {
    warnings.push("组件覆盖 JSON 解析失败");
  }

  for (const component of product?.components || []) {
    const source = { ...(component.source || {}), ...(overrides[component.id] || {}) };
    const uri = String(source.uri || source.url || source.path || "");
    if (source.version === "latest") {
      warnings.push(`${component.id} 的组件版本不能使用 latest`);
    }
    if (source.kind === "previous-build" && !source.uri) {
      warnings.push(`${component.id} 需要选择精确历史制品 URI`);
    }
    if (uri.includes("*")) {
      warnings.push(`${component.id} 的组件 URI 不能包含通配符`);
    }
  }

  return { ready: warnings.length === 0, warnings };
}

function defaultComponentSource(kind, current = {}) {
  if (kind === "artifact-repository") {
    return {
      kind,
      repository: current.repository || "minio",
      path: current.path || "",
    };
  }
  if (kind === "fixed-url") {
    return { kind, url: current.url || "" };
  }
  if (kind === "previous-build") {
    return { kind, uri: current.uri || "" };
  }
  if (kind === "manual-upload") {
    return { kind, uploadId: current.uploadId || "", uri: current.uri || "" };
  }
  return { kind };
}

function ProductsSection({
  products,
  selectedProduct,
  form,
  manifestText,
  originalManifestText,
  setManifestText,
  chooseProduct,
  loadManifest,
  saveManifest,
  savingManifest,
  runManifestDryRun,
  dryRunningManifest,
  manifestDryRun,
  manifestRevisions,
  loadingManifestRevisions,
  loadManifestRevisions,
  restoreManifestRevision,
}) {
  const manifestParse = useMemo(() => {
    try {
      return { manifest: JSON.parse(manifestText || "{}"), error: "" };
    } catch (err) {
      return { manifest: null, error: `JSON 解析错误：${err.message}` };
    }
  }, [manifestText]);
  const parsedManifest = manifestParse.manifest;
  const manifestValidation = useMemo(() => {
    if (manifestParse.error) {
      return { valid: false, errors: [manifestParse.error] };
    }
    return validateProductManifest(parsedManifest || {});
  }, [manifestParse.error, parsedManifest]);
  const manifestDiff = useMemo(
    () => diffLines(originalManifestText, manifestText).slice(0, 240),
    [originalManifestText, manifestText],
  );

  return (
    <div className="products-layout">
      <Panel title="产品列表" description="选择要维护的 manifest" icon={Boxes}>
        <div className="product-list">
          {products.map((product) => (
            <button
              key={product.id}
              className={`product-row ${product.id === form.productId ? "is-selected" : ""}`}
              onClick={() => chooseProduct(product)}
            >
              <div>
                <Text as="div" weight="bold">
                  {product.name}
                </Text>
                <Text as="div" size="1" color="gray">
                  {product.description}
                </Text>
              </div>
              <Flex gap="2" wrap="wrap">
                {product.targets?.map((target) => (
                  <Badge key={target} variant="soft">
                    {target}
                  </Badge>
                ))}
              </Flex>
            </button>
          ))}
        </div>
      </Panel>

      <Panel
        title="Manifest 编辑器"
        description={selectedProduct ? `${selectedProduct.name} / ${selectedProduct.id}` : "请选择产品"}
        icon={FileJson}
        action={
          <Flex gap="2" wrap="wrap">
            <Button variant="soft" onClick={() => loadManifest()}>
              重新加载
            </Button>
            <Button variant="soft" disabled={dryRunningManifest} onClick={runManifestDryRun}>
              {dryRunningManifest ? <Spinner /> : <GitCompare size={16} />}
              试运行
            </Button>
            <Button disabled={savingManifest} onClick={saveManifest}>
              {savingManifest ? <Spinner /> : null}
              保存 Manifest
            </Button>
          </Flex>
        }
      >
        <Tabs.Root defaultValue="editor">
          <Tabs.List>
            <Tabs.Trigger value="editor">编辑</Tabs.Trigger>
            <Tabs.Trigger value="structured">结构化</Tabs.Trigger>
            <Tabs.Trigger value="validate">校验</Tabs.Trigger>
            <Tabs.Trigger value="checks">功能检查</Tabs.Trigger>
            <Tabs.Trigger value="dryrun">试运行</Tabs.Trigger>
            <Tabs.Trigger value="revisions">历史</Tabs.Trigger>
            <Tabs.Trigger value="diff">差异</Tabs.Trigger>
          </Tabs.List>
          <Box pt="3">
            <Tabs.Content value="editor">
              <TextArea
                className="manifest-editor"
                resize="vertical"
                value={manifestText}
                onChange={(event) => setManifestText(event.target.value)}
              />
            </Tabs.Content>
            <Tabs.Content value="structured">
              <ManifestStructuredEditor manifest={parsedManifest} onChange={setManifestText} />
            </Tabs.Content>
            <Tabs.Content value="validate">
              <ValidationPanel result={manifestValidation} />
            </Tabs.Content>
            <Tabs.Content value="checks">
              <ManifestFeatureCheckPanel manifest={parsedManifest} />
            </Tabs.Content>
            <Tabs.Content value="dryrun">
              <ManifestDryRunPanel result={manifestDryRun} running={dryRunningManifest} onRun={runManifestDryRun} />
            </Tabs.Content>
            <Tabs.Content value="revisions">
              <ManifestRevisionsPanel
                revisions={manifestRevisions}
                loading={loadingManifestRevisions}
                onRefresh={() => loadManifestRevisions()}
                onRestore={restoreManifestRevision}
              />
            </Tabs.Content>
            <Tabs.Content value="diff">
              <DiffPanel lines={manifestDiff} />
            </Tabs.Content>
          </Box>
        </Tabs.Root>
      </Panel>
    </div>
  );
}

function OverlaysSection({
  productId,
  profileOverlays,
  customerOverlays,
  overlayKind,
  overlayName,
  overlayText,
  originalOverlayText,
  setOverlayKind,
  setOverlayName,
  setOverlayText,
  setOriginalOverlayText,
  loadOverlay,
  saveOverlay,
  savingOverlay,
}) {
  const [draftName, setDraftName] = useState("");
  const overlays = overlayKind === "profiles" ? profileOverlays : customerOverlays;
  const overlayDiff = useMemo(
    () => diffLines(originalOverlayText, overlayText).slice(0, 240),
    [originalOverlayText, overlayText],
  );
  const validation = useMemo(() => {
    try {
      JSON.parse(overlayText || "{}");
      return { valid: true, errors: [] };
    } catch (err) {
      return { valid: false, errors: [`JSON 解析错误：${err.message}`] };
    }
  }, [overlayText]);
  const nameValidation = useMemo(() => {
    const name = draftName.trim();
    if (!name) return { valid: true, errors: [] };
    if (!isValidOverlayName(name)) {
      return { valid: false, errors: ["名称只能包含字母、数字、点、下划线和中划线"] };
    }
    return { valid: true, errors: [] };
  }, [draftName]);
  const currentNameValid = !overlayName || isValidOverlayName(overlayName);

  function switchKind(kind) {
    setOverlayKind(kind);
    const list = kind === "profiles" ? profileOverlays : customerOverlays;
    const nextName = list[0]?.name || "";
    setOverlayName(nextName);
    if (nextName) {
      loadOverlay(productId, kind, nextName);
    } else {
      setOverlayText("{}");
      setOriginalOverlayText("{}");
    }
  }

  function startNewOverlay() {
    const name = draftName.trim();
    if (!name || !isValidOverlayName(name)) return;
    setOverlayName(name);
    setOverlayText("{}");
    setOriginalOverlayText("{}");
    setDraftName("");
  }

  return (
    <div className="products-layout">
      <Panel title="覆盖列表" description="Profile 和客户覆盖会参与构建配置合并" icon={GitCompare}>
        <Tabs.Root value={overlayKind} onValueChange={switchKind}>
          <Tabs.List>
            <Tabs.Trigger value="profiles">配置面</Tabs.Trigger>
            <Tabs.Trigger value="customers">客户覆盖</Tabs.Trigger>
          </Tabs.List>
          <Box pt="3">
            <div className="new-overlay-row">
              <TextField.Root
                placeholder="新建覆盖名称"
                value={draftName}
                onChange={(event) => setDraftName(event.target.value)}
              />
              <Button
                variant="soft"
                onClick={startNewOverlay}
                disabled={!draftName.trim() || !nameValidation.valid}
              >
                新建
              </Button>
            </div>
            {!nameValidation.valid && (
              <Text as="div" size="1" color="red" mb="3">
                {nameValidation.errors[0]}
              </Text>
            )}
            <div className="product-list">
              {overlays.length === 0 ? (
                <EmptyState icon={GitCompare} title="暂无覆盖配置" text="当前产品还没有该类型的覆盖文件。" />
              ) : (
                overlays.map((overlay) => (
                  <button
                    key={overlay.name}
                    className={`product-row ${overlay.name === overlayName ? "is-selected" : ""}`}
                    onClick={() => loadOverlay(productId, overlayKind, overlay.name)}
                  >
                    <Text as="div" weight="bold">
                      {overlay.name}
                    </Text>
                    <Text as="div" size="1" color="gray">
                      {overlay.kind}
                    </Text>
                  </button>
                ))
              )}
            </div>
          </Box>
        </Tabs.Root>
      </Panel>

      <Panel
        title="覆盖编辑器"
        description={overlayName ? `${overlayKind} / ${overlayName}` : "请选择覆盖配置"}
        icon={Code2}
        action={
          <Button
            disabled={!overlayName || !currentNameValid || !validation.valid || savingOverlay}
            onClick={saveOverlay}
          >
            {savingOverlay ? <Spinner /> : null}
            保存覆盖
          </Button>
        }
      >
        <Tabs.Root defaultValue="editor">
          <Tabs.List>
            <Tabs.Trigger value="editor">编辑</Tabs.Trigger>
            <Tabs.Trigger value="validate">校验</Tabs.Trigger>
            <Tabs.Trigger value="diff">差异</Tabs.Trigger>
          </Tabs.List>
          <Box pt="3">
            <Tabs.Content value="editor">
              <TextArea
                className="manifest-editor"
                resize="vertical"
                value={overlayText}
                onChange={(event) => setOverlayText(event.target.value)}
              />
            </Tabs.Content>
            <Tabs.Content value="validate">
              <ValidationPanel result={validation} />
            </Tabs.Content>
            <Tabs.Content value="diff">
              <DiffPanel lines={overlayDiff} />
            </Tabs.Content>
          </Box>
        </Tabs.Root>
      </Panel>
    </div>
  );
}

function ManifestFeatureCheckPanel({ manifest }) {
  if (!manifest) {
    return <ValidationPanel result={{ valid: false, errors: ["Manifest JSON 解析失败，无法执行功能检查"] }} />;
  }
  const checks = manifestFeatureChecks(manifest);
  const passed = checks.filter((item) => item.ok).length;

  return (
    <div className="section-stack">
      <Flex gap="3" align="center" wrap="wrap">
        <Badge color={passed === checks.length ? "green" : "amber"} variant="soft">
          {passed}/{checks.length} 通过
        </Badge>
        <Text size="2" color="gray">
          用于确认该 manifest 是否能覆盖 Linux/Windows、Preview 验证和 Release 复现发布。
        </Text>
      </Flex>
      <div className="validation-list">
        {checks.map((item) => (
          <div key={item.label} className={`validation-check ${item.ok ? "is-pass" : item.level === "warn" ? "is-warn" : "is-fail"}`}>
            <Flex justify="between" gap="4" align="start">
              <Box>
                <Text as="div" weight="bold">
                  {item.label}
                </Text>
                <Text as="div" size="2" color="gray">
                  {item.detail}
                </Text>
              </Box>
              <Badge color={item.ok ? "green" : item.level === "warn" ? "amber" : "red"} variant="soft">
                {item.ok ? "通过" : item.level === "warn" ? "需确认" : "未满足"}
              </Badge>
            </Flex>
          </div>
        ))}
      </div>
    </div>
  );
}

function ManifestStructuredEditor({ manifest, onChange }) {
  if (!manifest) {
    return <ValidationPanel result={{ valid: false, errors: ["Manifest JSON 解析失败，无法结构化编辑"] }} />;
  }

  function update(patch) {
    onChange(JSON.stringify({ ...manifest, ...patch }, null, 2));
  }

  function updateTarget(target, patch) {
    const targets = new Set(manifest.targets || []);
    targets.add(target);
    update({
      targets: Array.from(targets),
      targetConfig: {
        ...(manifest.targetConfig || {}),
        [target]: {
          ...(manifest.targetConfig?.[target] || {}),
          ...patch,
        },
      },
    });
  }

  function updateTargetTemplate(target, key, value) {
    const current = manifest.targetConfig?.[target] || {};
    updateTarget(target, {
      templates: {
        ...(current.templates || {}),
        [key]: value,
      },
    });
  }

  function updateConfig(section, patch) {
    update({
      config: {
        ...(manifest.config || {}),
        [section]: {
          ...(manifest.config?.[section] || {}),
          ...patch,
        },
      },
    });
  }

  function updateModule(name, enabled) {
    updateConfig("modules", { [name]: enabled });
  }

  function toggleTarget(target, enabled) {
    const nextTargets = new Set(manifest.targets || []);
    if (enabled) {
      nextTargets.add(target);
    } else {
      nextTargets.delete(target);
    }
    update({ targets: Array.from(nextTargets) });
  }

  function updateComponent(index, patch) {
    const components = [...(manifest.components || [])];
    components[index] = {
      ...components[index],
      ...patch,
      source: patch.source ? { ...(components[index]?.source || {}), ...patch.source } : components[index]?.source,
    };
    update({ components });
  }

  function addComponent() {
    update({
      components: [
        ...(manifest.components || []),
        {
          id: "new-component",
          type: "archive",
          required: true,
          source: { kind: "artifact-repository", repository: "minio", path: "artifact/path/{{version}}/file.zip" },
        },
      ],
    });
  }

  function removeComponent(index) {
    update({ components: (manifest.components || []).filter((_, itemIndex) => itemIndex !== index) });
  }

  function updateWorkload(index, patch) {
    const workloads = [...(manifest.workloads || [])];
    workloads[index] = {
      ...workloads[index],
      ...patch,
    };
    update({ workloads });
  }

  function addWorkload() {
    update({
      workloads: [
        ...(manifest.workloads || []),
        {
          id: "new-workload",
          runtime: "docker",
          component: manifest.components?.[0]?.id || "",
          enabled: true,
        },
      ],
    });
  }

  function removeWorkload(index) {
    update({ workloads: (manifest.workloads || []).filter((_, itemIndex) => itemIndex !== index) });
  }

  const config = manifest.config || {};
  const modules = config.modules || {};
  const moduleNames = Array.from(new Set(["camera", "push", "map", ...Object.keys(modules)]));

  return (
    <div className="section-stack">
      <div className="field-grid two">
        <LabeledInput label="产品名称" value={manifest.name || ""} onChange={(name) => update({ name })} />
        <LabeledInput label="产品 ID" value={manifest.id || ""} onChange={(id) => update({ id })} />
        <LabeledInput label="配置面" value={(manifest.profiles || []).join(", ")} onChange={(value) => update({ profiles: splitCSV(value) })} />
        <LabeledInput label="Versions" value={(manifest.versions || []).join(", ")} onChange={(value) => update({ versions: splitCSV(value) })} />
      </div>
      <label className="field">
        <Text size="2" weight="medium">
          描述
        </Text>
        <TextArea
          resize="vertical"
          value={manifest.description || ""}
          onChange={(event) => update({ description: event.target.value })}
        />
      </label>

      <div className="structured-grid">
        {["linux", "windows"].map((target) => {
          const enabled = (manifest.targets || []).includes(target);
          const targetConfig = manifest.targetConfig?.[target] || {};
          return (
            <div key={target} className="structured-block">
              <Flex align="center" justify="between" gap="3">
                <Text weight="bold">{target}</Text>
                <label className="inline-check">
                  <Checkbox checked={enabled} onCheckedChange={(value) => toggleTarget(target, value === true)} />
                  <Text size="2">启用</Text>
                </label>
              </Flex>
              <LabeledInput
                label="安装目录"
                value={targetConfig.installDir || ""}
                onChange={(installDir) => updateTarget(target, { installDir })}
              />
              <LabeledInput
                label="包名"
                value={targetConfig.packageName || ""}
                onChange={(packageName) => updateTarget(target, { packageName })}
              />
              <LabeledInput
                label="安装清单模板"
                value={targetConfig.templates?.manifest || ""}
                onChange={(value) => updateTargetTemplate(target, "manifest", value)}
                placeholder={`${target}/manifest.template.json`}
              />
              <LabeledInput
                label="配置模板目录"
                value={targetConfig.templates?.configDir || ""}
                onChange={(value) => updateTargetTemplate(target, "configDir", value)}
                placeholder={`${target}/templates`}
              />
            </div>
          );
        })}
      </div>

      <Flex justify="between" align="center" gap="3" wrap="wrap">
        <Text weight="bold">默认配置</Text>
        <Badge variant="soft">会参与最终配置合并</Badge>
      </Flex>
      <div className="structured-grid">
        <div className="structured-block">
          <Text weight="bold">运行参数</Text>
          <LabeledInput
            label="Web 端口"
            type="number"
            value={String(config.ports?.web ?? "")}
            onChange={(value) => updateConfig("ports", { web: Number(value) || 0 })}
          />
          <FieldSelect
            label="数据库密码策略"
            value={config.database?.passwordPolicy || "strong"}
            onValueChange={(passwordPolicy) => updateConfig("database", { passwordPolicy })}
            options={[
              { value: "strong", label: "强策略" },
              { value: "relaxed", label: "宽松策略" },
              { value: "custom", label: "自定义" },
            ]}
          />
        </div>
        <div className="structured-block">
          <Text weight="bold">客户默认值</Text>
          <LabeledInput
            label="客户名称"
            value={config.customer?.name || ""}
            onChange={(name) => updateConfig("customer", { name })}
          />
          <LabeledInput
            label="许可证"
            value={config.customer?.license || ""}
            onChange={(license) => updateConfig("customer", { license })}
          />
        </div>
      </div>
      <div className="structured-block">
        <Flex justify="between" align="center" gap="3" wrap="wrap">
          <Text weight="bold">模块开关</Text>
          <Text size="1" color="gray">
            workload 可通过 module 字段读取这些开关
          </Text>
        </Flex>
        <Flex gap="4" wrap="wrap">
          {moduleNames.map((name) => (
            <label key={name} className="inline-check">
              <Checkbox checked={modules[name] !== false} onCheckedChange={(value) => updateModule(name, value === true)} />
              <Text size="2">模块 {name}</Text>
            </label>
          ))}
        </Flex>
      </div>

      <Flex justify="between" align="center" gap="3" wrap="wrap">
        <Text weight="bold">组件来源</Text>
        <Button variant="soft" onClick={addComponent}>
          <FileJson size={16} />
          添加组件
        </Button>
      </Flex>
      <div className="component-grid">
        {(manifest.components || []).map((component, index) => (
          <div key={`${component.id}-${index}`} className="component-card">
            <Flex justify="between" align="start" gap="3">
              <LabeledInput label="组件 ID" value={component.id || ""} onChange={(id) => updateComponent(index, { id })} />
              <Button size="1" color="red" variant="soft" onClick={() => removeComponent(index)}>
                删除
              </Button>
            </Flex>
            <LabeledInput label="类型" value={component.type || ""} onChange={(type) => updateComponent(index, { type })} />
            <label className="inline-check">
              <Checkbox checked={component.required !== false} onCheckedChange={(value) => updateComponent(index, { required: value === true })} />
              <Text size="2">必需组件</Text>
            </label>
            <FieldSelect
              label="来源类型"
              value={component.source?.kind || "artifact-repository"}
              onValueChange={(kind) => updateComponent(index, { source: { kind } })}
              options={sourceKindOptions.map((option) => ({ value: option.key, label: option.label }))}
            />
            {(component.source?.kind || "artifact-repository") === "artifact-repository" && (
              <LabeledInput label="仓库路径" value={component.source?.path || ""} onChange={(path) => updateComponent(index, { source: { path } })} />
            )}
            {component.source?.kind === "fixed-url" && (
              <LabeledInput label="URL" value={component.source?.url || ""} onChange={(url) => updateComponent(index, { source: { url } })} />
            )}
            {component.source?.kind === "previous-build" && (
              <LabeledInput label="URI" value={component.source?.uri || ""} onChange={(uri) => updateComponent(index, { source: { uri } })} />
            )}
          </div>
        ))}
      </div>

      <Flex justify="between" align="center" gap="3" wrap="wrap">
        <Text weight="bold">工作负载</Text>
        <Button variant="soft" onClick={addWorkload}>
          <Server size={16} />
          添加 Workload
        </Button>
      </Flex>
      <div className="workload-grid">
        {(manifest.workloads || []).map((workload, index) => (
          <div key={`${workload.id}-${index}`} className="workload-card">
            <Flex justify="between" align="start" gap="3">
              <LabeledInput label="Workload ID" value={workload.id || ""} onChange={(id) => updateWorkload(index, { id })} />
              <Button size="1" color="red" variant="soft" onClick={() => removeWorkload(index)}>
                删除
              </Button>
            </Flex>
            <div className="field-grid two">
              <FieldSelect
                label="运行时"
                value={workload.runtime || "docker"}
                onValueChange={(runtime) => updateWorkload(index, { runtime })}
                options={[
                  { value: "docker", label: "Docker" },
                  { value: "systemd", label: "systemd" },
                ]}
              />
              <FieldSelect
                label="绑定组件"
                value={workload.component || "__none__"}
                onValueChange={(component) => updateWorkload(index, { component: component === "__none__" ? "" : component })}
                options={[
                  { value: "__none__", label: "不绑定组件" },
                  ...(manifest.components || []).map((component) => ({ value: component.id, label: component.id })),
                ]}
              />
              <LabeledInput label="模块开关" value={workload.module || ""} onChange={(module) => updateWorkload(index, { module })} />
              <label className="inline-check">
                <Checkbox checked={workload.enabled !== false} onCheckedChange={(value) => updateWorkload(index, { enabled: value === true })} />
                <Text size="2">默认启用</Text>
              </label>
            </div>
            {workload.runtime === "systemd" && (
              <LabeledInput
                label="ExecStart"
                value={workload.systemd?.execStart || ""}
                onChange={(execStart) =>
                  updateWorkload(index, {
                    systemd: { ...(workload.systemd || {}), execStart },
                  })
                }
              />
            )}
            {workload.runtime === "docker" && (
              <LabeledInput
                label="Docker 镜像"
                value={workload.docker?.image || ""}
                onChange={(image) =>
                  updateWorkload(index, {
                    docker: { ...(workload.docker || {}), image },
                  })
                }
              />
            )}
          </div>
        ))}
        {(manifest.workloads || []).length === 0 && (
          <EmptyState icon={Server} title="暂无工作负载" text="添加 workload 后可声明安装包内需要部署的服务。" />
        )}
      </div>
    </div>
  );
}

function ManifestDryRunPanel({ result, running, onRun }) {
  if (running) {
    return (
      <div className="loading-panel">
        <Spinner size="3" />
        <Text color="gray">正在解析 manifest 并生成构建锁预览</Text>
      </div>
    );
  }
  if (!result) {
    return (
      <EmptyState
        icon={GitCompare}
        title="尚未执行试运行"
        text="点击试运行可用当前构建参数解析 manifest，提前检查构建锁、组件 URI 和工作负载。"
      />
    );
  }
  if (!result.valid) {
    return <ValidationPanel result={{ valid: false, errors: result.errors || ["试运行未通过"] }} />;
  }
  return (
    <div className="section-stack">
      <Callout.Root color="green" variant="soft">
        <Callout.Icon>
          <Check size={18} />
        </Callout.Icon>
        <Callout.Text>试运行通过，构建锁可生成。</Callout.Text>
        <Button size="1" variant="ghost" onClick={onRun}>
          重新运行
        </Button>
      </Callout.Root>
      <div className="audit-grid">
        <AuditMetric label="目标平台" value={result.buildLock?.target || "-"} />
        <AuditMetric label="Profile" value={result.buildLock?.profile || "-"} />
        <AuditMetric label="组件锁定" value={result.components?.length || 0} />
        <AuditMetric label="工作负载" value={result.buildLock?.workloads?.length || 0} />
      </div>
      <div className="dryrun-lock">
        <Flex align="center" gap="3" mb="3">
          <div className="panel-icon">
            <GitCompare size={18} />
          </div>
          <Box>
            <Text as="div" weight="bold">
              构建锁预览
            </Text>
            <Text as="div" size="2" color="gray">
              不会创建真实构建，只用于保存前验证解析结果
            </Text>
          </Box>
        </Flex>
        <JsonBlock value={result.buildLock || {}} />
      </div>
    </div>
  );
}

function ManifestRevisionsPanel({ revisions, loading, onRefresh, onRestore }) {
  return (
    <div className="section-stack">
      <Flex justify="between" gap="3" align="center" wrap="wrap">
        <Text size="2" color="gray">
          每次保存 Manifest 会生成一个可恢复的历史版本。
        </Text>
        <Button variant="soft" onClick={onRefresh} disabled={loading}>
          {loading ? <Spinner /> : <RefreshCcw size={16} />}
          刷新历史
        </Button>
      </Flex>
      {loading ? (
        <div className="loading-panel">
          <Spinner size="3" />
          <Text color="gray">正在加载历史版本</Text>
        </div>
      ) : revisions.length === 0 ? (
        <EmptyState icon={History} title="暂无历史版本" text="保存 Manifest 后会在这里出现 revision。" />
      ) : (
        <div className="revision-list">
          {revisions.map((revision) => (
            <div key={revision.id} className="revision-row">
              <Box>
                <Text as="div" weight="bold">
                  {revision.id}
                </Text>
                <Text as="div" size="1" color="gray">
                  {formatDate(revision.createdAt)} / {shortHash(revision.hash)}
                </Text>
              </Box>
              <Text size="2" color="gray">
                {revision.summary || "manifest saved"}
              </Text>
              <Button size="1" variant="soft" onClick={() => onRestore(revision)}>
                <RotateCcw size={14} />
                恢复
              </Button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function manifestFeatureChecks(manifest) {
  const targets = manifest.targets || [];
  const targetConfig = manifest.targetConfig || {};
  const components = manifest.components || [];
  const hasLinux = targets.includes("linux") && Boolean(targetConfig.linux?.installDir);
  const hasWindows = targets.includes("windows") && Boolean(targetConfig.windows?.installDir);
  const requiredComponents = components.filter((component) => component.required !== false);
  const missingSource = requiredComponents.filter((component) => !component.source?.kind && !component.source?.path && !component.source?.uri && !component.source?.url);
  const wildcardSources = requiredComponents.filter((component) => String(component.source?.path || component.source?.uri || component.source?.url || "").includes("*"));
  const hasPreviewProfile = (manifest.profiles || []).includes("qa") || (manifest.profiles || []).includes("preview");
  const hasReleaseProfile = (manifest.profiles || []).includes("prod") || (manifest.profiles || []).includes("release");

  return [
    {
      label: "Linux 安装包目标",
      ok: hasLinux,
      detail: hasLinux ? `安装目录 ${targetConfig.linux.installDir}` : "targets 和 targetConfig 需要包含 linux。",
    },
    {
      label: "Windows 安装包目标",
      ok: hasWindows,
      detail: hasWindows ? `安装目录 ${targetConfig.windows.installDir}` : "targets 和 targetConfig 需要包含 windows。",
    },
    {
      label: "Preview 与 Release 配置面",
      ok: hasPreviewProfile && hasReleaseProfile,
      detail: "建议至少保留 qa/preview 与 prod/release 两类 profile，用于先验收再发布。",
      level: "warn",
    },
    {
      label: "必需组件来源",
      ok: requiredComponents.length > 0 && missingSource.length === 0,
      detail:
        missingSource.length === 0
          ? `${requiredComponents.length} 个必需组件都有来源定义。`
          : `${missingSource.map((item) => item.id).join(", ")} 缺少来源。`,
    },
    {
      label: "Release 复现性",
      ok: wildcardSources.length === 0 && !(manifest.versions || []).every((version) => version === "latest"),
      detail:
        wildcardSources.length === 0
          ? "Release 仍需在创建构建时选择非 latest 版本，后端会阻止通配符 URI 发布。"
          : `${wildcardSources.map((item) => item.id).join(", ")} 使用通配符来源，Release 前需要改为精确 URI。`,
      level: wildcardSources.length === 0 ? undefined : "warn",
    },
  ];
}

function isValidOverlayName(name) {
  return overlayNamePattern.test(name);
}

function UploadsSection({ uploads, uploadForm, setUploadForm, uploading, onUpload }) {
  return (
    <div className="section-stack">
      <Panel title="上传制品" description="上传文件后可在组件来源中选择手动上传记录" icon={Upload}>
        <div className="upload-policy-grid">
          <FieldSelect
            label="下载策略"
            value={uploadForm.downloadPolicy}
            onValueChange={(downloadPolicy) => setUploadForm({ ...uploadForm, downloadPolicy })}
            options={[
              { value: "platform", label: "平台内下载" },
              { value: "restricted", label: "受限下载" },
              { value: "public", label: "公开链接" },
            ]}
          />
          <LabeledInput
            label="保留天数"
            type="number"
            value={uploadForm.retentionDays}
            onChange={(retentionDays) => setUploadForm({ ...uploadForm, retentionDays })}
          />
          <LabeledInput
            label="产品标识"
            value={uploadForm.productId}
            onChange={(productId) => setUploadForm({ ...uploadForm, productId })}
            placeholder="例如 lsap"
          />
          <LabeledInput
            label="用途"
            value={uploadForm.purpose}
            onChange={(purpose) => setUploadForm({ ...uploadForm, purpose })}
          />
        </div>
        <div className="upload-box">
          <Upload size={26} />
          <Box>
            <Text as="div" weight="bold">
              选择文件上传到平台存储
            </Text>
            <Text as="div" size="2" color="gray">
              保存 SHA256、下载策略、保留期限和业务 metadata，当前接口大小限制为 512 MB。
            </Text>
          </Box>
          <input
            className="file-input"
            type="file"
            disabled={uploading}
            onChange={(event) => onUpload(event.target.files?.[0])}
          />
        </div>
      </Panel>

      <Panel title="上传记录" description="制品 metadata、下载策略和保留状态" icon={Database}>
        <div className="table-scroll">
          <Table.Root className="upload-table" variant="surface">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeaderCell>文件</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>大小</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>SHA256</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>策略</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>保留</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>Metadata</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>时间</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>下载</Table.ColumnHeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {uploads.length === 0 ? (
                <Table.Row>
                  <Table.Cell colSpan={8}>
                    <Text color="gray">暂无上传记录</Text>
                  </Table.Cell>
                </Table.Row>
              ) : (
                uploads.map((upload) => (
                  <Table.Row key={upload.id}>
                    <Table.Cell>
                      <div className="cell-main">
                        <Text weight="medium">{upload.fileName}</Text>
                        <Text size="1" color="gray">
                          {upload.uri}
                        </Text>
                      </div>
                    </Table.Cell>
                    <Table.Cell>{formatBytes(upload.size)}</Table.Cell>
                    <Table.Cell>
                      <Code>{shortHash(upload.sha256)}</Code>
                    </Table.Cell>
                    <Table.Cell>{downloadPolicyText(upload.downloadPolicy)}</Table.Cell>
                    <Table.Cell>
                      <Badge color={retentionColor(upload.retentionStatus)} variant="soft">
                        {retentionText(upload)}
                      </Badge>
                    </Table.Cell>
                    <Table.Cell>
                      <Text size="1" color="gray">
                        {uploadMetadataText(upload.metadata)}
                      </Text>
                    </Table.Cell>
                    <Table.Cell>{formatDate(upload.createdAt)}</Table.Cell>
                    <Table.Cell>
                      <Link
                        href={upload.downloadUrl}
                        target="_blank"
                        className={upload.retentionStatus === "expired" ? "is-disabled-link" : ""}
                        onClick={(event) => {
                          if (upload.retentionStatus === "expired") {
                            event.preventDefault();
                          }
                        }}
                      >
                        <Download size={14} /> 下载
                      </Link>
                    </Table.Cell>
                  </Table.Row>
                ))
              )}
            </Table.Body>
          </Table.Root>
        </div>
      </Panel>
    </div>
  );
}

function RunnersSection({ runners, builds, onInspect }) {
  const activeBuilds = builds.filter((build) => build.status === "running" || build.status === "queued");
  const queuedBuilds = builds.filter((build) => build.status === "queued");
  const runningBuilds = builds.filter((build) => build.status === "running");
  const staleRunners = runners.filter((runner) => ["stale", "offline", "unknown"].includes(runner.status));
  return (
    <div className="section-stack">
      <Grid className="metric-grid" gap="4">
        <MetricCard icon={Server} label="Runner 数量" value={runners.length} hint="已观测到的 runner" color="teal" />
        <MetricCard icon={Gauge} label="忙碌 Runner" value={runners.filter((runner) => runner.status === "busy").length} hint="正在执行构建" color="blue" />
        <MetricCard icon={AlertCircle} label="异常 Runner" value={staleRunners.length} hint="心跳异常或状态未知" color="amber" />
        <MetricCard icon={ClipboardList} label="活跃任务" value={activeBuilds.length} hint="排队中 + 构建中" color="green" />
      </Grid>
      <Panel title="Runner 执行链路" description="用于确认队列是否被 runner 消费，以及当前卡在哪个阶段" icon={Gauge}>
        <div className="runner-flow-grid">
          <AuditMetric label="排队中" value={queuedBuilds.length} />
          <AuditMetric label="构建中" value={runningBuilds.length} />
          <AuditMetric label="心跳异常" value={staleRunners.length} />
          <AuditMetric label="最近日志" value={latestRunnerLogTime(runners)} />
        </div>
        <div className="runner-flow-list">
          {activeBuilds.length === 0 ? (
            <Text size="2" color="gray">
              当前没有 queued 或 running 构建。
            </Text>
          ) : (
            activeBuilds.slice(0, 6).map((build) => (
              <button key={build.id} type="button" className="runner-flow-row" onClick={() => onInspect(build)}>
                <StatusBadge status={build.status} />
                <Code>{shortId(build.id)}</Code>
                <Text size="2">{build.productId}</Text>
                <Text size="2" color="gray">
                  {build.target} / {build.version}
                </Text>
                <Text size="2" color="gray">
                  {build.runnerId || "等待 runner"}
                </Text>
              </button>
            ))
          )}
        </div>
      </Panel>
      <Panel title="Runner 状态" description="由 claim、heartbeat、complete 和构建日志聚合生成" icon={Server}>
        {runners.length === 0 ? (
          <EmptyState icon={Server} title="暂无 Runner" text="Runner claim 或 heartbeat 后会在这里出现。" />
        ) : (
          <div className="runner-grid">
            {runners.map((runner) => {
              const currentBuild = builds.find((build) => build.id === runner.currentBuildId);
              return (
                <div key={runner.runnerId} className="runner-card">
                  <Flex justify="between" gap="3" align="start">
                    <Box>
                      <Text as="div" weight="bold">
                        {runner.runnerId}
                      </Text>
                      <Text as="div" size="1" color="gray">
                        {runner.target || "未声明目标平台"}
                      </Text>
                    </Box>
                    <Badge color={runnerStatusColor(runner.status)} variant="soft">
                      {runnerStatusText(runner.status)}
                    </Badge>
                  </Flex>
                  <div className="runner-metrics">
                    <AssetLine label="当前构建" value={runner.currentBuildId ? shortId(runner.currentBuildId) : "-"} />
                    <AssetLine label="当前状态" value={runner.currentBuildState || "-"} />
                    <AssetLine label="运行中" value={runner.runningBuilds} />
                    <AssetLine label="完成" value={runner.completedBuilds} />
                    <AssetLine label="失败" value={runner.failedBuilds} />
                    <AssetLine label="最后心跳" value={runner.lastHeartbeatAt ? formatDate(runner.lastHeartbeatAt) : "-"} />
                    <AssetLine label="最后日志" value={runner.lastLogAt ? formatDate(runner.lastLogAt) : "-"} />
                  </div>
                  <div className="runner-stage-strip" aria-label={`${runner.runnerId} 当前阶段`}>
                    {["claim", "package", "upload", "complete"].map((stage) => (
                      <div key={stage} className={`runner-stage ${runnerStageState(runner, stage)}`}>
                        {stage}
                      </div>
                    ))}
                  </div>
                  {currentBuild && (
                    <Button size="1" variant="soft" onClick={() => onInspect(currentBuild)}>
                      <ListChecks size={14} />
                      查看当前构建
                    </Button>
                  )}
                  <div className="runner-log-list">
                    {(runner.recentMessages || []).map((log) => (
                      <div key={`${runner.runnerId}-${log.sequence}-${log.time}`} className="runner-log-line">
                        <Code>{log.sequence || "-"}</Code>
                        <Text size="1" color="gray">{formatDate(log.time)}</Text>
                        <Text size="2">{log.message}</Text>
                      </div>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Panel>
    </div>
  );
}

function latestRunnerLogTime(runners) {
  const latest = runners
    .map((runner) => dateValue(runner.lastLogAt || runner.lastHeartbeatAt))
    .filter((value) => value > 0)
    .sort((a, b) => b - a)[0];
  return latest ? formatDate(new Date(latest).toISOString()) : "-";
}

function runnerStageState(runner, stage) {
  if (runner.status === "offline" || runner.status === "stale") return "is-muted";
  if (!runner.currentBuildId) return stage === "claim" ? "is-pass" : "is-pending";
  if (runner.currentBuildState === "running") {
    return stage === "claim" || stage === "package" ? "is-pass" : "is-pending";
  }
  if (["succeeded", "preview_validated", "published"].includes(runner.currentBuildState)) return "is-pass";
  if (runner.currentBuildState === "failed") return stage === "complete" ? "is-fail" : "is-pass";
  return stage === "claim" ? "is-pass" : "is-pending";
}

function LogsAndLockSection({ build, logs, buildLock, releaseCheck, onRefresh }) {
  return (
    <Panel
      title="日志与构建锁"
      description={build ? `${build.productId} / ${build.id}` : "从构建记录中选择一个任务"}
      icon={ScrollText}
      action={
        <Button variant="soft" onClick={onRefresh} disabled={!build}>
          <RefreshCcw size={16} />
          刷新详情
        </Button>
      }
    >
      {!build ? (
        <EmptyState icon={ScrollText} title="尚未选择构建" text="在构建记录中点击详情后，可查看日志、最终配置和组件锁定结果。" />
      ) : (
        <BuildInspectTabs build={build} logs={logs} buildLock={buildLock} releaseCheck={releaseCheck} />
      )}
    </Panel>
  );
}

function ComponentSourceEditor({ components, uploads, overridesText, onUpdate, onUpload }) {
  let overrides = {};
  try {
    overrides = JSON.parse(overridesText || "{}");
  } catch {
    overrides = {};
  }

  if (components.length === 0) {
    return <EmptyState icon={Boxes} title="暂无组件" text="当前产品 manifest 没有定义 components。" />;
  }

  const rows = components.map((component) => {
    const override = overrides[component.id] || {};
    const source = { ...component.source, ...override };
    const kind = source.kind || "artifact-repository";
    return {
      component,
      source,
      kind,
      health: componentSourceHealth(component, source),
    };
  });
  const readyCount = rows.filter((row) => row.health.tone === "ready").length;
  const warningCount = rows.filter((row) => row.health.tone === "warn").length;
  const blockedCount = rows.filter((row) => row.health.tone === "blocked").length;

  return (
    <div className="section-stack">
      <div className="component-health-strip">
        <AuditMetric label="组件总数" value={components.length} />
        <AuditMetric label="来源就绪" value={readyCount} />
        <AuditMetric label="需确认" value={warningCount} />
        <AuditMetric label="阻塞" value={blockedCount} />
      </div>
      <div className="table-scroll">
        <Table.Root className="component-source-table" variant="surface">
          <Table.Header>
            <Table.Row>
              <Table.ColumnHeaderCell>组件</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>来源</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>定位信息</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>上传</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>状态</Table.ColumnHeaderCell>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {rows.map(({ component, source, kind, health }) => (
              <Table.Row key={component.id}>
                <Table.Cell>
                  <div className="cell-main">
                    <Flex gap="2" align="center" wrap="wrap">
                      <Text weight="bold">{component.id}</Text>
                      <Badge color={component.required ? "red" : "gray"} variant="soft">
                        {component.required ? "必需" : "可选"}
                      </Badge>
                    </Flex>
                    <Text size="1" color="gray">
                      {component.type}
                    </Text>
                  </div>
                </Table.Cell>
                <Table.Cell>
                  <FieldSelect
                    label="来源类型"
                    value={kind}
                    onValueChange={(nextKind) => onUpdate(component.id, defaultComponentSource(nextKind, source))}
                    options={sourceKindOptions.map((option) => ({
                      value: option.key,
                      label: option.label,
                    }))}
                  />
                </Table.Cell>
                <Table.Cell>
                  {kind === "artifact-repository" && (
                    <LabeledInput
                      label="仓库路径"
                      value={source.path || ""}
                      onChange={(value) =>
                        onUpdate(component.id, {
                          kind: "artifact-repository",
                          repository: source.repository || "minio",
                          path: value,
                        })
                      }
                    />
                  )}
                  {kind === "fixed-url" && (
                    <LabeledInput
                      label="URL"
                      value={source.url || ""}
                      onChange={(value) => onUpdate(component.id, { kind: "fixed-url", url: value })}
                    />
                  )}
                  {kind === "previous-build" && (
                    <LabeledInput
                      label="制品 URI"
                      value={source.uri || ""}
                      onChange={(value) => onUpdate(component.id, { kind: "previous-build", uri: value })}
                    />
                  )}
                  {kind === "manual-upload" && (
                    <Code>{source.uri || "等待选择上传记录"}</Code>
                  )}
                </Table.Cell>
                <Table.Cell>
                  {kind === "manual-upload" ? (
                    <div className="field-grid">
                      <FieldSelect
                        label="已有上传"
                        value={source.uploadId || "__none__"}
                        onValueChange={(uploadId) => {
                          if (uploadId === "__none__") {
                            onUpdate(component.id, { kind: "manual-upload", uploadId: "", uri: "" });
                            return;
                          }
                          const upload = uploads.find((item) => item.id === uploadId);
                          onUpdate(component.id, {
                            kind: "manual-upload",
                            uploadId,
                            uri: upload?.uri || "",
                          });
                        }}
                        options={[
                          { value: "__none__", label: "不复用上传记录" },
                          ...uploads.map((upload) => ({
                            value: upload.id,
                            label: `${upload.fileName} (${formatBytes(upload.size)})`,
                          })),
                        ]}
                      />
                      <input
                        className="file-input"
                        aria-label={`上传 ${component.id} 组件文件`}
                        type="file"
                        onChange={(event) => onUpload(component.id, event.target.files?.[0])}
                      />
                    </div>
                  ) : (
                    <Text size="2" color="gray">
                      不需要上传
                    </Text>
                  )}
                </Table.Cell>
                <Table.Cell>
                  <div className={`component-health is-${health.tone}`}>
                    {health.tone === "ready" ? <Check size={15} /> : <AlertCircle size={15} />}
                    <span>{health.text}</span>
                  </div>
                </Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table.Root>
      </div>
    </div>
  );
}

function componentSourceHealth(component, source) {
  const kind = source.kind || "artifact-repository";
  if (kind === "manual-upload") {
    if (source.uploadId || source.uri) {
      return { tone: "ready", text: "已绑定上传制品" };
    }
    return component.required
      ? { tone: "blocked", text: "必需组件未绑定上传" }
      : { tone: "warn", text: "可选组件未绑定上传" };
  }
  if (kind === "artifact-repository") {
    if (!source.path) return { tone: "blocked", text: "缺少仓库路径" };
    if (String(source.path).includes("*")) return { tone: "warn", text: "Preview 可用，Release 需精确 URI" };
    if (String(source.path).includes("{{version}}")) return { tone: "warn", text: "依赖构建版本解析" };
    return { tone: "ready", text: "仓库路径已配置" };
  }
  if (kind === "fixed-url") {
    if (!source.url) return { tone: "blocked", text: "缺少固定 URL" };
    return String(source.url).includes("*")
      ? { tone: "warn", text: "URL 包含通配符" }
      : { tone: "ready", text: "固定 URL 已配置" };
  }
  if (kind === "previous-build") {
    return source.uri
      ? { tone: "ready", text: "历史制品 URI 已配置" }
      : { tone: "blocked", text: "缺少历史制品 URI" };
  }
  return { tone: "warn", text: "未知来源类型" };
}

function BuildsTable({
  builds,
  loading,
  busyAction,
  onApprove,
  onReject,
  onValidate,
  onPublish,
  onCancel,
  onRetry,
  onInspect,
}) {
  return (
    <>
      <div className="build-card-list">
        {builds.length === 0 ? (
          <Text color="gray">{loading ? "正在加载构建记录" : "没有匹配的构建记录"}</Text>
        ) : (
          builds.map((build) => (
            <div key={build.id} className="build-card-row">
              <Flex justify="between" align="start" gap="3">
                <div className="cell-main">
                  <Code>{shortId(build.id)}</Code>
                  <Text size="1" color="gray">
                    {build.productId} / {build.target} / {build.profile || "-"}
                  </Text>
                </div>
                <StatusBadge status={build.status} />
              </Flex>
              <div className="build-card-meta">
                <AssetLine label="版本" value={build.version || "-"} />
                <AssetLine label="更新" value={formatDate(build.updatedAt)} />
                <Flex justify="between" gap="3" className="asset-line">
                  <Text color="gray">门禁</Text>
                  <BuildReleaseGateInline build={build} />
                </Flex>
              </div>
              <BuildActions
                build={build}
                busyAction={busyAction}
                onApprove={onApprove}
                onReject={onReject}
                onValidate={onValidate}
                onPublish={onPublish}
                onCancel={onCancel}
                onRetry={onRetry}
                onInspect={onInspect}
              />
            </div>
          ))
        )}
      </div>
      <div className="table-scroll build-table-scroll">
        <Table.Root className="build-table" variant="surface">
          <Table.Header>
            <Table.Row>
              <Table.ColumnHeaderCell>ID</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>产品</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>目标</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>Profile</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>状态</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>发布门禁</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>更新时间</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>制品</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>操作</Table.ColumnHeaderCell>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {builds.length === 0 ? (
              <Table.Row>
                <Table.Cell colSpan={9}>
                  <Text color="gray">{loading ? "正在加载构建记录" : "没有匹配的构建记录"}</Text>
                </Table.Cell>
              </Table.Row>
            ) : (
              builds.map((build) => (
                <Table.Row key={build.id}>
                  <Table.Cell>
                    <Code>{shortId(build.id)}</Code>
                  </Table.Cell>
                  <Table.Cell>
                    <div className="cell-main">
                      <Text weight="medium">{build.productId}</Text>
                      <Text size="1" color="gray">
                        {build.version}
                      </Text>
                    </div>
                  </Table.Cell>
                  <Table.Cell>{build.target}</Table.Cell>
                  <Table.Cell>{build.profile || "-"}</Table.Cell>
                  <Table.Cell>
                    <StatusBadge status={build.status} />
                  </Table.Cell>
                  <Table.Cell>
                    <BuildReleaseGateInline build={build} />
                  </Table.Cell>
                  <Table.Cell>{formatDate(build.updatedAt)}</Table.Cell>
                  <Table.Cell>
                    {build.downloadUrl || build.artifactUri ? (
                      <Link href={build.downloadUrl || buildDownloadUrl(build.id)} target="_blank">
                        <Download size={14} /> 下载
                      </Link>
                    ) : (
                      <Text color="gray">未生成</Text>
                    )}
                  </Table.Cell>
                  <Table.Cell>
                    <BuildActions
                      build={build}
                      busyAction={busyAction}
                      onApprove={onApprove}
                      onReject={onReject}
                      onValidate={onValidate}
                      onPublish={onPublish}
                      onCancel={onCancel}
                      onRetry={onRetry}
                      onInspect={onInspect}
                    />
                  </Table.Cell>
                </Table.Row>
              ))
            )}
          </Table.Body>
        </Table.Root>
      </div>
    </>
  );
}

function BuildReleaseGateInline({ build }) {
  const readiness = getReleaseReadiness(build);
  if (build.status === "published") {
    return (
      <div className="release-readiness is-pass">
        <Check size={14} />
        <span>已发布</span>
      </div>
    );
  }
  if (build.status === "preview_validated") {
    return readiness.ready ? (
      <div className="release-readiness is-pass">
        <Rocket size={14} />
        <span>可发布</span>
      </div>
    ) : (
      <div className="release-readiness is-blocked">
        <AlertCircle size={14} />
        <span>{readiness.blockingCount || 1} 阻塞</span>
        <small>{readiness.reason || "发布门禁未通过"}</small>
      </div>
    );
  }
  if (build.status === "succeeded") {
    return (
      <div className="release-readiness is-waiting">
        <ShieldCheck size={14} />
        <span>待验证 Preview</span>
      </div>
    );
  }
  return (
    <div className="release-readiness is-muted">
      <CircleDot size={14} />
      <span>等待构建成功</span>
    </div>
  );
}

function BuildActions({
  build,
  busyAction,
  onApprove,
  onReject,
  onValidate,
  onPublish,
  onCancel,
  onRetry,
  onInspect,
  showInspect = true,
}) {
  const releaseReadiness = getReleaseReadiness(build);
  return (
    <Flex gap="2" wrap="wrap">
      {build.status === "pending_approval" && (
        <>
          <Button size="1" color="green" variant="soft" disabled={busyAction === `审批通过:${build.id}`} onClick={() => onApprove(build)}>
            <Check size={14} />
            通过
          </Button>
          <Button size="1" color="red" variant="soft" disabled={busyAction === `拒绝:${build.id}`} onClick={() => onReject(build)}>
            <X size={14} />
            拒绝
          </Button>
        </>
      )}
      {build.status === "succeeded" && (
        <Button
          size="1"
          color="teal"
          variant="soft"
          disabled={busyAction === `验证 Preview:${build.id}`}
          onClick={() => onValidate(build)}
        >
          <ShieldCheck size={14} />
          验证
        </Button>
      )}
      {build.status === "preview_validated" && (
        <Tooltip content={releaseReadiness.reason || "将已验证 Preview 转为 Release"}>
          <Button
            size="1"
            variant="soft"
            disabled={!releaseReadiness.ready || busyAction === `转 Release:${build.id}`}
            onClick={() => onPublish(build)}
          >
            <ShieldCheck size={14} />
            Release
          </Button>
        </Tooltip>
      )}
      {["pending_approval", "queued", "running"].includes(build.status) && (
        <Button size="1" color="amber" variant="soft" disabled={busyAction === `取消:${build.id}`} onClick={() => onCancel(build)}>
          <Ban size={14} />
          取消
        </Button>
      )}
      {["failed", "canceled"].includes(build.status) && (
        <Button size="1" color="purple" variant="soft" disabled={busyAction === `重试:${build.id}`} onClick={() => onRetry(build)}>
          <RotateCcw size={14} />
          重试
        </Button>
      )}
      {showInspect && (
        <Button size="1" variant="soft" onClick={() => onInspect(build)}>
          <ListChecks size={14} />
          详情
        </Button>
      )}
    </Flex>
  );
}

function BuildDetailDialog({
  open,
  onOpenChange,
  build,
  logs,
  buildLock,
  releaseCheck,
  onApprove,
  onReject,
  onValidate,
  onPublish,
  onCancel,
  onRetry,
  busyAction,
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Content className="detail-dialog">
        <Dialog.Title>
          <Flex align="center" gap="3" wrap="wrap">
            构建详情
            {build && <StatusBadge status={build.status} />}
          </Flex>
        </Dialog.Title>
        <Dialog.Description>
          {build ? `${build.productId} / ${build.id}` : "请选择一个构建记录"}
        </Dialog.Description>
        <Box mt="4">
          {build ? (
            <BuildInspectTabs build={build} logs={logs} buildLock={buildLock} releaseCheck={releaseCheck} />
          ) : (
            <EmptyState icon={ClipboardList} title="暂无构建" text="请选择一个构建记录。" />
          )}
        </Box>
        <Flex mt="4" justify="between" gap="3" wrap="wrap">
          {build ? (
            <BuildActions
              build={build}
              busyAction={busyAction}
              onApprove={onApprove}
              onReject={onReject}
              onValidate={onValidate}
              onPublish={onPublish}
              onCancel={onCancel}
              onRetry={onRetry}
              onInspect={() => {}}
              showInspect={false}
            />
          ) : (
            <span />
          )}
          <Dialog.Close>
            <Button variant="soft">关闭</Button>
          </Dialog.Close>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

function BuildInspectTabs({ build, logs, buildLock, releaseCheck }) {
  return (
    <Tabs.Root defaultValue="summary">
      <Tabs.List>
        <Tabs.Trigger value="summary">概要</Tabs.Trigger>
        <Tabs.Trigger value="config">最终配置</Tabs.Trigger>
        <Tabs.Trigger value="components">组件锁定</Tabs.Trigger>
        <Tabs.Trigger value="workloads">工作负载</Tabs.Trigger>
        <Tabs.Trigger value="release">发布门禁</Tabs.Trigger>
        <Tabs.Trigger value="audit">审计</Tabs.Trigger>
        <Tabs.Trigger value="lock">构建锁</Tabs.Trigger>
        <Tabs.Trigger value="logs">日志</Tabs.Trigger>
      </Tabs.List>
      <Box pt="4">
        <Tabs.Content value="summary">
          <BuildExecutionSummary build={build} buildLock={buildLock || build.buildLock} releaseCheck={releaseCheck} />
          <div className="detail-grid">
            <DetailItem label="构建 ID" value={build.id} mono />
            <DetailItem label="产品" value={build.productId} />
            <DetailItem label="目标平台" value={build.target} />
            <DetailItem label="版本" value={build.version} />
            <DetailItem label="Profile" value={build.profile || "-"} />
            <DetailItem label="构建通道" value={build.releaseChannel || "-"} />
            <DetailItem label="Runner" value={build.runnerId || "-"} />
            <DetailItem label="Nomad Job" value={build.nomadJobId || "-"} mono />
            <DetailItem label="创建时间" value={formatDate(build.createdAt)} />
            <DetailItem label="更新时间" value={formatDate(build.updatedAt)} />
            <DetailItem label="验证时间" value={build.validatedAt ? formatDate(build.validatedAt) : "-"} />
            <DetailItem label="发布时间" value={build.publishedAt ? formatDate(build.publishedAt) : "-"} />
            <DetailItem label="错误信息" value={build.error || "-"} wide />
            <DetailItem label="制品 URI" value={build.artifactUri || "-"} wide mono />
          </div>
        </Tabs.Content>
        <Tabs.Content value="config">
          <JsonBlock value={build.finalConfig || buildLock?.config || {}} />
        </Tabs.Content>
        <Tabs.Content value="components">
          <ResolvedComponents components={buildLock?.components || build.components || []} />
        </Tabs.Content>
        <Tabs.Content value="workloads">
          <ResolvedWorkloads workloads={buildLock?.workloads || build.workloads || []} />
        </Tabs.Content>
        <Tabs.Content value="release">
          <ReleaseGatePanel build={build} releaseCheck={releaseCheck} />
        </Tabs.Content>
        <Tabs.Content value="audit">
          <BuildLockAudit build={build} buildLock={buildLock || build.buildLock} />
        </Tabs.Content>
        <Tabs.Content value="lock">
          <JsonBlock value={buildLock || build.buildLock || {}} />
        </Tabs.Content>
        <Tabs.Content value="logs">
          <Flex justify="end" mb="3">
            <Link href={buildLogStreamUrl(build.id)} target="_blank">
              <Server size={14} /> 打开实时日志流
            </Link>
          </Flex>
          <LogsList logs={logs} />
        </Tabs.Content>
      </Box>
    </Tabs.Root>
  );
}

function ResolvedComponents({ components }) {
  if (!components.length) {
    return <EmptyState icon={Boxes} title="暂无组件锁定信息" text="构建创建后会生成组件来源和 URI。" />;
  }
  const summary = componentLockSummary(components);
  return (
    <div className="section-stack">
      <div className="component-health-strip">
        <AuditMetric label="锁定组件" value={components.length} />
        <AuditMetric label="精确 URI" value={summary.exactUris} />
        <AuditMetric label="固定版本" value={summary.fixedVersions} />
        <AuditMetric label="缺少 URI" value={summary.missingUris} />
      </div>
      {summary.risks.length > 0 && (
        <Callout.Root color="amber" variant="soft">
          <Callout.Icon>
            <AlertCircle size={18} />
          </Callout.Icon>
          <Callout.Text>{summary.risks.join("；")}</Callout.Text>
        </Callout.Root>
      )}
      <div className="table-scroll">
        <Table.Root className="component-lock-table" variant="surface">
          <Table.Header>
            <Table.Row>
              <Table.ColumnHeaderCell>组件</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>类型</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>来源</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>版本</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>URI</Table.ColumnHeaderCell>
              <Table.ColumnHeaderCell>SHA256</Table.ColumnHeaderCell>
            </Table.Row>
          </Table.Header>
          <Table.Body>
            {components.map((component) => {
              const uri = component.uri || component.source?.uri || "";
              return (
                <Table.Row key={component.id}>
                  <Table.Cell>
                    <Flex gap="2" align="center" wrap="wrap">
                      <Text>{component.id}</Text>
                      {component.required && <Badge variant="soft" color="red">必需</Badge>}
                    </Flex>
                  </Table.Cell>
                  <Table.Cell>{component.type}</Table.Cell>
                  <Table.Cell>{sourceKindLabels[component.source?.kind] || component.source?.kind}</Table.Cell>
                  <Table.Cell>
                    <Badge color={component.version === "latest" ? "amber" : "green"} variant="soft">
                      {component.version || "-"}
                    </Badge>
                  </Table.Cell>
                  <Table.Cell>
                    <Code>{uri || "-"}</Code>
                  </Table.Cell>
                  <Table.Cell>
                    <Code>{shortHash(component.sha256)}</Code>
                  </Table.Cell>
                </Table.Row>
              );
            })}
          </Table.Body>
        </Table.Root>
      </div>
    </div>
  );
}

function BuildExecutionSummary({ build, buildLock, releaseCheck }) {
  const check = releaseCheck || getLocalReleaseCheck(build);
  return (
    <div className="execution-summary">
      <AuditMetric label="当前状态" value={statusMeta[build.status]?.label || build.status || "-"} />
      <AuditMetric label="Runner" value={build.runnerId || "未分配"} />
      <AuditMetric label="Build Lock" value={buildLock?.buildId ? "已生成" : "缺失"} />
      <AuditMetric label="Release 门禁" value={check.releaseReady ? "通过" : `${check.blockingCount || 0} 阻塞`} />
    </div>
  );
}

function componentLockSummary(components) {
  const risks = [];
  let exactUris = 0;
  let fixedVersions = 0;
  let missingUris = 0;
  for (const component of components) {
    const uri = String(component.uri || component.source?.uri || "");
    if (!uri) {
      missingUris++;
      risks.push(`${component.id} 缺少 URI`);
    } else if (!uri.includes("*")) {
      exactUris++;
    } else {
      risks.push(`${component.id} URI 包含通配符`);
    }
    if (component.version && component.version !== "latest") {
      fixedVersions++;
    } else {
      risks.push(`${component.id} 版本未固定`);
    }
  }
  return { exactUris, fixedVersions, missingUris, risks };
}

function ResolvedWorkloads({ workloads }) {
  if (!workloads.length) {
    return <EmptyState icon={Server} title="暂无工作负载" text="产品 manifest 中没有定义运行负载。" />;
  }
  return (
    <div className="workload-grid">
      {workloads.map((workload) => (
        <div key={workload.id} className="workload-card">
          <Flex justify="between" align="start" gap="3">
            <Box>
              <Text as="div" weight="bold">
                {workload.id}
              </Text>
              <Text as="div" size="1" color="gray">
                {workload.component || "未绑定组件"}
              </Text>
            </Box>
            <Badge color={workload.enabled === false ? "gray" : "green"} variant="soft">
              {workload.enabled === false ? "禁用" : workload.runtime}
            </Badge>
          </Flex>
          <JsonBlock value={workload} compact />
        </div>
      ))}
    </div>
  );
}

function ReleaseGatePanel({ build, releaseCheck }) {
  const check = releaseCheck || getLocalReleaseCheck(build);
  const previewChecks = (check.checks || []).filter((item) => item.stage === "preview");
  const releaseChecks = (check.checks || []).filter((item) => item.stage === "release");
  const previewBlocked = previewChecks.filter((item) => item.status === "blocked").length;
  const releaseBlocked = releaseChecks.filter((item) => item.status === "blocked").length;
  const verdict = check.releaseReady
    ? { label: "Release 可发布", text: "门禁已通过", tone: "pass" }
    : check.previewReady
      ? { label: "等待 Release 条件", text: `${releaseBlocked} 个发布阻塞项`, tone: "warn" }
      : { label: "Preview 未就绪", text: `${previewBlocked} 个预览阻塞项`, tone: "blocked" };
  return (
    <div className="section-stack">
      <div className="release-gate-status">
        <GateStage
          label="Preview"
          title="构建验证"
          passed={check.previewReady}
          blockedCount={previewBlocked}
        />
        <ChevronRight className="release-gate-arrow" size={18} />
        <GateStage
          label="Release"
          title="发布准入"
          passed={check.releaseReady}
          blockedCount={releaseBlocked}
        />
        <div className={`release-gate-verdict is-${verdict.tone}`}>
          {check.releaseReady ? <Check size={18} /> : <AlertCircle size={18} />}
          <div>
            <Text as="div" weight="bold">
              {verdict.label}
            </Text>
            <Text as="div" size="1">
              {verdict.text}
            </Text>
          </div>
        </div>
      </div>
      <div className="release-gate-summary">
        <AuditMetric label="Preview 门禁" value={check.previewReady ? "通过" : "阻塞"} />
        <AuditMetric label="Release 门禁" value={check.releaseReady ? "通过" : "阻塞"} />
        <AuditMetric label="阻塞项" value={check.blockingCount || 0} />
        <AuditMetric label="阶段" value={check.stage === "release" ? "Release" : "Preview"} />
      </div>
      <div className="release-gate-columns">
        <ReleaseCheckGroup title="Preview 验证" checks={previewChecks} />
        <ReleaseCheckGroup title="Release 发布" checks={releaseChecks} />
      </div>
    </div>
  );
}

function GateStage({ label, title, passed, blockedCount }) {
  return (
    <div className={`gate-stage ${passed ? "is-pass" : "is-blocked"}`}>
      <div className="gate-stage-icon">
        {passed ? <Check size={16} /> : <AlertCircle size={16} />}
      </div>
      <div>
        <Text as="div" size="1" color="gray">
          {label}
        </Text>
        <Text as="div" weight="bold">
          {title}
        </Text>
        <Text as="div" size="1" color="gray">
          {passed ? "已通过" : `${blockedCount} 个阻塞项`}
        </Text>
      </div>
    </div>
  );
}

function ReleaseCheckGroup({ title, checks }) {
  return (
    <div className="release-check-group">
      <Flex justify="between" align="center" gap="3">
        <Text weight="bold">{title}</Text>
        <Badge color={checks.every((item) => item.status !== "blocked") ? "green" : "amber"} variant="soft">
          {checks.filter((item) => item.status === "blocked").length} 阻塞
        </Badge>
      </Flex>
      <div className="release-check-list">
        {checks.map((item) => (
          <div key={item.id} className={`release-check-row is-${item.status}`}>
            <div className="release-check-icon">
              {item.status === "blocked" ? <AlertCircle size={15} /> : <Check size={15} />}
            </div>
            <div>
              <Text as="div" weight="medium">
                {item.title}
              </Text>
              <Text as="div" size="1" color="gray">
                {item.reason ? releaseReasonText(item.reason) : "检查通过"}
              </Text>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function BuildLockAudit({ build, buildLock }) {
  if (!buildLock) {
    return <EmptyState icon={GitCompare} title="暂无审计数据" text="构建创建后会生成构建锁。" />;
  }

  const configDiff = diffLines(
    JSON.stringify(build.finalConfig || {}, null, 2),
    JSON.stringify(buildLock.config || {}, null, 2),
  );
  const componentCount = buildLock.components?.length || 0;
  const enabledWorkloads = (buildLock.workloads || []).filter((workload) => workload.enabled !== false).length;

  return (
    <div className="section-stack">
      <div className="audit-grid">
        <AuditMetric label="锁定组件" value={componentCount} />
        <AuditMetric label="启用工作负载" value={enabledWorkloads} />
        <AuditMetric label="构建目标" value={buildLock.target || build.target || "-"} />
        <AuditMetric label="配置 Profile" value={buildLock.profile || build.profile || "-"} />
      </div>
      <Panel title="最终配置与构建锁差异" description="用于确认界面配置和锁定配置是否一致" icon={GitCompare}>
        <DiffPanel lines={configDiff} emptyText="最终配置与构建锁配置一致。" />
      </Panel>
    </div>
  );
}

function AuditMetric({ label, value }) {
  return (
    <div className="audit-metric">
      <Text as="div" size="1" color="gray">
        {label}
      </Text>
      <Text as="div" size="5" weight="bold">
        {value}
      </Text>
    </div>
  );
}

function LogsList({ logs }) {
  const [filter, setFilter] = useState("");
  const filteredLogs = useMemo(() => {
    const keyword = filter.trim().toLowerCase();
    if (!keyword) return logs || [];
    return (logs || []).filter((entry) =>
      [entry.time, entry.source, entry.message]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(keyword)),
    );
  }, [filter, logs]);

  if (!logs?.length) {
    return <EmptyState icon={ScrollText} title="暂无日志" text="Runner 或系统日志会显示在这里。" />;
  }

  const logText = filteredLogs
    .map((entry) => `${formatDate(entry.time)} [${entry.source}] ${entry.message}`)
    .join("\n");

  return (
    <div className="section-stack">
      <Flex gap="2" align="center" justify="between" wrap="wrap">
        <TextField.Root
          className="search-input"
          placeholder="过滤时间、来源或日志内容"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
        >
          <TextField.Slot>
            <Search size={16} />
          </TextField.Slot>
        </TextField.Root>
        <Flex gap="2" wrap="wrap">
          <Button variant="soft" onClick={() => copyText(logText)}>
            <Copy size={16} />
            复制
          </Button>
          <Button variant="soft" onClick={() => downloadText("build-logs.txt", logText)}>
            <FileDown size={16} />
            下载
          </Button>
        </Flex>
      </Flex>
      <div className="log-list">
        {filteredLogs.map((entry, index) => (
          <div key={`${entry.time}-${index}`} className="log-line">
            <Code>{formatDate(entry.time)}</Code>
            <Code color="teal">[{entry.source}]</Code>
            <Text>{entry.message}</Text>
          </div>
        ))}
        {filteredLogs.length === 0 && (
          <Text color="gray">没有匹配的日志。</Text>
        )}
      </div>
    </div>
  );
}

function BuildTimeline({ builds, onInspect }) {
  if (!builds.length) {
    return <EmptyState icon={History} title="暂无构建记录" text="创建第一个构建后会显示最近活动。" />;
  }
  return (
    <div className="timeline-list">
      {builds.map((build) => (
        <button key={build.id} className="timeline-item" onClick={() => onInspect(build)}>
          <div className="timeline-dot" />
          <div className="timeline-main">
            <Box>
              <Text as="div" weight="bold">
                {build.productId}
              </Text>
              <Text as="div" size="1" color="gray">
                {shortId(build.id)} / {build.target} / {build.version}
              </Text>
            </Box>
            <StatusBadge status={build.status} />
          </div>
          <Text size="1" color="gray">
            {formatDate(build.updatedAt)}
          </Text>
        </button>
      ))}
    </div>
  );
}

function getLocalReleaseCheck(build) {
  const components = build?.buildLock?.components?.length
    ? build.buildLock.components
    : build?.components || [];
  const checks = [
    releaseCheckItem("preview.succeeded", "preview", "Preview 构建已成功", ["succeeded", "preview_validated", "published"].includes(build?.status), "需要成功构建 Preview"),
    releaseCheckItem("preview.channel", "preview", "构建通道为 Preview", build?.releaseChannel === "preview" || build?.status === "published", "Preview 验证只接受 preview 通道"),
    releaseCheckItem("preview.artifact", "preview", "Preview 制品可下载", Boolean(build?.artifactUri || build?.downloadUrl), "preview artifact is not available"),
    releaseCheckItem("preview.build-lock", "preview", "构建锁已生成", Boolean(build?.buildLock?.buildId), "Build Lock is not available"),
    releaseCheckItem("preview.lock-identity", "preview", "构建锁与构建参数一致", localBuildLockMatchesBuild(build), "Build Lock does not match build target, profile, or version"),
    releaseCheckItem("preview.required-components", "preview", "必需组件已锁定 URI", requiredComponentsLocked(components), "preview requires locked required component URIs"),
    releaseCheckItem("release.preview-validated", "release", "Preview 已验证", build?.status === "preview_validated" || build?.status === "published", "需要先验证 Preview"),
    releaseCheckItem("release.fixed-version", "release", "构建版本固定", Boolean(build?.version && build.version !== "latest"), "Release 需要选择固定版本，不能使用 latest"),
    releaseCheckItem("release.exact-component-uris", "release", "组件 URI 精确", componentURIsExact(components), "组件 URI 不能包含通配符"),
    releaseCheckItem("release.fixed-component-versions", "release", "组件版本固定", componentVersionsFixed(components), "组件版本不能是 latest"),
    releaseCheckItem("release.artifact", "release", "Release 制品可下载", Boolean(build?.artifactUri || build?.downloadUrl), "Release 制品不可用"),
    releaseCheckItem("release.no-build-error", "release", "构建无错误记录", !build?.error, "构建存在错误记录"),
  ];
  const blockingCount = checks.filter((item) => item.status === "blocked").length;
  return {
    buildId: build?.id || "",
    stage: build?.status === "preview_validated" || build?.status === "published" ? "release" : "preview",
    previewReady: checks.filter((item) => item.stage === "preview").every((item) => item.status !== "blocked"),
    releaseReady: checks.every((item) => item.status !== "blocked"),
    blockingCount,
    checks,
  };
}

function releaseCheckItem(id, stage, title, ok, reason) {
  return {
    id,
    stage,
    title,
    status: ok ? "pass" : "blocked",
    reason: ok ? "" : reason,
  };
}

function localBuildLockMatchesBuild(build) {
  const lock = build?.buildLock;
  if (!lock?.buildId) return false;
  if (lock.target && lock.target !== build.target) return false;
  if (lock.profile && lock.profile !== build.profile) return false;
  if (lock.version && lock.version !== build.version) return false;
  return true;
}

function requiredComponentsLocked(components) {
  for (const component of components) {
    if (component.required && !component.uri) {
      return false;
    }
  }
  return true;
}

function componentURIsExact(components) {
  for (const component of components) {
    if (String(component.uri || "").includes("*")) {
      return false;
    }
  }
  return true;
}

function componentVersionsFixed(components) {
  for (const component of components) {
    if (component.version === "latest") {
      return false;
    }
  }
  return true;
}

function getReleaseReadiness(build) {
  if (build?.releaseCheckSummary) {
    const summary = build.releaseCheckSummary;
    return {
      ready: summary.releaseReady === true,
      reason: releaseReasonText(summary.firstBlockedReason || summary.firstBlockedTitle || ""),
      blockingCount: summary.blockingCount || 0,
    };
  }
  const check = getLocalReleaseCheck(build);
  const firstBlocked = check.checks.find((item) => item.status === "blocked");
  return {
    ready: check.releaseReady,
    reason: releaseReasonText(firstBlocked?.reason || firstBlocked?.title || ""),
    blockingCount: check.blockingCount || 0,
  };
}

function normalizeError(error) {
  if (error?.name === "ApiError") {
    const details = (error.details || []).map((detail) => translateErrorDetail(detail));
    const message = translateError(error);
    const tone = error.severity === "warning" ? "warning" : "error";
    return {
      message,
      details,
      userAction: translateUserAction(error),
      color: error.severity === "warning" ? "amber" : "red",
      tone,
      surface: errorSurface(error),
    };
  }
  return {
    message: typeof error === "string" ? error : error?.message || "操作失败",
    details: [],
    userAction: "",
    color: "red",
    tone: "error",
    surface: "alert",
  };
}

function releaseReasonText(reason) {
  return translateLegacyReason(reason);
}

function MetricCard({ icon: Icon, label, value, hint, color }) {
  return (
    <Card className="metric-card">
      <Flex gap="3" align="center">
        <div className={`metric-icon tone-${color}`}>
          <Icon size={20} />
        </div>
        <Box>
          <Text as="div" size="1" color="gray">
            {label}
          </Text>
          <Text as="div" size="7" weight="bold">
            {value}
          </Text>
          <Text as="div" size="1" color="gray">
            {hint}
          </Text>
        </Box>
      </Flex>
    </Card>
  );
}

function Panel({ title, description, icon: Icon, action, children }) {
  return (
    <Card className="admin-panel">
      <Flex className="panel-header" justify="between" align="center" gap="4">
        <Flex align="center" gap="3" minWidth="0">
          {Icon && (
            <div className="panel-icon">
              <Icon size={18} />
            </div>
          )}
          <Box>
            <Heading as="h2" size="3">
              {title}
            </Heading>
            {description && (
              <Text as="div" size="2" color="gray">
                {description}
              </Text>
            )}
          </Box>
        </Flex>
        {action && <Box className="panel-action">{action}</Box>}
      </Flex>
      <Separator size="4" />
      <Box className="panel-body">{children}</Box>
    </Card>
  );
}

function StatusBadge({ status }) {
  const meta = statusMeta[status] || { label: status || "未知", color: "gray" };
  return (
    <Badge color={meta.color} variant="soft">
      {meta.label}
    </Badge>
  );
}

function DetailItem({ label, value, mono, wide }) {
  return (
    <div className={`detail-item ${wide ? "wide" : ""}`}>
      <Text size="1" color="gray">
        {label}
      </Text>
      <Text size="2" weight="medium" className={mono ? "mono-text" : ""}>
        {value}
      </Text>
    </div>
  );
}

function AssetLine({ label, value }) {
  return (
    <Flex justify="between" gap="3" className="asset-line">
      <Text color="gray">{label}</Text>
      <Text weight="medium">{value}</Text>
    </Flex>
  );
}

function EmptyState({ icon: Icon, title, text }) {
  return (
    <div className="empty-state">
      <Icon size={24} />
      <Text weight="bold">{title}</Text>
      <Text size="2" color="gray">
        {text}
      </Text>
    </div>
  );
}

function ValidationPanel({ result }) {
  return (
    <Callout.Root color={result.valid ? "green" : "red"} variant="soft">
      <Callout.Icon>
        {result.valid ? <Check size={18} /> : <AlertCircle size={18} />}
      </Callout.Icon>
      <Callout.Text>
        {result.valid ? "Manifest 校验通过。" : result.errors.join("；")}
      </Callout.Text>
    </Callout.Root>
  );
}

function DiffPanel({ lines, emptyText = "没有差异。" }) {
  const changed = lines.filter((line) => line.type !== "same");
  if (changed.length === 0) {
    return <EmptyState icon={GitCompare} title="无差异" text={emptyText} />;
  }
  return (
    <pre className="diff-block">
      {lines.map((line, index) => (
        <span key={`${line.type}-${index}`} className={`diff-line ${line.type}`}>
          {line.prefix} {line.text}
        </span>
      ))}
    </pre>
  );
}

function ConfirmActionDialog({ action, onCancel, onConfirm }) {
  return (
    <Dialog.Root open={Boolean(action)} onOpenChange={(open) => !open && onCancel()}>
      <Dialog.Content className="confirm-dialog">
        <Dialog.Title>确认{action?.label}</Dialog.Title>
        <Dialog.Description>
          {action?.description || "请确认是否继续执行该操作。"}
        </Dialog.Description>
        {action?.build && (
          <Box mt="3">
            <Code>{action.build.productId}</Code>{" "}
            <Code>{action.build.id}</Code>
          </Box>
        )}
        <Flex mt="4" gap="3" justify="end">
          <Dialog.Close>
            <Button variant="soft" onClick={onCancel}>
              返回
            </Button>
          </Dialog.Close>
          <Button color="red" onClick={onConfirm}>
            确认{action?.label}
          </Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  );
}

function JsonBlock({ value, compact }) {
  return (
    <pre className={`json-block ${compact ? "is-compact" : ""}`}>
      {JSON.stringify(value || {}, null, 2)}
    </pre>
  );
}

function FieldSelect({ label, value, onValueChange, options }) {
  const safeOptions = options.length ? options : [{ value: "-", label: "-" }];
  const safeValue = value || safeOptions[0].value;
  return (
    <label className="field">
      <Text size="2" weight="medium">
        {label}
      </Text>
      <Select.Root value={safeValue} onValueChange={onValueChange}>
        <Select.Trigger />
        <Select.Content>
          {safeOptions.map((option) => (
            <Select.Item key={option.value} value={option.value}>
              {option.label}
            </Select.Item>
          ))}
        </Select.Content>
      </Select.Root>
    </label>
  );
}

function LabeledInput({ label, value, onChange, type = "text", placeholder }) {
  return (
    <label className="field">
      <Text size="2" weight="medium">
        {label}
      </Text>
      <TextField.Root
        type={type}
        value={value}
        placeholder={placeholder}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}

function shortId(id) {
  if (!id) return "-";
  return String(id).slice(0, 12);
}

function shortHash(hash) {
  if (!hash) return "-";
  return hash.length > 18 ? `${hash.slice(0, 18)}...` : hash;
}

function dateValue(value) {
  const time = new Date(value || 0).getTime();
  return Number.isNaN(time) ? 0 : time;
}

function formatDate(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString("zh-CN", { hour12: false });
}

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function downloadPolicyText(policy) {
  const labels = {
    platform: "平台内",
    restricted: "受限",
    public: "公开",
  };
  return labels[policy] || policy || "平台内";
}

function retentionText(upload) {
  if (upload.retentionStatus === "permanent") return "永久";
  if (upload.retentionStatus === "expired") return "已过期";
  if (upload.expiresAt) return `至 ${formatDate(upload.expiresAt)}`;
  return `${upload.retentionDays || 30} 天`;
}

function retentionColor(status) {
  if (status === "expired") return "red";
  if (status === "permanent") return "green";
  return "teal";
}

function uploadMetadataText(metadata) {
  const entries = Object.entries(metadata || {});
  if (!entries.length) return "-";
  return entries.map(([key, value]) => `${key}=${value}`).join(" / ");
}

function runnerStatusText(status) {
  const labels = {
    busy: "忙碌",
    online: "在线",
    idle: "空闲",
    stale: "心跳超时",
    offline: "离线",
    unknown: "未知",
  };
  return labels[status] || status || "未知";
}

function runnerStatusColor(status) {
  if (status === "busy") return "blue";
  if (status === "online" || status === "idle") return "green";
  if (status === "stale" || status === "unknown") return "amber";
  return "red";
}

function splitCSV(value) {
  return String(value || "")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function validateProductManifest(manifest) {
  const errors = [];
  const required = productManifestSchema.required || [];
  for (const key of required) {
    if (manifest?.[key] === undefined || manifest?.[key] === null || manifest?.[key] === "") {
      errors.push(`缺少必填字段 ${key}`);
    }
  }
  if (!Array.isArray(manifest?.targets) || manifest.targets.length === 0) {
    errors.push("targets 必须至少包含 linux 或 windows");
  } else {
    const targetEnum = productManifestSchema.properties.targets.items.enum || ["linux", "windows"];
    const invalidTargets = manifest.targets.filter((target) => !targetEnum.includes(target));
    if (invalidTargets.length) {
      errors.push(`targets 包含不支持的目标：${invalidTargets.join(", ")}`);
    }
    manifest.targets.forEach((target) => {
      if (!manifest.targetConfig?.[target]?.installDir) {
        errors.push(`targetConfig.${target}.installDir 缺失`);
      }
      if (!manifest.targetConfig?.[target]?.packageName) {
        errors.push(`targetConfig.${target}.packageName 缺失`);
      }
    });
  }
  if (!Array.isArray(manifest?.components) && !manifest?.artifacts) {
    errors.push("components 必须是数组");
  } else if (Array.isArray(manifest?.components)) {
    const sourceEnum =
      productManifestSchema.properties.components.items.properties.source.properties.kind.enum ||
      Object.keys(sourceKindLabels);
    const componentIds = new Set();
    manifest.components.forEach((component, index) => {
      if (!component.id) errors.push(`components[${index}] 缺少 id`);
      if (component.id && componentIds.has(component.id)) errors.push(`components[${index}] id 重复：${component.id}`);
      if (component.id) componentIds.add(component.id);
      if (!component.type) errors.push(`components[${index}] 缺少 type`);
      if (!component.source?.kind) {
        errors.push(`components[${index}] 缺少 source.kind`);
      } else if (!sourceEnum.includes(component.source.kind)) {
        errors.push(`components[${index}] source.kind 不支持：${component.source.kind}`);
      } else if (component.source.kind === "artifact-repository" && !component.source.path) {
        errors.push(`components[${index}] artifact-repository 需要 path`);
      } else if (component.source.kind === "fixed-url" && !component.source.url) {
        errors.push(`components[${index}] fixed-url 需要 url`);
      } else if (component.source.kind === "previous-build" && !component.source.uri) {
        errors.push(`components[${index}] previous-build 需要 uri`);
      }
    });
  }
  if (manifest?.workloads && !Array.isArray(manifest.workloads)) {
    errors.push("workloads 必须是数组");
  }
  if (Array.isArray(manifest?.workloads)) {
    const componentIds = new Set((manifest.components || []).map((component) => component.id));
    manifest.workloads.forEach((workload, index) => {
      if (!workload.id) errors.push(`workloads[${index}] 缺少 id`);
      const runtimeEnum = productManifestSchema.properties.workloads.items.properties.runtime.enum || ["docker", "systemd"];
      if (!runtimeEnum.includes(workload.runtime)) {
        errors.push(`workloads[${index}] runtime 必须是 docker 或 systemd`);
      }
      if (workload.component && componentIds.size > 0 && !componentIds.has(workload.component)) {
        errors.push(`workloads[${index}] 引用了未知组件：${workload.component}`);
      }
      if (workload.runtime === "systemd" && !workload.systemd) {
        errors.push(`workloads[${index}] systemd runtime 需要 systemd 配置`);
      }
    });
  }
  return { valid: errors.length === 0, errors };
}

function diffLines(before = "", after = "") {
  if (before === after) {
    return [{ type: "same", prefix: " ", text: "内容一致" }];
  }
  const left = String(before || "").split(/\r?\n/);
  const right = String(after || "").split(/\r?\n/);
  const total = Math.max(left.length, right.length);
  const lines = [];
  for (let index = 0; index < total; index += 1) {
    const oldLine = left[index];
    const newLine = right[index];
    if (oldLine === newLine) {
      lines.push({ type: "same", prefix: " ", text: oldLine ?? "" });
      continue;
    }
    if (oldLine !== undefined) {
      lines.push({ type: "removed", prefix: "-", text: oldLine });
    }
    if (newLine !== undefined) {
      lines.push({ type: "added", prefix: "+", text: newLine });
    }
  }
  return lines;
}

async function copyText(text) {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand("copy");
  textarea.remove();
}

function downloadText(fileName, text) {
  const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = fileName;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}
