# 一键本地验收流程

本文档说明如何在本机用一条命令验证 Vela 的核心闭环：

```text
PostgreSQL -> API -> Build -> Docker Runner -> Dagger -> Artifact Upload -> Preview Validate -> Release Gate
```

该流程用于确认当前代码、数据库、runner、Dagger 和本地文件制品存储能否一起工作。

`acceptance_cli.py --auth-enabled` 会故意拒绝本地 Docker fixture 和进程环境中的 runner bearer/capability/HMAC；本地 fixture 只能验证控制面，不是 auth-enabled Nomad 生产验收。真实部署请在 dispatch 后使用 `runner_e2e_cli.py check --live`。

## 1. 前置条件

需要先确认以下依赖可用：

- Python 3。
- Go 工具链。
- Docker 可运行 Linux 容器。
- PostgreSQL 容器已启动，默认容器名为 `packaging-postgres`。
- 本地已有或可拉取以下镜像：
  - `debian:stable-slim`
  - `alpine:latest`
  - `registry.dagger.io/engine:v0.21.3`
- runner 镜像可构建，默认镜像名为 `packaging-runner:local`。

默认数据库连接：

```bash
postgres://packaging:packaging@127.0.0.1:5432/packaging?sslmode=disable
```

如果你的数据库连接不同，可以通过 `--database-url` 指定。

## 2. 最短使用方式

在仓库根目录执行：

```bash
python scripts/acceptance_cli.py run
```

默认行为：

1. 如果 `http://127.0.0.1:8080/api/health` 不可用，脚本会启动一个 Docker 化 API 容器。
2. API 容器名默认为 `packaging-api-acceptance`。
3. API 容器会加入 Docker 网络 `packaging-net`。
4. 脚本会把 `packaging-postgres` 连接到 `packaging-net`。
5. 脚本会构建临时 Linux API 二进制并挂载仓库到 API 容器。
6. 脚本会上传测试组件，创建 Linux Preview 构建，并审批。
7. 脚本会运行 `packaging-runner:local`，runner 内部调用 Dagger。
8. 构建成功后，脚本会校验制品下载、执行 Preview 验证，并检查 Release gate。
9. 脚本结束时会停止临时 API 容器并清理临时 API 二进制。

成功时会看到类似输出：

```text
[09/09] Check Release gate

Confirmation lights:
  [OK] Preview validation: status=preview_validated
  [OK] Release gate: blockingCount=0
  [OK] Build artifact: http://127.0.0.1:8080/api/uploads/.../download

Acceptance complete: buildId=... namespace=...
```

## 3. 只检查环境

如果只想确认前置条件，不创建构建、不运行 runner：

```bash
python scripts/acceptance_cli.py check
```

通过时会显示：

```text
Confirmation lights:
  [OK] Packaging API: http://127.0.0.1:8080/api/health
  [OK] Docker daemon: ...
  [OK] Product manifest: lsap
  [OK] Runner queue isolation: no queued target build
```

## 4. 检查 RustFS 存储（不使用 MinIO）

如果本机已经启动 RustFS 容器，可以验证制品仓库解析能力：

```bash
python scripts/rustfs_check.py
```

默认参数：

- 容器名：`packaging-rustfs`
- Endpoint：`127.0.0.1:9000`
- 凭据：`rustfsadmin` / `rustfsadmin`

该检查会在需要时把容器连接到 Docker `bridge`，确认 TCP 端口可访问，然后运行后端 RustFS 集成测试。测试会创建临时 bucket，上传组件制品和 `.sha256` sidecar，解析 `artifact-repository` 通配符来源，并确认结果可以锁定为精确 `minio://bucket/object` URI 和 sha256。

如果要同时验证审计归档对象锁：

```bash
python scripts/rustfs_check.py \
  --object-lock \
  --object-lock-bucket packaging-audit-lock
```

如果目标是托管 RustFS 服务而不是本地 Docker 容器：

```bash
python scripts/rustfs_check.py \
  --production \
  --skip-container-check \
  --endpoint rustfs.example.com:9000 \
  --access-key "$RUSTFS_ACCESS_KEY" \
  --secret-key "$RUSTFS_SECRET_KEY" \
  --use-ssl \
  --object-lock \
  --object-lock-bucket packaging-audit-lock \
  --retention-mode COMPLIANCE \
  --retention-days 3650 \
  --legal-hold
```

生产模式会拒绝未开启 `--object-lock`、未显式指定 bucket、retention mode 非 `GOVERNANCE`/`COMPLIANCE` 或 retention days 小于 1 的验收，避免用临时 bucket 或无效策略冒充对象锁 live 验收。

## 5. 强制重建 Runner

如果刚修改过 `runner/`，建议强制重建 runner 镜像：

```bash
python scripts/acceptance_cli.py run --rebuild-runner
```

脚本会执行等价于下面的构建：

```bash
docker build -t packaging-runner:local runner
```

## 6. Windows Preview Fixture 验收

Windows 生产打包最终应由 Windows runner 主机上的 Inno Setup、WiX 或其他真实打包工具执行。为了让本地和 CI 可以在 Linux Docker runner 内先验证控制面闭环，runner 镜像内置了一个确定性的 Windows setup fixture：

```bash
python scripts/acceptance_cli.py run --target windows --rebuild-runner
```

默认行为：

1. acceptance CLI 会把 `WINDOWS_PACKAGER_CMD` 设置为 `python3 /app/fixtures/windows_setup_packager.py`。
2. acceptance CLI 会把 `WINDOWS_SIGNING_CMD` 设置为 `python3 /app/fixtures/windows_signing_fixture.py`。
3. packager fixture 从 runner workspace 的 `context/build-lock.json`、`context/final-config.json`、`context/rendered/components.json` 和组件文件生成一个 `*-setup.exe` fixture 文件。
4. signing fixture 在 runner 计算 sha256 和上传之前追加固定 marker `PACKAGING_PLATFORM_WINDOWS_SIGNING_FIXTURE_V1` 和确定性签名元数据。
5. 生成内容包含固定 marker `PACKAGING_PLATFORM_WINDOWS_SETUP_FIXTURE_V1`、build id、产品、版本、组件数量、上下文 checksum 和签名 fixture marker。
6. 脚本下载最终制品后会校验 setup marker、signing marker 和 build id，防止误把 runner 的占位文件或未签名 fixture 当成通过。

如果要用真实 Windows 打包命令，可以覆盖：

```bash
python scripts/windows_runner_check_cli.py check \
  --packager-command "iscc C:\\packaging-runner\\context\\rendered\\setup.iss" \
  --signing-command "signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 %OUTPUT%"

python scripts/acceptance_cli.py run \
  --target windows \
  --windows-packager-command "iscc C:\\packaging-runner\\context\\rendered\\setup.iss" \
  --windows-signing-command "signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 %OUTPUT%" \
  --no-verify-windows-fixture
```

`windows_runner_check_cli.py` 应在 Windows runner 主机上执行，用于确认真实 Inno/WiX/SignTool 工具链存在，并拒绝把 fixture packager/signing command 当成生产 Release 候选链路。

如果你只是想验证旧的占位控制面路径，可以显式关闭 fixture：

```bash
python scripts/acceptance_cli.py run --target windows --no-windows-packager-fixture --no-windows-signing-fixture --no-verify-windows-fixture --no-verify-windows-signing-fixture
```

## 7. Linux Dagger Fixture 验收

Linux 生产打包最终应由产品侧提供真实 `DAGGER_LINUX_PACKAGE_CMD` 或默认 tar 包逻辑。为了让本地和 CI 可以验证 Dagger 路径、组件下载、上下文渲染、上传和 Preview gate，runner 镜像内置了一个确定性的 Linux package fixture：

```bash
python scripts/acceptance_cli.py run --linux-packager-fixture --rebuild-runner
```

默认行为：

1. acceptance CLI 会把 `DAGGER_LINUX_PACKAGE_CMD` 设置为 `python3 /workspace/context/rendered/linux_package_packager.py`。
2. runner 会把 `runner/fixtures/linux_package_packager.py` 复制到 Dagger context 的 `context/rendered/`。
3. 如果 `--dagger-install-tools` 仍是默认 `skip`，acceptance CLI 会为 fixture 设置 `apk add --no-cache python3 tar gzip coreutils bash`，适配默认 Alpine base image。
4. fixture 会生成一个 `.tar.gz`，包含固定 marker `PACKAGING_PLATFORM_LINUX_PACKAGE_FIXTURE_V1`、build id、产品、版本、组件/工作负载数量和上下文 checksum。
5. 脚本下载最终制品后会校验 marker 和 build id，防止误把普通 tar 包当成 Dagger fixture 验收通过。

如果你要验证真实 Linux 打包命令，不要开启 `--linux-packager-fixture`，而是通过环境变量或 Nomad 配置设置 `DAGGER_LINUX_PACKAGE_CMD` / `LINUX_PACKAGER_CMD`。

## 8. 使用宿主机已有 API

默认模式会启动 Docker 化 API。若你已经在宿主机启动了 API，可以使用 host 模式：

```bash
python scripts/acceptance_cli.py run \
  --api-mode host \
  --runner-api-url http://host.docker.internal:8080
```

注意：

- Windows 或 Docker Desktop 环境中，`host.docker.internal` 通常可用。
- 如果 runner 容器无法访问宿主机 API，请改用默认 Docker API 模式。

## 9. 使用自定义端口或容器名

如果 8080 已被占用，可以改用 8081：

```bash
python scripts/acceptance_cli.py run \
  --api-url http://127.0.0.1:8081 \
  --api-container packaging-api-docker \
  --runner-api-url http://packaging-api-docker:8080
```

此时：

- 浏览器和宿主机访问 `http://127.0.0.1:8081`。
- runner 容器访问 `http://packaging-api-docker:8080`。

## 10. 常用参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--api-url` | `http://127.0.0.1:8080` | 宿主机访问 API 的地址 |
| `--runner-api-url` | 自动推导 | runner 容器访问 API 的地址 |
| `--api-mode` | `docker` | `docker` 或 `host` |
| `--api-container` | `packaging-api-acceptance` | 临时 API 容器名 |
| `--api-docker-network` | `packaging-net` | API 和 runner 使用的 Docker 网络 |
| `--postgres-container` | `packaging-postgres` | PostgreSQL 容器名 |
| `--database-url` | 默认 PostgreSQL URL | 数据库连接 |
| `--artifact-store` | `local` | `local` 或 `minio`；`minio` 可用于 RustFS/MinIO-compatible 全链路验收 |
| `--minio-container` | `packaging-rustfs` | RustFS/MinIO-compatible 容器名，Docker API 模式下会连接到验收网络 |
| `--minio-endpoint` | `127.0.0.1:9000` | 宿主机访问对象存储的 endpoint |
| `--api-minio-endpoint` | 自动推导 | API 容器访问对象存储的 endpoint，默认使用 `--minio-container:9000` |
| `--runner-minio-public-url` | 自动推导 | runner 访问对象存储对象的 HTTP base URL |
| `--runner-image` | `packaging-runner:local` | runner 镜像名 |
| `--rebuild-runner` | false | 是否强制重建 runner 镜像 |
| `--dagger-base-image-source` | `host` | Dagger 基础镜像来源 |
| `--dagger-base-image` | `alpine:latest` | Linux 打包基础镜像 |
| `--dagger-install-tools` | `skip` | 是否跳过基础工具安装 |
| `--linux-packager-fixture` | false | Linux 目标显式使用内置 Dagger fixture，生成可校验的 `.tar.gz` |
| `--linux-fixture-command` | `python3 /workspace/context/rendered/linux_package_packager.py` | Linux fixture 在 Dagger 容器中的命令 |
| `--verify-linux-fixture` | true | 使用 Linux fixture 时下载产物并校验 marker 和 build id |
| `--windows-packager-fixture` | true | Windows 目标默认使用内置 fixture packager，生成可校验的 `setup.exe` fixture |
| `--windows-packager-command` | 空 | 自定义 Windows 打包命令；设置后优先于内置 fixture |
| `--verify-windows-fixture` | true | 使用内置 fixture 时下载产物并校验 marker 和 build id |
| `--windows-signing-fixture` | true | Windows 目标默认使用内置 signing fixture，验证签名 hook 在 checksum/upload 前执行 |
| `--windows-signing-command` | 空 | 自定义 Windows 签名命令；设置后优先于内置 signing fixture |
| `--verify-windows-signing-fixture` | true | 使用内置 signing fixture 时下载产物并校验 signing marker 和 build id |
| `--keep-api` | false | 验收结束后是否保留临时 API |

## 11. 队列隔离

脚本默认会拒绝在已有 Linux `queued` 构建时运行，避免 runner 领错任务。

如果你确认队列里的任务可以被当前验收流程处理，可以显式允许：

```bash
python scripts/acceptance_cli.py run --allow-existing-queued
```

脚本还会自动取消上一次 `acceptance_cli.py` 遗留的 `pending_approval`、`queued` 或 `running` 构建。可关闭：

```bash
python scripts/acceptance_cli.py run --no-cancel-stale-acceptance
```

## 12. 验收完成后看什么

脚本成功后，重点看：

- `buildId`：本次验收构建 ID。
- build 状态应为 `preview_validated`。
- `artifactUri` 应为 `local-upload://...`。
- `downloadUrl` 应可下载。
- `/api/builds/{buildId}/release-check` 应返回：

```json
{
  "previewReady": true,
  "releaseReady": true,
  "blockingCount": 0
}
```

如果使用 RustFS/MinIO-compatible 存储：

```bash
python scripts/acceptance_cli.py run \
  --artifact-store minio \
  --minio-container packaging-rustfs \
  --minio-endpoint 127.0.0.1:9000 \
  --minio-access-key rustfsadmin \
  --minio-secret-key rustfsadmin \
  --minio-bucket packaging-artifacts
```

此模式会启动临时 API 时设置 `ARTIFACT_STORE=minio` 和 MinIO/RustFS 连接参数，runner 仍通过 API 上传最终安装包，API 会把最终制品写入 RustFS/MinIO-compatible 存储。脚本会额外校验最终 `artifactUri` 必须以 `minio://` 开头，`downloadUrl` 仍通过 API 下载验证。

## 12. 清理命令

通常脚本会自动清理临时 API 容器。若中途强制退出，可以手工清理：

```bash
docker rm -f packaging-api-acceptance
```

Dagger engine 容器如果不再需要，也可以停止：

```bash
docker rm -f dagger-engine-v0.21.3
```

不要清理 `packaging-postgres`，除非你明确要删除本地数据库。

## 13. 常见问题

### runner 无法连接 API

症状：

```text
Unable to connect. Is the computer able to access the url?
```

处理：

- 优先使用默认 `--api-mode docker`。
- 如果使用 `--api-mode host`，确认 `--runner-api-url http://host.docker.internal:8080` 在 runner 容器内可访问。

### Docker 镜像缺失或拉取失败

脚本默认会尝试拉取缺失镜像。如果你的网络不能访问公网 registry，先手工准备镜像：

```bash
docker pull debian:stable-slim
docker pull alpine:latest
docker pull registry.dagger.io/engine:v0.21.3
```

也可以改用已存在的本地基础镜像：

```bash
python scripts/acceptance_cli.py run --dagger-base-image alpine:latest
```

### 有 queued 构建导致检查失败

症状：

```text
[FAIL] Runner queue isolation: 1 queued linux build(s) exist
```

处理：

- 在管理后台取消 queued 构建。
- 或确认安全后使用 `--allow-existing-queued`。

### PostgreSQL 容器名不是 packaging-postgres

指定你的容器名：

```bash
python scripts/acceptance_cli.py run --postgres-container <your-postgres-container>
```

如果 PostgreSQL 不在 Docker 网络内，需要确保 API 容器可以访问 `--database-url` 中的主机名。
