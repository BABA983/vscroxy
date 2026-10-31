# vscroxy UI 设计规范与改版计划

状态：**方向 v2，2026-10-31 定稿。** 本文是界面的视觉规范和改版进度；交互和文案见 [UX 规范](ux-guidelines.md)，功能范围见 [功能路线图](feature-roadmap.md)，代码基础设施见 [基础设施方向](architecture.md)。

v1 改版（靛蓝强调色、Capture／Requests 两个工作区，阶段 0–8）已经完成并被本文取代，它的规范和各阶段实施记录见 git 历史里提交 `44f2c82` 的本文件。

## 1. 方向

v1 统一了结构和 token，但界面仍然是一张普通表格加一堆表单：同一种控件有好几种样子，异常要靠眼睛一行行扫，Capture 和 Requests 之间要来回搬运请求。v2 从 UX 规范第 1 节的前两项核心任务出发：**让时间和异常一眼可见，让抓到的请求和保存的请求是同一种东西。**

目标：

1. 看起来像一个精心打磨的原生桌面工具（Proxyman、Linear 这个级别），而不是拼起来的表单。
2. 所有界面只用一套设计系统 `internal/uikit`：同样的字号、间距、控件、颜色语义。
3. 信息密度高：chrome 退后，屏幕留给流量、请求和响应体。
4. 改版不丢功能：现有的功能、快捷键和无障碍标签都保留；确实要改名的标签，在同一个提交里同步改测试。

不在范围内：改数据模型或存储格式、改 `internal/request`、`internal/vault` 等非 UI 包、换掉 MyGo。

## 2. 设计稿

都是 Painter 概念图，只用来定方向；数据和个别细节是生成的，和本文冲突时以本文为准。

关键界面（布局和信息架构以这组为准；它们用的是早期的荧光绿强调色，**颜色以第 5 节为准**）：

| 界面 | 深色 | 浅色 |
| --- | --- | --- |
| 主工作区 · 左右布局 | https://ampcode.com/user-content/attachments/73d7d0576b5f516381540f10044c3cf38d1afdeb1b68161795af386a7036d72a-file.png | https://ampcode.com/user-content/attachments/726673b3d777a6fc578c34483a15d469be751b0f79052f0d0d6c249855984540-file.png |
| 主工作区 · 上下布局 | https://ampcode.com/user-content/attachments/e4d07f9ff768c045340168e753c0665102f2dc2aa28737e2eb8289f5e6ad1dbd-file.png | https://ampcode.com/user-content/attachments/8810b61f2a1b3d49303cc7f86ec517891751a3f864eeeb3ce16b4bb603050348-file.png |
| 编辑和发送已保存的请求 | https://ampcode.com/user-content/attachments/13614a07a5f933ecb3ac7289311016152f8ca48c12d6438f3389b9d25230a032-file.png | https://ampcode.com/user-content/attachments/689468df6df55143d9c8a0c83e34275032f682d0b13c49f73978490c6c9c57a1-file.png |
| 首次启动引导 | https://ampcode.com/user-content/attachments/42ae2e5117ad05431780016bd82a1133a0febc0724eb49dd06333b2538365247-file.png | https://ampcode.com/user-content/attachments/d2fbbaa9ccc1ed92f220a83081300df3399ad0ad3f10f7f0cdab6be50eb102d1-file.png |
| Settings 独立窗口 | https://ampcode.com/user-content/attachments/fd2d1cee395051fa3a32edf1d4fa38022e5ee3c45f14cd7bb3b44e1c5377f979-file.png | https://ampcode.com/user-content/attachments/d91616c93f71c3d587bd9ab6e1890a6a91d950ce20a8bbebd35a89c308399b0a-file.png |
| 主工作区 · Paper／Ink 配色 | https://ampcode.com/user-content/attachments/4264e67bf143883b93aeb4f42f8c4a95410859a42a81bceda68f6293e9fd335d-file.png | https://ampcode.com/user-content/attachments/60501cbdcdd682610c79458646ac794c4b17389f1a5881d836585b8888110756-file.png |

uikit 组件总览设计稿（实现后以第 11 节的总览截图为准）：

| 总览 | Ink（深色） | Paper（浅色） |
| --- | --- | --- |
| 控件 | https://ampcode.com/user-content/attachments/900406bfa1a1edc21aed8a1797a9b2b4ee67b47a31c6bf6571efade3b54b909e-file.png | https://ampcode.com/user-content/attachments/8d629c20f8e012b5227369f81b842995009326d280089a1a57d8bb80a4083fd6-file.png |
| 结构和反馈 | https://ampcode.com/user-content/attachments/7994da9f07c86a3f3c3866e1d99c73af2a197a848381afbc90d05d89c3703c9e-file.png | https://ampcode.com/user-content/attachments/f007c7d55451f9a1c97f9f7d7bc0401172770a7b6cda3c841cf004ced536cc15-file.png |
| 标志性组件 | https://ampcode.com/user-content/attachments/ce3837e7e7e6d41c58c45c2e7972bf29ba5cbc55810b146a5aa8386a81365e3c-file.png | https://ampcode.com/user-content/attachments/8fb1ae2fd8231030e32f8f65b3d6b982434c64255534472029fa88767d1dc1ee-file.png |

设计稿里和规范不一致、实现时以规范为准的地方：

- Ink 主工作区里的耗时分段全是灰色；五段要可区分。
- 浅色"已保存请求"的录制 pill 被画成了 `Staging`，响应区多了一个重复的 Save to collection；浅色引导页的说明文字误用了等宽字体；表格里有 `/v1/prders` 之类的拼写错误。
- Paper「结构和反馈」的面板头把标题和 tab 分成了两行；规范是一行。
- 空状态图标偏大；规范是 16px、不加框。
- 「标志性组件」里的校验结果画成了带底色的卡片；规范是行内的一行结果加一行原因。
- 按钮 hover／pressed 以第 5 节的颜色为准，焦点环一律画在控件外侧。

## 3. 信息架构：一个工作区

- **去掉 Capture／Requests 切换。** 抓到的请求就是"还没保存的请求"：点开是可编辑的文档，顶部有来源标记（`Captured · 12:03:42 · Safari`），可以直接 Send，或者 Save to collection。Edit & replay、Save as request 这类搬运动作并入这个文档，不再是跨工作区的跳转。
- **侧栏三组：** LIVE（All traffic、Errors、Slow、各 App、各域名、保存的筛选）、COLLECTIONS（集合树，请求前是 method 标签）、SESSIONS（保存的会话文件）。环境、APIs、Runner 从集合的菜单和命令面板进入，不再占一条资源栏。
- **侧栏只有一个选中项，** 表示"现在看的是什么"。视图、快速筛选这类开关不是第二个选中项：开着时图标用强调色、文字用正文色，不画选中底和标记线。
- **中间栏随侧栏选择变化：** 选 LIVE 或 SESSIONS 时是流量表格；选一个已保存的请求时，是这个请求的发送历史（History／Examples 切换），可以标基线、对比。
- **两种布局，** 用标题栏右侧的两个图标切换，各自记住分隔线比例，双击分隔线恢复默认：
  - 左右（默认）：列表在左，请求文档和响应在右，适合宽屏逐条翻看。
  - 上下：列表占满宽度（多出 Client 列），下方 Request 和 Response 左右并排，适合对照请求和响应。
- **标题栏 44px：** 品牌（保留现有图标）、录制 pill（状态点、监听地址、实时速率曲线、暂停）、命令栏（`⌘K`，同时是搜索）、布局切换、Settings。Tools 窗口（M4）和 Host 方案（M2）的入口在录制 pill 右侧。
- **已启用工具栏：** 标题栏下方 32px，每个工具一个 chip（图标、名称、×），没有工具时不占空间。行为见 UX 规范第 5 节。
- 状态栏、对象动作表、撤销和确认规则按 UX 规范。

## 4. 视觉语言："仪器"

- **界面本身是单色的，颜色只表达信号：** 状态码、method、耗时条、校验结果。工具栏、侧栏、边框、图标一律中性色。
- **一个强调色，只用于：** 主操作、焦点、选中标记、录制、开关和"已开启"。
- **正常的东西不上色。** 2xx 和 304 是普通的次要文字，不加底色、不用绿色；只有 3xx（灰蓝）、4xx（赭黄）、5xx（朱红）上色。满屏的 200 退到背景，异常自己跳出来。这条规则也让强调色可以是绿色而不和"成功"撞色。
- **数据一律等宽：** method、URL、路径、header、body、时间、耗时、大小都用 JetBrains Mono，会变化的数字开 `tnum`。界面文字是 Inter。method 是纯色等宽文字，不做 chip。
- **结论大字：** 响应区第一行是 22px 等宽的状态行（`500  Internal Server Error`），下面一行次要文字写耗时、大小、类型。
- **常驻的状态信息：** 列表上方一条 52px 的活动柱状图（最近 5 分钟，按状态码上色，可拖选时间范围）；筛选是查询语法 chip（`status:>=400`、`host:`、`app:`、`slow`），带自动补全；每行带一根耗时条，慢请求是朱红。
- **选中是强调浅底、正常文字颜色，分两种形状：**
  - 选位置或选东西的行（侧栏、集合树、各种挑选列表）是离边缘内缩的圆角块，不画标记线，和 macOS 的 source list 一样。侧栏的选中表示"现在在哪"，不是键盘光标，焦点在别处时仍是强调浅底。
  - 表格和数据列表（流量表、消息、Protobuf、Runner 结果）的行占满宽度，左侧加 2px 强调色标记线。列表失焦时浅底变成 hover 色、标记线变成次要色，靠标记线和 hover 行区分开。
  - 菜单和命令面板的高亮只有浅底。
- **形状靠近 macOS：** 圆角比 Windows 的 Fluent 大一档（第 6.3 节），嵌套的圆角同心（内层 = 外层 − 内边距），浮层是细边框加柔和的大阴影。不做胶囊按钮和毛玻璃：行高 28 下胶囊显得像玩具，毛玻璃跨平台做不一致，也和平涂的 Paper 冲突。
- **同一种东西只有一种样子：** tab、按钮、面板头、表格各一种写法，都来自 uikit。
- **密而不挤：** 表格和编辑器保持桌面工具的密度，最小字号 11，正文 13，行高 28。

## 5. 主题：Paper／Ink

默认主题是"油墨绿"一套两张：浅色 **Paper**（印在暖色纸上的技术手册），深色 **Ink**（暖色油墨印在墨绿黑底上）。只用平涂色，不做纸纹、做旧和渐变。外观跟随系统，也可以手动选浅色或深色。

下表是 `internal/uikit/theme.go` 里的实际值：

| Token | Paper | Ink | 说明 |
| --- | --- | --- | --- |
| Background | `#f7f4ec` | `#121614` | 窗口；也是 MyGo 控件的 Surface（输入框、按钮、表头） |
| Panel | `#efebe0` | `#181d1a` | 侧栏、各种条栏 |
| Hover | `#e9e4d6` | `#1f2622` | |
| Pressed | `#e0dacb` | `#26302a` | |
| Border | `#d3ccba` | `#343c37` | 分隔线和输入框边框 |
| Text | `#1f2a24` | `#e6e1d3` | 正文 |
| TextMuted | `#6d6a5f` | `#9a978b` | 次要文字，也是 2xx 的颜色 |
| TextFaint | `#a29d8f` | `#66665e` | 只用于禁用和占位，不承载信息 |
| Accent | `#1e5b47` | `#6fb593` | 主按钮、焦点、选中标记、录制、开关；也是 Success |
| AccentHover／AccentPressed | `#174a3a`／`#123d30` | `#82c4a4`／`#5ea482` | |
| AccentText | `#f7f4ec` | `#121614` | 主按钮文字 |
| AccentTint | `#e3ece4` | `#1f2d26` | 选中浅底、变量 chip |
| Info／3xx | `#396482` | `#8fb3d1` | |
| Warning／4xx／PUT | `#8d520e` | `#e0a458` | |
| Danger／5xx／DELETE | `#ab382c` | `#e8806f` | |
| GET／POST | `#2f5d8a`／`#58661e` | `#8fb3d1`／`#b5c27a` | |
| PATCH／WS | `#7a3e6d`／`#2b6868` | `#c99abd`／`#7cc4c0` | |
| Inverse／InverseText | `#1f2a24`／`#f7f4ec` | `#e6e1d3`／`#121614` | tooltip |
| 柱状图／耗时条（正常） | `#cfc8b8` | `#4a4d47` | 待实现 |
| 耗时分段 DNS／Connect／TLS／Wait／Download | Info／WS／PATCH／Warning／Accent | 同左 | 待实现；五段必须可区分 |

对比度（线性 sRGB 相对亮度，在背景、Panel、Hover、Pressed 上计算）由 `internal/uikit/theme_test.go` 检查：正文和信号色、method 色、强调色、主按钮文字 ≥ 4.5:1，次要文字 ≥ 3:1，TextFaint 不参与。Paper 的信息、警告、危险、POST、WS 比最初的设计值加深过，就是为了在 Hover／Pressed 底上达标。

## 6. 设计系统：`internal/uikit`

### 6.1 规则

- **独立的包，** 只依赖 MyGo（`ui`、`ui/editor`、`ui/chart`），不碰 app 状态和业务包。
- **主题 = 语义 token 表。** `Palette` 是一张表，Paper、Ink 是内置的两张，`Theme(dark)` 由它生成 `ui.Theme`，`Colors(t)` 取回当前表。界面代码只引用语义 token，不能假设强调色是哪种颜色。字号、高度、圆角、间距由 uikit 导出，主题不能改。
- **界面只用 uikit。** `internal/app` 不直接调用 `ui.Button`、`ui.PrimaryButton`、`ui.ButtonBase`、`ui.TextInput`、`ui.Select`，不写 `ui.Hex`、数字字号和手写 hover／pressed。uikit 的组件建在 MyGo 的 Base 上，键盘、焦点和无障碍由 MyGo 负责；每个可交互组件都要传无障碍标签。
- **组件按变体设计，** 调用方选角色和变体，不调样式。布局（Grow、Width、SingleLine、Tooltip）照常在返回值上链式设置。
- **禁用放在外层：** uikit 按钮在构建时就决定 hover 外观，MyGo 的 `Hovered()` 也只看那一刻是否禁用。可能被禁用的按钮，用外层元素禁用（`ui.Row(c).Disabled(x).Children(...)`），不要在返回值上再调 `Disabled`。
- 不做防回退的检查脚本；写死值靠评审和组件总览发现。

### 6.2 字体

- 内嵌 Inter 4.1（400／500／600，界面）和 JetBrains Mono 2.304（400／700，数据），在 `internal/uikit/fonts`，来源和许可见那里的 README 和 `THIRD_PARTY_NOTICES.md`。注册失败只记录一次日志并回退。
- 中文不内嵌（一个字重就要 10 MB 以上），用系统字体回退。拉丁回退字体放在 CJK 字体前面，避免英文变宽、等宽失效：
  - 界面：`Inter, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
  - 等宽：`JetBrains Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
- 中文界面的行高和按钮宽度要用中文截图单独检查。Linux 上没有 Noto Sans CJK 时，Settings → 外观提示安装（待做）。

### 6.3 尺寸

| Token | 值 | 用途 |
| --- | --- | --- |
| `TextMeta`／`TextBody`／`TextTitle` | 11／13／15 | 计数和次要信息／正文、表格、控件／标题；响应结论另加 22（待做） |
| `HeightCompact`／`HeightControl`／`HeightLarge` | 24／28／32 | 表格内和条栏里的小按钮／按钮、输入框、侧栏条目／URL 栏和 Send |
| `HeightTabs`／`HeightBar` | 32／44 | tab 行／标题栏和 Capture 工具栏 |
| `RadiusControl`／`RadiusPanel`／`RadiusDialog` | 6／10／14 | 控件和 pill／弹层、卡片／对话框；嵌在容器内边距里的控件用容器圆角减内边距（分段控件的滑块是 6 − 2 = 4） |
| `IconInline`／`IconLarge` | 14／16 | 行内和按钮／rail 和空状态 |
| `DialogSmall`／`DialogMedium`／`DialogLarge` | 480／640／840 | 对话框宽度档，随窗口收缩 |

- 间距用 `t.Space(n)`，网格 4px，只用 4／8／12／16／24（细缝可用 `t.Space(0.5)`）。
- 表格行高 28。MyGo 的 `ui.Table` 最小行高是 `t.Space(8)`，所以流量表格在局部主题里设 `Spacing = 3.5`。
- 分隔统一 1px `Border`，不靠色块分区。只有菜单、弹层、对话框有阴影：1px 边框画边缘，阴影只做柔和的浮起（弹层 `0 8 28` 透明度 0.14，对话框 `0 20 56` 透明度 0.2；Ink 下乘 2.5 才看得出）。
- MyGo 自带的弹出菜单圆角写死为面板 `t.Radius + 2`、菜单项 `t.Radius`，即 8／6，不完全同心；不为此改库。

### 6.4 文字角色（`text.go`）

| 角色 | 样子 | 用途 |
| --- | --- | --- |
| `Heading` | 15，600 | 对话框、面板、检查器里的请求名 |
| `Body` | 13，颜色继承 | 普通文字；在按钮和选中行里也对 |
| `Mono` | Body + 等宽 | URL、header、路径等数据 |
| `Hint` | 13，次要色 | 控件下或空面板里的一句说明 |
| `Meta` | 11，次要色 | 计数、时间、大小；会变化的数字加 `FontFeatures("tnum")` |
| `Label` | Meta + 600 | 字段名、分组标题（分组标题大写加字距） |

等宽的次要信息写成 `Meta(c, s).Font(uikit.FontMono)`。

### 6.5 组件

已有（组件总览里都有）：

| 类别 | 组件 |
| --- | --- |
| 按钮 | `PrimaryButton`、`SecondaryButton`、`DangerButton`（28）、`CompactButton`（24）、`ToolbarButton`（透明底，可带 active 浅底）、`IconButton`（24 正方形，必带 tooltip）、`RowButton`（列表里的整行）、`SwatchButton`（标记色） |
| 输入 | `Input`、`Select`、`Segmented`、`Tabs`（32 下划线，可带计数） |
| 列表和侧栏 | `ListTheme`（MyGo 表格／列表／树的选中色）、`Marker`／`RowMarker`、`Selected`、`SidebarItem`、`SidebarSection` |
| 信号 | `StatusPill`（2xx／304 不加底色）、`MethodLabel`、`MethodColor`、`StatusColor`、`MarkColors`、`BadgeColors` |
| 结构和反馈 | `KVRow`（只读）、`EmptyState`、`Toasts`、`Modal`、`DialogColumn`、`DialogTitle`、`DialogFooter`、`PopoverPanel`、`EditorTheme` |

待做（随用到它们的步骤加入）：

- 控件：Button 的 ghost 变体和 32 档、Input 的错误态、Checkbox／Switch 包装、LayoutToggle。
- 结构：PaneHeader（标题、tab、次要信息、操作在同一行）、可编辑 KVGrid、FormRow（标签左、控件右、说明在控件下一行）、Splitter。
- 反馈：Callout（信息／警告／错误，一行文字加文字操作）、StatusText、CountBadge、CheckResult。
- 标志性组件：RecordingPill、QueryBar、ActivityStrip、DurationBar、TimingBar、ResponseHeadline、SourceBadge、EnvironmentSelect。

### 6.6 图标

只用 Lucide。尺寸只有 14 和 16 两档；颜色跟随文字，不单独上色。例外：开关类条目开着时图标用强调色（第 3 节）。

## 7. 交互和状态

- **Hover：** 可点的元素背景变 Hover，文字颜色不变；不可点的没有 hover。立即变化，不做过渡。
- **按下：** 背景变 Pressed。
- **焦点环：** 键盘焦点显示 2px 强调色外框，鼠标点击不显示，所有可交互元素都要有。对话框关闭后焦点回到打开它的控件，`Modal` 会请求一帧重绘让焦点环显示出来。
- **禁用：** 文字用 TextFaint，不响应 hover；tooltip 说明为什么不可用。
- **动效：** 只用于说明变化（展开收起、面板出现、toast 进出），150–200ms，ease-out，只改透明度和位移；尊重"减少动效"。列表滚动、新流量到达不做动画。
- **拖拽：** 分隔线 hover 时变成强调色细线，指针变成调整大小；双击恢复默认。
- **右键：** 任何有动作的对象都支持右键，菜单内容见 UX 规范第 6 节。
- **选中：** 单击选中；Cmd／Ctrl 单击加选；Shift 单击连选；Enter 进入检查器；Esc 返回。

每个列表、面板、编辑器都要设计四种状态：

| 状态 | 样子 |
| --- | --- |
| 空 | `EmptyState`：16px 图标、一句标题、一句说明、最多一个主操作和一个次操作 |
| 加载 | 超过 300ms 才显示；列表用占位行，面板用居中的小转圈，按钮内显示转圈并禁用 |
| 错误 | 面板内显示错误图标、一句说明、"重试"和"详情"；不用弹窗报错 |
| 部分数据 | 数据被截断、不完整时，内容上方一条警告 Callout，说明原因 |

**Toast：** 右下角，宽 360，最多同时 3 个，从下往上堆叠，级别用左侧图标区分（不用彩色背景）。行为见 UX 规范第 8 节。

**对话框：** 用宽度档、`DialogTitle`、可滚动内容和 `DialogFooter`（取消在左，主操作在右，footer 不随内容滚走）。一屏最多一个主按钮。

**文案：** 语气、长度、省略号和术语表见 UX 规范第 11 节。

## 8. 第三方主题（VS Code 主题）

uikit 已按"主题 = 语义 token 表"实现，导入第三方主题不用返工结构，在界面迁移完成后做。VS Code 主题 JSON 的映射：

| VS Code 主题字段 | 映射到 |
| --- | --- |
| `colors` 的 `editor.background`、`sideBar.background`、`list.hoverBackground`、`list.activeSelectionBackground`、`focusBorder`、`button.background`、`input.border` 等 | Background、Panel、Hover、AccentTint、焦点、Accent、Border |
| `terminal.ansiRed`／`ansiYellow`／`ansiBlue`／`ansiGreen`／`ansiMagenta`／`ansiCyan` | 状态码、method、耗时分段 |
| `tokenColors`（TextMate scope） | body 编辑器的语法高亮 |

约束：

1. 主题只换颜色，不换字号、密度和形状。
2. 映射后按第 5 节的对比度标准检查；不达标的组合自动调整，并在 Settings → 外观里提示。
3. 内置主题（候选：One Dark Pro、Dracula、Tokyo Night、Catppuccin、GitHub Light/Dark、Solarized）逐个确认许可证，写进 `THIRD_PARTY_NOTICES.md`；同时支持导入 `.json` 主题文件。

## 9. 实施顺序和进度

每一步都能单独合并，附 Paper／Ink 前后截图；涉及交互的另外录屏。

1. **uikit 和主题（完成）：** 建 `internal/uikit`（token、Paper／Ink、字体、共用组件、对比度检查、组件总览截图），删掉 `internal/app/style.go`。不改布局。
2. **迁移现有界面到 uikit（完成）：**
   - 选中改成标记线（第 4 节），接入流量表、消息列表、Protobuf、Runner、各侧栏和 Settings 分类、集合树、环境树、Requests rail、Rules、WebSocket 会话和调查板。
   - `internal/app` 不再直接用 `ui.Button`、`ui.PrimaryButton`、`ui.ButtonBase`、`ui.TextInput`、`ui.Select`，也没有手写 hover／pressed。
   - 文字改用文字角色；`TextColor(t.TextMuted)` 还剩约 30 处，是图标、表格单元格（有自己的 `text` 帮手）、分段控件和菜单触发器，随第 3 步重写这些界面时处理。
   - Capture 侧栏的视图和快速筛选不再像第二个选中项（第 3 节）。
3. **一个工作区和两种布局：** 合并侧栏、请求文档、发送历史中间栏、布局切换。这一步改变导航，同步改 UX 规范第 3 节涉及的测试和快捷键清单。
   - 3.0 形状（完成）：圆角 6／10／14、嵌套同心、柔和阴影，侧栏和挑选列表的选中去掉标记线（第 4 节）。
   - 3.1 外壳合并（完成）：去掉 Capture／Requests 切换和 Requests 的 rail，只留一个侧栏（LIVE、COLLECTIONS、SESSIONS），内容区跟着侧栏的唯一选中项走；环境、运行记录、Runner、API 的新建和导入从集合的 `⋯` 菜单和命令面板进入；标题栏右侧两个布局图标，默认左右，adaptive 删掉（`bottom` 留上下，其余迁移成左右）；Cmd+1–3 跳到侧栏三组并滚动到那一组。内容界面不动，例外是运行记录页仍然占满窗口（三栏放不下侧栏），Protobuf 工具行在窄检查器里换行。
     - 还没做：双击分隔线恢复默认（MyGo 的分隔线不暴露双击，要先改库；现在用 View → Reset desktop layout）。
   - 3.2 发送历史栏（完成）：选中已保存的请求时，编辑器左边多一栏 History／Examples（240，内容区不足 900 时不显示，仍可从请求的 `⋯` → Send history 进入）。History 只列从这个请求发出的运行记录（按请求 ID 或这个文档关联，不按 URL 匹配），点一条在响应区只读显示，可以 Open as draft，× 或再次 Send 回到自己的响应；Examples 点一条在 Examples tab 打开。设基线、对比留给 M1。
   - 3.3 抓到的请求就是请求文档（完成）：选中抓到的 HTTP 请求，检查器就是它的请求文档：顶部 `Captured · 时间 · 进程` 和抓到的状态，下面是 method、URL、Send 和 Save to collection（检查器不足 600 时只留图标），编辑器的 tab 以 Original（抓到的请求读取器）打头，旁边是抓到的响应，发送后换成这次的响应。第一次编辑或发送时才为这条抓包留一份草稿（copy on write），头部和行里有 Edited 标记，抓包本身不变；检查器 `⋯` → Discard edits 回到原样。Edit & replay、Back to traffic、Source #N 都去掉了；落盘的大请求体点 Edit request 才读，流和隧道只读。检查器在列表旁边时请求在上、响应在下；在列表下方（上下布局，或窗口不足 1000 时）或占满窗口时左右并排，不再按宽度切换。窄检查器里 Send 窄到 64，发送中只留 Cancel。
   - 3.4 收尾：待做，包括去掉文档 tab 栏（一次只显示一个文档）。
4. **标志性组件：** 录制 pill 的速率曲线、查询语法筛选、活动柱状图、耗时条、响应结论、瀑布视图（Table／Waterfall 切换）。
5. **随里程碑落地：** 首次启动引导和 Settings 独立窗口随 M1，Host 方案随 M2，Tools 窗口随 M4（见第 10 节）。
6. **第三方主题：** 第 8 节的映射、内置主题和导入。

## 10. 新界面

功能路线图带来的新界面，都按本文和 UX 规范设计。每个界面在对应里程碑开始前先出设计稿，确认后再实现。

| 界面 | 要点 | 里程碑 |
| --- | --- | --- |
| 首次启动引导 | 嵌在流量列表空状态里的 4 步引导（UX 规范第 9 节）；每步一个主按钮，完成的步骤打勾，可跳过 | M1 |
| 录制 pill 和暂停 | 三种状态（录制中、已暂停、代理停止），暂停按钮在 pill 内 | M1 |
| Settings 窗口 | 独立窗口，左侧分类（通用、外观、代理、HTTPS、抓包、设备），右侧 FormRow；改动立即生效，需要重启的项目标明 | M1 |
| Sessions | 侧栏 SESSIONS 组：最近打开的会话文件，打开后只读，带"历史"标记 | M1 |
| History | 选中已保存请求时的中间栏：按时间倒序的发送记录，可按来源、状态筛选；可以设为基线、对比、打开成草稿 | M1 |
| 连接诊断 | 一个页面：上方输入目标 URL，下方按"监听 → 系统代理 → 证书 → 解密规则 → 实际流量"逐项打勾或打叉，每项一个修复按钮 | M1 |
| Host 方案 | 标题栏下拉框：列出方案和开关，底部"管理方案…"；Tools 窗口里左侧方案列表（拖动排序、开关），右侧 hosts 编辑器（语法高亮、冲突提示） | M2 |
| 整理成集合向导 | 大弹窗，顶部步骤条（范围 → 合并接口 → 变量 → 结构 → 示例 → 完成），每步左侧设置、右侧结果预览 | M3 |
| 对应请求提示 | 请求文档头部一个小链接"对应请求：Get user"，点开或存为示例 | M3 |
| Tools 窗口 | 独立窗口，左侧分类（规则、场景、脚本、Host 方案、模拟），中间列表（开关、名称、匹配条件），右侧详情；顶部规则测试输入框 | M4 |
| 规则脚本编辑器 | Tools 窗口右侧的代码编辑器，下方 Console；和请求的 Scripts 用同一个组件 | M4 |
| JSON 筛选、快速解码 | Body 面板头部的筛选输入框（JSONPath／jq 切换）；选中文字后的小浮层，显示 Base64、JWT、时间戳解码结果 | M5 |
| 耗时瀑布图 | Timing：横向分段条（五段颜色见第 5 节），下方表格列出数值 | M5 |
| 协议客户端 | gRPC、SSE、Socket.IO、MCP、MQTT 都是请求文档的请求类型，共用地址栏和响应区，只有编辑区和消息时间线不同 | M8 |

## 11. 验证

每一步：

```bash
go build ./...
go vet ./...
go tool mygo vet .
go test ./... -count=1
CGO_ENABLED=0 go test ./... -count=1
```

截图（输出目录必须不存在或为空；`~/.cache/mygo` 不可写时用独立的 `XDG_CACHE_HOME`，否则内嵌字体不生效）：

```bash
# 组件总览：Paper／Ink 各一张总览，加按钮的 hover／pressed
XDG_CACHE_HOME=/tmp/vscroxy-ui-cache VSCROXY_UIKIT_SNAPSHOTS="$PWD/.amp/in/artifacts/uikit" \
  go test ./internal/uikit -run '^TestOverviewSnapshots$' -count=1

# 界面矩阵：浅／深色 × 1280／960 等宽度，覆盖各界面、菜单、对话框和状态
XDG_CACHE_HOME=/tmp/vscroxy-ui-cache VSCROXY_SNAPSHOTS="$PWD/.amp/in/artifacts/ui" \
  go test ./internal/app -run '^TestUISnapshots$' -count=1
```

- 评审外观先看组件总览，再看界面矩阵；每一步附前后对比。
- 菜单、hover、tab、拖动分栏这类交互，另外在真实窗口里录屏检查。
- 现有 UI 测试按无障碍标签点击和查找，是行为不变的保障。
- macOS、Windows 的原生窗口、读屏软件还没有实机验收（Windows 没有 runner）。

## 12. 决策记录

1. 内嵌 Inter 和 JetBrains Mono，三个平台都内嵌；中文用系统字体。
2. 强调色是油墨绿：Paper `#1e5b47`、Ink `#6fb593`（v1 的靛蓝已取代）。
3. 外观默认跟随系统，可选浅色或深色（现在还是深色开关，随 Settings 窗口改）。
4. 行高 28。
5. 一个工作区（取代 Capture／Requests 两个工作区）；不做全局图标栏；规则、Host 方案、脚本、Mock 集中到 Tools 窗口。
6. 网络相关的设置对话框并进 Settings。
7. 组件放在独立的 `internal/uikit`，只依赖 MyGo；界面只用 uikit 的组件和 token。
8. 默认主题是 Paper／Ink，平涂、不做纸纹。
9. 正常的不上色：2xx 用次要文字，只有 3xx／4xx／5xx 上色；method 用纯色等宽文字，不做 chip。
10. 选中是浅底，不用实心整行；侧栏和挑选列表是内缩圆角块，表格和数据列表加标记线；侧栏只有一个选中项。
11. 详情区有左右和上下两种布局，标题栏切换，各自记住比例。
12. 支持 VS Code 主题映射和导入，在界面迁移完成后做；主题只换颜色。
13. 保留现有品牌图标和 app 图标。
14. 不加防写死值回退的检查脚本。
15. 形状靠近 macOS：圆角 6／10／14，嵌套同心，浮层柔和阴影；不做胶囊按钮和毛玻璃（3.0）。
16. 抓到的请求原地编辑：第一次编辑或发送才留草稿，草稿跟着抓包，只在本次会话，清掉抓包就没了；保存到集合要先起名、选集合（3.3）。
