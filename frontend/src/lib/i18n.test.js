import { describe, expect, it } from "vitest";
import { errorSurface, translateError, translateUserAction } from "./i18n";

describe("i18n auth errors", () => {
  it("translates authentication and authorization errors", () => {
    const required = {
      code: "AUTH_REQUIRED",
      status: 401,
      messageKey: "errors.auth.required",
      userActionKey: "actions.signIn",
    };
    const forbidden = {
      code: "AUTH_FORBIDDEN",
      status: 403,
      messageKey: "errors.auth.forbidden",
      userActionKey: "actions.requestPermission",
    };

    expect(translateError(required)).toBe("请先登录后再继续");
    expect(translateUserAction(required)).toBe("请登录后重试");
    expect(errorSurface(required)).toBe("alert");
    expect(translateError(forbidden)).toBe("当前账号没有执行该操作的权限");
    expect(translateUserAction(forbidden)).toBe("请联系管理员开通所需权限");
    expect(errorSurface(forbidden)).toBe("toast");
  });
});
