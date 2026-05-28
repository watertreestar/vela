import {
  Button,
  Card,
  CardBody,
  CardHeader,
  Checkbox,
  Chip,
  Divider,
  Input,
  Link,
  Select,
  SelectItem,
  Spinner,
  Tab,
  Table,
  TableBody,
  TableCell,
  TableColumn,
  TableHeader,
  TableRow,
  Tabs,
  Textarea,
} from "@heroui/react";
import {
  Boxes,
  Check,
  Download,
  FileJson,
  PackageCheck,
  Play,
  RefreshCcw,
  Rocket,
  ScrollText,
  Server,
  X,
  Ban,
  RotateCcw,
  Upload,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import {
  approveBuild,
  cancelBuild,
  createBuild,
  getBuildLogs,
  getProductManifest,
  listBuilds,
  listProducts,
  publishBuild,
  rejectBuild,
  retryBuild,
  updateProductManifest,
  uploadArtifact,
} from "./lib/api";

const statusColor = {
  pending_approval: "warning",
  queued: "primary",
  running: "secondary",
  succeeded: "success",
  published: "success",
  rejected: "default",
  failed: "danger",
  canceled: "warning",
};

export default function App() {
  const [products, setProducts] = useState([]);
  const [builds, setBuilds] = useState([]);
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [savingManifest, setSavingManifest] = useState(false);
  const [error, setError] = useState("");
  const [selectedBuild, setSelectedBuild] = useState(null);
  const [logs, setLogs] = useState([]);
  const [manifestText, setManifestText] = useState("");
  const [form, setForm] = useState({
    productId: "",
    target: "linux",
    profile: "qa",
    customer: "",
    version: "latest",
    releaseChannel: "qa",
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

  const selectedProduct = useMemo(
    () => products.find((product) => product.id === form.productId),
    [products, form.productId],
  );

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

  async function refresh() {
    setError("");
    setLoading(true);
    try {
      const [nextProducts, nextBuilds] = await Promise.all([
        listProducts(),
        listBuilds(),
      ]);
      setProducts(nextProducts);
      setBuilds(nextBuilds);
      if (!form.productId && nextProducts[0]) {
        chooseProduct(nextProducts[0], false);
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    refresh();
  }, []);

  async function chooseProduct(product, shouldLoadManifest = true) {
    const target = product.targets?.[0] || "linux";
    const defaults = productDefaults(product, target);
    setForm((current) => ({
      ...current,
      productId: product.id,
      target,
      profile: product.profiles?.[0] || "qa",
      version: product.versions?.[0] || "latest",
      releaseChannel: product.profiles?.[0] || "qa",
      ...defaults,
      variablesText: "{}",
      componentOverridesText: "{}",
    }));
    if (shouldLoadManifest) {
      await loadManifest(product.id);
    }
  }

  async function loadManifest(productId = form.productId) {
    if (!productId) return;
    setError("");
    try {
      const manifest = await getProductManifest(productId);
      setManifestText(JSON.stringify(manifest, null, 2));
    } catch (err) {
      setError(err.message);
    }
  }

  async function saveManifest() {
    setError("");
    setSavingManifest(true);
    try {
      const parsed = JSON.parse(manifestText);
      await updateProductManifest(form.productId, parsed);
      await refresh();
    } catch (err) {
      setError(err.message);
    } finally {
      setSavingManifest(false);
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
    setSubmitting(true);
    try {
      const variables = JSON.parse(form.variablesText || "{}");
      const componentOverrides = JSON.parse(form.componentOverridesText || "{}");
      const configOverrides = {
        ...buildConfigOverrides(),
        ...variables,
      };
      await createBuild({
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
      await refresh();
    } catch (err) {
      setError(err.message);
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
    } catch (err) {
      setError(err.message);
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
      setError(err.message);
    }
  }

  function setModule(name, value) {
    setForm({
      ...form,
      modules: {
        ...form.modules,
        [name]: value,
      },
    });
  }

  async function action(fn, build) {
    setError("");
    try {
      await fn(build.id);
      await refresh();
      if (selectedBuild?.id === build.id) {
        await loadLogs(build);
      }
    } catch (err) {
      setError(err.message);
    }
  }

  async function loadLogs(build) {
    setSelectedBuild(build);
    setError("");
    try {
      setLogs(await getBuildLogs(build.id));
    } catch (err) {
      setError(err.message);
    }
  }

  return (
    <main className="min-h-screen">
      <header className="border-b border-slate-200 bg-white">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-md bg-slate-900 text-white">
              <PackageCheck size={21} />
            </div>
            <div>
              <h1 className="text-xl font-semibold tracking-normal">
                Packaging Platform
              </h1>
              <p className="text-sm text-slate-500">
                Products, manifests, approvals, runners, releases
              </p>
            </div>
          </div>
          <Button
            startContent={<RefreshCcw size={16} />}
            variant="flat"
            onPress={refresh}
          >
            Refresh
          </Button>
        </div>
      </header>

      <div className="mx-auto grid max-w-7xl grid-cols-1 gap-5 px-6 py-6 lg:grid-cols-[360px_1fr]">
        <aside className="space-y-5">
          <Card radius="sm" shadow="none" className="border border-slate-200">
            <CardHeader className="flex gap-3">
              <Boxes size={18} />
              <div>
                <h2 className="text-base font-semibold">Products</h2>
                <p className="text-sm text-slate-500">
                  {products.length} packaging definitions
                </p>
              </div>
            </CardHeader>
            <Divider />
            <CardBody className="gap-3">
              {loading ? (
                <Spinner size="sm" />
              ) : (
                products.map((product) => (
                  <button
                    key={product.id}
                    className={`rounded-md border p-3 text-left transition ${
                      product.id === form.productId
                        ? "border-slate-900 bg-slate-50"
                        : "border-slate-200 bg-white hover:bg-slate-50"
                    }`}
                    onClick={() => chooseProduct(product)}
                  >
                    <div className="font-medium">{product.name}</div>
                    <div className="mt-1 text-sm text-slate-500">
                      {product.description}
                    </div>
                    <div className="mt-2 flex flex-wrap gap-2">
                      {product.targets?.map((target) => (
                        <Chip key={target} size="sm" variant="flat">
                          {target}
                        </Chip>
                      ))}
                    </div>
                  </button>
                ))
              )}
            </CardBody>
          </Card>

          <Card radius="sm" shadow="none" className="border border-slate-200">
            <CardHeader className="flex gap-3">
              <Play size={18} />
              <h2 className="text-base font-semibold">Build Parameters</h2>
            </CardHeader>
            <Divider />
            <CardBody>
              <form className="space-y-4" onSubmit={submitBuild}>
                <Select
                  label="Target"
                  selectedKeys={[form.target]}
                  onSelectionChange={(keys) => {
                    const target = Array.from(keys)[0] || "linux";
                    setForm({
                      ...form,
                      target,
                      installDir:
                        selectedProduct?.targetConfig?.[target]?.installDir ||
                        form.installDir,
                    });
                  }}
                >
                  {(selectedProduct?.targets || ["linux"]).map((target) => (
                    <SelectItem key={target}>{target}</SelectItem>
                  ))}
                </Select>
                <Select
                  label="Version"
                  selectedKeys={[form.version]}
                  onSelectionChange={(keys) =>
                    setForm({ ...form, version: Array.from(keys)[0] || "latest" })
                  }
                >
                  {(selectedProduct?.versions || ["latest"]).map((version) => (
                    <SelectItem key={version}>{version}</SelectItem>
                  ))}
                </Select>
                <Select
                  label="Profile"
                  selectedKeys={[form.profile]}
                  onSelectionChange={(keys) =>
                    setForm({ ...form, profile: Array.from(keys)[0] || "qa" })
                  }
                >
                  {(selectedProduct?.profiles || ["qa"]).map((profile) => (
                    <SelectItem key={profile}>{profile}</SelectItem>
                  ))}
                </Select>
                <Input
                  label="Customer"
                  value={form.customer}
                  onValueChange={(customer) => setForm({ ...form, customer })}
                />
                <Input
                  label="Install directory"
                  value={form.installDir}
                  onValueChange={(installDir) =>
                    setForm({ ...form, installDir })
                  }
                />
                <Input
                  label="Web port"
                  type="number"
                  value={form.webPort}
                  onValueChange={(webPort) => setForm({ ...form, webPort })}
                />
                <Input
                  label="Service version"
                  value={form.serviceVersion}
                  onValueChange={(serviceVersion) =>
                    setForm({ ...form, serviceVersion })
                  }
                />
                <Input
                  label="Customer name"
                  value={form.customerName}
                  onValueChange={(customerName) =>
                    setForm({ ...form, customerName })
                  }
                />
                <Input
                  label="License"
                  value={form.license}
                  onValueChange={(license) => setForm({ ...form, license })}
                />
                <Select
                  label="Database password policy"
                  selectedKeys={[form.passwordPolicy]}
                  onSelectionChange={(keys) =>
                    setForm({
                      ...form,
                      passwordPolicy: Array.from(keys)[0] || "strong",
                    })
                  }
                >
                  <SelectItem key="strong">strong</SelectItem>
                  <SelectItem key="relaxed">relaxed</SelectItem>
                  <SelectItem key="custom">custom</SelectItem>
                </Select>
                <Select
                  label="Release channel"
                  selectedKeys={[form.releaseChannel]}
                  onSelectionChange={(keys) =>
                    setForm({
                      ...form,
                      releaseChannel: Array.from(keys)[0] || form.profile,
                    })
                  }
                >
                  {["qa", "prod", "customer"].map((channel) => (
                    <SelectItem key={channel}>{channel}</SelectItem>
                  ))}
                </Select>
                <div className="grid grid-cols-3 gap-2">
                  {Object.entries(form.modules).map(([name, enabled]) => (
                    <Checkbox
                      key={name}
                      isSelected={enabled}
                      onValueChange={(value) => setModule(name, value)}
                    >
                      {name}
                    </Checkbox>
                  ))}
                </div>
                <Textarea
                  label="Build variables JSON"
                  minRows={5}
                  value={form.variablesText}
                  onValueChange={(variablesText) =>
                    setForm({ ...form, variablesText })
                  }
                />
                <Textarea
                  label="Component source overrides JSON"
                  minRows={5}
                  value={form.componentOverridesText}
                  onValueChange={(componentOverridesText) =>
                    setForm({ ...form, componentOverridesText })
                  }
                />
                <ComponentSourceEditor
                  components={selectedProduct?.components || []}
                  overridesText={form.componentOverridesText}
                  onUpdate={updateComponentOverride}
                  onUpload={uploadComponent}
                />
                <Button
                  type="submit"
                  color="primary"
                  startContent={<Server size={16} />}
                  isLoading={submitting}
                  isDisabled={!form.productId}
                >
                  Request Build
                </Button>
              </form>
            </CardBody>
          </Card>
        </aside>

        <section className="space-y-5">
          {error && (
            <div className="rounded-md border border-red-200 bg-red-50 p-3 text-sm text-red-700">
              {error}
            </div>
          )}

          <Tabs aria-label="Packaging sections" radius="sm">
            <Tab
              key="builds"
              title={
                <span className="flex items-center gap-2">
                  <Rocket size={16} /> Builds
                </span>
              }
            >
              <BuildsTable
                builds={builds}
                loading={loading}
                onApprove={(build) => action(approveBuild, build)}
                onReject={(build) => action(rejectBuild, build)}
                onPublish={(build) => action(publishBuild, build)}
                onCancel={(build) => action(cancelBuild, build)}
                onRetry={(build) => action(retryBuild, build)}
                onLogs={loadLogs}
              />
            </Tab>
            <Tab
              key="manifest"
              title={
                <span className="flex items-center gap-2">
                  <FileJson size={16} /> Manifest
                </span>
              }
            >
              <Card radius="sm" shadow="none" className="border border-slate-200">
                <CardHeader className="flex justify-between">
                  <div>
                    <h2 className="text-base font-semibold">Manifest Config</h2>
                    <p className="text-sm text-slate-500">
                      Edit product manifest and defaults
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <Button variant="flat" onPress={() => loadManifest()}>
                      Load
                    </Button>
                    <Button
                      color="primary"
                      isLoading={savingManifest}
                      onPress={saveManifest}
                    >
                      Save
                    </Button>
                  </div>
                </CardHeader>
                <Divider />
                <CardBody>
                  <Textarea
                    minRows={20}
                    value={manifestText}
                    onValueChange={setManifestText}
                    classNames={{
                      input: "font-mono text-xs",
                    }}
                  />
                </CardBody>
              </Card>
            </Tab>
            <Tab
              key="logs"
              title={
                <span className="flex items-center gap-2">
                  <ScrollText size={16} /> Logs
                </span>
              }
            >
              <LogsPanel build={selectedBuild} logs={logs} />
            </Tab>
          </Tabs>
        </section>
      </div>
    </main>
  );
}

function ComponentSourceEditor({ components, overridesText, onUpdate, onUpload }) {
  let overrides = {};
  try {
    overrides = JSON.parse(overridesText || "{}");
  } catch {
    overrides = {};
  }

  if (components.length === 0) {
    return null;
  }

  return (
    <div className="space-y-3 rounded-md border border-slate-200 p-3">
      <div className="text-sm font-medium">Component Sources</div>
      {components.map((component) => {
        const override = overrides[component.id] || {};
        const source = { ...component.source, ...override };
        return (
          <div key={component.id} className="space-y-2 border-t border-slate-100 pt-3">
            <div className="flex items-center justify-between gap-2">
              <div>
                <div className="text-sm font-medium">{component.id}</div>
                <div className="text-xs text-slate-500">{component.type}</div>
              </div>
              <Chip size="sm" variant="flat">
                {component.required ? "required" : "optional"}
              </Chip>
            </div>
            <Select
              label="Source kind"
              size="sm"
              selectedKeys={[source.kind || "artifact-repository"]}
              onSelectionChange={(keys) =>
                onUpdate(component.id, { ...source, kind: Array.from(keys)[0] })
              }
            >
              <SelectItem key="artifact-repository">artifact-repository</SelectItem>
              <SelectItem key="manual-upload">manual-upload</SelectItem>
              <SelectItem key="fixed-url">fixed-url</SelectItem>
              <SelectItem key="previous-build">previous-build</SelectItem>
            </Select>
            {(source.kind || "artifact-repository") === "artifact-repository" && (
              <Input
                size="sm"
                label="Repository path"
                value={source.path || ""}
                onValueChange={(value) =>
                  onUpdate(component.id, {
                    kind: "artifact-repository",
                    repository: source.repository || "minio",
                    path: value,
                  })
                }
              />
            )}
            {source.kind === "fixed-url" && (
              <Input
                size="sm"
                label="URL"
                value={source.url || ""}
                onValueChange={(value) =>
                  onUpdate(component.id, { kind: "fixed-url", url: value })
                }
              />
            )}
            {source.kind === "previous-build" && (
              <Input
                size="sm"
                label="Artifact URI"
                value={source.uri || ""}
                onValueChange={(value) =>
                  onUpdate(component.id, { kind: "previous-build", uri: value })
                }
              />
            )}
            {source.kind === "manual-upload" && (
              <Input
                type="file"
                size="sm"
                label="Upload file"
                startContent={<Upload size={14} />}
                onChange={(event) =>
                  onUpload(component.id, event.target.files?.[0])
                }
              />
            )}
          </div>
        );
      })}
    </div>
  );
}

function BuildsTable({
  builds,
  loading,
  onApprove,
  onReject,
  onPublish,
  onCancel,
  onRetry,
  onLogs,
}) {
  return (
    <Card radius="sm" shadow="none" className="border border-slate-200">
      <CardHeader>
        <h2 className="text-base font-semibold">Build Records</h2>
      </CardHeader>
      <Divider />
      <CardBody>
        <Table removeWrapper aria-label="Build records">
          <TableHeader>
            <TableColumn>ID</TableColumn>
            <TableColumn>Product</TableColumn>
            <TableColumn>Target</TableColumn>
            <TableColumn>Version</TableColumn>
            <TableColumn>Status</TableColumn>
            <TableColumn>Download</TableColumn>
            <TableColumn>Actions</TableColumn>
          </TableHeader>
          <TableBody emptyContent={loading ? "Loading builds" : "No builds yet"}>
            {builds.map((build) => (
              <TableRow key={build.id}>
                <TableCell className="font-mono text-xs">{build.id}</TableCell>
                <TableCell>{build.productId}</TableCell>
                <TableCell>{build.target}</TableCell>
                <TableCell>{build.version}</TableCell>
                <TableCell>
                  <Chip
                    color={statusColor[build.status] || "default"}
                    size="sm"
                    variant="flat"
                  >
                    {build.status}
                  </Chip>
                </TableCell>
                <TableCell>
                  {build.downloadUrl ? (
                    <Link href={build.downloadUrl} isExternal size="sm">
                      <Download size={14} /> Download
                    </Link>
                  ) : (
                    <span className="text-slate-400">-</span>
                  )}
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-2">
                    {build.status === "pending_approval" && (
                      <>
                        <Button
                          size="sm"
                          color="success"
                          variant="flat"
                          startContent={<Check size={14} />}
                          onPress={() => onApprove(build)}
                        >
                          Approve
                        </Button>
                        <Button
                          size="sm"
                          color="danger"
                          variant="flat"
                          startContent={<X size={14} />}
                          onPress={() => onReject(build)}
                        >
                          Reject
                        </Button>
                      </>
                    )}
                    {build.status === "succeeded" && (
                      <Button
                        size="sm"
                        color="primary"
                        variant="flat"
                        onPress={() => onPublish(build)}
                      >
                        Publish
                      </Button>
                    )}
                    {["pending_approval", "queued", "running"].includes(
                      build.status,
                    ) && (
                      <Button
                        size="sm"
                        color="warning"
                        variant="flat"
                        startContent={<Ban size={14} />}
                        onPress={() => onCancel(build)}
                      >
                        Cancel
                      </Button>
                    )}
                    {["failed", "canceled"].includes(build.status) && (
                      <Button
                        size="sm"
                        color="secondary"
                        variant="flat"
                        startContent={<RotateCcw size={14} />}
                        onPress={() => onRetry(build)}
                      >
                        Retry
                      </Button>
                    )}
                    <Button
                      size="sm"
                      variant="flat"
                      startContent={<ScrollText size={14} />}
                      onPress={() => onLogs(build)}
                    >
                      Logs
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardBody>
    </Card>
  );
}

function LogsPanel({ build, logs }) {
  return (
    <Card radius="sm" shadow="none" className="border border-slate-200">
      <CardHeader>
        <div>
          <h2 className="text-base font-semibold">Build Logs</h2>
          <p className="text-sm text-slate-500">
            {build ? `${build.productId} / ${build.id}` : "Select a build"}
          </p>
        </div>
      </CardHeader>
      <Divider />
      <CardBody>
        {!build ? (
          <div className="text-sm text-slate-500">No build selected.</div>
        ) : (
          <div className="space-y-2">
            {logs.length === 0 ? (
              <div className="text-sm text-slate-500">No logs.</div>
            ) : (
              logs.map((entry, index) => (
                <div
                  key={`${entry.time}-${index}`}
                  className="rounded-md bg-slate-950 p-3 font-mono text-xs text-slate-100"
                >
                  <span className="text-slate-400">
                    {new Date(entry.time).toLocaleString()} [{entry.source}]
                  </span>{" "}
                  {entry.message}
                </div>
              ))
            )}
          </div>
        )}
      </CardBody>
    </Card>
  );
}
