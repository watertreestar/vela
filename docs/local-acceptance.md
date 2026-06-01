# 一键本地验收流程

本文档说明如何在本机用一条命令验证 Packaging Platform 的核心闭环：

```text
PostgreSQL -> API -> Build -> Docker Runner -> Dagger -> Artifact Upload -> Preview Validate -> Release Gate
```

该流程用于确认当前代码、数据库、runner、Dagger 和本地文件制品存储能否一起工作。

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

## 4. 强制重建 Runner

如果刚修改过 `runner/`，建议强制重建 runner 镜像：

```bash
python scripts/acceptance_cli.py run --rebuild-runner
```

脚本会执行等价于下面的构建：

```bash
docker build -t packaging-runner:local runner
```

## 5. 使用宿主机已有 API

默认模式会启动 Docker 化 API。若你已经在宿主机启动了 API，可以使用 host 模式：

```bash
python scripts/acceptance_cli.py run \
  --api-mode host \
  --runner-api-url http://host.docker.internal:8080
```

注意：

- Windows 或 Docker Desktop 环境中，`host.docker.internal` 通常可用。
- 如果 runner 容器无法访问宿主机 API，请改用默认 Docker API 模式。

## 6. 使用自定义端口或容器名

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

## 7. 常用参数

| 参数 | 默认值 | 说明 |
|---|---|---|
| `--api-url` | `http://127.0.0.1:8080` | 宿主机访问 API 的地址 |
| `--runner-api-url` | 自动推导 | runner 容器访问 API 的地址 |
| `--api-mode` | `docker` | `docker` 或 `host` |
| `--api-container` | `packaging-api-acceptance` | 临时 API 容器名 |
| `--api-docker-network` | `packaging-net` | API 和 runner 使用的 Docker 网络 |
| `--postgres-container` | `packaging-postgres` | PostgreSQL 容器名 |
| `--database-url` | 默认 PostgreSQL URL | 数据库连接 |
| `--runner-image` | `packaging-runner:local` | runner 镜像名 |
| `--rebuild-runner` | false | 是否强制重建 runner 镜像 |
| `--dagger-base-image-source` | `host` | Dagger 基础镜像来源 |
| `--dagger-base-image` | `alpine:latest` | Linux 打包基础镜像 |
| `--dagger-install-tools` | `skip` | 是否跳过基础工具安装 |
| `--keep-api` | false | 验收结束后是否保留临时 API |

## 8. 队列隔离

脚本默认会拒绝在已有 Linux `queued` 构建时运行，避免 runner 领错任务。

如果你确认队列里的任务可以被当前验收流程处理，可以显式允许：

```bash
python scripts/acceptance_cli.py run --allow-existing-queued
```

脚本还会自动取消上一次 `acceptance_cli.py` 遗留的 `pending_approval`、`queued` 或 `running` 构建。可关闭：

```bash
python scripts/acceptance_cli.py run --no-cancel-stale-acceptance
```

## 9. 验收完成后看什么

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

## 10. 清理命令

通常脚本会自动清理临时 API 容器。若中途强制退出，可以手工清理：

```bash
docker rm -f packaging-api-acceptance
```

Dagger engine 容器如果不再需要，也可以停止：

```bash
docker rm -f dagger-engine-v0.21.3
```

不要清理 `packaging-postgres`，除非你明确要删除本地数据库。

## 11. 常见问题

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
