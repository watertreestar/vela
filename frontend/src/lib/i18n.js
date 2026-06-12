const errorMessages = {
  VALIDATION_INVALID_JSON: "请求 JSON 无法解析",
  VALIDATION_REQUIRED_FIELD: "缺少必填字段",
  VALIDATION_INVALID_REQUEST: "请求参数不正确",
  MANIFEST_SCHEMA_INVALID: "Manifest 校验失败",
  MANIFEST_DRY_RUN_FAILED: "Manifest 试运行失败",
  RESOURCE_NOT_FOUND: "资源不存在",
  BUILD_NOT_FOUND: "构建不存在",
  BUILD_STATE_CONFLICT: "当前构建状态不允许执行该操作",
  BUILD_CREATE_INVALID_RELEASE_CHANNEL: "不能直接创建 Release 构建",
  BUILD_RELEASE_GATE_BLOCKED: "Release 门禁未通过",
  BUILD_PREVIEW_NOT_READY: "Preview 验证门禁未通过",
  BUILD_ARTIFACT_NOT_AVAILABLE: "构建制品不可下载",
  ARTIFACT_EXPIRED: "制品已超过保留期限",
  UPLOAD_TOO_LARGE: "上传文件超过限制",
  UPLOAD_STORE_FAILED: "上传存储失败",
  RUNNER_UNAVAILABLE: "Runner 不可用",
  SCHEDULER_SUBMIT_FAILED: "调度器提交失败",
  SCHEDULER_STOP_FAILED: "调度器停止任务失败",
  STORAGE_UNAVAILABLE: "存储服务不可用",
  UPSTREAM_UNAVAILABLE: "上游依赖不可用",
  SYSTEM_INTERNAL_ERROR: "服务端内部错误",
  API_UNAVAILABLE: "无法连接后端 API",
  REQUEST_FAILED: "请求失败",
};

const messageKeys = {
  "errors.validation.invalidJson": "请求 JSON 无法解析",
  "errors.validation.requiredField": "缺少必填字段",
  "errors.validation.invalidRequest": "请求参数不正确",
  "errors.manifest.schemaInvalid": "Manifest 校验失败",
  "errors.build.stateConflict": "当前构建状态不允许执行该操作",
  "errors.build.invalidReleaseChannel": "不能直接创建 Release 构建",
  "errors.build.releaseGateBlocked": "Release 门禁未通过",
  "errors.build.previewNotReady": "Preview 验证门禁未通过",
  "errors.build.artifactNotAvailable": "构建制品不可下载",
  "errors.artifact.expired": "制品已超过保留期限",
  "errors.scheduler.submitFailed": "调度器提交失败",
  "errors.scheduler.stopFailed": "调度器停止任务失败",
  "errors.upstream.unavailable": "上游依赖不可用",
  "errors.system.internal": "服务端内部错误",
  "errors.api.unavailable": "无法连接后端 API",
  "errors.request.failed": "请求失败",
};

const userActionKeys = {
  "actions.fixManifestSchema": "请修复 Manifest 校验错误后再保存",
  "actions.createPreviewFirst": "请先创建 Preview 构建，验证通过后再转 Release",
  "actions.fixReleaseGate": "请先修复阻塞项后再发布",
  "actions.validatePreview": "请先完成 Preview 构建并确认制品可下载",
  "actions.checkApiService": "请确认后端服务、PostgreSQL 和对象存储依赖已启动后重试",
};

const detailMessages = {
  "preview.succeeded": "需要先完成 Preview 构建",
  "preview.channel": "Preview 验证只接受 preview 通道",
  "preview.artifact": "Preview 制品不可下载",
  "preview.build-lock": "构建锁未生成",
  "preview.lock-identity": "构建锁与构建参数不一致",
  "preview.required-components": "必需组件还没有锁定 URI",
  "release.preview-validated": "需要先验证 Preview",
  "release.fixed-version": "Release 需要固定版本，不能使用 latest",
  "release.exact-component-uris": "组件 URI 不能包含通配符",
  "release.fixed-component-versions": "组件版本不能使用 latest",
  "release.artifact": "Release 制品不可下载",
  "release.no-build-error": "构建存在错误记录",
};

const detailTitleMessages = {
  "preview.succeeded": "Preview 构建已成功",
  "preview.channel": "构建通道为 Preview",
  "preview.artifact": "Preview 制品可下载",
  "preview.build-lock": "构建锁已生成",
  "preview.lock-identity": "构建锁与构建参数一致",
  "preview.required-components": "必需组件已锁定 URI",
  "release.preview-validated": "Preview 已验证",
  "release.fixed-version": "构建版本固定",
  "release.exact-component-uris": "组件 URI 精确",
  "release.fixed-component-versions": "组件版本固定",
  "release.artifact": "Release 制品可下载",
  "release.no-build-error": "构建无错误记录",
};

const legacyReasonMessages = {
  "only succeeded preview builds can be validated": "需要先完成 Preview 构建",
  "preview validation requires preview channel": "Preview 验证只接受 preview 通道",
  "preview artifact is not available": "Preview 制品不可下载",
  "Build Lock is not available": "构建锁未生成",
  "Build Lock does not match build target, profile, or version": "构建锁与构建参数不一致",
  "preview requires locked required component URIs": "必需组件还没有锁定 URI",
  "only validated preview builds can be published": "需要先验证 Preview",
  "release requires a fixed build version": "Release 需要固定版本，不能使用 latest",
  "release requires exact component URIs without wildcards": "组件 URI 不能包含通配符",
  "release requires fixed component versions": "组件版本不能使用 latest",
  "release artifact is not available": "Release 制品不可下载",
  "release requires a build without errors": "构建存在错误记录",
};

const errorSurfaces = {
  BUILD_STATE_CONFLICT: "toast",
  MANIFEST_SCHEMA_INVALID: "alert",
  MANIFEST_DRY_RUN_FAILED: "alert",
  BUILD_RELEASE_GATE_BLOCKED: "alert",
  BUILD_PREVIEW_NOT_READY: "alert",
  ARTIFACT_EXPIRED: "alert",
  SCHEDULER_SUBMIT_FAILED: "alert",
  SCHEDULER_STOP_FAILED: "alert",
  STORAGE_UNAVAILABLE: "alert",
  UPSTREAM_UNAVAILABLE: "alert",
  API_UNAVAILABLE: "alert",
};

export function translateError(error) {
  if (!error) return "操作失败";
  return (
    messageKeys[error.messageKey] ||
    errorMessages[error.code] ||
    translateLegacyReason(error.message) ||
    error.message ||
    "操作失败"
  );
}

export function translateUserAction(error) {
  if (!error) return "";
  return userActionKeys[error.userActionKey] || translateLegacyReason(error.userAction) || error.userAction || "";
}

export function translateErrorDetail(detail) {
  if (!detail) return { title: "", message: "" };
  return {
    ...detail,
    title: detailTitleMessages[detail.code] || messageKeys[detail.titleKey] || detail.title || "",
    message:
      detailMessages[detail.code] ||
      messageKeys[detail.messageKey] ||
      translateLegacyReason(detail.message) ||
      detail.message ||
      "",
  };
}

export function translateLegacyReason(reason) {
  const normalized = String(reason || "").trim();
  if (!normalized) return "";
  return legacyReasonMessages[normalized] || normalized;
}

export function errorSurface(error) {
  if (!error) return "alert";
  if (errorSurfaces[error.code]) return errorSurfaces[error.code];
  if (error.status === 409) return "toast";
  if (error.severity === "critical") return "alert";
  return "alert";
}
