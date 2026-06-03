# Packaging Platform 操作文档

本文档面向使用和验收 Packaging Platform 的产品、构建、发布和运维人员。目标是把“产品 Manifest 管理、Preview 构建、发布门禁、Linux/Windows Runner、测试验证”串成一条可执行流程，方便核对当前实现是否满足需求。

## 1. 系统定位

Packaging Platform 是一个内部安装包构建控制台，用来集中管理产品打包配置，并把实际构建任务交给受控 Runner 执行。

当前系统覆盖的核心目标：

- 管理产品 Manifest、Profile 覆盖、客户覆盖和历史版本。
- 支持 Linux 与 Windows 两类构建目标。
- 支持创建 Preview 安装包。
- Preview 验证通过后，才允许提升为 Release。
- Release 发布必须满足可复现要求，包括固定构建版本、固定组件版本、精确组件 URI、可下载制品和无构建错误。
- Runner 可以本地单次执行，也可以通过 Nomad 调度到 Linux Docker Runner 或 Windows raw_exec Runner。

当前不覆盖的内容：

- 用户认证和权限控制暂不实现。
- 产品真实业务打包脚本仍需要由产品侧提供，例如 Inno Setup、WiX、签名命令、定制 Linux 打包命令。
- Windows 真实 `setup.exe` 需要配置 `WINDOWS_PACKAGER_CMD`，未配置时只生成占位安装包用于控制面联调。

## 2. 角色与职责

建议按以下方式划分使用职责：

| 角色 | 主要操作 |
|---|---|
| 产品配置维护人 | 维护 `products/{productId}/product.json`、Profile、客户覆盖、组件来源和 workload |
| 构建申请人 | 在管理后台创建 Preview 构建，选择目标平台、版本、环境、客户和组件来源 |
| 审批/发布人员 | 审批构建、检查构建结果、执行 Preview 验证、确认门禁后发布 Release |
| Runner 运维人员 | 部署 Linux/Windows Runner、维护 Nomad 节点、配置缓存、制品仓库和打包工具链 |
| 测试人员 | 执行 API 自动化测试、前端单测、E2E UI 测试和构建验证 |

## 3. 系统组成

主要目录：

```text
backend/              Go API 服务
frontend/             React + Radix 管理后台
products/             产品 Manifest、Profile 覆盖、客户覆盖
runner/               Bun Runner，负责领取任务和执行打包
infra/nomad/          Nomad Linux/Windows 节点配置
infra/windows/        Windows Runner 准备和启动脚本
docs/                 架构、API、Runner 生命周期和本文档
lsapctl/              Linux 包生命周期管理工具
```

运行时组件：

```text
管理后台 -> Go API -> PostgreSQL/JSON Store
                  -> 产品 Manifest 文件
                  -> Nomad
                  -> MinIO 或本地制品存储

Nomad -> Linux Runner: Docker + Bun + Dagger
Nomad -> Windows Runner: raw_exec + Bun + Inno Setup/WiX/SignTool
```

## 4. 本地启动

### 4.1 启动 PostgreSQL

默认使用 PostgreSQL：

```bash
docker run -d --name packaging-postgres \
  -e POSTGRES_USER=packaging \
  -e POSTGRES_PASSWORD=packaging \
  -e POSTGRES_DB=packaging \
  -p 5432:5432 postgres:16
```

默认数据库连接：

```text
postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable
```

### 4.2 启动后端

```bash
cd backend
go run ./cmd/server
```

常用环境变量：

```bash
PORT=8080
STORE_DRIVER=postgres
DATABASE_URL=postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable
PRODUCTS_DIR=../products
DRY_RUN=true
```

说明：

- `DRY_RUN=true` 时，审批构建不会真正提交 Nomad，只会进入队列，便于本地调试。
- 如果要接入真实 Nomad，需要设置 `DRY_RUN=false`、`NOMAD_ADDR`、`PACKAGING_RUNNER_IMAGE`、`PACKAGING_API_URL` 等变量。

### 4.3 启动前端

```bash
cd frontend
npm install
npm run dev
```

前端默认访问：

```text
http://localhost:5173
```

前端 API 地址：

```bash
VITE_API_URL=http://localhost:8080
```

## 5. 管理后台页面说明

当前管理后台是 Dashboard Admin 风格，主要页面如下：

| 页面 | 用途 |
|---|---|
| 总览 | 查看活跃任务、成功率、可发布 Release、发布阻塞、最近构建和平台资产 |
| 创建构建 | 选择产品、target、profile、customer、version、组件来源和配置覆盖，提交构建申请 |
| 构建记录 | 审批、拒绝、取消、重试、验证 Preview、发布 Release、查看详情 |
| Manifest | 编辑产品 Manifest，执行结构化编辑、校验、功能检查、Dry Run、历史恢复和差异查看 |
| 覆盖配置 | 管理 Profile 覆盖和客户覆盖 |
| 制品上传 | 上传手动制品，供组件选择 `manual-upload` 时使用 |
| 构建详情 | 查看概要、最终配置、组件锁定、workloads、发布门禁、审计、Build Lock 和日志 |

操作建议：

1. 先在 Manifest 页面维护产品定义。
2. 使用 Dry Run 检查 Manifest 能否生成 Build Lock。
3. 在创建构建页面创建 Preview。
4. 构建成功后进入构建详情检查日志、制品、Build Lock 和发布门禁。
5. Preview 验证通过后，再尝试 Release 发布。

## 6. 产品 Manifest 管理

产品 Manifest 默认存放在：

```text
products/{productId}/product.json
```

示例：

```text
products/lsap/product.json
```

Manifest 主要字段：

| 字段 | 说明 |
|---|---|
| `id` | 产品 ID，和目录名保持一致 |
| `name` | 产品中文名称 |
| `description` | 产品说明 |
| `targets` | 支持的构建目标，只允许 `linux`、`windows` |
| `targetConfig` | 每个 target 的安装目录、包名、模板和目标默认配置 |
| `profiles` | 环境列表，例如 `qa`、`prod` |
| `versions` | 可选版本，例如 `latest`、`1.0.0` |
| `components` | 构建输入组件列表 |
| `workloads` | 安装后运行负载，例如 Docker 容器或 systemd 服务 |
| `config` | 产品默认配置，例如端口、客户信息、模块开关 |

### 6.1 target 配置

Linux 示例：

```json
{
  "installDir": "/opt/lsap",
  "packageName": "lsap-linux-x86_64",
  "templates": {
    "compose": "linux/compose.template.yml",
    "manifest": "linux/manifest.template.json"
  },
  "config": {
    "runtime": "docker-compose"
  }
}
```

Windows 示例：

```json
{
  "installDir": "C:\\Program Files\\LSAP",
  "packageName": "lsap-windows-x64-setup",
  "templates": {
    "installer": "windows/setup.template.iss"
  },
  "config": {
    "installer": "inno"
  }
}
```

功能检查会关注：

- `targets` 是否同时覆盖 Linux/Windows。
- 每个 target 是否存在 `targetConfig`。
- 每个 target 是否配置 `installDir` 和 `packageName`。

### 6.2 组件来源

组件定义在 `components` 中。当前支持四类来源：

| kind | 用途 |
|---|---|
| `artifact-repository` | 从制品仓库读取，例如 MinIO 路径 |
| `manual-upload` | 使用后台上传的文件 |
| `fixed-url` | 使用固定 URL 或对象地址 |
| `previous-build` | 使用历史构建输出 |

制品仓库组件示例：

```json
{
  "id": "location-guard",
  "type": "docker-image",
  "required": true,
  "source": {
    "kind": "artifact-repository",
    "repository": "minio",
    "path": "artifact/Publish/location-guard/main/{{version}}/location-guard-*.tar.gz"
  }
}
```

手动上传组件示例：

```json
{
  "id": "license",
  "type": "license",
  "required": false,
  "source": {
    "kind": "manual-upload",
    "accept": [".lic", ".json"]
  }
}
```

Release 可复现要求：

- Release 不允许组件版本仍是 `latest`。
- Release 不允许组件 URI 包含 `*` 通配符。
- 必需组件必须在 Build Lock 中锁定具体 URI。
- Preview 阶段可以使用 `latest` 和通配符做验证，但不能直接发布为 Release。

### 6.3 workload 配置

`workloads` 描述安装后服务如何运行。

Docker workload 示例：

```json
{
  "id": "location-guard",
  "runtime": "docker",
  "component": "location-guard",
  "module": "map",
  "docker": {
    "image": "location-guard:{{version}}",
    "containerName": "lsap-location-guard",
    "ports": ["{{ports.web}}:8080"],
    "volumes": [
      "{{installDir}}/conf/location-guard:/app/conf",
      "{{installDir}}/logs/location-guard:/app/logs"
    ],
    "environment": {
      "CUSTOMER_NAME": "{{customer.name}}"
    },
    "restart": "unless-stopped"
  }
}
```

systemd workload 示例：

```json
{
  "id": "location-worker",
  "runtime": "systemd",
  "component": "location-worker",
  "module": "camera",
  "systemd": {
    "unitName": "lsap-location-worker.service",
    "workingDirectory": "{{installDir}}/services/location-worker",
    "execStart": "{{installDir}}/services/location-worker/location-worker --config {{installDir}}/conf/location-worker.yaml",
    "restart": "always"
  }
}
```

校验规则：

- `runtime` 只能是 `docker` 或 `systemd`。
- workload 引用的 `component` 必须存在。
- workload 可以通过 `module` 和 `config.modules` 联动开关。

### 6.4 Profile 和客户覆盖

Profile 覆盖：

```text
products/{productId}/profiles/{profile}.json
```

客户覆盖：

```text
products/{productId}/customers/{customer}.json
```

配置合并优先级：

```text
构建表单覆盖 > 构建变量 > 客户覆盖 > Profile 覆盖 > target 默认配置 > 产品默认配置
```

建议：

- `qa` 或 `preview` 用于 Preview 验证。
- `prod` 或 `release` 用于 Release 候选。
- 客户差异尽量放在客户覆盖里，不要复制整份 Manifest。

### 6.5 Manifest 编辑流程

在管理后台进入 Manifest 页面：

1. 选择产品。
2. 点击“加载”读取当前 Manifest。
3. 在“编辑”页签修改 JSON，或在“结构化”页签修改常见字段。
4. 在“校验”页签查看 schema 和业务校验结果。
5. 在“功能检查”页签确认 Linux/Windows、Preview、Release 可复现能力。
6. 点击“Dry Run”用当前构建参数解析 Manifest，检查 Build Lock 预览。
7. 确认无误后点击“保存 Manifest”。
8. 如果保存后发现问题，可在“历史”页签恢复到旧 revision。

Manifest API：

```http
GET /api/products/{id}/manifest
PUT /api/products/{id}/manifest
POST /api/products/{id}/manifest/dry-run
GET /api/products/{id}/manifest/revisions
POST /api/products/{id}/manifest/revisions/{revision}/restore
```

Dry Run 请求示例：

```json
{
  "manifest": {},
  "target": "linux",
  "profile": "qa",
  "customer": "example-customer",
  "version": "1.0.0",
  "releaseChannel": "preview",
  "configOverrides": {},
  "componentOverrides": {},
  "variables": {}
}
```

Dry Run 只验证和解析，不创建构建记录。

## 7. 创建 Preview 构建

在“创建构建”页面填写：

| 字段 | 说明 |
|---|---|
| 产品 | 选择产品 Manifest |
| 构建目标 | `linux` 或 `windows` |
| Profile | 例如 `qa`、`prod` |
| 客户 | 可选客户覆盖 |
| 版本 | Preview 可以用 `latest` 或固定版本；Release 必须最终使用固定版本 |
| 构建通道 | 当前建议直接创建 `preview` |
| 配置覆盖 | 安装目录、端口、客户名、license、密码策略、模块开关 |
| 组件来源 | 仓库、上传文件、固定 URL、历史构建 |
| 构建变量 | 参与解析的动态变量 |

创建构建 API：

```http
POST /api/builds
```

请求示例：

```json
{
  "productId": "lsap",
  "target": "linux",
  "profile": "qa",
  "customer": "example-customer",
  "version": "1.0.0",
  "releaseChannel": "preview",
  "configOverrides": {
    "installDir": "/opt/customer-lsap",
    "ports": {
      "web": 49000
    },
    "customer": {
      "name": "Customer A",
      "license": "enterprise"
    },
    "database": {
      "passwordPolicy": "strong"
    },
    "modules": {
      "camera": false,
      "push": true
    }
  },
  "componentOverrides": {
    "ui": {
      "kind": "fixed-url",
      "url": "https://artifacts.example.com/ui/dist.zip"
    }
  },
  "variables": {
    "defaultPort": "48000"
  }
}
```

创建成功后状态为：

```text
pending_approval
```

此时系统已经生成 Build Lock、最终配置和组件锁定信息，但 Runner 还不会执行。

## 8. 审批和构建执行

构建状态流转：

```text
pending_approval -> queued -> running -> succeeded -> preview_validated -> published
pending_approval -> rejected
running -> failed
pending_approval|queued|running -> canceled
failed|canceled -> queued
```

操作说明：

| 操作 | 前置状态 | 结果 |
|---|---|---|
| 审批通过 | `pending_approval` | 进入 `queued`，并在真实模式下提交 Nomad job |
| 拒绝 | `pending_approval` | 进入 `rejected` |
| 取消 | `pending_approval`、`queued`、`running` | 进入 `canceled`，真实模式下会注销 Nomad job |
| 重试 | `failed`、`canceled` | 重新进入 `queued` |
| Runner claim | `queued` | 进入 `running` |
| Runner complete | `running` | 成功进入 `succeeded`，失败进入 `failed` |

审批 API：

```http
POST /api/builds/{id}/approve
POST /api/builds/{id}/reject
POST /api/builds/{id}/cancel
POST /api/builds/{id}/retry
```

Runner API：

```http
POST /api/runner/tasks/claim
POST /api/runner/heartbeat
POST /api/builds/{id}/logs
POST /api/runner/tasks/{id}/complete
```

Runner 领取任务请求：

```json
{
  "runnerId": "linux-builder-01",
  "target": "linux"
}
```

Runner 完成任务请求：

```json
{
  "status": "succeeded",
  "artifactUri": "minio://packaging-artifacts/products/lsap/qa/1.0.0/linux/build-id",
  "downloadUrl": "https://artifact.example.com/lsap/1.0.0/linux.tar.gz"
}
```

## 9. Preview 验证

Preview 构建成功后，发布人员必须先执行 Preview 验证。

Preview 验证 API：

```http
POST /api/builds/{id}/validate-preview
```

Preview 门禁要求：

| 检查项 | 要求 |
|---|---|
| Preview 构建已成功 | 构建状态必须是 `succeeded`、`preview_validated` 或 `published` |
| 构建通道为 Preview | `releaseChannel` 必须是 `preview` |
| Preview 制品可下载 | 必须存在 `artifactUri` 或 `downloadUrl` |
| Build Lock 已生成 | 必须存在 Build Lock |
| Build Lock 与构建参数一致 | target、profile、version 必须匹配 |
| 必需组件已锁定 URI | `required=true` 的组件必须有具体 URI |

验证通过后状态变为：

```text
preview_validated
```

如果 Preview 门禁未通过，系统会返回阻塞原因，管理后台“发布门禁”页签也会展示具体失败项。

## 10. Release 发布

Release 发布不是重新构建，而是把已经验证过的 Preview 提升为 Release。

Release 发布 API：

```http
POST /api/builds/{id}/publish
```

Release 发布前置条件：

| 检查项 | 要求 |
|---|---|
| Preview 已验证 | 状态必须是 `preview_validated` 或已发布 |
| 构建版本固定 | `version` 不能是 `latest` |
| 组件 URI 精确 | 组件 URI 不能包含 `*` |
| 组件版本固定 | 组件版本不能是 `latest` |
| Release 制品可下载 | 必须存在 `artifactUri` 或 `downloadUrl` |
| 构建无错误记录 | `error` 为空 |

发布成功后：

```text
status = published
releaseChannel = release
publishedAt = 当前时间
```

### 10.1 发布门禁查询

管理后台和后端动作共用同一个门禁接口：

```http
GET /api/builds/{id}/release-check
```

响应示例：

```json
{
  "buildId": "build-id",
  "stage": "preview",
  "previewReady": true,
  "releaseReady": false,
  "blockingCount": 1,
  "checks": [
    {
      "id": "release.fixed-version",
      "stage": "release",
      "title": "构建版本固定",
      "status": "blocked",
      "reason": "release requires a fixed build version"
    }
  ]
}
```

操作建议：

- Preview 可以使用 `latest` 进行快速验证。
- 要发布 Release，必须重新创建或确保构建使用固定版本，例如 `1.0.0`。
- 组件仓库路径如果包含 `*`，需要在构建解析时最终锁定到精确 URI。
- 如果门禁显示组件 URI 仍包含通配符，不能强行发布，应先修复组件来源或解析逻辑。

## 11. 构建详情与审计

在构建记录中点击详情，可以查看：

| 页签 | 内容 |
|---|---|
| 概要 | 构建 ID、产品、target、profile、version、状态、runner、制品地址、时间 |
| 最终配置 | 合并后的配置结果 |
| 组件锁定 | 每个组件的来源、版本、URI、required 状态 |
| Workloads | 解析后的 Docker/systemd 运行负载 |
| 发布门禁 | Preview 与 Release 的检查结果 |
| 审计 | Build Lock 的关键摘要 |
| Build Lock | 完整 Build Lock JSON |
| 日志 | 系统日志和 Runner 日志 |

Build Lock 是可复现构建的关键证据，应该用于追踪：

- 使用了哪个产品 Manifest。
- 使用了哪个 target、profile、customer 和 version。
- 每个组件解析到了哪个固定 URI。
- 最终配置和 workload 是否符合预期。

## 12. 制品上传

后台支持手动上传制品：

```http
POST /api/uploads
GET /api/uploads
GET /api/uploads/{id}/download
```

上传接口使用 multipart 字段：

```text
file
```

返回示例：

```json
{
  "id": "upload-id",
  "fileName": "license.lic",
  "uri": "local-upload://upload-id/license.lic",
  "downloadUrl": "http://127.0.0.1:8080/api/uploads/upload-id/download",
  "sha256": "..."
}
```

使用方式：

1. 在“制品上传”页面上传文件。
2. 在“创建构建”的组件来源中选择 `manual-upload`。
3. 选择刚上传的文件。
4. 提交构建后，该 URI 会写入 Build Lock。

## 13. Runner 部署和执行

### 13.1 本地单次 Runner

用于调试一个已排队构建：

```bash
cd runner
PACKAGING_API_URL=http://localhost:8080 \
TARGET=linux \
MINIO_PUBLIC_URL=http://localhost:9000 \
bun package-runner.js
```

Windows PowerShell 示例：

```powershell
cd runner
$env:PACKAGING_API_URL="http://localhost:8080"
$env:TARGET="windows"
$env:MINIO_PUBLIC_URL="http://localhost:9000"
bun package-runner.js
```

本地调试流程：

1. 启动 API。
2. 创建 Preview 构建。
3. 审批构建，使状态进入 `queued`。
4. 启动本地 Runner。
5. 检查构建是否从 `queued` 进入 `running`，最终进入 `succeeded` 或 `failed`。
6. 查看构建详情中的日志、制品 URI 和发布门禁。

### 13.2 Linux Nomad Runner

Linux 生产拓扑：

```text
Packaging API -> Nomad server -> Linux Nomad client -> Docker runner image -> Dagger -> API upload endpoint
```

Linux Runner 主机要求：

- 安装 Nomad client。
- 安装 Docker。
- Docker socket 可访问，或配置远程 Dagger engine。
- Runner 能访问 Packaging API 和 MinIO public URL。
- 准备稳定缓存目录。

构建 Runner 镜像：

```bash
cd runner
docker build -t packaging-runner:local .
```

准备缓存：

```bash
sudo mkdir -p /var/cache/packaging/{bun,dagger,artifacts}
sudo chmod -R 0777 /var/cache/packaging
```

启动 Linux Nomad client：

```bash
sudo nomad agent -config infra/nomad/linux-client.hcl
```

API 侧关键变量：

```bash
DRY_RUN=false
NOMAD_ADDR=http://<nomad-server>:4646
PACKAGING_API_URL=http://<api-host>:8080
PACKAGING_RUNNER_IMAGE=packaging-runner:local
PACKAGING_RUNNER_COMMAND='bun /app/package-runner.js'
NOMAD_LINUX_NODE_CLASS=linux-packager
RUNNER_BUN_CACHE_DIR=/var/cache/packaging/bun
RUNNER_DAGGER_CACHE_DIR=/var/cache/packaging/dagger
ARTIFACT_ROOT=/var/cache/packaging/artifacts
DAGGER_DOCKER_SOCKET=/var/run/docker.sock
```

如果 Linux 包需要包含 `lsapctl`：

```bash
LSAPCTL_SOURCE_DIR=/opt/packaging-platform/lsapctl
LSAPCTL_BUILD_CMD='cd /workspace/context/lsapctl-src && GOOS=linux GOARCH=amd64 go build -o "$OUTPUT_DIR/lsapctl" .'
```

验证步骤：

1. `nomad node status` 能看到 Linux 节点。
2. 节点 meta 中 `node_class` 是 `linux-packager`。
3. 创建并审批一个 Linux Preview 构建。
4. `nomad job status <job-id>` 能看到 API 提交的 job。
5. allocation 日志能看到 Runner claim、下载组件、打包、上传。
6. 构建进入 `succeeded`。
7. 管理后台 Preview 门禁通过。

### 13.3 Windows Nomad Runner

Windows 生产拓扑：

```text
Packaging API -> Nomad server -> Windows Nomad client raw_exec -> Bun runner -> Inno Setup/WiX/SignTool -> API upload endpoint
```

Windows Runner 主机要求：

- Windows Server 或专用 Windows 工作站。
- Nomad client，以 Administrator 运行。
- Bun 已安装并在 `PATH` 中。
- 真实 `setup.exe` 输出需要安装 Inno Setup 或 WiX。
- 如需签名，需要 SignTool 和证书。
- Runner 能访问 Packaging API 和 MinIO public URL。

准备 Runner 文件：

```powershell
powershell -ExecutionPolicy Bypass -File infra\windows\prepare-runner.ps1 `
  -RunnerDir C:\packaging-runner `
  -ApiUrl http://<api-host>:8080 `
  -MinioPublicUrl http://<minio-host>:9000 `
  -WindowsPackagerCmd 'iscc C:\packaging-runner\context\rendered\setup.iss'
```

准备缓存：

```powershell
New-Item -ItemType Directory -Force -Path C:\packaging-cache\bun | Out-Null
New-Item -ItemType Directory -Force -Path C:\packaging-cache\artifacts | Out-Null

[Environment]::SetEnvironmentVariable("BUN_INSTALL_CACHE_DIR", "C:\packaging-cache\bun", "Machine")
[Environment]::SetEnvironmentVariable("ARTIFACT_ROOT", "C:\packaging-cache\artifacts", "Machine")
```

启动 Windows Nomad client：

```powershell
powershell -ExecutionPolicy Bypass -File infra\windows\start-nomad-client.ps1 `
  -NomadExe C:\tools\nomad\nomad.exe `
  -ServerAddr <nomad-server-ip>:4647
```

API 侧关键变量：

```bash
DRY_RUN=false
NOMAD_ADDR=http://<nomad-server>:4646
PACKAGING_API_URL=http://<api-host>:8080
NOMAD_WINDOWS_NODE_CLASS=windows-packager
WINDOWS_RUNNER_COMMAND='bun C:\packaging-runner\package-runner.js'
```

真实 Windows 安装包命令示例：

```powershell
[Environment]::SetEnvironmentVariable(
  "WINDOWS_PACKAGER_CMD",
  "iscc C:\packaging-runner\context\rendered\setup.iss",
  "Machine"
)
```

注意：

- 未配置 `WINDOWS_PACKAGER_CMD` 时，Runner 只生成占位 setup 文件。
- 生产发布前必须用真实 Windows 打包工具链跑通 Preview 验证。
- 如果使用签名证书，证书权限必须对 Nomad 运行用户可用。

## 14. 制品存储

系统支持本地存储和 MinIO。

本地存储适合开发测试：

```bash
ARTIFACT_STORE=local
ARTIFACT_ROOT=./data/artifacts
```

MinIO 适合联调或生产：

```bash
ARTIFACT_STORE=minio
MINIO_ENDPOINT=<minio-host>:9000
MINIO_PUBLIC_URL=http://<minio-host>:9000
MINIO_ACCESS_KEY=<access-key>
MINIO_SECRET_KEY=<secret-key>
MINIO_BUCKET=packaging-artifacts
```

`MINIO_PUBLIC_URL` 很重要，Runner 需要通过它下载 `minio://...` 组件。

## 15. 代理和网络

如果构建节点需要代理访问外部制品源，需要在 API 和 Runner 环境中配置：

```bash
HTTP_PROXY=http://proxy.internal:8080
HTTPS_PROXY=http://proxy.internal:8080
NO_PROXY=localhost,127.0.0.1,<api-host>,<minio-host>,<nomad-host>
```

这些变量会被传给 Nomad Runner task。

排查网络问题时优先确认：

- Runner 主机能访问 `PACKAGING_API_URL`。
- Runner 主机能访问 `MINIO_PUBLIC_URL`。
- Runner 主机能解析组件固定 URL。
- `NO_PROXY` 包含 API、MinIO、Nomad 内网地址。

## 16. 自动化测试

### 16.1 后端 API 测试

```bash
cd backend
go test ./... -count=1
```

覆盖范围：

- 产品和 Manifest API。
- Manifest 校验和 Dry Run。
- 构建创建、审批、取消、重试、日志、Runner claim/complete。
- Preview 验证和 Release 发布门禁。
- 上传记录和下载信息。

### 16.2 前端单元测试

```bash
cd frontend
npm test -- --run
```

覆盖范围：

- 管理后台核心渲染。
- 表单和状态展示。
- 门禁和构建数据展示逻辑。

### 16.3 E2E UI 测试

```bash
cd frontend
npm run test:e2e
```

E2E 使用 Playwright 和 mock API 数据，覆盖：

- Dashboard 关键指标。
- 构建记录、发布门禁和详情页。
- 创建构建流程。
- 桌面和移动 Chromium 视口。

### 16.4 前端构建

```bash
cd frontend
npm run build
```

### 16.5 自动化监测

本地或 CI 可以使用 Python 监测脚本做一次性健康检查：

```bash
python scripts/monitor_cli.py check --api-url http://127.0.0.1:8080
```

持续监测：

```bash
python scripts/monitor_cli.py watch \
  --api-url http://127.0.0.1:8080 \
  --interval 30
```

CI 中建议把告警提升为失败：

```bash
python scripts/monitor_cli.py check \
  --api-url http://127.0.0.1:8080 \
  --fail-on-warning
```

如果需要给外部监控系统采集结构化结果：

```bash
python scripts/monitor_cli.py check --json
```

脚本会输出 confirmation lights：

```text
[OK]    可继续
[WARN]  有风险，默认不让命令失败
[FAIL]  接口不可用或契约损坏，命令返回非 0
```

当前监测项：

| 检查项 | 失败条件 | 告警条件 |
|---|---|---|
| API health | `/api/health` 不可达或不是 `ok` | 无 |
| Product manifests | `/api/products` 契约错误 | 未加载任何产品 |
| Build API | `/api/builds` 契约错误 | 队列超过阈值、running build 没有 runner、失败构建超过阈值 |
| Release gate summary | 无 | 存在阻塞门禁 |
| Build log stream | 有日志样本但 SSE 契约错误 | 无 |
| Upload API | `/api/uploads` 契约错误 | 无 |
| Artifact metadata contract | 缺少 `sha256`、`downloadUrl` 或 `downloadPolicy` | 无 |
| Artifact retention | 无 | 过期制品超过阈值 |
| Runner API | `/api/runners` 契约错误 | 无 |
| Runner availability | 无 | stale、offline、unknown runner 超过阈值 |
| Active runner heartbeat | 无 | active runner 心跳或日志超过阈值 |

常用阈值参数：

```bash
python scripts/monitor_cli.py check \
  --max-queued-builds 20 \
  --max-failed-builds 50 \
  --max-expired-artifacts 5 \
  --max-unhealthy-runners 1 \
  --runner-stale-minutes 10
```

监测脚本的单元测试：

```bash
python -m unittest scripts.monitor_cli_test
```

### 16.6 文档和格式检查

```bash
git diff --check
```

## 17. 常见故障处理

### 17.1 Manifest 保存失败

现象：

- 保存时提示 `manifest validation failed`。

检查：

- 是否缺少 `id`、`name`、`targets`、`profiles`、`versions`、`components`、`config`。
- `targets` 是否只包含 `linux` 或 `windows`。
- 每个 target 是否有 `targetConfig`。
- 组件 source 是否有合法 `kind`。
- workload runtime 是否为 `docker` 或 `systemd`。
- workload 引用的 component 是否存在。

### 17.2 Dry Run 不通过

检查：

- 当前选择的 target 是否在 Manifest 中声明。
- 当前 profile/customer 覆盖 JSON 是否有效。
- required 组件是否能解析出 URI。
- 模板变量是否能从 config、variables、targetConfig 中取得。

### 17.3 构建一直停在 pending_approval

说明：

- 构建创建后默认等待审批。

处理：

- 在构建记录页面点击“审批通过”。
- 或调用 `POST /api/builds/{id}/approve`。

### 17.4 构建一直停在 queued

检查：

- 本地调试时是否启动了 Runner。
- 真实模式下 API 是否设置 `DRY_RUN=false`。
- Nomad server 是否可用。
- Nomad client 是否在线。
- target 是否匹配 Runner，例如 Linux 构建不能被 Windows Runner 领取。
- node class 是否匹配 `linux-packager` 或 `windows-packager`。

### 17.5 构建 running 后超时或失败

检查：

- Runner 日志是否能看到心跳。
- Runner 是否能下载组件。
- `MINIO_PUBLIC_URL` 是否可访问。
- `ARTIFACT_ROOT` 是否正确。
- Linux Dagger 是否能访问 Docker socket。
- Windows `WINDOWS_PACKAGER_CMD` 是否存在且能在宿主机运行。
- `RUNNER_TIMEOUT_SECONDS` 是否过短。

### 17.6 Preview 验证被阻塞

检查发布门禁：

- 构建是否成功。
- 构建是否是 preview 通道。
- 是否有 artifact 或 download URL。
- 是否生成 Build Lock。
- Build Lock target/profile/version 是否和构建记录一致。
- required 组件是否都有 URI。

### 17.7 Release 发布被阻塞

常见原因：

- Preview 尚未验证。
- version 仍是 `latest`。
- 组件版本仍是 `latest`。
- 组件 URI 包含 `*`。
- 构建没有可下载制品。
- 构建记录存在 error。

处理建议：

- 使用固定版本重新创建 Preview 构建。
- 确保组件路径最终解析到精确文件。
- 重新跑 Preview，验证通过后再发布 Release。

## 18. 需求核对清单

可用以下清单核对当前实现与需求是否一致：

| 需求 | 当前状态 | 核对方式 |
|---|---|---|
| 管理后台中文化 | 已覆盖 | 前端页面和关键操作文案为中文 |
| Dashboard Admin 风格 | 已覆盖 | 总览指标、构建队列、资产、最近构建、详情抽屉 |
| 产品 Manifest 管理 | 已覆盖 | Manifest 页面支持编辑、结构化、校验、功能检查、Dry Run |
| Manifest 历史恢复 | 已覆盖 | Manifest 历史页签和 revision restore API |
| Profile/客户覆盖 | 已覆盖 | 覆盖配置页面和 overlay API |
| Linux/Windows target | 已覆盖 | Manifest targets、targetConfig、Runner target claim |
| Preview 安装包构建 | 已覆盖 | 创建 preview 构建、审批、Runner 执行、上传制品 |
| Preview 验证后才能 Release | 已强制 | `publish` 只接受 `preview_validated` |
| Release 可复现门禁 | 已强制 | fixed version、fixed component version、exact URI、artifact、no error |
| 发布门禁前后端对齐 | 已覆盖 | UI 和动作使用 `/api/builds/{id}/release-check` |
| 自动化监测 | 已覆盖 | `python scripts/monitor_cli.py check` |
| Linux Runner 部署文档 | 已覆盖 | 本文档和 `docs/nomad-runner-lifecycle.md` |
| Windows Runner 部署文档 | 已覆盖 | 本文档和 `runner/README.md` |
| API 自动化测试 | 已覆盖 | `go test ./... -count=1` |
| 前端 E2E UI 测试 | 已覆盖 | `npm run test:e2e` |
| 认证和权限 | 暂不覆盖 | 已按当前需求暂不考虑 |

## 19. 一键本地验收流程

本地完整链路建议优先使用 Python 验收脚本：

```bash
python scripts/acceptance_cli.py run
```

脚本会执行以下动作：

1. 检查或启动本地 API。默认会启动 Docker 化 API 容器，并加入 `packaging-net`。
2. 检查 Docker daemon、runner 镜像、Dagger engine 镜像、Dagger 基础镜像和队列隔离。
3. 上传本地测试组件，作为 `manual-upload` 组件来源。
4. 创建 Linux Preview 构建，版本固定为 `1.0.0`。
5. 审批构建，进入 runner 队列。
6. 运行 Docker runner，runner 内部调用 Dagger 生成 `tar.gz`。
7. 校验构建状态、制品下载、Preview 验证和 Release gate。

默认前提：

- PostgreSQL 容器 `packaging-postgres` 已启动；脚本会把它连接到 `packaging-net`。
- Docker 可以运行 Linux 容器。
- Docker runner 可以挂载 `/var/run/docker.sock`。
- 本地没有其他 `queued` 的 Linux 构建，避免 runner 领错任务。

常用命令：

```bash
# 只检查前置条件
python scripts/acceptance_cli.py check

# 强制重建 runner 镜像
python scripts/acceptance_cli.py run --rebuild-runner

# 使用宿主机上已有的 API
python scripts/acceptance_cli.py run \
  --api-mode host \
  --runner-api-url http://host.docker.internal:8080

# 复用或指定 Docker API 容器名
python scripts/acceptance_cli.py run \
  --api-url http://127.0.0.1:8081 \
  --api-container packaging-api-docker \
  --runner-api-url http://packaging-api-docker:8080
```

成功后应看到：

- build 状态从 `pending_approval -> queued -> running -> succeeded -> preview_validated`。
- `artifactUri` 指向 `local-upload://...`。
- `downloadUrl` 可下载。
- `/api/builds/{id}/release-check` 返回 `releaseReady=true`。

## 20. 手工推荐验收流程

建议按下面顺序验收：

1. 启动 PostgreSQL、后端和前端。
2. 打开管理后台，确认总览、创建构建、构建记录、Manifest、覆盖配置、制品上传都能进入。
3. 在 Manifest 页面加载 `lsap`，检查校验、功能检查、Dry Run。
4. 上传一个测试制品，确认上传记录、URI、sha256 和下载链接正常。
5. 创建一个 Linux Preview 构建，使用固定版本 `1.0.0`。
6. 审批构建，并用本地 Runner 或 Nomad Runner 执行。
7. 构建成功后查看 Build Lock、组件锁定、日志和发布门禁。
8. 执行 Preview 验证，确认状态变为 `preview_validated`。
9. 如果 Release 门禁通过，执行发布，确认状态变为 `published`。
10. 创建一个 `latest` 或通配符组件 URI 的构建，确认 Release 发布被阻止。
11. 对 Windows target 重复 Preview 构建流程，确认 Windows Runner 或占位包流程正常。
12. 执行后端 API 测试、前端单测、E2E UI 测试和前端构建。

## 21. 关联文档

- [Architecture](architecture.md)
- [API](api.md)
- [Local Acceptance](local-acceptance.md)
- [Nomad Runner Lifecycle](nomad-runner-lifecycle.md)
- [Runner README](../runner/README.md)
- [lsapctl Runtime Contract](lsapctl-runtime.md)
- [Product Manifest Schema](product-manifest.schema.json)
