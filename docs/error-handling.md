# 错误处理与前端提示体系

本文定义 API 错误契约、HTTP 状态码、前端展示分流和测试要求。目标是让错误可展示、可测试、可审计，而不是依赖不稳定的字符串判断。

## API 错误结构

非 2xx JSON 错误统一返回：

```json
{
  "error": {
    "code": "BUILD_RELEASE_GATE_BLOCKED",
    "message": "Release 门禁未通过",
    "severity": "warning",
    "category": "business",
    "retryable": false,
    "userAction": "请先修复阻塞项后再发布",
    "requestId": "req_xxx",
    "details": [
      {
        "code": "release.fixed-version",
        "field": "version",
        "stage": "release",
        "title": "构建版本固定",
        "message": "Release 需要固定版本，不能使用 latest"
      }
    ]
  },
  "errors": [
    "Release 需要固定版本，不能使用 latest"
  ]
}
```

`errors` 是兼容字段，便于旧调用方和简单客户端展示多条明细。新前端应优先读取 `error.details`。

## HTTP 状态码

| 状态码 | 场景 | 前端默认展示 |
|---|---|---|
| `400` | JSON 格式错误、参数缺失、字段非法 | 表单 inline error 或页面 alert |
| `404` | 产品、构建、上传文件不存在 | 页面 alert |
| `409` | 状态冲突，例如构建状态不允许当前操作 | toast + 保持页面 |
| `422` | 业务规则或门禁未通过 | 页面 alert 或门禁详情面板 |
| `429` | 限流、重复提交太频繁 | toast |
| `500` | 未预期服务端错误 | sticky page alert |
| `502/503/504` | runner、scheduler、存储或外部依赖失败 | notification + page alert |

成功状态：

| 状态码 | 场景 |
|---|---|
| `200` | 查询、普通状态变更成功 |
| `201` | 上传创建成功 |
| `202` | 构建请求已接受，进入异步流程 |
| `204` | runner 无任务、删除类无返回 |

## 错误 code

错误 code 使用稳定枚举，前端不能依赖英文 message 判断。

| code | 场景 |
|---|---|
| `VALIDATION_INVALID_JSON` | 请求 JSON 无法解析 |
| `VALIDATION_REQUIRED_FIELD` | 必填字段缺失 |
| `MANIFEST_SCHEMA_INVALID` | Manifest 校验失败 |
| `MANIFEST_DRY_RUN_FAILED` | Manifest 试运行失败 |
| `BUILD_NOT_FOUND` | 构建不存在 |
| `BUILD_STATE_CONFLICT` | 构建状态不允许当前动作 |
| `BUILD_CREATE_INVALID_RELEASE_CHANNEL` | 直接创建 release 构建 |
| `BUILD_RELEASE_GATE_BLOCKED` | Release 门禁未通过 |
| `BUILD_PREVIEW_NOT_READY` | Preview 验证门禁未通过 |
| `BUILD_ARTIFACT_NOT_AVAILABLE` | 制品不可下载 |
| `ARTIFACT_EXPIRED` | 制品超过保留期限，不能下载 |
| `UPLOAD_TOO_LARGE` | 上传文件超过限制 |
| `UPLOAD_STORE_FAILED` | 上传存储失败 |
| `RUNNER_UNAVAILABLE` | runner 不可用 |
| `SCHEDULER_SUBMIT_FAILED` | scheduler 提交失败 |
| `STORAGE_UNAVAILABLE` | 存储不可用 |
| `SYSTEM_INTERNAL_ERROR` | 未预期服务端错误 |

## severity 与展示

| severity | 含义 | 默认展示 |
|---|---|---|
| `info` | 非阻塞状态提示 | toast |
| `success` | 操作成功 | toast |
| `warning` | 可继续但有风险 | inline alert / panel alert |
| `error` | 当前操作失败，但页面仍可用 | toast 或 alert |
| `critical` | 页面数据不可用或系统依赖不可用 | sticky page alert |

## Toast / Alert / Notification 边界

Toast 用于短暂、无需长期保留的操作反馈：

- 构建请求已创建
- Manifest 已保存
- 文件已上传
- 已取消、已重试、已验证 Preview
- `409` 状态冲突

Alert 用于当前页面必须处理的问题：

- 表单校验失败
- Manifest 校验失败
- Release 门禁未通过
- 构建详情加载失败
- API 不可用导致当前页面无法展示

Notification 用于异步后台事件和需要追踪的系统事件：

- Runner 离线
- Scheduler 提交失败
- 构建失败或完成
- Preview 已生成等待验证
- Release 发布完成
- 存储不可用、下载不可用

当前前端已实现 toast 与 page alert 的分流；通知中心后续实现时应复用同一套 `ApiError` / event 结构。

## 前端处理规则

前端 API helper 抛出结构化 `ApiError`：

```js
{
  status,
  code,
  message,
  severity,
  category,
  retryable,
  userAction,
  requestId,
  details
}
```

默认分流：

| 操作 | 成功 | 失败 |
|---|---|---|
| 刷新数据 | 手动刷新才 toast | page alert |
| 创建构建 | toast | alert，字段错误 inline |
| Manifest 保存 | toast | alert + details |
| Manifest 试运行 | inline alert/panel | inline alert/panel |
| 覆盖配置保存 | toast | alert |
| 上传文件 | toast | toast 或 alert，大文件用 alert |
| 审批/确认/取消/重试 | toast | toast，409 用 toast |
| 验证 Preview | toast | alert，显示 gate details |
| 转 Release | notification + toast | alert，显示 gate details |
| runner/scheduler 故障 | notification | notification + page alert |

## Release 门禁

Release gate 不作为普通错误处理。`POST /api/builds/{id}/publish` 门禁失败时应返回 `BUILD_RELEASE_GATE_BLOCKED` 和 details。前端展示为发布门禁 alert 或门禁面板，不只显示 toast。

## 测试要求

- 后端测试应断言结构化错误的 `code`、`status`、`details`。
- 前端 API helper 测试应覆盖新旧错误格式。
- UI 测试应覆盖 Manifest 校验失败、Release gate blocked、构建状态冲突。
- E2E 测试应至少覆盖一个 toast 成功场景和一个 alert 失败场景。
