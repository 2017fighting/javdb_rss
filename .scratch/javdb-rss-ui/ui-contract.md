{
  "$description": "shadcn/ui default theme — CSS custom properties normalized to DTCG. Source: ui.shadcn.com default (neutral) theme. Refresh: regenerate from the shadcn CSS variables block.",
  "color": {
    "background": {
      "$type": "color",
      "$value": "hsl(0 0% 100%)"
    },
    "foreground": {
      "$type": "color",
      "$value": "hsl(0 0% 3.9%)"
    },
    "card": {
      "$type": "color",
      "$value": "hsl(0 0% 100%)"
    },
    "card-foreground": {
      "$type": "color",
      "$value": "hsl(0 0% 3.9%)"
    },
    "primary": {
      "$type": "color",
      "$value": "hsl(0 0% 9%)"
    },
    "primary-foreground": {
      "$type": "color",
      "$value": "hsl(0 0% 98%)"
    },
    "secondary": {
      "$type": "color",
      "$value": "hsl(0 0% 96.1%)"
    },
    "secondary-foreground": {
      "$type": "color",
      "$value": "hsl(0 0% 9%)"
    },
    "muted": {
      "$type": "color",
      "$value": "hsl(0 0% 96.1%)"
    },
    "muted-foreground": {
      "$type": "color",
      "$value": "hsl(0 0% 45.1%)"
    },
    "accent": {
      "$type": "color",
      "$value": "hsl(0 0% 96.1%)"
    },
    "destructive": {
      "$type": "color",
      "$value": "hsl(0 84.2% 60.2%)"
    },
    "border": {
      "$type": "color",
      "$value": "hsl(0 0% 89.8%)"
    },
    "input": {
      "$type": "color",
      "$value": "hsl(0 0% 89.8%)"
    },
    "ring": {
      "$type": "color",
      "$value": "hsl(0 0% 3.9%)"
    }
  },
  "radius": {
    "base": {
      "$type": "dimension",
      "$value": "0.5rem"
    }
  },
  "spacing": {
    "1": {
      "$type": "dimension",
      "$value": "4px"
    },
    "2": {
      "$type": "dimension",
      "$value": "8px"
    },
    "3": {
      "$type": "dimension",
      "$value": "12px"
    },
    "4": {
      "$type": "dimension",
      "$value": "16px"
    },
    "6": {
      "$type": "dimension",
      "$value": "24px"
    },
    "8": {
      "$type": "dimension",
      "$value": "32px"
    }
  }
}

---

# 本 effort 的补充（javdb-rss 订阅链接生成器）

上面那段是 `init_ui_contract{system:"shadcn"}` 生成的 shadcn 默认主题快照。
这一节是**这一个产品**对它的引用与两处有意偏离。所有值仍然只引用令牌。

## 对默认主题的两处偏离（都有 WCAG 理由，不是口味）

| 令牌 | shadcn 默认 | 本 effort | 为什么 |
|---|---|---|---|
| `--muted-foreground` | `hsl(0 0% 45.1%)` | `hsl(0 0% 40%)` | 默认值在 `bg-muted`(#f5f5f5) 上只有 **4.35:1**，不过 WCAG AA 4.5:1。取 40% 后：白底 5.74:1、`bg-muted` 上 5.27:1。这个页面大量用「灰字 + 灰底」（作品数、URL、说明），所以必须修。 |
| `--destructive` | `hsl(0 84.2% 60.2%)` | 亮色 `hsl(0 72.2% 50.6%)` / 暗色 `hsl(0 62.8% 45%)` | 默认值当**文字**用在白底上只有 3.15:1。改深后 4.71:1。它在这个页面里就是文字色（状态标签「上游出错」），不是只有填充。 |

## 本 effort 用到的组件（都来自 shadcn 的形态，不新增视觉语言）

| 组件 | 令牌 | 不变量 |
|---|---|---|
| `.btn` | `bg-primary` / `border-input` / `bg-accent` / `--radius` | 主操作分**两个区域**算：内容区（视图）与底部固定工具条（待复制）各**最多一个** `btn-primary`，两者刻意允许共存 —— 工具条是常驻的输出台，不是视图的一部分。主操作 `h-11`（44px），次要 `h-10`/`h-8`。焦点态一律 `ring-2 ring-ring ring-offset-2 ring-offset-background`。 |
| `.field` | `border-input` / `bg-background` / `--radius` | 标签永远在字段**上方**且**常驻**（不用 placeholder 当标签）。`h-9`/`h-10`。 |
| `.segmented` | `border-input` / `bg-muted` / `bg-background` | 二选一/三选一的分段控件，`role="radiogroup"` + `role="radio"`，方向键可切。用于：链接模式、标签来源、链接范围。 |
| `.chip` | `border-input` / `bg-primary` | 多选标签。选中态**三通道**：填充 + `✓` + `aria-pressed`（WCAG 1.4.1，不靠颜色单独表意）。触顶时未选中的一律 `disabled` 并 `title` 说明。 |
| `.tab` | `border-foreground` / `text-muted-foreground` | 分区导航，最多 4 个。选中态 = 下划线 + `text-foreground`（不是只改颜色深浅）。`role="tablist"` 的直接子元素必须是 `role="tab"`，**不能**隔一层 `li`。 |
| `.note` | `bg-muted/50` / `text-muted-foreground` | 解释性文字块。用于状态、为什么这么设计、缺失了什么。 |
| `.tab-count` / 计数 | `bg-muted` / `text-foreground` / `tnum` | 数字一律 `tnum`（等宽），列才对得齐。 |
| `text-2xs` | 0.625rem | 微型标签字号。**不**在各处写 `text-[10px]` —— 字号的唯一来源。 |

## 这个页面的交互不变量（promote 时必须 1:1 复现）

1. **链接是唯一产物。** 页面上每个分区最终都收敛到一条可复制的 URL。底部「待复制」是唯一的输出台，别处只放摘要，不放第二份 URL 列表。
2. **URL 现算，不存字符串。** 待复制里存的是「怎么生成」（女优 id / 模式 / 标签），URL 每次派生。服务地址一改，所有链接当场跟上。
3. **追新 = `?since=<今天>`；全量 = `?pages=20`。** 一天一变是刻意的：链接带上生成当天的日期，之后它只增不减，qBittorrent 靠 guid 去重。
4. **`filter_by` 主属性必须逗号分隔**（`0:a:{id}:c,m::`）。拼在一起（`0:a:{id}:cm::`）会被上游静默忽略 —— 服务会拦 400，但生成端就不该生成它。
5. **标签上限 5**（与 App 一致）。触顶时未选中的 chip `disabled`，并在旁边说明为什么点不动。
6. **生成不出来的就说不出来。** 全站标签、清单这两处后端没有，页面给禁用按钮 + 缺什么 + 为什么，**绝不**给一条会 404 的链接。
7. **复制必须有回退。** 局域网 http 下 `navigator.clipboard` 不存在（非安全上下文）。回退顺序：`navigator.clipboard` → `execCommand('copy')` → 摊开链接让人手动选。
8. **服务地址可改且默认 `http://127.0.0.1:8080`**（服务自己的默认）。promote 时默认改成 `location.origin`（页面由服务自己提供时那就是对的），并持久化。
9. **动作反馈 1.8s**（`已复制 ✓`）+ `aria-live="polite"` 播报。不改状态码、不用 toast 堆叠。
10. **尊重 `prefers-reduced-motion`**；深色模式默认跟随系统并可手动覆盖（localStorage）。

## 标签分组（2026-10-04 增补）

上游 `GET /api/v2/tags?type={zone}` 直接给出分组，**分组名、顺序、组内标签全部原样使用** ——
`category`（显示名）与 `category_id`（slug）都保留，不翻译、不重排、不合并。
详细结论见 `notes/tag-vocabulary.md`。

| 组件 | 令牌 | 不变量 |
|---|---|---|
| `.tag-group` | 无新增 | 每组一个 `<h3>`：显示名（`text-foreground`）+ slug（`font-mono text-2xs`）+ 条数 + 该组已选数。组之间 `space-y-5`，组内 chip `gap-2` —— 组间距必须大于组内间距（Proximity）。 |
| 基本组 | `.chip` | 它走 `filter_by` 的字母位，**不是** `filter_by_tags`。标题里必须写明这一点，否则它看起来和别的组一样。 |
| 已选区 | `bg-muted/40` | 只放**摘要**（可点掉），不再放一份完整 chip —— 两处同一份列表就是重复。 |

### 三条硬规则（都来自实测，不是口味）

1. **`filter_by_tags` 只喂真标签 id。** 年份/月份/時長 的 id 与真实标签 id 撞号
   （月份 1–12 全部撞），喂进去会**静默筛出另一批作品**。这三组不渲染为可点 chip，
   只出说明段 + 证据。
2. **id 反查分组必须按名字消歧。** 同一 id 有多组候选时，只有名字与女优 `tags[]` 里那条
   一致才算数；对不上就**不显示**（宁缺勿错）。
3. **URL 按词表顺序拼，不按点击顺序。** 主属性按 `main` 组顺序、标签按组顺序 + id，
   这样同样的选择永远得到逐字符相同的链接。

## 标签分组的折叠与时间维度（2026-10-04 第三批）

上游分组 7 组 × 每组 6–22 个标签，平铺时手机上标签区 1676px。改成 App 那种折叠：

| 组件 | 令牌 | 不变量 |
|---|---|---|
| `.tag-group`（`<details>`） | `border-border` | 每组一个原生 `<details>`，`<summary>` 是披露按钮（不是 `<h3>`）。默认**全部折叠**，只展开「有已选」的组；搜索时全展开（否则命中被藏在折叠里，等于没搜）；折叠状态由用户显式点击决定并跨重渲染保持（H3 用户控制）。折叠后标签区 286px。 |
| 年份 | `select.field` | **原生 select**，不是 chip 排。26 个选项铺成一排是 Hick's Law 的反面；select 自带键盘跳转与手机滚轮。顺序保留词表倒序（最近的年份在前）。 |
| 月份 / 时长 | `.chip[role=radio]` | 单选。月份**按日历升序**（周期量，Jakob's Law）；词表的倒序是为了「最近优先」，对月份不适用。 |
| 禁用的 chip | `disabled` | 时长四个档位一律禁用并带 `title` 说明缺什么 —— 可点却不能生成链接比灰掉更坑人（H5 预防错误）。 |

### 新增的两条硬规则

4. **年/月/时长不走 `filter_by_tags`。** 上游给了这三组的词表，但它们的 id 不是标签 id
   （月份 1–12 与真标签 id 全撞号），实测传进去会**静默筛出别的作品**；独立参数
   `year=`/`month=`/`duration=` 又全被静默忽略。它们改由**本服务本地**按
   `release_date` / `duration` 过滤 —— 因此 URL 里出现的是 `since=`（现有）与未来的
   `duration=`（未实现），而不是 `filter_by_tags`。
5. **年份/月份只给下界。** `since=` 的语义是「这个日期起」，不是「只这一段」。
   要卡上界需要后端新增 `until=`。页面上必须写出这一条 —— 一个看起来像区间、
   实际只有下界的筛选器，是最典型的静默错误。
