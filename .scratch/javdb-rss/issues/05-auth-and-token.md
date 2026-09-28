# 鉴权打通：token 怎么放、怎么导出、失效怎么办

Type: task
Status: open
Blocked by: 02

> ## 部分已解（2026-09-28，来自 ticket 01）
>
> 原 Q1/Q2/Q3 已有一手证据，**不需要重新试探**：
>
> - **存放位置**：`Authorization: Bearer <token>` 请求头（`client/transport.go:165`）。
> - **不参与签名**：`jdsignature` 只由 `ts` + 硬编码 Prefix/Suffix 算出，**与 token 无关**。
> - **发布形式**：token 是 JWT（服务端错误名为 `JWTVerificationError`）。
> - **失效特征**：错误 `action` 属于
>   `JWTVerificationError` / `Unauthorized` / `LoginRequired` / `TokenInvalid` / `TokenExpired`
>   之一 → 可直接据此在日志里给出「去重新导出 token」的提示。（实测无 token 时返回
>   `JWTVerificationError: Invalid Signature`。）
> - 公共参数里的 `device_uuid` 取值任意（实测用一个饼字串 UUID 即可）；它是否需与 token
>   同源**仍未确认**（Q3 的剩余部分）。
>
> 因此本 ticket 真正剩下的是：**怎么从 App 里把 token 导出**（Q4，HITL）+ 验证它真能用。

## Question

用户选了「手工从 App 导出 token」。那么具体是导出什么、放到哪、过期怎么发现？

要回答：

1. **token 到底是什么**：调 `/api/v1/sessions` 之后服务端给的什么字段？
   在这之前先从 `libapp.so` 的字符串里定位 `TokenInterceptor` / `AuthInterceptor` 用的
   存储键名和 header 名（已知有 `authorization`、`setToken`/`getToken`）。
2. **放哪**：header（`Authorization: Bearer ...`？自定义 header？）还是 query 参数？
   `jdsignature` 的计算是否**包含** token（若包含，顺序与拼法要一并确定）。
3. **配套字段**：`deviceId` 是不是必须与 token 同源？`user-agent` / `app_version` 服务端是否校验？
   换一个 `deviceId` 用同一个 token 会不会被拒？
4. **怎么导出**（HITL，需要用户动手）：给用户一份**准确到可照抄**的步骤。
   优先选不需要 root 的路径 —— 例如 App 自己的本地存储（`sqflite` / `hive` / `shared_preferences`），
   在需要时再用 Android 备份或 adb 取。步骤要包含「导出后怎么验证它是活的」。
5. **失效表现**：token 过期 / 被顶下线时，接口返回什么？（HTTP 码 + 错误信封）
   服务端要怎么识别并在日志里给出**可操作的**提示，而不是静默返回空 feed。
6. **单账号多设备**：登录会不会踢掉 App 上的会话？

## 产出

- `notes/auth.md`：header 名、payload 形态、deviceId 处理、失效特征
- 给用户的导出步骤（可直接执行的清单）
- 至少一个用真实 token 打通的端点记录
