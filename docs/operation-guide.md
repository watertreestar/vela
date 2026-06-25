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

- 用户认证和权限控制目前是 P1 基础能力：本地默认关闭；生产可通过 `AUTH_ENABLED=true` 和 bearer token 开启；静态 token、OIDC/JWKS JWT、前端 OIDC 登录、Authorization Code + PKCE、可选服务端 session/token broker、持久化 session store 和 logout/revoke 已有 MVP，生产 IdP 真实验收和 refresh-token 轮换策略仍属于 P1 后续工作。
- 产品真实业务打包脚本仍需要由产品侧提供，例如 Inno Setup、WiX、签名命令、定制 Linux 打包命令。
- Windows 真实 `setup.exe` 需要配置 `WINDOWS_PACKAGER_CMD`。本地/CI 可以使用内置 Windows fixture 生成可校验的 Preview 安装包；生产 Release 仍应使用 Inno Setup、WiX 和签名工具链。

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
- 本地默认 `AUTH_ENABLED=false`，无需登录即可走完整流程。
- 生产或权限验收可以设置：

```bash
AUTH_ENABLED=true
AUTH_ADMIN_TOKEN=<admin-token>
AUTH_OPERATOR_TOKEN=<operator-token>
AUTH_VIEWER_TOKEN=<viewer-token>
AUTH_RUNNER_TOKEN=<runner-token>
```

开启后，Manifest/Overlay 写入、构建创建和审批动作、制品上传、Runner claim/complete/heartbeat/log API 都要求 bearer token；Release publish 仅允许 admin；`restricted` 下载要求 operator/admin，`platform` 下载要求 viewer/operator/admin，`public` 下载仍可匿名访问。

如果要接入企业身份源，可以在 `AUTH_ENABLED=true` 的基础上启用 OIDC/JWKS bearer JWT 验证：

```bash
OIDC_ISSUER=https://idp.example.com/realms/packaging
OIDC_AUDIENCE=packaging-platform
OIDC_JWKS_URL=https://idp.example.com/realms/packaging/protocol/openid-connect/certs
OIDC_TOKEN_URL=https://idp.example.com/realms/packaging/protocol/openid-connect/token
OIDC_CLIENT_ID=packaging-admin
# 如果 IdP 使用 confidential client，再配置 OIDC_CLIENT_SECRET
OIDC_CLIENT_SECRET=
OIDC_ROLE_CLAIM=roles
OIDC_ADMIN_ROLE=admin
OIDC_OPERATOR_ROLE=operator
OIDC_VIEWER_ROLE=viewer
# 可选：启用后端 token broker，让浏览器拿平台 session token 而不是直接保存 IdP token
AUTH_SESSION_HMAC_KEY=<32+ bytes random secret>
AUTH_SESSION_TTL_SECONDS=3600
# 可选：生产建议开启，支持服务端 session 撤销和 logout
AUTH_SESSION_STORE_ENABLED=true
```

后端当前支持 RS256 JWT，并校验签名、issuer、audience、`exp` 和可选 `nbf`。角色 claim 可以是字符串、空格/逗号分隔字符串或字符串数组；匹配后映射为平台内置 `admin`、`operator`、`viewer`。OIDC principal 会以 `oidc:<sub>` 记录到审计 actor 中。Runner 仍建议使用独立的 `AUTH_RUNNER_TOKEN`，不要把 runner API 暴露给普通 OIDC 用户角色。

如果配置了 `OIDC_TOKEN_URL`、`OIDC_CLIENT_ID` 和 `AUTH_SESSION_HMAC_KEY`，后端会启用 `POST /api/auth/oidc/token`。该接口接收浏览器回调中的 `code`、PKCE `codeVerifier` 和 `redirectUri`，由后端向 IdP token endpoint 换取 token，再用同一套 OIDC/JWKS 规则校验身份，最后签发平台短期 session token。这样浏览器后续保存的是平台 token，IdP access token 不必直接暴露给前端业务代码。

默认 session token 是 HMAC 签名的短期 bearer token，后端只校验签名和过期时间。生产建议设置 `AUTH_SESSION_STORE_ENABLED=true`，此时 broker 会把 session 写入当前 backend store，token 内包含 `sessionId`，后端每次认证都会确认 session 未过期且未撤销。管理后台“清除令牌”会调用 `POST /api/auth/logout`，服务端会撤销持久化 session；静态 token 或原始 IdP token 的 logout 是服务端 no-op，只清除浏览器本地 token。当前 token 仍是 bearer token，不是 HttpOnly Cookie；如果要做 refresh token 隔离，可以在此 session store 基础上继续增加 refresh token 轮换。

OIDC 配置可以用 Python 验收脚本检查：

```bash
python scripts/oidc_check_cli.py check \
  --issuer "$OIDC_ISSUER" \
  --audience "$OIDC_AUDIENCE" \
  --jwks-url "$OIDC_JWKS_URL" \
  --discovery-url "$OIDC_ISSUER/.well-known/openid-configuration" \
  --authorization-url "$OIDC_AUTHORIZATION_URL" \
  --token-url "$OIDC_TOKEN_URL" \
  --role-claim roles \
  --expected-role operator \
  --token "$OIDC_CHECK_TOKEN" \
  --api-url http://127.0.0.1:8080
```

脚本会检查 discovery 文档、JWKS 是否包含可用 RS256 signing key、token 的 issuer/audience/role/subject/exp claim，并可调用后端受保护接口验证后端是否真正接受该 token。生产验收建议加 `--production`，它会要求 HTTPS、discovery 对齐、提供 token，并要求 `--api-url` 完成 live acceptance。签名验收由后端完成；脚本本身不保存 token，也不输出 token 内容。

生产模式还会强制提供 authorization endpoint、token endpoint 和 expected role，并校验 JWT 未过期；如果 token 带 `nbf`，脚本会确认它已经生效。这样可以避免只验证了“有一个 JWKS 和 token”，但没有验证真实登录流、角色映射和后端受保护接口的情况。

如果生产启用了服务端 broker，还可以用一次性 Authorization Code + PKCE verifier 做 live 验收：

```bash
python scripts/oidc_check_cli.py check \
  --api-url https://packaging-api.example.com \
  --broker-live \
  --broker-code "$OIDC_BROKER_CODE" \
  --broker-code-verifier "$OIDC_BROKER_CODE_VERIFIER" \
  --broker-redirect-uri "$OIDC_REDIRECT_URI" \
  --broker-logout \
  --require-session-store
```

该检查会调用 `/api/auth/oidc/token` 换取平台 session token，再用这个 token 调后端受保护接口；`--broker-logout --require-session-store` 会要求 broker 返回 `sessionId`、logout 返回 `revoked=true`，并在 logout 后再次使用同一 token 调受保护接口，必须得到 401/403，用于确认持久化 session store 和撤销链路生效。完整验收说明见 [生产级身份与对象锁验收](production-readiness.md)。

管理后台可以显示 OIDC 登录入口。前端构建时配置：

```bash
VITE_OIDC_AUTHORIZATION_URL=https://idp.example.com/realms/packaging/protocol/openid-connect/auth
VITE_OIDC_CLIENT_ID=packaging-admin
VITE_OIDC_SCOPE="openid profile email"
VITE_OIDC_RESPONSE_TYPE=code
VITE_OIDC_TOKEN_URL=https://idp.example.com/realms/packaging/protocol/openid-connect/token
# 如果使用后端 broker，改用：
# VITE_OIDC_BROKER_URL=/api/auth/oidc/token
VITE_OIDC_REDIRECT_URI=http://localhost:5173/
VITE_OIDC_ROLE_CLAIM=roles
VITE_OIDC_ADMIN_ROLE=admin
VITE_OIDC_OPERATOR_ROLE=operator
VITE_OIDC_VIEWER_ROLE=viewer
```

配置后，“访问令牌”弹窗会出现“使用 OIDC 登录”。推荐生产配置使用 `VITE_OIDC_RESPONSE_TYPE=code`，前端会生成 PKCE `code_verifier` / `code_challenge`。如果配置 `VITE_OIDC_BROKER_URL=/api/auth/oidc/token`，回调拿到 `code` 后会交给后端 broker 换取平台 session token；如果配置 `VITE_OIDC_TOKEN_URL`，则由浏览器直接向 IdP token endpoint 换取 `access_token` 或 `id_token`。如果企业 IdP 或本地测试仍使用旧模式，可以设置 `VITE_OIDC_RESPONSE_TYPE=token`，登录回调直接携带 `access_token`。

前端把最终 token 存入当前浏览器 `localStorage`，后续 API 请求自动带 `Authorization: Bearer ...`。前端会从 JWT role claim 或 broker 返回角色推导按钮权限显示；最终权限仍以后端 OIDC/JWKS 校验、session token 验签、session store 撤销状态和角色映射为准。

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

如果后端开启了 `AUTH_ENABLED=true`，打开管理后台后点击顶部的“免登录/角色”按钮，填入对应 token。这个 token 可以是平台静态 token，也可以是符合后端 OIDC 配置的 access token：

- 管理员：`AUTH_ADMIN_TOKEN`，可以发布 Release 和执行所有受保护操作。
- 操作员：`AUTH_OPERATOR_TOKEN`，可以创建构建、审批、验证 Preview、取消/重试、维护 Manifest/Overlay 和上传制品。
- 只读：`AUTH_VIEWER_TOKEN`，可以查看平台数据，并下载 `platform` 级别制品。

前端会把 token 保存在当前浏览器 `localStorage`，后续 API 请求和制品下载会自动带上 `Authorization: Bearer ...`。角色选择用于前端按钮禁用和提示；最终权限以后端 token 校验为准。清除令牌后前端回到本地免登录显示模式。

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
| 审计中心 | 查询审计事件，按 action/resource/build/product/upload 过滤，并导出带 digest、签名和对象锁证据的归档 |
| 构建详情 | 查看概要、最终配置、组件锁定、workloads、发布门禁、可复现报告、审计、Build Lock 和日志 |

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

仓库路径约定：

- `repository=minio` 或空值：`path` 可以写 `bucket/object`，也可以在配置了默认 `MINIO_BUCKET` 时只写 `object`；MinIO/RustFS 和本地 `ARTIFACT_ROOT` 支持 `*` 通配符，Preview 会解析为最新匹配文件并计算 sha256。
- `repository=<bucket>`：MinIO/RustFS 会把 `repository` 当作 bucket，`path` 当作 object key。
- `repository=gitlab`：需要配置 `GITLAB_PACKAGE_BASE_URL`，`path` 写 `project/package/{{version}}/file`，例如 `42/location-guard/{{version}}/location-guard.tar.gz`。
- `repository=gitlab:<project>`：project 写在 repository 中，`path` 写 `package/{{version}}/file`，例如 `location-guard/{{version}}/location-guard.tar.gz`。
- GitLab project 可以使用数字 ID，也可以使用 URL 编码后的项目路径，例如 `platform%2Fbackend`。GitLab source 目前只支持精确文件，不支持通配符；API 会下载 package 文件计算 sha256，runner 下载同一 URL 时会对匹配 `GITLAB_PACKAGE_BASE_URL` 的地址带上 `PRIVATE-TOKEN`。

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
      "CUSTOMER_NAME": "{{customer.name}}",
      "MYSQL_ROOT_PASSWORD": "secret:db/root-password"
    },
    "restart": "unless-stopped"
  }
}
```

`secret:<name>` 或 `secret://<name>` 表示 secret 引用，不应写入明文。Build Lock 会保留引用；真实值只在 Runner 渲染安装上下文时注入。

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
| 可复现报告 | 源组件、最终制品、Runner、生命周期和签名 Release 证据导出 |
| 审计 | Build Lock 的关键摘要 |
| Build Lock | 完整 Build Lock JSON |
| 日志 | 系统日志和 Runner 日志 |

Build Lock 是可复现构建的关键证据，应该用于追踪：

- 使用了哪个产品 Manifest。
- 使用了哪个 target、profile、customer 和 version。
- 每个组件解析到了哪个固定 URI。
- 最终配置和 workload 是否符合预期。

“可复现报告”页签中，“下载报告”会下载当前报告 JSON；“签名证据”会调用 `GET /api/builds/{id}/release-evidence/archive`，生成包含 `digest`、可选 HMAC `signature`、`signatureKeyId`、`archiveUri` 和对象锁证据的 Release evidence archive。该归档会存储在 `release-evidence/sha256/<digest>.json`，并写入 `release.evidence.archive.export` 审计事件。

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

Nomad 生产任务策略：

```bash
RUNNER_CPU=2000
RUNNER_MEMORY_MB=4096
RUNNER_MEMORY_MAX_MB=0
RUNNER_DISK_MB=4096
RUNNER_LOG_MAX_FILES=10
RUNNER_LOG_MAX_FILE_MB=20
RUNNER_KILL_TIMEOUT_SECONDS=30
RUNNER_MAX_RUN_SECONDS=3600
RUNNER_RESTART_ATTEMPTS=0
RUNNER_RESCHEDULE_ATTEMPTS=0
RUNNER_DOCKER_PRIVILEGED=false
```

默认策略会在 Nomad job 上设置 resources、ephemeral disk、allocation log rotation、kill timeout、max run duration、restart 和 reschedule。`RUNNER_RESTART_ATTEMPTS=0` 与 `RUNNER_RESCHEDULE_ATTEMPTS=0` 表示失败由平台记录并通过 UI/API retry，不让 Nomad 隐式反复执行同一个打包任务。

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

[Environment]::SetEnvironmentVariable(
  "WINDOWS_SIGNING_CMD",
  "signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 %OUTPUT%",
  "Machine"
)
```

注意：

- 未配置 `WINDOWS_PACKAGER_CMD` 时，Runner 只生成占位 setup 文件。
- `WINDOWS_SIGNING_CMD` 在 setup 文件生成后、sha256 和上传前执行；命令会收到 `OUTPUT`、`ARTIFACT`、`BUILD_ID`、`PRODUCT_ID`、`TARGET`、`VERSION`。
- 生产发布前必须用真实 Windows 打包工具链跑通 Preview 验证。
- 本地/CI 可以使用 `python3 /app/fixtures/windows_setup_packager.py` 作为 `WINDOWS_PACKAGER_CMD`，使用 `python3 /app/fixtures/windows_signing_fixture.py` 作为 `WINDOWS_SIGNING_CMD`。两个 fixture 会生成包含 setup marker、signing marker、build id 和上下文 checksum 的 `setup.exe` fixture，适合验证控制面、上传、Preview gate 和 Release gate，但不代表生产安装器或真实签名。
- 如果使用签名证书，证书权限必须对 Nomad 运行用户可用。

Windows Release 候选主机在跑真实 Preview 前，建议先执行 readiness check：

```powershell
python scripts\windows_runner_check_cli.py check `
  --api-url http://<api-host>:8080 `
  --packager-command "iscc C:\packaging-runner\context\rendered\setup.iss" `
  --signing-command "signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 %OUTPUT%"
```

该检查会确认当前主机是 Windows、Packaging API URL 和 Nomad node class 已配置、runner 命令可用、`WINDOWS_PACKAGER_CMD` 不是 fixture、签名要求开启时 `WINDOWS_SIGNING_CMD` 不是 fixture，并按命令类型检查 Inno Setup、WiX 或 SignTool 是否能从 PATH 找到。生产 Release 候选默认要求签名；只有明确的未签名 Preview 环境才使用 `--no-require-signing`。

## 14. Secret 管理

Manifest、覆盖配置和构建变量中可以使用 secret 引用：

```json
{
  "config": {
    "database": {
      "rootPassword": "secret:db/root-password"
    }
  }
}
```

支持格式：

- `secret:<name>`
- `secret://<name>`

本地或简单生产环境可以使用环境变量 backend：

```bash
SECRET_BACKEND=env
SECRET_ENV_PREFIX=PACKAGING_SECRET_
PACKAGING_SECRET_DB_ROOT_PASSWORD=<real-password>
```

命名规则：`secret:db/root-password` 会映射成 `PACKAGING_SECRET_DB_ROOT_PASSWORD`，非字母数字字符会转为 `_`。

也可以使用 JSON 文件 backend：

```bash
SECRET_BACKEND=file
SECRET_FILE=/etc/packaging/secrets.json
```

`/etc/packaging/secrets.json`：

```json
{
  "db/root-password": "<real-password>",
  "signing/cert-password": "<cert-password>"
}
```

如果团队使用 SOPS 管理加密文件，可以使用 SOPS backend。后端提交 runner job 时会调用 SOPS CLI 解密 JSON 到内存，不会把明文写回仓库或 Build Lock：

```bash
SECRET_BACKEND=sops
SOPS_FILE=/etc/packaging/secrets.enc.json
SOPS_COMMAND=sops                 # 默认 sops；也可以设置为绝对路径
SOPS_SECRET_FIELD=value           # 默认 value
```

`/etc/packaging/secrets.enc.json` 解密后的 JSON 可以使用精确 key：

```json
{
  "db/root-password": "<real-password>",
  "signing/cert-password": "<cert-password>"
}
```

也可以使用嵌套结构，`secret:db/root-password` 会读取 `db.root-password.<SOPS_SECRET_FIELD>`：

```json
{
  "db": {
    "root-password": {
      "value": "<real-password>"
    }
  }
}
```

SOPS 生产注意事项：

- 后端主机或容器必须能执行 `SOPS_COMMAND --decrypt --output-type json "$SOPS_FILE"`。
- Age、GPG、KMS、云账号或外部 KMS 凭据由 SOPS 自己通过环境变量、实例身份或本机配置读取。
- `SOPS_FILE` 可以放在只读配置目录；不要把解密后的 JSON 落盘。
- 如果同时存在精确 key 和嵌套路径，精确 key 优先。

生产环境可以使用 Vault KV v2 backend：

```bash
SECRET_BACKEND=vault
VAULT_ADDR=https://vault.internal
VAULT_TOKEN=<vault-token>
VAULT_NAMESPACE=<namespace>        # 可选
VAULT_KV_MOUNT=secret              # 默认 secret
VAULT_SECRET_FIELD=value           # 默认 value
```

解析规则：`secret:db/root-password` 会读取：

```text
GET /v1/<VAULT_KV_MOUNT>/data/db/root-password
X-Vault-Token: <VAULT_TOKEN>
X-Vault-Namespace: <VAULT_NAMESPACE>   # 设置时发送
```

Vault KV v2 返回体中会读取 `data.data.<VAULT_SECRET_FIELD>`，默认字段是 `value`。例如：

```json
{
  "data": {
    "data": {
      "value": "<real-password>"
    }
  }
}
```

如果平台已经使用 Nomad ACL 和 Variables，可以使用 Nomad Variables backend：

```bash
SECRET_BACKEND=nomad
NOMAD_ADDR=http://nomad.service.consul:4646
NOMAD_TOKEN=<nomad-token>
NOMAD_NAMESPACE=packaging
NOMAD_REGION=global
NOMAD_VARIABLE_PATH_PREFIX=packaging/secrets
NOMAD_VARIABLE_ITEM_KEY=value
```

解析规则：`secret:db/root-password` 会读取：

```text
GET /v1/var/packaging/secrets/db/root-password?namespace=packaging&region=global
X-Nomad-Token: <NOMAD_TOKEN>
```

Nomad Variable 返回体中会读取 `Items.<NOMAD_VARIABLE_ITEM_KEY>`，默认字段是 `value`。例如：

```json
{
  "Items": {
    "value": "<real-password>"
  }
}
```

处理边界：

- Manifest、Build Lock、审计事件和可复现报告只保存 `secret:<name>` 引用。
- API 在提交 Nomad job 时解析 secret，并把值注入为 `PACKAGING_SECRET_*` runner 环境变量。
- API 在 approve/retry 触发 runner 调度时会记录 `secret.resolve` 审计事件，metadata 只包含 secret 引用名、数量、target 和阶段，不包含真实值或环境变量值。
- Runner 在生成 workspace 前把 `secret:<name>` 替换为真实值，用于渲染 `docker-compose.yml`、systemd unit 或产品打包脚本。
- 如果缺少 secret，真实 Nomad 提交会失败并返回调度错误；本地 `DRY_RUN=true` 不会提交真实 runner job。
- 当前实现支持 env/file/SOPS/Vault KV v2/Nomad Variables backend；后续可以按生产密钥体系继续增加云 KMS 原生 backend。

不要把真实密码、证书口令、token 写入 `product.json`、overlay、Build Lock、`.http` 示例或 Git 仓库。

## 15. 制品存储

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

如果组件来源使用 GitLab Generic Package Registry，额外配置：

```bash
GITLAB_PACKAGE_BASE_URL=https://gitlab.example.com
GITLAB_PACKAGE_TOKEN=<token>
```

GitLab resolver 会访问 `/api/v4/projects/<project>/packages/generic/<package>/<version>/<file>`，并把响应内容的 sha256 写入 Build Lock。`GITLAB_PACKAGE_TOKEN` 会以 `PRIVATE-TOKEN` 发送给 GitLab；Nomad runner 也会收到这两个变量，仅在下载 URL 匹配 `GITLAB_PACKAGE_BASE_URL` 时附加该 token。

GitLab live 验收可以在发布前单独执行：

```bash
python scripts/gitlab_package_check_cli.py check \
  --production \
  --base-url "$GITLAB_PACKAGE_BASE_URL" \
  --token "$GITLAB_PACKAGE_TOKEN" \
  --path "42/engine/1.2.3/engine.tar.gz" \
  --expected-sha256 "$GITLAB_PACKAGE_EXPECTED_SHA256"
```

生产模式会要求 GitLab base URL 使用 HTTPS，并拒绝通配符路径。私有项目建议加 `--require-token`；如果没有 `--expected-sha256`，脚本只会输出计算得到的 sha256 并给 warning，不能作为强发布门禁。

## 16. 代理和网络

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

## 17. 自动化测试

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

链路排查使用 `requestId` 或 `correlationId`：

```bash
python scripts/correlation_trace_cli.py trace \
  --api-url http://127.0.0.1:8080 \
  --correlation-id corr_xxx
```

脚本会只读查询审计事件、构建记录、构建日志、上传元数据和通知，输出每类资源的匹配数量。需要机器可读证据时使用：

```bash
python scripts/correlation_trace_cli.py trace \
  --api-url http://127.0.0.1:8080 \
  --request-id req_xxx \
  --json
```

默认使用 `/api/builds` 返回的内嵌日志；如果要逐个读取 `/api/builds/{id}/logs`，加 `--scan-build-log-endpoints`。当 `AUTH_ENABLED=true` 时，传入 `--token`，或设置 `PACKAGING_API_TOKEN` / `AUTH_ADMIN_TOKEN`。

Prometheus 风格指标：

```bash
curl http://127.0.0.1:8080/metrics
```

当 `AUTH_ENABLED=true` 时，`/metrics` 需要 viewer、operator 或 admin bearer token：

```bash
curl -H "Authorization: Bearer ${AUTH_VIEWER_TOKEN}" http://127.0.0.1:8080/metrics
```

当前指标包括：

| 指标 | 说明 |
|---|---|
| `packaging_builds_total` | 按 status、target、release channel 统计构建数量 |
| `packaging_release_gate_blockers_total` | 按 preview/release 阶段统计发布门禁阻塞项 |
| `packaging_preview_ready_builds_total` | 当前满足 Preview 验证门禁的构建数量 |
| `packaging_release_ready_builds_total` | 当前满足 Release 发布门禁的构建数量 |
| `packaging_uploads_total` | 按保留状态和下载策略统计上传制品 |
| `packaging_upload_bytes_total` | 按保留状态和下载策略统计上传制品字节数 |
| `packaging_runners_total` | 按状态和 target 统计 Runner |
| `packaging_runner_running_builds` | 每个 Runner 当前运行构建数量 |
| `packaging_runner_last_heartbeat_age_seconds` | Runner 最近 heartbeat/log 距当前秒数 |
| `packaging_runner_cache_entries` | 按 runner、target、stage、cache、status 暴露最近 cache 状态 |
| `packaging_runner_cache_bytes` | 从最近 cache heartbeat/log 摘要解析出的 cache 字节数 |
| `packaging_runner_cache_files` | 从最近 cache heartbeat/log 摘要解析出的 cache 文件数 |
| `packaging_runner_cache_scan_truncated` | Runner cache 扫描是否触达 `RUNNER_CACHE_SCAN_FILE_LIMIT` |

Prometheus 告警规则：

```bash
python scripts/observability_rules_cli.py check
```

规则文件位于 `infra/prometheus/packaging-platform-rules.yml`，可以按 Prometheus rule_files 方式加载：

```yaml
rule_files:
  - /etc/prometheus/rules/packaging-platform-rules.yml
```

当前告警覆盖：

| Alert | Severity | 触发条件 |
|---|---|---|
| `PackagingPlatformAPIDown` | critical | Prometheus 无法抓取 Packaging Platform API |
| `PackagingPlatformRunnerUnavailable` | warning | 存在 stale、offline 或 unknown Runner |
| `PackagingPlatformActiveRunnerHeartbeatStale` | critical | Runner 有运行中构建但 heartbeat/log 超过 5 分钟 |
| `PackagingPlatformRunnerCacheScanTruncated` | warning | Runner cache 扫描持续触达文件数上限 |
| `PackagingPlatformBuildQueueBacklog` | warning | queued 构建超过 10 个并持续 10 分钟 |
| `PackagingPlatformFailedBuildVolume` | warning | failed 构建超过 20 个并持续 15 分钟 |
| `PackagingPlatformReleaseGateBlocked` | warning | Release gate 阻塞持续 30 分钟 |
| `PackagingPlatformExpiredArtifactsPresent` | warning | 存在过期制品持续 30 分钟 |

规则校验会检查必备 alert、基础 Prometheus rule 结构、关键指标引用和 tab 缩进。生产环境可以按团队容量调整阈值，但不要删除 release gate、runner heartbeat 和 artifact retention 这三类告警。

Alertmanager 路由示例：

```bash
python scripts/alertmanager_config_cli.py check
```

示例文件位于 `infra/prometheus/alertmanager-packaging-platform.yml`，可以作为 Alertmanager 配置模板。生产 Prometheus 通过 `alerting.alertmanagers` 指向 Alertmanager：

```yaml
alerting:
  alertmanagers:
    - static_configs:
        - targets:
            - alertmanager.internal:9093
```

当前路由意图：

| Receiver | 匹配条件 | 用途 |
|---|---|---|
| `packaging-critical-oncall` | `severity="critical"` | API down、active runner heartbeat stale 等需要快速响应的问题 |
| `packaging-runner-ops` | `category="runner"` | Runner 离线、心跳异常、运行中任务异常 |
| `packaging-release-managers` | `category="release-gate"` | Release gate 长时间阻塞 |
| `packaging-artifact-ops` | `category="artifact"` | 过期制品、保留策略和清理问题 |
| `packaging-build-ops` | `category="build"` | 队列积压和失败构建堆积 |
| `packaging-default` | 默认路由 | 未匹配的 Packaging Platform 告警 |

示例 receiver 使用 webhook placeholder：`http://alert-router.internal/packaging/...`。生产环境可以替换为真实 webhook，也可以改成 Email、Slack、PagerDuty 或企业内部通知系统。保留 `send_resolved: true`，这样恢复事件也能进入通知闭环。

独立审计事件：

```bash
curl http://127.0.0.1:8080/api/audit-events
curl "http://127.0.0.1:8080/api/audit-events?buildId=<build-id>"
curl "http://127.0.0.1:8080/api/audit-events?uploadId=<upload-id>"
curl "http://127.0.0.1:8080/api/audit-events/archive?buildId=<build-id>"
```

当 `AUTH_ENABLED=true` 时，审计查询和归档导出需要 viewer、operator 或 admin bearer token。当前会记录 Manifest 更新/恢复、Overlay 更新、构建创建/审批/拒绝/Preview 验证/Release 发布/重试/取消、secret 解析、制品上传/下载和审计归档导出。每条事件包含 actor、role、requestId、correlationId、resource、buildId/productId/uploadId 和动作元数据。

管理后台“审计中心”提供同一能力的 UI 入口：可以按 `action`、`resourceType`、`resourceId`、`productId`、`buildId`、`uploadId` 和 `limit` 过滤，查看 actor、role、requestId/correlationId 和 metadata 摘要，并导出当前筛选归档。导出成功后页面会显示最近一次归档的 `digest`、`signatureKeyId`、`archiveUri`、`archiveRetentionMode`、`archiveRetainUntil` 和 `archiveLegalHold`。

审计归档：

- `GET /api/audit-events/archive` 使用与审计查询相同的 filter。
- 返回 `schemaVersion=packaging.audit.archive/v1`、`generatedAt`、`archiveUri`、`archiveStoredAt`、`eventCount`、`filter`、`events`、`digestAlgorithm=sha256` 和 `digest`。
- 如果配置 `AUDIT_ARCHIVE_HMAC_KEY`，响应会包含 `signatureAlgorithm=hmac-sha256` 和 `signature`；如果同时配置 `AUDIT_ARCHIVE_HMAC_KEY_ID`，响应会包含非敏感的 `signatureKeyId`，用于说明当前使用哪一代 HMAC key。
- API 会把归档 payload 按 digest 写入当前 artifact store：本地为 `DATA_DIR/audit-archives/sha256/<digest>.json`，MinIO/RustFS 为 `minio://<bucket>/audit-archives/sha256/<digest>.json`。同 digest 已存在时复用，不覆盖。
- 生产 WORM/对象锁可以配置：

```bash
AUDIT_ARCHIVE_RETENTION_MODE=COMPLIANCE   # 或 GOVERNANCE
AUDIT_ARCHIVE_RETENTION_DAYS=3650
AUDIT_ARCHIVE_LEGAL_HOLD=true
```

- 这些对象锁字段只在 `ARTIFACT_STORE=minio` 时生效；返回体会包含 `archiveRetentionMode`、`archiveRetainUntil` 和 `archiveLegalHold` 作为存储证据。本地文件系统只保证 digest-addressed 和首次写入不覆盖，不声称具备合规 WORM。
- RustFS/MinIO bucket 必须在创建时启用 Object Lock；如果对未启用对象锁的 bucket 写入 retention/legal hold headers，对象存储会拒绝写入。新 bucket 由平台创建时会按上述配置请求 `ObjectLocking=true`；已有 bucket 需要提前由运维创建并验证。
- 导出归档会额外写入一条 `audit.archive.export` 审计事件，但该事件不会包含在当前归档内容里。
- 归档可用于离线留存和验真；生产环境应把 HMAC key 放入 secret backend 或平台密钥管理系统，不要写入仓库。

对象锁 live 验收：

```bash
python scripts/rustfs_check.py \
  --endpoint 127.0.0.1:9000 \
  --access-key rustfsadmin \
  --secret-key rustfsadmin \
  --object-lock \
  --object-lock-bucket packaging-audit-lock
```

`--object-lock` 会运行额外的 Go 集成测试：创建或使用 object-lock bucket，写入 `audit-archives/sha256/<digest>.json`，再通过 S3 API 读取 object retention 和 legal hold 状态。生产验收必须使用 `--production` 并显式指定 `--object-lock-bucket` 或 `--bucket`；如果对象存储不是本机 Docker 容器，增加 `--skip-container-check`：

```bash
python scripts/rustfs_check.py \
  --production \
  --skip-container-check \
  --endpoint rustfs.example.com:9000 \
  --access-key "$MINIO_ACCESS_KEY" \
  --secret-key "$MINIO_SECRET_KEY" \
  --use-ssl \
  --object-lock \
  --object-lock-bucket packaging-audit-lock \
  --retention-mode COMPLIANCE \
  --retention-days 3650 \
  --legal-hold
```

生产模式会拒绝未开启 `--object-lock`、未显式指定 bucket、retention mode 非 `GOVERNANCE`/`COMPLIANCE` 或 retention days 小于 1 的验收。生产验收时建议使用一个新的生产同配置临时 bucket，避免已有 bucket 未启用 Object Lock 导致存储端拒绝 retention/legal hold header。

Runner cache 观测：

- Runner 每次构建会在 Build log 中写入 `cache before`、`cache components`、`cache after`。
- `/api/runners` 的 recent messages 也能看到 cache heartbeat 摘要。
- `/metrics` 会从最近 cache heartbeat/log 摘要导出 `packaging_runner_cache_entries`、`packaging_runner_cache_bytes`、`packaging_runner_cache_files` 和 `packaging_runner_cache_scan_truncated`。
- Runner workspace 会生成 `context/rendered/runner-cache-before.json`、`runner-cache-components.json`、`runner-cache-after.json`。
- 可通过 `RUNNER_CACHE_SCAN_FILE_LIMIT` 控制每个 cache 路径最多扫描的文件数，默认 `5000`。

Runner 主机 cache 巡检：

```bash
python scripts/runner_cache_cli.py report \
  --path bun=/var/cache/packaging/bun \
  --path dagger=/var/cache/packaging/dagger \
  --path artifacts=/var/cache/packaging/artifacts
```

Runner cache 清理默认是 dry-run：

```bash
python scripts/runner_cache_cli.py cleanup --path artifacts=/var/cache/packaging/artifacts
python scripts/runner_cache_cli.py cleanup --apply --path artifacts=/var/cache/packaging/artifacts
```

`cleanup --apply` 只删除 cache 目录下的子项，不删除 cache 根目录；脚本会拒绝看起来不像 cache 或 packaging 的路径。

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
python -m unittest scripts.observability_rules_cli_test
python -m unittest scripts.alertmanager_config_cli_test
python -m unittest scripts.gitlab_package_check_cli_test
python -m unittest scripts.lifecycle_cli_test
python -m unittest scripts.fixture_golden_cli_test
python -m unittest scripts.correlation_trace_cli_test
python -m unittest scripts.oidc_check_cli_test
python -m unittest scripts.production_readiness_cli_test
python -m unittest scripts.windows_runner_check_cli_test
```

生产 readiness 聚合门禁：

```bash
python scripts/production_readiness_cli.py check
```

默认会运行本地治理检查，包括 Prometheus 规则、Alertmanager 路由示例、生命周期回收检查和 runner fixture golden artifact 检查；需要真实外部系统的 live gate 默认显示 `[WARN] skipped`。发布前自动化应显式要求 live gate，避免漏跑：

```bash
python scripts/production_readiness_cli.py check \
  --full-backend \
  --require-idp-live \
  --idp-live \
  --require-object-lock-live \
  --object-lock-live \
  --require-gitlab-live \
  --gitlab-live \
  --require-windows-live \
  --windows-live \
  --require-nomad-live \
  --nomad-live \
  --require-nomad-linux-node \
  --require-nomad-windows-node \
  --require-artifact-retention-live \
  --artifact-retention-live \
  --require-notification-routing-live \
  --notification-routing-live \
  --notification-route critical=https://alert-router.internal/packaging/critical \
  --notification-route error=https://alert-router.internal/packaging/default \
  --report-file artifacts/production-readiness.json
```

`--report-file` 会保留终端里的 `Confirmation lights`，同时写出机器可读 JSON；如果流水线只需要 JSON 输出，可以使用 `--json`。报表 schema 是 `packaging.production_readiness.report/v1`，字段包括 `generatedAt`、`status`、`exitCode`、`summary` 和 `gates`，可直接作为发布门禁证据归档。

如果生产启用 server-side session/token broker，聚合门禁需要把 broker 作为 IdP live 的强制子检查运行：

```bash
python scripts/production_readiness_cli.py check \
  --idp-live \
  --broker-live \
  --require-broker \
  --broker-code "$OIDC_BROKER_CODE" \
  --broker-code-verifier "$OIDC_BROKER_CODE_VERIFIER" \
  --broker-redirect-uri "$OIDC_REDIRECT_URI" \
  --broker-logout \
  --require-session-store
```

生产环境可以把 IdP、RustFS、GitLab、Windows runner、Nomad、通知路由和后端 API 参数通过命令行传入，也可以使用 `OIDC_*`、`RUSTFS_*`、`GITLAB_*`、`WINDOWS_*`、`NOMAD_*`、`PACKAGING_API_URL`、`PACKAGING_API_TOKEN` 等环境变量。聚合脚本只负责编排专项脚本；实际 OIDC/JWKS、broker、object-lock bucket、GitLab package、Windows packager/signing、Nomad leader/node class、Artifact retention API 行为、Notification routing 的校验仍由对应专项脚本完成。

Nomad 生命周期回收检查：

```bash
python scripts/lifecycle_cli.py check
python scripts/lifecycle_cli.py check --full-backend
python scripts/nomad_check_cli.py check \
  --production \
  --address "$NOMAD_ADDR" \
  --require-linux-node \
  --require-windows-node
python scripts/artifact_retention_cli.py check \
  --api-url "$PACKAGING_API_URL" \
  --token "$PACKAGING_API_TOKEN" \
  --max-expired-artifacts 0
python scripts/notification_routing_cli.py check \
  --api-url "$PACKAGING_API_URL" \
  --token "$PACKAGING_API_TOKEN" \
  --route critical=https://alert-router.internal/packaging/critical \
  --route error=https://alert-router.internal/packaging/default
```

`lifecycle_cli.py` 使用 fake scheduler 覆盖 Nomad allocation failed/lost、Nomad 提前 complete、Runner heartbeat timeout、终态构建不被回收、Nomad inspect 错误可观测等场景，不需要真实 Nomad 集群。`nomad_check_cli.py` 是真实集群 live readiness 检查，会确认 leader、ready nodes，以及可选的 Linux/Windows node class；生产模式默认要求 HTTPS，内部 HTTP 集群必须显式增加 `--allow-http`。

`artifact_retention_cli.py` 是真实 API 行为检查，会确认上传记录包含 sha256、downloadUrl、downloadPolicy 和 retentionStatus，并抽样请求 expired 上传的 downloadUrl，期望返回 HTTP 410。它用于验证平台保留策略和下载门禁；对象锁、retention mode、legal hold 仍由 `rustfs_check.py --object-lock` 验证。

`notification_routing_cli.py` 是通知外部接收器检查，会确认通知 severity 使用稳定枚举，并验证高风险 severity 已配置 webhook route。默认 dry-run，不会向外部发送；加 `--send` 后才会把匹配通知 POST 到对应 route，payload schema 为 `packaging.notification.dispatch/v1`。

### 16.6 文档和格式检查

```bash
git diff --check
```

### 16.7 错误提示、日志和国际化

错误处理设计见 [Error Handling](error-handling.md)。

当前约定：

- 前端用户提示使用中文，后续通过 `error.code` 和 `error.details[].code` 做国际化。
- 后端服务日志、Runner 日志和构建技术日志可以使用英文。
- API 错误返回结构化字段，包括 `status`、`code`、`messageKey`、`severity`、`category`、`retryable`、`requestId`、`correlationId` 和 `details`。
- 前端不能依赖后端 `message` 或英文 `reason` 判断错误类型；这些文本只作为 fallback 或技术详情。
- API/audit/build/runner 请求日志会持久化 requestId/correlationId；artifact metadata 会记录上传创建请求的链路字段，下载请求通过 `upload.download` audit event 关联。

## 18. 常见故障处理

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

## 19. 需求核对清单

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
| 认证和权限 | 基础覆盖 | 默认关闭；开启后保护写操作、构建动作、Runner API 和下载策略；OIDC/JWKS JWT、前端 OIDC Code + PKCE、服务端 session/token broker、持久化 session store 和 logout/revoke 已有 MVP，生产 IdP 验收和 refresh-token 轮换仍在 P1 |
| 审计中心 | 已覆盖 MVP | 审计事件查询、筛选、归档导出和 digest/signature/object-lock evidence 展示 |
| 通知中心 | 已覆盖 MVP | 顶部通知入口、未读数、单条/全部已读、构建生命周期通知、Runner 离线通知、存储异常通知、已读通知保留策略和构建详情跳转 |

## 20. 一键本地验收流程

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

# 使用 RustFS/MinIO-compatible 存储跑完整验收
python scripts/acceptance_cli.py run \
  --artifact-store minio \
  --minio-container packaging-rustfs \
  --minio-endpoint 127.0.0.1:9000 \
  --minio-access-key rustfsadmin \
  --minio-secret-key rustfsadmin \
  --minio-bucket packaging-artifacts

# Windows Preview fixture 验收
python scripts/acceptance_cli.py run --target windows --rebuild-runner
```

成功后应看到：

- build 状态从 `pending_approval -> queued -> running -> succeeded -> preview_validated`。
- `artifactUri` 指向 `local-upload://...`。
- `downloadUrl` 可下载。
- `/api/builds/{id}/release-check` 返回 `releaseReady=true`。

使用 `--artifact-store minio` 时，成功结果中的 `artifactUri` 应指向 `minio://...`，用于验证最终制品已经写入 RustFS/MinIO-compatible 存储。

Windows fixture 验收会默认把 runner 的 `WINDOWS_PACKAGER_CMD` 设置为 `python3 /app/fixtures/windows_setup_packager.py`，把 `WINDOWS_SIGNING_CMD` 设置为 `python3 /app/fixtures/windows_signing_fixture.py`。验收脚本会下载最终制品并校验 `PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1`、`PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1` marker 和 build id，确保 Windows 目标没有退回到占位包，也没有跳过签名 hook。使用真实 Inno/WiX/SignTool 命令时可以传入 `--windows-packager-command` 和 `--windows-signing-command`；如果不用内置 fixture，可以配合 `--no-verify-windows-fixture` / `--no-verify-windows-signing-fixture` 关闭 fixture marker 校验。

## 21. 手工推荐验收流程

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
11. 对 Windows target 重复 Preview 构建流程，至少使用内置 fixture 确认 Windows 控制面和制品上传正常；生产发布前再用真实 Windows Runner、Inno/WiX 和签名链路验证。
12. 执行后端 API 测试、前端单测、E2E UI 测试和前端构建。

## 22. 关联文档

- [Architecture](architecture.md)
- [API](api.md)
- [Local Acceptance](local-acceptance.md)
- [Nomad Runner Lifecycle](nomad-runner-lifecycle.md)
- [Runner README](../runner/README.md)
- [lsapctl Runtime Contract](lsapctl-runtime.md)
- [Product Manifest Schema](product-manifest.schema.json)
