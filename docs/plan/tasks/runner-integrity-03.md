---
id: runner-integrity-03
package: conductor, runner
module: trusted-runner-output
status: pending
depends-on: [runner-integrity-02]
---

# PR-3：Runner 专用上传、制品归属与可信成功状态

## Objective

提供 capability-bound final/checksum upload，并只允许服务端验证的绑定 output 让 Build 转为 `succeeded`；Preview/Release/evidence 均以权威 upload binding 为准。

## Context

- [Phase 7 执行契约](../../phase7-execution-contract.md)
- 依赖 PR-2 的 execution authorization 和 Build attempt fencing。

## Required implementation

1. Build/Upload 增加 structured ownership，Postgres unique/index/migration 与 JSON Store 等价约束。
2. 增加 final/checksum Runner endpoints；保存时计算 hash，checksum 格式与 final hash/name 强验证，禁止 client metadata/URI/policy override。
3. successful complete 从 verified bindings 回填 URI/URL/hash/size；不接收 URI/URL/digest。object readback 失败/篡改/缺 checksum 必须阻断。
4. 让 preview/release/download/report/evidence 以 `ArtifactUploadID` 工作；legacy 历史保留下载并标 `legacy/unverified`，但不能走新 verification。
5. 收紧 `/api/uploads` list，保持 common upload 仅 operator/admin。
6. 更新 Runner 调用与完整正/负测试。

## Verification

```bash
cd conductor && go test ./...
cd ../runner && bun test && bun run check
```
