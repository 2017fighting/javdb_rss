# 配置与部署模型

Type: grilling
Status: open

## Question

这个服务最终怎么被跑起来、怎么被改配置？

给人类的问题（需要用户回答，不要替他决定）：

1. **跑在哪**：NAS / 家用服务器 / 容器 / 裸机？有没有现成的反向代理（Caddy / Nginx / Traefik）？
   是否已有 Docker 或 systemd 的部署习惯？
2. **谁能访问**：只在内网，还是 qBittorrent 跑在另一台机器/另一个网络？
   这决定 feed URL 要不要带 secret、要不要 TLS。
3. **配置怎么改**：改文件后重启？改文件热重载？还是一个 `/reload`？
   订阅会频繁增删吗？
4. **日志与可观测**：需要看到什么？（轮询失败、签名失效、接口字段变了）
   要不要把失败也变成一个「feed item」或者通知到别处？
5. **单二进制 vs 容器**：用户选了 Go 单二进制，但要不要**顺带**提供一个 Dockerfile？

## 为什么这是 grilling 而不是 task

这些是用户的价值取舍，不是事实。给推荐答案，但等用户拍板。

## 参考推荐

- 配置：单个 YAML，启动时读 + `SIGHUP` 重载（比 watch 文件简单，比重启体验好）
- 部署：裸二进制 + 可选 Dockerfile；不强制
- feed 鉴权：URL 里带一个 secret path 段（`/rss/<secret>/code/ABC-123.xml`），
  在内网场景下够用且比 Basic Auth 对 qBittorrent 友好
- 日志：结构化 stdout，签名失效/接口契约变化做成显式告警级日志

## 产出

写进 map 的 Decisions-so-far，并把需要落到骨架里的部分回写到 ticket 03。
