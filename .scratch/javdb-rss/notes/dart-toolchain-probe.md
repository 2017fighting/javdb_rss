# Dart AOT 逆向工具链探测与备灾记录

> **定位说明**：当前关键路径已由先例开源实现（`github.com/FlanChanXwO/javdb-cli`）证实：1.9.35 服务端签名校验算法为 `jdsignature = "{ts}.{suffix}.{md5(ts + prefix)}"`，旧版 Prefix/Suffix 仍有效且已实测连通。因此本轮不需要强行静态逆向 `libapp.so`。
> 本文档记录当前容器环境的 Dart AOT 分析能力与前置约束，作为**未来 JavDB App 升级导致签名失效时**，重新逆向所需的环境准备清单与避坑指南。

---

## 1. Dart SDK 版本与 Snapshot 形态（实测硬事实）

### 1.1 Dart SDK 版本串与 Snapshot Hash

通过对 APK 中解压出的 `libflutter.so` 与 `libapp.so` 进行探测：

```bash
$ strings libflutter.so | grep -iE "(flutter [0-9]|dart [0-9]|engine [0-9]|[0-9]+\.[0-9]+\.[0-9]+)" | head -n 10
Enable deprecated dart:cli waitFor. This feature will be fully removed in Dart 3.4 release. See https://dartbug.com/52121.
3.5.4 (stable) (Wed Oct 16 16:18:51 2024 +0000) on "android_arm64"
Linker: Fuchsia LLD 18.0.0
Android (10552028, +pgo, +bolt, +lto, -mlgo, based on r487747d) clang version 17.0.2 (https://android.googlesource.com/toolchain/llvm-project d9f89f4d16663d5012e5c09495f3b30ece3d2362)
```

- **Dart SDK 版本**：`3.5.4 (stable) (Wed Oct 16 16:18:51 2024 +0000)`
- **引擎架构**：`android_arm64`

### 1.2 VM Snapshot 头部特征与 Hash

读取 `libapp.so` 的 `_kDartVmSnapshotData` 导出符号（偏移 `0x200`）：

```bash
$ readelf -sW libapp.so | grep -E "(Dart|Snapshot|_k)"
     1: 0000000000490000 92944 OBJECT  GLOBAL DEFAULT    7 _kDartVmSnapshotInstructions
     2: 00000000004a6b40 0x7c4340 OBJECT  GLOBAL DEFAULT    7 _kDartIsolateSnapshotInstructions
     3: 0000000000000200 16032 OBJECT  GLOBAL DEFAULT    2 _kDartVmSnapshotData
     4: 00000000000040c0 0x47fbe0 OBJECT  GLOBAL DEFAULT    2 _kDartIsolateSnapshotData
     5: 00000000000001c8    32 OBJECT  GLOBAL DEFAULT    1 _kDartSnapshotBuildId

$ xxd -s 0x200 -l 64 libapp.so
00000200: f5f5 dcdc c238 0000 0000 0000 0300 0000  .....8..........
00000210: 0000 0000 3830 6134 3963 3731 3131 3038  ....80a49c711108
00000220: 3831 3030 6132 3333 6232 6165 3738 3865  8100a233b2ae788e
00000230: 3166 3438 7072 6f64 7563 7420 6e6f 2d63  1f48product no-c
```

- **Magic**：`0xdcdcf5f5`
- **Snapshot Hash (32-byte hex)**：`80a49c7111088100a233b2ae788e1f48`
- **Features**：`product no-code_comments no-dwarf_stack_traces_mode dedup_instructions no-tsan`

### 1.3 关键误区澄清：`_signPrefix` / `_signSuffix` 的真实来源

实测探测发现：
```
000b752b: _signPrefix@1419441731
000dce23: _signSuffix@1419441731
```
其中后缀 `@1419441731` 是 Dart Library ID。反查同 Library ID 的符号：
`NumberFormat._@1419441731`、`_decimalSeparator@1419441731`、`_formatExponential@1419441731` 等。
**结论**：`_signPrefix` 和 `_signSuffix` 属于标准国际化库 `package:intl/src/intl/number_format.dart` 中格式化数字正负号（sign）的内部字段，**与 API 的 `jdsignature` 没有任何关联**。后人切勿望文生义。

---

## 2. 环境工具可得性矩阵

当前容器操作系统为 **Arch Linux (rolling)**，内核 6.6.87.2-microsoft-standard-WSL2。

| 工具 / 依赖 | 状态 | 详细路径 / 版本 | 探测证据 |
|---|---|---|---|
| **网络可达性** | ✅ 可用 | GitHub / PyPI 均正常 | `curl -I https://github.com` (HTTP 200), `pip download requests` 成功 |
| **系统包管理器** | ✅ 可用 | `/usr/bin/pacman` (root 权限) | 可直连 Arch 官方源查询与下载 |
| **C/C++ 编译器** | ✅ 已安装 | GCC 15.2.1 (`/usr/bin/gcc`, `/usr/bin/g++`) | `which gcc g++ make` |
| **构建系统 (CMake)** | ✅ 已安装 | CMake 4.4.3 (`/usr/bin/cmake`) | `which cmake` |
| **构建系统 (Ninja)** | ❌ 缺失 | 官方仓库有包 `ninja` (约 1.5MB) | `pacman -Si ninja` (Version: 1.13.1-1) |
| **Clang / LLVM** | ⚠️ 部分 | `llvm-libs 22.1.8-2` 在，无 `clang` CLI | `pacman -Si clang` (Download: ~45MB, Install: ~220MB) |
| **Capstone 反汇编库** | ✅ 已安装 | C 头文件与 `.so` 均全；Python 绑定已在 | `/usr/lib/libcapstone.so.5`, `/usr/include/capstone/arm64.h`, `capstone 5.0.7` |
| **逆向分析工具** | ✅ 已安装 | Radare2 6.2.0, Rabin2, JADX (`/usr/bin/jadx`) | `jadx`、`radare2` 实测可用 |
| **Dart SDK** | ❌ 缺失 | 系统无 dart CLI | pacman extra 仓库无官方 dart 包，需从 Dart 官网下载对应版本二进制 |
| **Flutter SDK** | ❌ 缺失 | 系统无 flutter CLI | 需通过 git / tarball 安装 |
| **Blutter** | ❌ 缺失 | 未安装 | 依赖匹配的 Dart SDK 版本头文件与源码结构 |

---

## 3. 未来备灾：重做 Dart 静态逆向所需的前置清单

若未来 App 协议升级导致常量或算法变更，需要对 `libapp.so` 重新做静态快照分析，应按以下步骤准备工具链：

1. **补齐基础构建组件（约 2 分钟）**：
   ```bash
   pacman -Sy --noconfirm ninja
   ```
2. **下载匹配的 Dart SDK 源码/头文件**：
   - 目标 Dart 版本：必须与 Snapshot 内部版本一致（当前版本为 `3.5.4`，Hash `80a49c7111088100a233b2ae788e1f48`）。
   - 从 `https://storage.googleapis.com/dart-archive/channels/stable/release/3.5.4/sdk/dartsdk-linux-x64-release.zip` 获取 Dart SDK。
3. **获取与构建 AOT 反编译器**：
   - 使用开源工具 `blutter` (`https://github.com/worawit/blutter`)：
     - 其原理是根据指定的 Dart SDK 版本下载/编译对应版本的 VM 数据结构定义，然后解析 `_kDartIsolateSnapshotInstructions` 与 `_kDartIsolateSnapshotData` 中的 ObjectPool。
     - 本机已具备 `cmake`、`g++`、`libcapstone`，只需安装 `ninja` 即可运行 blutter 的 python 构建驱动。
4. **备选方案（轻量脚本解析）**：
   - 在已安装 `capstone 5.0.7` 的 Python 环境下，通过解析 ELF 的 `.rodata`（`_kDartIsolateSnapshotData`），寻找 `AuthInterceptor` 对应的 Class / Library 对象在 ObjectPool 里的索引，直接读取常量指针。
