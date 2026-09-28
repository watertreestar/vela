# Phase 7：构建执行闭环、Runner 鉴权与真实 Release

> 状态：Finalized  
> 交付分支：`phase7/runner-integrity`  
> 基线：`6ec7a6f1e8f425e1d7b51c3cdfc016c688b01140`

## 目标

Phase 7 将构建执行链路收敛为：每次已审批 Build 都由其 Nomad dispatch 指定的 Runner 执行；Runner 只能更新自己当前 attempt；`succeeded` 只能引用服务端验证并绑定的 final artifact 与 checksum。

## 固定约定

### Runner 凭据

1. `AUTH_RUNNER_TOKEN` 是第一因子，只由 Conductor 验证，绝不进入 `Task.Env`、Build、审计、Runner workspace 或包。
2. Conductor 为每个真实 Nomad dispatch 在该 Job 的 task-owned Nomad Variable 路径创建包含 runner bearer 与 execution capability 的小型 Variable。Nomad task workload identity 只读自己的 `nomad/jobs/{jobId}/builder/package/...` 路径。
3. Job template 以 `nomadVar` 将两个值渲染到 `$NOMAD_SECRETS_DIR` 下的文件。Linux permissions 为 `0400`；Windows 使用 Nomad allocation secrets directory ACL，不将 `0400` 描述为 Windows ACL。
4. `RUNNER_EXECUTION_HMAC_KEY` 只由 Conductor 持有，不能复用会话或审计 HMAC key。capability claims 包含 `typ`、`aud`、`buildId`、`attempt`、`target`、`runnerId`、`jti`、`iat` 与 `exp`。
5. raw capability 不存入 Build、Store、日志、审计或 E2E 报告；Store 只保存 dispatch identity、JTI 和 expiry。

### 执行状态

```text
pending_approval|rejected|failed|canceled
  -> dispatching -> queued -> running -> succeeded|failed|canceled
succeeded -> preview_validated -> published
```

- `Build.Attempt` 是 dispatch generation，初始为 `0`，approve/retry 每次递增。
- Job ID：`pkg-{productId}-{buildId}-a{attempt}`。
- Store 先持久化 `dispatching` 与 attempt/预期 Runner/Job/capability metadata，再创建 Nomad Variable 和提交 Job；提交成功才转 `queued`。
- 失败、取消、超时、terminal completion 使 capability 失效；Variable 删除是 best effort，服务端状态与 attempt fencing 是最终防线。

### Runner API

所有 build-scoped Runner 写操作都需 static bearer + `X-Runner-Execution` capability，并验证 path Build ID、attempt、target、预期 runner ID 与 Build 状态：

```text
POST /api/runner/tasks/{buildId}/claim
POST /api/builds/{buildId}/logs
POST /api/runner/tasks/{buildId}/heartbeat
POST /api/runner/tasks/{buildId}/artifacts/final
POST /api/runner/tasks/{buildId}/artifacts/checksum
POST /api/runner/tasks/{buildId}/complete
```

Runner 不再按 target 拉取全局队列。`BUILD_ID` 缺失、claim 返回 ID 不同或 target 不同均 fail fast。

### 输入与输出真实性

- 对 platform `local-upload://` inputs，execution capability 只能读取本 Build Lock 指定的 upload；Runner 下载后验证 locked SHA-256。
- 通用 `POST /api/uploads` 继续只允许 operator/admin。Runner 只能使用 Build-scoped final/checksum endpoints。
- Build 保存权威 `ArtifactUploadID`、`ArtifactSHA256`、`ArtifactSize`、`ChecksumUploadID`、`ArtifactVerifiedAt`。
- Server 保存 final 时计算 hash；complete 前重新读回并验证 final 与 checksum `<sha256>  <fileName>`。successful complete 不再接收 URI/URL/digest。
- Preview/Release gate、download、reproducibility report、release evidence 以 `ArtifactUploadID` 为权威关联；legacy Build 保留下载，但标记 `legacy/unverified`，不能通过新的 Preview/Release verification。

## 四个 PR

| PR | 目标 | 依赖 | 完成条件 |
|---|---|---|---|
| PR-1 | Runner bearer 文件读取与 Nomad secrets 投递 | 无 | auth-enabled Job 不把 bearer 放 Env；Runner 请求有 bearer |
| PR-2 | Build/attempt dispatch 与 capability 执行绑定 | PR-1 | 不存在 target-wide claim 或跨 Build runner 写入；locked input 下载有 hash 验证 |
| PR-3 | Runner-only output upload 与可信 complete | PR-2 | `succeeded` 必有服务端 readback 验证的 final/checksum 绑定 |
| PR-4 | Auth-enabled Linux/Windows E2E、readiness、文档 | PR-3 | Linux E2E 自动通过；Windows host 有可执行 live gate 与 non-leak checks |

## 共同验收

- 任意 runner bearer 没有正确 capability，不能 claim/log/heartbeat/upload/complete 或读取另一个 Build 的 input。
- retry 的旧 capability 不能影响新 attempt。
- runner、Nomad Job 序列化、allocation logs、Build logs、audit events、E2E 输出均不能出现 bearer、capability 或 HMAC key。
- 不将 auth-disabled local mode 宣称为生产安全验收。

## 消融检查

- 不能删 execution capability：共享 static bearer 无法阻断跨 Build 伪造。
- 不能删 service-side artifact binding/readback：仅让 Runner 上传或自报 URI 无法证明 output 属于当前 Build。
- 不需要新的人类角色或放宽通用 uploads：Runner 专用 build-scoped endpoints 已覆盖最小权限。
