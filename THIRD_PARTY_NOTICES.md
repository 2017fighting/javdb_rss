# 第三方代码与归属

## javdb-cli

本项目的 `jdsignature` 实现（`internal/appapi/signature.go`）包含两个常量与一段
三段式签名拼接逻辑，来自开源项目 **javdb-cli**：

- 项目：https://github.com/FlanChanXwO/javdb-cli
- 原始文件：`internal/javdb/protocol/signature/sign.go`
- 许可：**MIT License**
- 版权：Copyright (c) FlanChanXwO

该项目把 `jdsignature` 从 JavDB.apk **1.9.28** 中逆向出来。

### 我们的改动

没有直接依赖该项目的 SDK（那会引入 37 个模块，并把返回类型退化成 `map[string]any`），
而是把它逆向出的算法拷贝进本项目，并：

1. 用 **1.9.35** 的服务端重新做了实测验证（2026-09-28）；
2. 补上了失效检测（`internal/appapi/probe.go`、`internal/health/`）——
   这是原项目没有的，因为签名常量派生自 App 内的 access key，App 升级或服务端轮换
   都可能让它失效；
3. 把两种签名失败形态（`InvalidSignature` / `ParameterInvalid`）都纳入了告警分类。

### MIT 许可要求

MIT 许可要求在任何副本中保留版权声明与许可声明。算法与常量本身是事实性的
逆向结果而非创意作品，但我们仍然在此明确归属，并把上游项目标注为
「Prefix 失效时首先应当去查看的地方」。

上游许可证全文见其仓库：https://github.com/FlanChanXwO/javdb-cli/blob/main/LICENSE

## 其他

本项目其余代码（RSS 渲染、配置、路由、健康检查、领域模型）全部为原创。
唯一的外部依赖是 `gopkg.in/yaml.v3`（MIT / Apache-2.0 双许可）。
