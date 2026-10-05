#!/usr/bin/env bash
# 把手机 App 正在用的 session token 从**运行中的进程内存**里取出来。
#
# 为什么必须这样做（而不是读文件）：
#
#   token 在手机的存储里是密文。它落在
#   shared_prefs/FlutterSharedPreferences.xml 的 `flutter.accessToken`
#   （176 base64 字符 / 131 字节），而解它的密钥在 Android KeyStore 里 ——
#   classes.dex 含 AndroidKeyStore / KeyGenParameterSpec$Builder /
#   AES/GCM/NoPadding / KeyStore$SecretKeyEntry，设备侧 native.xml 里只有两个
#   **别名**（KEY_ALIAS / TOKEN_ALIAS）。KeyStore 的密钥按设计永不出 TEE，
#   所以这不是「工作量问题」而是做不到：pull 下整个数据目录也解不开。
#
#   明文只在进程内存里出现 —— App 得把它放进 `authorization: Bearer …`
#   请求头。所以读那份内存。
#
# 为什么不用 Frida：/proc/<pid>/maps + dd + grep -a 在 root 设备上就够，
# 这样就不必往设备推一个 59 MB 的 frida-server、也不必对齐客户端与服务端版本。
# （第一版是 Frida，能跑，但没必要。）
#
# 为什么需要 root：读别的进程的 /proc/<pid>/mem 是 root-only。
#
# 为什么值得：本服务与手机共用**同一个**会话，谁也不踢谁。对比之下
# `javdb-rss login` 会把你手机上的会话挤下线（单会话账号，见 README 与
# .scratch/javdb-rss/notes/auth.md）。
#
# 它**不写盘、不碰网络**，也不会把 token 存到任何地方 —— 只打到 stdout。
# 用完会删掉推上去的临时脚本（trap，异常退出也删）。
#
# 用法：
#   ./scripts/export-phone-token.sh                      # 扫一个已经在跑的 App
#   ./scripts/export-phone-token.sh --serial 4b0350c4    # adb 看到多个设备时指定
#   ./scripts/export-phone-token.sh --launch --user 10   # 先起 App（私密空间）
#
# 细节与证据见 .scratch/javdb-rss/notes/auth.md 的「从 App 里挖 token」。

# 这个脚本要 bash（下面的 printf %q、${2:?}、pipefail 都不是 POSIX sh）。
# `sh scripts/export-phone-token.sh` 在 Ubuntu 上落到 dash，会把 argv 拼成一句
# 错命令再交给设备 —— 那种失败很难从输出里看出来，所以先拦下来。
if [ -z "${BASH_VERSION:-}" ]; then
  echo "!! 本脚本需要 bash：请用 ./scripts/export-phone-token.sh 或 bash scripts/export-phone-token.sh" >&2
  exit 1
fi

set -euo pipefail

PKG="xxx.pornhub.fuck"
HELPER="/data/local/tmp/scan-token.sh"
SERIAL="${ADB_SERIAL:-${ANDROID_SERIAL:-}}"
LAUNCH=0
USER_ID=0

usage() {
  sed -n '/^# 用法：/,/^# 细节与证据/p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --serial)  SERIAL="${2:?--serial 需要一个设备 id}"; shift 2 ;;
    --package) PKG="${2:?--package 需要一个包名}"; shift 2 ;;
    --launch)  LAUNCH=1; shift ;;
    --user)    USER_ID="${2:?--user 需要一个 uid}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "未知参数：$1（--help 看用法）" >&2; exit 2 ;;
  esac
done

# --- adb 在哪 --------------------------------------------------------------
# 这个脚本会被两种环境调用：仓库自己的终端（adb 在 PATH 里），以及
# ~/android-capture 那套 harness（它靠 env.sh 把 platform-tools 塞进 PATH）。
# 直接依赖 PATH 会让第二种环境里「命令找不到」看起来像设备问题，所以这里显式找。
if command -v adb >/dev/null 2>&1; then
  ADB="$(command -v adb)"
elif [ -x "${ANDROID_HOME:-}/platform-tools/adb" ]; then
  ADB="${ANDROID_HOME}/platform-tools/adb"
elif [ -x "${ANDROID_SDK_ROOT:-}/platform-tools/adb" ]; then
  ADB="${ANDROID_SDK_ROOT}/platform-tools/adb"
else
  echo "!! 找不到 adb。装 platform-tools，或设 ANDROID_HOME。" >&2
  exit 1
fi

# adb 必须钉住设备：同一台手机同时插着 USB、又开着无线调试时，adb 会看见两个
# 设备，裸命令直接报 "more than one device/emulator" 而不是干活。
adb_() {
  if [ -n "$SERIAL" ]; then "$ADB" -s "$SERIAL" "$@"; else "$ADB" "$@"; fi
}

# --- 设备 ----------------------------------------------------------------
if [ -z "$SERIAL" ]; then
  n="$("$ADB" devices | awk 'NR>1 && $2=="device"' | wc -l | tr -d ' ')"
  if [ "$n" -eq 0 ]; then
    echo "!! 没有处于 device 状态的 adb 设备。插上手机，或先：" >&2
    echo "     adb connect <host>:<port>    # 无线调试" >&2
    exit 1
  fi
  if [ "$n" -gt 1 ]; then
    echo "!! adb 看到 $n 个设备 —— 必须指定一个，否则每条命令都会失败：" >&2
    "$ADB" devices | awk 'NR>1 && $2=="device" {printf "     %s\n", $1}' >&2
    echo "   ./scripts/export-phone-token.sh --serial <id>" >&2
    exit 1
  fi
  echo "==> 设备：$("$ADB" devices | awk 'NR>1 && $2=="device" {print $1}')"
else
  echo "==> 设备：$SERIAL"
fi

# --- 怎么拿到 root shell --------------------------------------------------
# 两种世界，需要两套管子：
#   模拟器 / adbd 已是 root  -> 裸 `adb shell` 就是 uid 0
#   Magisk 真机              -> `adb root` 会被拒，得走 `su -c "<命令>"`
# 弄错的症状是一个看起来像「/proc 坏了」的权限错误，所以这里显式判定而不是假设。
ROOT_VIA=""
if adb_ shell id -u 2>/dev/null | tr -d '\r' | grep -qx 0; then
  ROOT_VIA="direct"
else
  adb_ root >/dev/null 2>&1 || true
  adb_ wait-for-device 2>/dev/null || true
  if adb_ shell id -u 2>/dev/null | tr -d '\r' | grep -qx 0; then
    ROOT_VIA="direct"
  elif adb_ shell 'su -c id' 2>/dev/null | tr -d '\r' | grep -q 'uid=0'; then
    ROOT_VIA="su"
  else
    cat >&2 <<'EOF'
!! 拿不到 root shell。

   这条路线需要 root：token 要读的是**另一个进程**的地址空间，
   /proc/<pid>/mem 是 root-only。两条出路：
     - Magisk：`adb shell su -c id` 应当打印 uid=0（首次会弹授权）
     - 没有 root 的手机：这条路走不通，改用 `javdb-rss login`（会踢掉手机）
EOF
    exit 1
  fi
fi
echo "==> root 方式：$ROOT_VIA"

# 需要特权的都走这里，两种世界只在这一处分开。`su -c` 只吃一个字符串，
# 所以用 printf %q 把 argv 收拢成一条命令。
root_sh() {
  if [ "$ROOT_VIA" = "su" ]; then adb_ shell "su -c $(printf '%q' "$1")"
  else adb_ shell "$1"; fi
}

# --- 可选：先起 App -------------------------------------------------------
if [ "$LAUNCH" = 1 ]; then
  # --user 不能省：装在私密空间（小米「第二空间」之类）的 App 属于 user 10，
  # 不带这个参数 `am start` 只会静默失败，而失败信息里没有任何线索指向 user id。
  echo "==> 启动 $PKG（user $USER_ID）"
  root_sh "am start --user $USER_ID -n $PKG/$PKG.MainActivity" >/dev/null
  sleep 6
fi

PID="$(root_sh "pidof $PKG" | tr -d '\r' | awk '{print $1}')"
if [ -z "$PID" ]; then
  echo "!! $PKG 没在运行 —— token 只在 App 活着的时候在内存里。" >&2
  echo "   加 --launch 重跑（装在私密空间就再加 --user N）。" >&2
  exit 1
fi
echo "==> App pid：$PID"

# --- 把扫描脚本推上去 -----------------------------------------------------
# 推一个脚本文件，而不是内联 `su -c "..."`：扫描是一条带引号、算术和正则的管道，
# 经 adb + su 两层重新引用，任何一层都可能悄悄改写它。
# 模板里必须带 X：BSD 的 mktemp -t PREFIX 能用，但 GNU mktemp 会因为「模板里
# 没有 X」直接报错（"too few X's in template"）。这个仓库是公开的，Linux 读者
# 会踩这个坑，所以用两种实现都接受的写法。
LOCAL_HELPER="$(mktemp "${TMPDIR:-/tmp}/scan-token.XXXXXX")"
cleanup() { rm -f "$LOCAL_HELPER"; root_sh "rm -f $HELPER" >/dev/null 2>&1 || true; }
trap cleanup EXIT

cat > "$LOCAL_HELPER" <<'ON_DEVICE'
#!/system/bin/sh
# 扫一个进程的每个可读区，打印 JWT 形状的字符串。
#
# 为什么用 dd 而不是把 /proc/<pid>/mem 当一个文件读完：那个文件是地址空间的视图，
# 顺序读会在第一个未映射的空洞处停下。逐区 seek 才是它能工作的原因。
#
# 为什么模式是有界的：`eyJ…{10,200}…{43}` 恰好圈出一个 HS256 JWT —— header、
# payload、以及 43 个 base64url 字符的签名 —— 所以匹配不会顺延吃进内存里后面
# 恰好也像 base64 的字节。它也不会误伤这个 App 里的其它 base64（话题列表、
# 域名 blob）：那些不含 `.`，而三段式形状要求两个点。
#
# 而且它只有**一条**模式：写成 `A|B|C` 会让 toybox 的 grep 退化约 10 倍
# （实测 6m09s vs 16s，因为在多 GB 的行上反复回溯）。
PID="${1:-$(pidof xxx.pornhub.fuck | awk '{print $1}')}"
[ -n "$PID" ] || { echo "!! App 没在跑" >&2; exit 1; }

grep ' r' "/proc/$PID/maps" | awk '{print $1}' | while read -r R; do
  S=$((0x${R%-*})); N=$((0x${R#*-} - S))
  [ "$N" -gt 0 ] || continue
  # bs=4096：mmap 的区都是页对齐的，所以 skip 是精确的。
  dd if="/proc/$PID/mem" bs=4096 skip=$((S / 4096)) count=$((N / 4096 + 1)) 2>/dev/null
done | grep -a -oE 'eyJhbGciOiJIUzI1NiJ9\.[A-Za-z0-9_-]{10,200}\.[A-Za-z0-9_-]{43}' | sort -u
ON_DEVICE

# 推失败就直接说，而不是让 `set -e` 抛出一个没有上下文的退出码；
# 成功时把 adb 那行进度吃掉，免得夹在扫描输出里像出错了。
if ! adb_ push "$LOCAL_HELPER" "$HELPER" >/dev/null 2>&1; then
  echo "!! 推 $HELPER 到设备失败（设备只读 / 空间不足？）" >&2
  exit 1
fi

# --- 扫 -------------------------------------------------------------------
echo "==> 扫描中（要走完整个可读地址空间，15–40 秒是正常的）"
FOUND="$(root_sh "sh $HELPER $PID" | tr -d '\r')"

if [ -z "$FOUND" ]; then
  cat >&2 <<EOF
!! 在 pid $PID 里没找到 JWT。

   App 在跑，但这次扫描期间它内存里没有 token。最可能的原因是它这个会话还没发过
   需要凭据的请求 —— 打开「我的 / 收藏 / 想看」这类页面，然后再跑一次。

   另一种可能是 App 已经登出。那这条路帮不上忙，只能 `javdb-rss login`（会踢掉手机）。

   如果 App 里这些页面都正常、却在内存里找不到，那说明上游换了 token 形状
   （本脚本的模式写死了 HS256 三段式）。放宽 $HELPER 里的模式再查。
EOF
  exit 1
fi

echo
printf '%s\n' "$FOUND" | sed 's/^/    /'
echo
echo "==> 哪一串是 API 会话：payload 解出来应当是 {\"id\":…,\"username\":…}"
printf '%s\n' "$FOUND" | while read -r T; do
  P="$(printf '%s' "${T#*.}" | cut -d. -f1)"
  # base64url -> base64 再补 padding；只用 tr + shell，保持可移植。
  case $(( ${#P} % 4 )) in 2) P="$P==";; 3) P="$P=";; esac
  printf '    %s…  ->  %s\n' "${T%%\.*}" "$(printf '%s' "$P" | tr '_-' '/+' | base64 -d 2>/dev/null)"
done

cat <<EOF

==> 下一步（本脚本不替你写盘 —— token 是凭据，落盘与否由你决定）

    # 方式 A：写进 token.json（服务默认读它，与 config.yaml 同目录）
    printf '{"token":"%s"}\\n' '<上面那串>' > token.json && chmod 600 token.json

    # 方式 B：环境变量（优先级高于文件）
    export JAVDB_TOKEN='<上面那串>'

    然后验证 —— 这一步**不会碰你的手机**（探针读到已配置 token 时不会登录）：
      unset JAVDB_USERNAME JAVDB_PASSWORD   # 否则 token 读不到时它会自己登录、把你手机踢掉
      go run ./cmd/contractprobe collected
      # 开头应当打印「使用已配置的 token（不会碰你的手机会话）」
EOF
