# 错误处理、国际化与可观测性设计

本文定义 Packaging Platform 的错误契约、前端提示分流、日志语言、国际化和测试要求。目标是把“用户提示可本地化”和“日志可排障”分开，避免前端依赖不稳定的自然语言字符串。

## 1. 设计原则

- 错误判断只能依赖稳定字段，例如 `error.code`、`error.details[].code`、`field`、`stage`、`severity`、`category`。
- 用户提示默认中文，并通过前端 i18n 字典生成；后端 `message` 只能作为 fallback。
- 服务日志、Runner 日志、构建技术日志可以使用英文，便于排障和跨团队协作。
- API 错误必须可测试、可审计、可关联到日志；每个错误都应有 `requestId`，跨构建链路还应有 `correlationId`。
- Release gate、Manifest dry run 这类业务检查可以返回业务结果，不一定都是 HTTP error，但其检查项也必须使用稳定 code。

## 2. 当前实现状态

当前已经实现：

- API 错误响应包含 `status`、`code`、`messageKey`、`message`、`severity`、`category`、`retryable`、`userActionKey`、`userAction`、`requestId`、`correlationId`、`details`。
- 后端已有错误注册表，集中维护 `code -> messageKey/severity/category/retryable/userActionKey`。
- 后端已有 `internal/apperr.AppError`，业务层可构造稳定 `Code` / `Kind` / `Params` / `Details`，API 层统一映射 HTTP 状态和标准 JSON response。
- API 响应头会写入 `X-Request-Id` 和 `X-Correlation-Id`，并优先继承调用方传入的同名请求头。
- 服务端 access log 已通过 `slog` 统一记录 method、path、status、duration、requestId、correlationId。
- API 边界会对 5xx 和上游依赖错误打印一次结构化 error log，400/409/422 业务错误默认不作为 error log。
- 前端 `ApiError` 能读取结构化错误，并通过 i18n 字典分流到 toast 或 page alert。
- Release gate 返回 `checks[].id`，前端和后端都能用它展示阻塞项。
- SSE log stream 的 `event:error` 已使用结构化 JSON payload。
- 构建日志和 Runner 日志大多是英文技术日志。

当前尚未完全实现：

- `details[].params` 目前仅作为预留字段，尚未在所有错误里填充。
- `requestId` / `correlationId` 已进入 API response、access log、audit event、Build log、Runner 请求日志和上传 artifact metadata。
- Notification center 已实现构建生命周期 MVP；Runner 离线、存储不可用、归档和签名策略仍待补齐。

## 3. Go 内部错误、API 返回与日志责任边界

后端分三层处理错误：

| 层级 | 负责内容 | 不负责内容 |
|---|---|---|
| domain / resolver / store / service | 构造或包装 Go error；对业务可识别错误使用 `apperr.AppError` | 不写 HTTP status，不直接生成 API response，不打印重复日志 |
| API handler / API writer | 将 Go error 映射成 HTTP status 和标准 JSON error response | 不把数据库、文件系统、scheduler 原始错误暴露给用户文案 |
| API 边界日志 / access log | 统一记录 requestId、correlationId、status、error_code 和 cause | 不为每个底层函数重复打印同一个错误 |

### 3.1 AppError 应该谁负责构造

`AppError` 由最先知道业务语义的代码构造。

推荐规则：

- store 层知道资源不存在时，构造 `apperr.NotFound("build", id)`、`apperr.NotFound("upload", id)`。
- resolver / domain service 知道 manifest、component、workload 规则失败时，构造 `apperr.Validation(...)` 或 `apperr.Business(...)`。
- 状态机代码知道当前动作和状态冲突时，构造 `apperr.Conflict("BUILD_STATE_CONFLICT", "...")`。
- scheduler、artifact、database、filesystem 这类依赖失败时，底层优先 `fmt.Errorf("...: %w", err)` 包装；如果已经能判断为上游不可用，则用 `apperr.Dependency(...)`。
- API handler 只在请求边界场景构造错误，例如 JSON 无法解析、缺少必填 body 字段、路径参数非法。业务流程中的错误应尽量交给 store/service/resolver 构造。

示例：

```go
return apperr.Conflict("BUILD_STATE_CONFLICT", "only failed or canceled builds can be retried")
```

```go
return apperr.Validation("VALIDATION_REQUIRED_FIELD", "workload id is required").
	WithField("workloads.id")
```

```go
return apperr.Wrap(
	err,
	apperr.KindNotFound,
	"RESOURCE_NOT_FOUND",
	fmt.Sprintf("component %s upload %s not found", component.ID, uploadID),
).WithParam("componentId", component.ID).WithParam("uploadId", uploadID)
```

### 3.2 模块内部 helper 与模块出口

一个内聚模块内部可能有很多复用 helper。不要让每个 helper 都构造 `AppError`，否则错误模型会污染到过低层级，也会让复用方法承担它不知道的业务语义。

推荐分层：

| 位置 | 推荐返回 | 说明 |
|---|---|---|
| 内部普通 helper | `fmt.Errorf("...: %w", err)` 或普通 error | 只保留局部技术细节和调用链 |
| 状态机 / 规则函数 | `apperr.AppError` | 如果函数明确知道稳定业务语义，例如状态冲突、规则失败 |
| 模块出口 service 方法 | `apperr.AppError` 或保留可被 `errors.As` 找到的 wrapped `AppError` | 负责把内部错误升级成稳定业务失败 |
| store / adapter 技术失败 | wrap 普通 error | 数据库、文件系统、JSON、网络失败通常只是技术 cause |
| adapter 可识别上游不可用 | `apperr.Dependency(...)` | 例如 scheduler submit/stop 明确失败 |

原则：

- `AppError` 不是因为调用方是 API 才返回，而是因为模块出口需要稳定表达业务失败。
- 模块内部 helper 不知道整体业务语义时，只 wrap 普通 error。
- 模块出口知道“这类内部失败在产品上是什么”时，再升级成 `AppError`。
- 中间层可以继续 `fmt.Errorf("create build lock: %w", err)`，只要链路中包含 `AppError`，API 层仍可通过 `errors.As` 找到它。

示例：

```go
func mergeConfig(base, overlay map[string]any) (map[string]any, error) {
	if err := validateOverlayShape(overlay); err != nil {
		return nil, fmt.Errorf("merge overlay config: %w", err)
	}
	return merged, nil
}
```

```go
func ResolveBuild(product domain.Product, request domain.BuildRequest) (domain.BuildLock, error) {
	lock, err := resolveBuildLock(product, request)
	if err != nil {
		return domain.BuildLock{}, apperr.Wrap(
			err,
			apperr.KindValidation,
			"MANIFEST_RESOLVE_FAILED",
			"manifest resolve failed",
		).WithParam("productId", product.ID)
	}
	return lock, nil
}
```

```go
func canRetry(status domain.BuildStatus) error {
	if status != domain.BuildFailed && status != domain.BuildCanceled {
		return apperr.Conflict("BUILD_STATE_CONFLICT", "only failed or canceled builds can be retried")
	}
	return nil
}
```

### 3.3 AppError 字段语义

`internal/apperr.AppError` 是后端内部错误模型：

| 字段 | 说明 |
|---|---|
| `Kind` | 内部分类，API 层用它映射 HTTP status |
| `Code` | 稳定产品错误码，API response 和前端 i18n 使用 |
| `Message` | 英文诊断文本，不能作为前端主展示文案 |
| `Resource` | 资源类型，例如 `build`、`upload`、`product` |
| `Field` | 字段路径，例如 `workloads.runtime` |
| `Params` | i18n 插值参数和诊断上下文 |
| `Details` | 多条业务明细，例如 release gate 阻塞项 |
| `Cause` | 原始 cause，日志使用，默认不进入用户文案 |

`Kind -> HTTP status` 由 API 层统一维护：

| Kind | HTTP status |
|---|---|
| `validation` | `400` |
| `not_found` | `404` |
| `conflict` | `409` |
| `business` | `422` |
| `resource` | `404`，已过期等特殊场景可由 API fallback 保持 `410` |
| `dependency` | `502` |
| `system` | `500` |

### 3.4 API 层如何统一映射

API writer 的标准流程：

1. handler 调用业务代码，拿到普通 `error`。
2. `writeError` 使用 `errors.As(err, *apperr.AppError)` 识别内部 typed error。
3. API 层根据 `AppError.Kind` 计算 HTTP status。
4. API 层根据 `AppError.Code` 查错误注册表，补齐 `messageKey`、`severity`、`category`、`retryable`、`userActionKey`。
5. API 层把 `Field`、`Params`、`Details` 转成 `error.details`。
6. API 层写入 `requestId`、`correlationId`。
7. API 层返回标准 `APIErrorResponse`。

示例响应：

```json
{
  "error": {
    "code": "BUILD_STATE_CONFLICT",
    "status": 409,
    "messageKey": "errors.build.stateConflict",
    "message": "only failed or canceled builds can be retried",
    "severity": "warning",
    "category": "business",
    "retryable": false,
    "requestId": "req_xxx",
    "correlationId": "corr_xxx"
  }
}
```

### 3.5 命名风格

本项目区分 Go 代码标识符、API JSON 字段和日志字段的命名风格：

| 位置 | 命名风格 | 示例 |
|---|---|---|
| Go struct 字段、函数、类型 | MixedCaps / CamelCase | `RequestID`、`CorrelationID`、`BuildLock` |
| API JSON response/request | camelCase | `requestId`、`correlationId`、`productId`、`messageKey` |
| 日志字段、监测字段 | snake_case | `request_id`、`correlation_id`、`error_code`、`duration_ms` |
| 错误 code 常量 | SCREAMING_SNAKE_CASE | `BUILD_STATE_CONFLICT`、`RESOURCE_NOT_FOUND` |
| release gate/detail id | kebab-case 或 dotted kebab-case | `release.fixed-version`、`preview.build-lock` |

说明：

- Go 官方风格通常是 `MixedCaps`，不是下划线命名；例如 `RequestID` 比 `request_id` 更符合 Go 代码习惯。
- API JSON 当前已经大量使用 `camelCase`，例如 `productId`、`buildId`、`requestId`。为了前后端兼容，应继续保持 `camelCase`。
- 日志和监测字段使用 `snake_case`，便于日志系统、指标系统和查询语句处理。
- 不要把日志字段风格反向带入 API JSON；也不要把 API JSON 的 `camelCase` 强行带入日志字段。

### 3.6 日志打印规则

日志使用英文和结构化字段。规则是“底层包装，边界打印一次”：

- store/resolver/scheduler/artifact 层返回 error，不打印。
- API access log 每个请求打印一次，包含 `method`、`path`、`status`、`duration_ms`、`request_id`、`correlation_id`。
- API error log 只打印 5xx、502、503、504 等系统或依赖错误。
- 400、409、422 属于预期业务错误，默认不打 error log；必要时可进入 audit 或 info log。
- scheduler submit/stop 失败这类上游错误，应在 API 边界记录 cause，但 API response 只返回稳定 code 和用户可翻译字段。

推荐日志字段：

```go
slog.Error("api request failed",
	"error", err,
	"status", status,
	"error_code", code,
	"request_id", requestID,
	"correlation_id", correlationID,
)
```

## 4. 推荐错误结构

未来非 2xx JSON 错误推荐统一返回：

```json
{
  "error": {
    "code": "BUILD_RELEASE_GATE_BLOCKED",
    "messageKey": "errors.build.releaseGateBlocked",
    "message": "release gate blocked",
    "severity": "warning",
    "category": "business",
    "retryable": false,
    "userActionKey": "actions.fixReleaseGate",
    "userAction": "fix release gate blockers before publishing",
    "requestId": "req_xxx",
    "correlationId": "corr_xxx",
    "details": [
      {
        "code": "release.fixed-version",
        "field": "version",
        "stage": "release",
        "titleKey": "release.check.fixedVersion.title",
        "messageKey": "release.check.fixedVersion.message",
        "message": "release requires fixed version",
        "params": {
          "version": "latest"
        }
      }
    ]
  },
  "errors": [
    "release requires fixed version"
  ]
}
```

字段说明：

| 字段 | 说明 |
|---|---|
| `code` | 稳定错误码，前端展示、测试和监测的主依据 |
| `messageKey` | 前端 i18n 字典 key |
| `message` | 英文诊断文本或后端默认 fallback，不作为 UI 主文案 |
| `severity` | `info`、`success`、`warning`、`error`、`critical` |
| `category` | `validation`、`business`、`system`、`resource`、`security` |
| `retryable` | 客户端或用户重试是否合理 |
| `userActionKey` | 用户操作建议的 i18n key |
| `userAction` | 后端 fallback 操作建议 |
| `requestId` | 单次 HTTP 请求 ID |
| `correlationId` | 跨 API、scheduler、runner、artifact 的链路 ID |
| `details[].code` | 明细稳定 code，例如 release gate check id |
| `details[].params` | i18n 插值参数 |
| `errors` | 兼容旧客户端的明细文本数组 |

## 5. 语言分层

| 层级 | 语言 | 面向对象 | 规则 |
|---|---|---|---|
| 前端 toast / alert / inline / notification | 中文，未来可切换语言 | 普通用户、运维用户 | 通过 `code` / `details[].code` 查 i18n 字典 |
| API `message` | 英文诊断文本或 fallback | API 调用方、开发者 | 不作为前端主展示依据 |
| `userAction` fallback | 英文或默认语言 fallback | 无 i18n 客户端 | 前端应优先用 `userActionKey` 翻译 |
| 服务日志 | 英文 | 开发、运维 | 结构化输出，包含 requestId/correlationId |
| Build / Runner 日志 | 英文 | 构建排障人员 | 保留技术细节，不替代用户提示 |
| Audit event | 稳定英文事件名 | 审计、合规、UI 翻译 | 例如 `build.approved`、`release.published` |

## 6. HTTP 状态码

| 状态码 | 场景 | 默认展示 |
|---|---|---|
| `400` | JSON 格式错误、参数缺失、字段非法 | 表单 inline error 或 page alert |
| `404` | 产品、构建、上传文件不存在 | page alert |
| `409` | 状态冲突，例如构建状态不允许当前操作 | toast |
| `410` | 制品已过保留期 | alert 或 disabled download 状态 |
| `422` | 业务规则或门禁未通过 | page alert 或 gate panel |
| `429` | 限流、重复提交太频繁 | toast |
| `500` | 未预期服务端错误 | sticky page alert |
| `502/503/504` | scheduler、runner、存储或外部依赖失败 | notification + page alert |

成功状态：

| 状态码 | 场景 |
|---|---|
| `200` | 查询、普通状态变更成功 |
| `201` | 上传创建成功 |
| `202` | 构建请求已接受，进入异步流程 |
| `204` | Runner 无任务、删除类无返回 |

## 7. 错误 code

错误 code 使用稳定枚举。前端不能依赖英文 `message` 判断。

| code | 场景 |
|---|---|
| `VALIDATION_INVALID_JSON` | 请求 JSON 无法解析 |
| `VALIDATION_REQUIRED_FIELD` | 必填字段缺失 |
| `VALIDATION_INVALID_REQUEST` | 请求字段格式非法 |
| `MANIFEST_SCHEMA_INVALID` | Manifest 校验失败 |
| `MANIFEST_DRY_RUN_FAILED` | Manifest 试运行失败 |
| `RESOURCE_NOT_FOUND` | 资源不存在的通用 fallback |
| `BUILD_NOT_FOUND` | 构建不存在 |
| `BUILD_STATE_CONFLICT` | 构建状态不允许当前动作 |
| `BUILD_CREATE_INVALID_RELEASE_CHANNEL` | 直接创建 release 构建 |
| `BUILD_RELEASE_GATE_BLOCKED` | Release 门禁未通过 |
| `BUILD_PREVIEW_NOT_READY` | Preview 验证门禁未通过 |
| `BUILD_ARTIFACT_NOT_AVAILABLE` | 制品不可下载 |
| `ARTIFACT_EXPIRED` | 制品超过保留期限，不能下载 |
| `UPLOAD_TOO_LARGE` | 上传文件超过限制 |
| `UPLOAD_STORE_FAILED` | 上传存储失败 |
| `RUNNER_UNAVAILABLE` | Runner 不可用 |
| `SCHEDULER_SUBMIT_FAILED` | Scheduler 提交失败 |
| `STORAGE_UNAVAILABLE` | 存储不可用 |
| `UPSTREAM_UNAVAILABLE` | 上游依赖不可用的通用 fallback |
| `SYSTEM_INTERNAL_ERROR` | 未预期服务端错误 |
| `REQUEST_FAILED` | 其他请求失败 fallback |

## 8. 前端 i18n 与展示分流

前端应新增统一翻译层，例如 `frontend/src/lib/i18n.js`：

```js
const errorMessages = {
  BUILD_RELEASE_GATE_BLOCKED: "Release 门禁未通过",
  BUILD_PREVIEW_NOT_READY: "Preview 验证门禁未通过",
  BUILD_STATE_CONFLICT: "当前构建状态不允许执行该操作",
  ARTIFACT_EXPIRED: "制品已超过保留期限",
};

const errorDetails = {
  "release.fixed-version": "Release 需要固定版本，不能使用 latest",
  "release.exact-component-uris": "组件 URI 不能包含通配符",
  "release.no-build-error": "构建存在错误记录",
};
```

展示规则：

1. 优先使用 `error.code` 翻译主标题。
2. 优先使用 `details[].code` 翻译明细。
3. 如存在 `messageKey`，优先使用 `messageKey`。
4. 未知 code 才 fallback 到 `message`。
5. 原始英文 `message` 可放入“技术详情”，不放在 toast 主标题。

Surface 分流建议：

| code / category | 默认 surface |
|---|---|
| `BUILD_STATE_CONFLICT` | toast |
| `MANIFEST_SCHEMA_INVALID` | Manifest inline panel 或 page alert |
| `MANIFEST_DRY_RUN_FAILED` | Dry Run inline panel |
| `BUILD_RELEASE_GATE_BLOCKED` | Release gate panel / page alert |
| `BUILD_PREVIEW_NOT_READY` | Preview gate panel / page alert |
| `ARTIFACT_EXPIRED` | alert 或禁用下载态 |
| `SCHEDULER_SUBMIT_FAILED` | notification + page alert |
| `STORAGE_UNAVAILABLE` | notification + page alert |
| `critical` severity | sticky page alert |

## 9. Toast / Alert / Notification 边界

Toast 用于短暂、无需长期保留的操作反馈：

- 构建请求已创建。
- Manifest 已保存。
- 文件已上传。
- 已取消、已重试、已验证 Preview。
- `409 BUILD_STATE_CONFLICT`。

Alert 用于当前页面必须处理的问题：

- 表单校验失败。
- Manifest 校验失败。
- Release 门禁未通过。
- 构建详情加载失败。
- API 不可用导致当前页面无法展示。

Notification 用于异步后台事件和需要追踪的系统事件：

- 已落地：Scheduler 提交失败、构建失败、Preview 已生成等待验证、Preview 已验证、Release 发布完成。
- 后续补齐：Runner 离线、存储不可用、下载不可用、审计归档或签名失败。

当前前端已实现 toast、page alert 和 notification center。Notification center 使用持久通知 API，支持未读数、单条已读、全部已读和跳转构建详情；不替代 audit event，audit event 仍用于追责和审计。

## 10. RequestId 与 CorrelationId

当前 request middleware：

- 读取请求头 `X-Request-Id`；不存在时生成 `req_xxx`。
- 写回响应头 `X-Request-Id` 和 `X-Correlation-Id`。
- 将 requestId 放入 `APIError.requestId`。
- 服务端 access log 和 error log 都打印 requestId/correlationId。

当前 correlationId 规则：

- 请求头包含 `X-Correlation-Id` 时沿用调用方传入值。
- 请求头缺失时以 requestId 派生 `corr_xxx`。
- 创建、审批、拒绝、Preview 验证、Release 发布、重试、取消、runner claim、runner log append、heartbeat、complete 写入 Build log 时持久化 requestId/correlationId。
- audit event 同样持久化 requestId/correlationId。
- 上传 artifact metadata 持久化创建请求的 requestId/correlationId；下载请求通过 `upload.download` audit event 关联。

链路排查可以使用只读 Python CLI：

```bash
python scripts/correlation_trace_cli.py trace \
  --api-url http://127.0.0.1:8080 \
  --correlation-id corr_xxx
```

该命令会查询 `/api/audit-events`、`/api/builds`、`/api/uploads` 和 `/api/notifications`，并从审计事件、构建记录、构建日志、上传元数据和通知中汇总匹配的 requestId/correlationId。默认使用构建列表里的内嵌日志；如果需要强制读取每个构建的日志端点，可以加：

```bash
python scripts/correlation_trace_cli.py trace \
  --api-url http://127.0.0.1:8080 \
  --correlation-id corr_xxx \
  --scan-build-log-endpoints
```

当 `AUTH_ENABLED=true` 时，需要传入 viewer/operator/admin bearer token，或通过 `PACKAGING_API_TOKEN` / `AUTH_ADMIN_TOKEN` 环境变量提供 token。机器可读排查证据使用 `--json`，schema 为 `packaging.correlation_trace.report/v1`。

## 11. Build Log、Runner Log 与 Audit

Build log 当前是业务和技术日志的混合，字段包括：

```json
{
  "time": "2026-06-03T00:00:00Z",
  "level": "info",
  "eventType": "runner.heartbeat",
  "source": "runner-1",
  "runnerId": "runner-1",
  "buildId": "build-id",
  "requestId": "req_xxx",
  "correlationId": "corr_xxx",
  "errorCode": "",
  "stage": "package",
  "step": "dagger",
  "attempt": 1,
  "message": "dagger pipeline completed",
  "metadata": {}
}
```

独立 audit event 建议字段：

| 字段 | 说明 |
|---|---|
| `auditId` | 审计事件 ID |
| `time` | 事件时间 |
| `actorType` | `user`、`system`、`runner` |
| `actorId` | 操作者 ID；未做认证前可用 `system` |
| `action` | 稳定英文事件名，例如 `build.approved` |
| `resourceType` | `build`、`manifest`、`artifact` |
| `resourceId` | 资源 ID |
| `result` | `success` 或 `failure` |
| `errorCode` | 失败时的稳定错误码 |
| `requestId` | 请求 ID |
| `correlationId` | 链路 ID |
| `beforeHash` / `afterHash` | 变更摘要 |
| `ip` / `userAgent` | 来源信息 |
| `reason` / `comment` | 操作说明 |

## 12. Release Gate

Release gate 不作为普通错误处理。`GET /api/builds/{id}/release-check` 返回业务检查结果；`POST /api/builds/{id}/publish` 门禁失败时返回 `BUILD_RELEASE_GATE_BLOCKED`。

Release check item 应使用稳定 id：

```json
{
  "id": "release.fixed-version",
  "stage": "release",
  "title": "构建版本固定",
  "status": "blocked",
  "reason": "release requires a fixed build version"
}
```

后续建议扩展：

```json
{
  "id": "release.fixed-version",
  "stage": "release",
  "titleKey": "release.check.fixedVersion.title",
  "reasonKey": "release.check.fixedVersion.reason",
  "status": "blocked",
  "reason": "release requires a fixed build version",
  "params": {
    "version": "latest"
  }
}
```

前端展示应优先按 `id` 或 `reasonKey` 翻译，不依赖 `reason` 文本。

## 13. SSE 错误

`GET /api/builds/{id}/logs/stream` 是 `text/event-stream`，不能直接复用 JSON API error 响应。

建议 SSE error 事件使用结构化 payload：

```text
event: error
data: {"code":"RESOURCE_NOT_FOUND","message":"build not found","requestId":"req_xxx"}
```

前端实时日志组件应按 `code` 翻译错误标题，并保留原始 message 作为技术详情。

## 14. 监测与告警

`scripts/monitor_cli.py` 适合本地验收、CI smoke check 和部署后巡检。后端已提供基础 `/metrics` 和独立 audit event API；生产环境仍需要补充服务日志采集、告警规则和审计归档策略。

建议告警策略：

| 级别 | 条件 |
|---|---|
| P0 | `/api/health` 连续失败 |
| P0 | 5xx 错误率持续超过阈值 |
| P0 | 所有 Linux 或 Windows Runner 都 offline/stale |
| P1 | queued 构建积压持续超过阈值 |
| P1 | running 构建超过 runner timeout 或 heartbeat 长时间未更新 |
| P1 | artifact metadata contract 失败，例如缺少 `sha256/downloadUrl/downloadPolicy` |
| P1 | 上传/下载连续失败 |
| P1 | scheduler submit/stop 失败 |
| P2 | release gate blocked 数量升高 |
| P2 | expired artifacts 超过阈值 |
| P2 | failed builds 数量或比例异常升高 |
| P2 | SSE log stream 契约失败 |

## 15. 测试要求

后端测试：

- 表驱动断言典型错误的 `status`、`error.code`、`severity`、`category`、`retryable`、`requestId`。
- 断言 `details[].code`、`details[].field`、`details[].stage`、`errors` 兼容字段。
- 覆盖 `400`、`404`、`409`、`410`、`422`、`502`、`500`。
- 覆盖 SSE `event:error` 的结构化 payload。

前端测试：

- `ApiError` helper 覆盖新结构、旧结构、非 JSON fallback。
- UI 不依赖英文 `message`，后端返回英文 message 时仍按 `code` 显示中文。
- `details[].code` 能翻译 release gate 明细。
- 未知 code fallback 到后端 message。
- `BUILD_STATE_CONFLICT` 走 toast，不覆盖 page alert。
- `BUILD_RELEASE_GATE_BLOCKED` 走 alert 或 gate panel。

E2E 测试：

- Manifest 保存返回 `MANIFEST_SCHEMA_INVALID` 时展示 alert、details 和 user action。
- Approve 返回 `BUILD_STATE_CONFLICT` 时展示 toast，页面保持可操作。
- Release publish 返回 `BUILD_RELEASE_GATE_BLOCKED` 时展示 gate detail。

## 16. 实施顺序

建议按以下顺序推进：

1. 后端新增错误注册表，统一 `code -> status/severity/category/retryable/messageKey/defaultMessage/userActionKey`。
2. 后端扩展 `APIError` 和 `APIErrorDetail`，加入 `messageKey`、`userActionKey`、`params`、`correlationId`。
3. 增加 `internal/apperr.AppError`，让 store/resolver/service 构造 typed error，API 层统一映射。
4. 增加 requestId/correlationId middleware，并写入响应头和日志。
5. 前端新增 i18n 字典，`normalizeError()` 改为 `code/detail.code` 驱动。
6. 建立 `code -> surface` 分流表，逐步接入 notification center。
6. 补后端错误契约测试、前端 i18n 单测和 E2E 错误路径。
7. 后续为 audit event 补归档/签名策略，结构化 BuildLog，并为 `/metrics` 补生产告警规则。
