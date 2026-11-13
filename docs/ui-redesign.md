# vscroxy UI 设计规范与改版计划

状态：**方向 v3，2026-11-11 定稿；v3 的四个阶段（uikit 基础层、检查器的标签页和面板头、侧栏、标题栏与列表）、第五阶段（细节打磨：分隔线、输入框、命令按钮、侧栏退后、表格去装饰、树的方法槽、分区折叠、侧栏可隐藏、空状态引导、交互反馈）、第六阶段（强调色从绿色改成墨黑，Success 拆成独立信号色）和第七阶段（面板头的裁切、编辑器语法色、Ink 的 Socket）全部完成。仍与设计稿不一致的地方列在第 13 节。** 本文是界面的视觉规范和改版进度；交互和文案见 [UX 规范](ux-guidelines.md)，功能范围见 [功能路线图](feature-roadmap.md)，代码基础设施见 [基础设施方向](architecture.md)。

v1 改版（靛蓝强调色、Capture／Requests 两个工作区，阶段 0–8）和 v2（暖纸色 Paper／Ink、油墨绿强调色）都已经完成并被 v3 取代：v3 保留 v2 的信息架构和密度，把暖纸色换成中性灰白，并把界面收成三种表面、一套控件规则。第六阶段再把强调色从绿色换成墨黑（Ink 下是白），中性色去掉绿调、带一点暖，绿色只剩 Success。它们的规范和各阶段实施记录见 git 历史里的本文件。

## 1. 方向

v1 统一了结构和 token，但界面仍然是一张普通表格加一堆表单：同一种控件有好几种样子，异常要靠眼睛一行行扫，Capture 和 Requests 之间要来回搬运请求。v2 从 UX 规范第 1 节的前两项核心任务出发：**让时间和异常一眼可见，让抓到的请求和保存的请求是同一种东西。**

目标：

1. 看起来像一个精心打磨的原生桌面工具（Proxyman、Linear 这个级别），而不是拼起来的表单。
2. 所有界面只用一套设计系统 `internal/uikit`：同样的字号、间距、控件、颜色语义。
3. 信息密度高：chrome 退后，屏幕留给流量、请求和响应体。
4. 改版不丢功能：现有的功能、快捷键和无障碍标签都保留；确实要改名的标签，在同一个提交里同步改测试。

不在范围内：改数据模型或存储格式、改 `internal/request`、`internal/vault` 等非 UI 包、换掉 MyGo。

## 2. 设计稿

v3 的设计稿是一份可切换的 artifact，含 Capture／Request editor／System 三屏和 Light／Dark 两套：<https://ampcode.com/artifacts/visuals/vscroxy-design-system-v3-302b0099-8f58-4b2f-abe7-53ca7710b330>。**颜色、表面、尺寸和组件以它为准**，本文第 5、6 节是它的落地记录；屏幕的布局和结构仍按第 3 节。

下面 v2 时代的 Painter 概念图只保留来定方向和布局，数据和个别细节是生成的，和本文冲突时以本文为准。

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

3.5「macOS 质感」（密度、tab、侧栏材质、控件形状以这组为准，数值以第 6.3 节为准；Painter 画不准像素，图里的行高仍比 24 高）：

| 设计稿 | 链接 |
| --- | --- |
| 主工作区 · Paper | https://ampcode.com/user-content/attachments/1f7855404ee0e721a439d66473c9d87476cc8fab892dedeb6fd4e77de80d422e-file.png |
| 主工作区 · Ink | https://ampcode.com/user-content/attachments/e21b22a0870927c6301a6794325b063d5c51fe16bba20dfa96de911a0c92272f-file.png |
| tab 三种方案（选 B） | https://ampcode.com/user-content/attachments/aaf96d6a6a15e2fbd9c5ef07aec1ff425162053eafd461bf79c41a4db454f2ea-file.png |
| 组件总览 · Paper | https://ampcode.com/user-content/attachments/b61d8c6facc74d8de884ad0d85334bee3f1bdeb9fab6ce280d292b1e19a0c47a-file.png |
| 组件总览 · Ink | https://ampcode.com/user-content/attachments/6d731fdff658c99fbc162178fce775f625d94dfa1a381c4f7c61127218a27511-file.png |

3.5 的两张组件总览早于 tab 方案 B 和 24 档高度：里面的 tab 和 28 高的控件以第 6 节为准；Ink 组件总览里对话框的 Delete 应该是危险色；Ink 主工作区响应区的 Body 多了下划线。

设计稿里和规范不一致、实现时以规范为准的地方：

- Ink 主工作区里的耗时分段全是灰色；五段要可区分。
- 浅色"已保存请求"的录制 pill 被画成了 `Staging`，响应区多了一个重复的 Save to collection；浅色引导页的说明文字误用了等宽字体；表格里有 `/v1/prders` 之类的拼写错误。
- Paper「结构和反馈」的面板头把标题和 tab 分成了两行；规范是一行。
- 空状态图标偏大；规范是 16px、不加框。
- 「标志性组件」里的校验结果画成了带底色的卡片；规范是行内的一行结果加一行原因。
- 按钮 hover／pressed 以第 5 节的颜色为准，焦点环一律画在控件外侧。

## 3. 信息架构：一个工作区

- **去掉 Capture／Requests 切换。** 抓到的请求就是"还没保存的请求"：点开是可编辑的文档，顶部有来源标记（`Captured · 12:03:42 · Safari`），可以直接 Send，或者 Save to collection。Edit & replay、Save as request 这类搬运动作并入这个文档，不再是跨工作区的跳转。
- **侧栏两个固定区域，各自滚动，中间一条细线：** 上区是流量——Traffic（All requests、Errors、Slow、Pinned，各带计数，0 不显示）、保存的视图和快速筛选、Sources（标题右侧 Apps｜Domains 开关，只列前五项，超过时末尾一行 Show all）；下区是 Collections——标题行右端是 `⋯` 和加号，下面是无边框的搜索框（`ChromeFieldFace`）、集合树（请求行带 method 标签）和树末尾可折叠的 Sessions。三个分区标题都可以点击折叠，悬停时显示箭头，状态记在 settings 里。流量再多也不会把集合推出屏幕。侧栏本身可以隐藏（标题栏的按钮、`⌘B`、命令面板），窗口窄于 860 时自动让开、变宽后回到原来的选择。环境、APIs、Runner 从集合的 `⋯` 菜单和命令面板进入，不再占一条资源栏；Diagnostics 在页脚（Client setup、HTTPS rules 旁边）。
- **侧栏只有一个选中项，** 表示"现在看的是什么"。视图、快速筛选这类开关不是第二个选中项：开着时图标用强调色、文字用正文色，不画选中底和标记线。
- **中间栏随侧栏选择变化：** 选 LIVE 或 SESSIONS 时是流量表格；选一个已保存的请求时，是这个请求的发送历史（History／Examples 切换），可以标基线、对比。
- **两种布局，** 用标题栏右侧的两个图标切换，各自记住分隔线比例，双击分隔线恢复默认：
  - 左右（默认）：列表在左，请求文档和响应在右，适合宽屏逐条翻看。
  - 上下：列表占满宽度（多出 Client 列），下方 Request 和 Response 左右并排，适合对照请求和响应。
- **macOS 上侧栏占满窗口高度（3.5）：** 和 macOS 带 source list 的窗口一样，红绿灯和品牌在侧栏顶部，标题栏、工具栏、内容和状态栏在侧栏右边的一栏里；侧栏收起时品牌回到标题栏左侧（让开红绿灯）。Windows、Linux 的窗口按钮在右边，标题栏横跨整个窗口，品牌在它左侧，侧栏从标题栏下面开始。
- **标题栏 40px：** 没有色带和底边框。从左到右：品牌（非 macOS；macOS 上在侧栏顶部）、录制 pill（状态点、状态、监听地址、速率曲线、暂停）、Host 方案 chip、已启用的工具 chip（Breakpoints、限速、No Caching、Allow list、Scenario、Upstream 等，带 ×；放不下时横向滚动并在边缘渐隐）；右端是命令按钮（放大镜加 `⌘K`／`Ctrl K`，点它打开命令面板）、侧栏开关（`⌘B`）、布局切换、Tools、Settings、`⋯`。命令入口是一个 24 高的按钮而不是输入框：它和过滤栏的搜索框不该长得一样，省下的宽度留给工具 chip。Tools 窗口（M4）和 Host 方案（M2）的入口就在这条栏里。
- **已启用的工具：** 在标题栏里，pill 和命令栏之间，每个工具一个 chip（图标、名称、×），没有工具时不占空间；放不下时横向滚动并在右侧渐隐。行为见 UX 规范第 5 节。
- 状态栏、对象动作表、撤销和确认规则按 UX 规范。

## 4. 视觉语言：三种表面，一个强调色

v3 把界面收成三种表面和一套控件规则（设计稿见第 2 节）。表面负责结构；强调色是墨（Ink 下是白），负责"选中、主操作、已开启"；绿色只表示"通过"和"正在录制"。

- **三种表面：** **chrome** 框住窗口（侧栏、各种栏、表头），**canvas** 放数据（表格、编辑器、面板），**well** 是 canvas 上的输入框（比 canvas 深一档）。chrome 上的输入框反过来，是 canvas 底加 1px 细线。Hairline 只分隔同一表面的两个区域，不靠色块分区。
- **外壳一整块，内容抬起来（第八阶段）：** 标题栏、侧栏、底栏是同一块 chrome，三者之间不画线。流量列表、过滤栏、活动行、检查器（以及请求编辑器页的全部内容）合成一张 canvas 面板：圆角 `RadiusPanel`、1px hairline 加一层很浅的阴影，右边留 8 的 chrome，上下紧贴标题栏和底栏；侧栏收起时左边也留 8。面板内部的区域线（列表｜检查器、请求｜响应）保留，过滤栏在面板里、下沿一条 hairline。
- **强调色是墨，绿色只表示通过和录制。** 主按钮、焦点、选中标记、开关和复选框的开启态、活动行选区的把手、表头拖动线用 `Accent`（Paper `#1a1a18`，Ink `#e9e8e4`）；检查通过、录制点、信任状态、向导里已完成的步骤用 `Success`（Paper `#26734a`，Ink `#7cc492`）。标签页、过滤器和命令面板的高亮一律中性底，一屏不再有好几块彩色。
- **控件不描边。** 按钮、tab、chip 的面是文字色的 6%（静止）／10%（悬停）／14%（按下），叠在什么表面上都对，所以同一个按钮放在窗口、chrome 或选中行里都一样。只有输入框在 chrome 上有 1px 细线，浮层和对话框有细线边框加阴影。
- **状态色和方法色不填充背景。** 2xx 和 304 是普通的次要文字，前面没有圆点；4xx（赭黄）和 5xx（朱红）只染状态文字本身，3xx 也按次要文字读。慢请求（≥ 1 秒）的耗时用朱红文字，没有底色块。method 是纯色文字，不做 chip（表格里是界面字体，侧栏和树里是等宽）。
- **选中保留形状。** 列表失焦时，选中底从强调浅底换成中性面（6%），表格行同时把标记线换成 faint 色，位置和形状都不变。
- **等宽只用于线上数据：** 路径、header 值、URL、数字、body。host、client、标签用界面字体。会变的数字开 `tnum`。
- **四个字号，两个字重：** 15（标题）／13（正文和控件）／12（等宽数据）／11（次要信息），只有 400 和 500，没有 600。
- **三档栏高，两档控件：** 栏 40／32／24，控件 24（URL 和 Send 28），行 24；圆角 4／6／10；间距 4／8／12／16／24，左右边距统一 12。
- **线只画在区域之间。** 侧栏与内容、列表与检查器、请求面板与响应面板、状态栏上沿；区域内部靠间距和表面区分，面板头、抓包信息行、URL 行都没有底线。流量表表头没有列分隔线，拖动列宽的命中区还在，指针停在那一列边缘时才画出强调色细线。
- **输入框只有两种：** 在 chrome 上的是 canvas 底加 1px 细线（`ChromeFieldFace`），在 canvas 上的是 well 填充（`FieldFace`）。标题栏的命令入口不是输入框，是一个 24 高的按钮（放大镜加 `⌘K`／`Ctrl K`）。
- **侧栏退后。** 未选中行的文字用次要色、图标再低一档（faint），悬停和选中时回到正文色；计数为 0 不显示。侧栏的三个分区标题可以点击折叠，折叠状态记在 settings 里。
- **常驻的状态信息：** 列表上方一条 24 高的活动柱状图：摘要（`Last 5 min · 138 requests`）在左、柱在右、最右是结束时间（等宽）；最近 5 分钟，按状态码上色（正常中性面，3xx／4xx／5xx 和慢请求用信号色），可拖选时间范围。筛选是查询语法 chip（`status:>=400`、`host:`、`app:`、`slow`），带自动补全；每行带一根耗时条，慢请求是朱红。
- **选中分两种形状，都是选中浅底（`AccentTint`）、正常文字颜色：**
  - 选位置或选东西的行（侧栏、集合树、各种挑选列表）是离边缘内缩的圆角块，不画标记线，和 macOS 的 source list 一样。侧栏的选中表示"现在在哪"，不是键盘光标，焦点在别处时仍是强调浅底。
  - 表格和数据列表（流量表、消息、Protobuf、Runner 结果）的行占满宽度，左侧加 2px 强调色标记线。列表失焦时浅底变成中性面、标记线变成 faint 色，靠标记线和中性底区分开。选中浅底 `AccentTint` 比悬停面明显深一档（Paper `#d8d4cb`，Ink `#45443f`），所以一眼能看出选中的是哪一行，标记线则说明列表有没有焦点。
  - 菜单和命令面板的高亮只有浅底。
- **形状靠近 macOS：** 圆角 4／6／10（第 6.3 节），嵌套的圆角同心（内层 = 外层 − 内边距），浮层是细边框加柔和的大阴影。不做胶囊按钮：控件高 24 时胶囊显得像玩具。
- **macOS 上的原生感（3.5，保留）：** 参考了 onegit／onevra、kero、mygo-cloudmusic 三个 MyGo 应用、Telegram 和 Apple HIG，共同点是窗口外壳原生、控件用中性色叠加、少画线。
  - 只在 macOS 上：界面字体用系统字体（SF Pro），侧栏用 `VibrancySidebar` 材质，但上面盖约 90% 不透明的 chrome 色，桌面只隐约透出一点色调；内容区始终不透明。Windows、Linux 用内嵌的 Geist 和平涂。
  - 标题栏不画色带和底边框，红绿灯对齐标题栏中线（40 高，Y 13）。
  - 强调色不跟随系统，是墨黑（Ink 下是白）。
- **每个面板只有一排标签页：** 面板头 32 高，标题在左、标签页紧跟、元信息在右。请求面板是 Headers／Params／Body／Auth，响应面板是 Body／Headers／Cookies／Timing；Scripts、Checks、Examples、Docs、Settings、Raw、Original、Bulk edit 收进请求面板的 More，响应的 Raw／Checks／Console 收进它右侧的 `⋯`。选中的 tab 是中性面，不再有强调色文字。
- **面板头按量出来的宽度取舍，不按猜的阈值：** 面板记下自己上一帧的宽度，标题和标签页的量好的宽度从中扣掉，剩下的才是元信息和控件的位置。放不下的按顺序进 `⋯`——先是耗时和大小，然后是面板自己的控件（编辑器里是格式菜单、动作按钮，Decode 最先让），标签页和状态永远不动。所以窄面板里不会出现半个词，只会少一个按钮。
- **同一种东西只有一种样子：** tab、按钮、面板头、表格各一种写法，都来自 uikit。
- **密而不挤：** 表格和编辑器保持桌面工具的密度，最小字号 11，正文 13，行高 24。

## 5. 主题：Paper／Ink

默认主题是一套暖调中性灰白：浅色 **Paper**（白底、暖灰 chrome），深色 **Ink**（暖黑底）。色相里没有绿，只有一点暖；强调色是墨，绿色只留给 Success。只用平涂色，不做渐变、纹理和做旧。外观跟随系统，也可以手动选浅色或深色。主题名保留 Paper／Ink。

下表是 `internal/uikit/theme.go` 里的实际值：

| Token | Paper | Ink | 说明 |
| --- | --- | --- | --- |
| `Chrome` | `#f4f3f0` | `#171716` | 外壳：标题栏、侧栏、底栏，以及窗口右侧（侧栏收起时还有左侧）的 8px 边 |
| `Canvas` | `#ffffff` | `#1f1f1d` | 抬起的内容面板，也是 MyGo 的 Background 和 Surface |
| `Well` | `#ecebe7` | `#101010` | canvas 上的输入框 |
| `Hairline` | `#e3e1dc` | `#2e2e2b` | 面板的 1px 边、同一表面的分隔线、表头边框、对话框边缘 |
| `Fill`／`FillHover`／`FillPressed` | 文字色 6%／10%／14% | 文字色 8%／12%／17% | 控件、tab、chip 的面；失焦选中底用 `Fill` |
| `Text` | `#1a1a18` | `#e9e8e4` | 正文 |
| `TextMuted` | `#5f5d58` | `#a09e98` | 次要文字、2xx 的颜色，也是输入框聚焦时那 1px 边和编辑器里的标点 |
| `TextFaint` | `#9e9c96` | `#686660` | 只用于图标、禁用、占位和失焦标记线，不承载信息 |
| `Accent` | `#1a1a18` | `#e9e8e4` | 主按钮、焦点、选中标记、开关、复选框的开启态、活动行把手、表头拖动线 |
| `AccentHover`／`AccentPressed` | `#33332f`／`#474743` | `#ffffff`／`#cfcdc8` | |
| `AccentText` | `#ffffff` | `#101010` | 主按钮文字，也是 Success 面（向导的完成标记）上的文字 |
| `AccentTint` | `#d8d4cb` | `#45443f` | 聚焦时的选中浅底、输入框的焦点晕圈、活动行选区 |
| `Success` | `#26734a` | `#7cc492` | 检查通过、录制点、信任状态、向导里已完成的步骤 |
| `Info`／3xx／GET | `#2f6ea5` | `#8fb3d1` | |
| `Warning`／4xx／PUT | `#9c5906` | `#e0a458` | 设计稿是 `#a15c07`，见下面的对比度说明 |
| `Danger`／5xx／DELETE／慢请求 | `#c0392b` | `#e8806f` | |
| `MethodPost`／`MethodPatch`／`Socket` | `#5b6b1c`／`#7a3e6d`／`#2b6868` | `#b5c27a`／`#c99abd`／`#6fc3d6` | Ink 的 `Socket` 往蓝青挪，和 `Success` 的绿分开（决策 56） |
| `Inverse`／`InverseText` | `#1a1a18`／`#ffffff` | `#e9e8e4`／`#101010` | tooltip |
| `Shadow` | `rgba(26,26,24,.16)` | `rgba(0,0,0,.5)` | 浮层和对话框的柔和阴影 |
| 耗时分段 DNS／Wait／Download | PATCH／Info／Socket | 同左 | 三段相加是服务器用时（4.6） |

`Bar` 这个 token 已删掉：耗时条的底就是 `Fill`，和设计稿的 `.dur` 一样。

**Success 与 Accent 是两回事。** 第六阶段之前 `Success` 直接等于 `Accent`，强调色一变黑，"通过"就没有颜色了。现在 `Success` 是独立的一支绿，只出现在"这件事通过了"和"正在录制"：检查通过、录制 pill 的点、`https` 的信任状态、向导和引导里已完成的步骤、toast 的成功图标。其余全部走 `Accent`（墨）或中性面。耗时分段和消息方向原先借 `Success` 当第四种色相，改成 `Socket` 的青色——它们是数据，不是结论。

对比度由 `internal/uikit/theme_test.go` 检查（线性 sRGB 相对亮度，合成半透明底后计算）：正文、信号色（Info／Warning／Danger／Accent／Success）、method 色、主按钮文字在 canvas／chrome／well 三个表面上 ≥ 4.5:1，次要文字 ≥ 3:1，TextFaint 不参与；叠在表面上的 6%／10%／14% 面和选中浅底上，正文仍 ≥ 4.5:1，信号色和 method 色按 UI 组件的 3:1 计。第六阶段的实测最低值是 Paper 的 Info 在 well 上 4.52:1，信号色在按下态最低 3.39:1（Paper 的 Info 在 well 的 14% 面上）；`Success` 在三个表面上分别是 Paper 5.78／5.21／4.84、Ink 8.69／7.99／9.21，`AccentText` 在 `Success` 面上 5.78（Paper）／9.1（Ink）。Paper 的 warning 从设计稿的 `#a15c07` 深到 `#9c5906`，其余信号色与设计稿一致。第七阶段把 Ink 的 `Socket` 改成 `#6fc3d6`，它作为 method 色在三种表面上仍是 9.0:1 以上。

## 6. 设计系统：`internal/uikit`

### 6.1 规则

- **独立的包，** 只依赖 MyGo（`ui`、`ui/editor`、`ui/chart`），不碰 app 状态和业务包。
- **主题 = 语义 token 表。** `Palette` 是一张表，Paper、Ink 是内置的两张，`Theme(dark)` 由它生成 `ui.Theme`，`Colors(t)` 取回当前表。界面代码只引用语义 token，不能假设强调色是哪种颜色。字号、高度、圆角、间距由 uikit 导出，主题不能改。
- **界面只用 uikit。** `internal/app` 不直接调用 `ui.Button`、`ui.PrimaryButton`、`ui.ButtonBase`、`ui.TextInput`、`ui.Select`，不写 `ui.Hex`、数字字号和手写 hover／pressed。uikit 的组件建在 MyGo 的 Base 上，键盘、焦点和无障碍由 MyGo 负责；每个可交互组件都要传无障碍标签。
- **组件按变体设计，** 调用方选角色和变体，不调样式。布局（Grow、Width、SingleLine、Tooltip）照常在返回值上链式设置。
- **禁用放在外层：** uikit 按钮在构建时就决定 hover 外观，MyGo 的 `Hovered()` 也只看那一刻是否禁用。可能被禁用的按钮，用外层元素禁用（`ui.Row(c).Disabled(x).Children(...)`），不要在返回值上再调 `Disabled`。
- 不做防回退的检查脚本；写死值靠评审和组件总览发现。

### 6.2 字体

- 内嵌 Geist 1.9.0（400／500／600，界面）和同家族的 Geist Mono 1.9.0（400／700，数据），给 Windows、Linux 用，在 `internal/uikit/fonts`，来源和许可见那里的 README 和 `THIRD_PARTY_NOTICES.md`。注册失败只记录一次日志并回退。
- macOS 上用系统字体：界面 `FontUI = "system-ui"`（SF Pro），数据 `FontMono = "monospace"`（SF Mono），中文走系统的级联；SF 的许可不允许内嵌。在 `font_darwin.go`；其他平台在 `font_other.go`。等宽从 JetBrains Mono 换成 Geist Mono（3.5）：JetBrains 字面宽、x 高大，像代码编辑器，显得重；Geist Mono 窄一些、更安静。界面字体同时从 Inter 换成 Geist（3.5）：Inter 在小字号下显得瘦长，Geist 字面更开、和 Geist Mono 同一家族，两者放在一起协调。
- **不引入 Geist Serif。** 1.9.0 的归档里已经有 `GeistSerif-Regular` 和 Heart／Square／Triangle 三个衬线形状变体，但它们只有常规一个字重，是展示字体，没有界面角色；这个 app 的层级靠无衬线正文加等宽数据建立，衬线只适合网站或欢迎页的大标题。需要时单独引入，不要混进 uikit 的文字角色。
- **SF Pro／SF Mono 只在 macOS 上以系统字体使用。** Apple 的字体文档明确要求不要打包系统自带或可下载的 SF 字体；运行时用 `system-ui`／`monospace` 取系统的那一份是允许的，Windows、Linux 上则没有合法来源，所以继续用内嵌的 Geist。
- 中文不内嵌（一个字重就要 10 MB 以上），用系统字体回退。拉丁回退字体放在 CJK 字体前面，避免英文变宽、等宽失效：
  - 界面（Windows、Linux）：`Geist, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
  - 等宽（Windows、Linux）：`Geist Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
- 中文的成段说明用 `Prose`／`Hint`，行高 1.5 倍字号；单行标签仍用 `Body` 的紧凑行高。组件总览的 Chinese 一节固定渲染中英混排，作为每次改字体后的检查样张。中文界面的按钮宽度仍要用中文截图单独检查。Linux 上没有 Noto Sans CJK 时，Settings → 外观提示安装（待做）。

### 6.3 尺寸

| Token | 值 | 用途 |
| --- | --- | --- |
| `TextMeta`／`TextData`／`TextBody`／`TextTitle` | 11／12／13／15 | 计数和次要信息／等宽数据／正文、控件／标题。18 已删掉：结论也用 15 |
| `LineMeta`／`LineProse`／`LineTitle` | 1.45／1.5／1.3 | 次要信息／说明文字／标题的行高倍数；`Body` 和 `Mono` 保持字体自带的行高，避免撑高控件和表格行 |
| `HeightBar`／`HeightTabs`／`HeightStatus` | 40／32／24 | 标题栏／面板头和过滤栏／状态栏。只有这三档栏高 |
| `HeightControl`／`HeightLarge` | 24／28 | 按钮、输入框、tab、侧栏条目、表格行／URL 栏和 Send。`HeightCompact`（20）已删掉：设计稿没有第四档 |
| `RadiusInner`／`RadiusControl`／`RadiusPanel` | 4／6／10 | 嵌在控件里的面（分段控件的滑块、chip 里的按钮）／控件和 tab／弹层、卡片、对话框。`RadiusDialog`（14）已删掉 |
| `IconInline`／`IconLarge` | 14／16 | 行内和按钮／rail 和空状态 |
| `DurationFast`／`DurationHover`／`DurationStandard`／`DurationSlow` | 80／120／160／200ms | 离开和收起／悬停面和弹层进出／选中面和数字变化／面板和区域的开合。界面只写 token，不写毫秒 |
| `MotionSpring` | 200ms，无回弹 | 侧栏区域收起展开、面板和分栏改变尺寸时用的唯一弹簧；`RegionMotion` 和 `PaneMotion` 由它组成 |
| `DialogSmall`／`DialogMedium`／`DialogLarge` | 480／640／840 | 对话框宽度档，随窗口收缩 |

- 间距用 `t.Space(n)`，网格 4px，只用 4／8／12／16／24（细缝可用 `t.Space(0.5)`），每档有固定用途：

| 间距 | 用途 |
| --- | --- |
| 4 | 图标和文字之间（tab 之间只留 2，选中块自带 6 的左右内边距，窄检查器里也放得下一整行） |
| 8 | 同一行的控件之间 |
| 12 | 面板内边距 |
| 16 | 分组之间 |
| 24 | 区块之间 |

- 表格行高 24。MyGo 的 `ui.Table` 最小行高是 `t.Space(8)`，所以流量表格在局部主题里设 `Spacing = 3`。
- 分隔统一 1px `Hairline`，不靠色块分区。只有菜单、弹层、对话框有阴影：1px 边框画边缘，阴影只做柔和的浮起（弹层 `0 8 28`、对话框 `0 20 56`，颜色是 palette 的 `Shadow`：Paper `rgba(25,32,28,.16)`、Ink `rgba(0,0,0,.5)`）。
- 菜单的面由 uikit 接管：v0.9 的 `PopoverMenuButtonOptions.Panel` 让 `MenuSelect` 用和弹层同一张面（canvas、hairline、`RadiusPanel`、同样的阴影），并挂上 `popoverMotion`。菜单项本身仍是库画的（高亮是强调色实底，和 macOS 一致），圆角写死为 `t.Radius`。

### 6.4 文字角色（`text.go`）

| 角色 | 样子 | 用途 |
| --- | --- | --- |
| `Heading` | 15，500，行高 1.3 | 对话框、面板、检查器里的请求名 |
| `Body` | 13，颜色继承，字体自带行高 | 普通文字；在按钮和选中行里也对 |
| `Prose` | Body，行高 1.5 | 成段的说明文字、设备设置步骤、导入提示 |
| `Mono` | 12 等宽 | 线上数据：URL、header 值、路径、数字 |
| `Hint` | 13，次要色，行高 1.5 | 控件下或空面板里的一句说明，也是长句和中文正文的角色 |
| `Meta` | 11，次要色，行高 1.45，`tnum` | 计数、时间、大小；数字默认等宽，不再逐个调用点开 |
| `Label` | Meta + 500 | 字段名、分组标题（标题式大小写，不全大写，和 Finder 侧栏一致） |

等宽的次要信息写成 `Meta(c, s).Font(uikit.FontMono)`。`TextData`（12）只给等宽数据；界面文字最小 11、正文 13。

### 6.5 组件

已有（组件总览里都有）：

| 类别 | 组件 |
| --- | --- |
| 按钮 | `PrimaryButton`（24，强调色底、500 字重）、`ChipButton`（24，中性面，内容由调用方画：图标加快捷键）、`SecondaryButton`／`DangerButton`（24，无边框，面是文字色 6%／10%／14%）、`CompactButton`（24，同样的面，11 号字）、`ToolbarButton`（透明底，hover／按下是中性面，active 是强调浅底）、`IconButton`（24 正方形，必带 tooltip）、`RowButton`（列表里的整行，没有 hover）、`SwatchButton`（标记色） |
| 输入 | `Input`（well，无边框；聚焦时 1px 强调色内边和 3px 浅底光晕）、`FieldFace`（同样的面给自带控件的输入行，如搜索框和 URL 栏）、`ChromeFieldFace`（chrome 上的输入框：canvas 底加 1px 细线）、`Select`／`MenuSelect`（按钮的面；后者给用菜单做的选择，如 body 格式）、`Segmented`（中性凹槽加浮起的滑块，选中是 canvas 底加细线，只用于互斥的模式）、`Tabs`／`CountedTabs`（选中是中性面圆角块加正文色文字，可带计数；放在 `TabStrip` 里横向滚动，有藏起来的 tab 的一侧渐隐）、`SubTabs`（嵌在另一行 tab 选中内容里的第二层，选中只有正文色文字） |
| 列表和侧栏 | `ListTheme`（MyGo 表格／列表／树的选中色）、`Marker`／`RowMarker`、`Selected`（失焦换成中性面）、`SidebarHeading`（11 号、次要色、500，右端可带开关或加号）、`SidebarItem`（24 高、内缩 6 的圆角行，图标／圆点／箭头占同一个槽，右侧可带计数）、`SidebarButton`（同样的行，做动作而不是选中，`on` 时图标用强调色）、`SidebarDot`（没有图标时占住槽的圆点）、`SearchField`（带放大镜的单行搜索框，canvas 上是 well、chrome 上是 canvas 加细线） |
| 信号 | `Sparkline`（录制 pill 的速率曲线）、`ResponseMeta`（面板头右侧的耗时和大小，耗时悬停显示分段）、`StatusPill`（只有文字色，不填底；2xx／304 是次要色）、`MethodLabel`（12 等宽 500）、`MethodColor`、`StatusColor`、`MarkColors`、`BadgeColors` |
| 结构和反馈 | `EdgeFade`（滚动条被裁掉的一侧渐隐到表面色，`TabStrip` 和标题栏的 chip 用它）、`PaneHeader`（面板头的 32 高一行：内容 + 右侧）、`PaneTitle`（面板名，带状态点，点它把共享读取器交给这一侧）、`TabMenu`（长得像 tab 的菜单，放 More）、`KVRow`（只读）、`EmptyState`、`Toasts`、`Modal`、`DialogColumn`、`DialogTitle`、`DialogFooter`、`PopoverPanel`、`EditorTheme` |

待做（随用到它们的步骤加入）：

- 控件：Input 的错误态（设计稿是 1px 危险色内边）、32 档按钮、Checkbox／Switch 包装、LayoutToggle。
- 结构：可编辑 KVGrid（现在是 Key／Value 表头加行、没有外框）、FormRow（标签左、控件右、说明在控件下一行）、Splitter。
- 反馈：Callout（信息／警告／错误，一行文字加文字操作）、StatusText、CountBadge、CheckResult。
- 标志性组件：QueryBar（v3 的 chip 已改成中性面）、TimingBar、SourceBadge、EnvironmentSelect。`ActivityStrip` 在 v3 里是 24 高的一行：摘要、堆叠柱（14 高）和结束时间，没有单独的时间刻度行。

### 6.6 图标

只用 Lucide。尺寸只有 14 和 16 两档；颜色跟随文字，不单独上色。例外：开关类条目开着时图标用强调色（第 3 节）。

## 7. 交互和状态

- **Hover：** 按钮、工具栏按钮、tab 的背景变 Hover（第二层 tab 和分段控件只把文字变成正文色），立即变化，不做过渡。和 macOS 一样，列表、表格、侧栏的行没有 hover；不可点的也没有。
- **窗口失焦：** 窗口在别的窗口后面时，侧栏和挑选列表的选中浅底变成中性灰，表格的标记线变次要色。
- **按下：** 背景变 Pressed。
- **焦点环：** 键盘焦点显示 2px 强调色外框，鼠标点击不显示，所有可交互元素都要有。输入框例外：聚焦时是强调色细边加 3px 柔和光晕，不再画外框。对话框关闭后焦点回到打开它的控件，`Modal` 会请求一帧重绘让焦点环显示出来。
- **禁用：** 文字用 TextFaint，不响应 hover；tooltip 说明为什么不可用。
- **动效：** 只用于说明变化，时长一律用 token（`DurationFast` 80／`DurationHover` 120／`DurationStandard` 160／`DurationSlow` 200），面板和区域改变尺寸用 `MotionSpring`（200ms，无回弹）。
  - 悬停和按下的面：进入即时，离开用 120ms 淡回，指针扫过一排按钮时不落后。过渡每一帧都挂着（悬停帧时长 `-1`）：MyGo 只在上一帧也有过渡时才混色，撤掉过渡等于让离开的那一帧当成新元素。
  - 菜单、弹层、工具提示：淡入加上移 4px，进 120、出 80。
  - 对话框和命令面板：淡入，160。放大不做：缩放把画出来的像素挪离布局位置，开场那几帧点不到控件。
  - 侧栏分区折叠、面板和分栏改变尺寸：`RegionMotion`／`PaneMotion`，无回弹弹簧，约 200。
  - 计数、状态码、耗时这类数字变化：`ContentTransition{Numeric}`，160。
  - 录制中：有流量经过时状态点轻微脉动（1.2s，55%–100%），没有流量或暂停时静止。脉动画在点自己的 `Draw` 里（30fps），只重绘不重跑视图，流量表不会跟着每秒重建 60 次。
  - 不做动效的地方（高频或必须即时）：流量表新行进入、行选中和键盘上下移动、输入、自动跟随滚动。流量持续进来时用 `AnimatePose` 一类只重绘不重建的写法，保证列表不掉帧。
  - 系统要求减少动态效果时 MyGo 自己把值直接送到终态、过渡立即结束；`Loop` 继续（系统转圈也这样）。
- **拖拽：** 分隔线 hover 时变成强调色细线，指针变成调整大小；双击恢复默认。
- **右键：** 任何有动作的对象都支持右键，菜单内容见 UX 规范第 6 节。菜单的高亮项是强调色实底、反色文字（MyGo 菜单本来的画法，和 macOS 一致）。
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
| `colors` 的 `editor.background`、`sideBar.background`、`list.hoverBackground`、`list.activeSelectionBackground`、`focusBorder`、`button.background`、`input.border` 等 | Canvas、Chrome、Fill、AccentTint、焦点、Accent、Hairline |
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
     - 双击分隔线恢复默认（完成）：MyGo v0.8.1 的 `SplitReset` 让侧栏、左右／上下布局和检查器 Request／Response 分隔线双击回到默认；View → Reset desktop layout 仍保留。
   - 3.2 发送历史栏（完成）：选中已保存的请求时，编辑器左边多一栏 History／Examples（240，内容区不足 900 时不显示，仍可从请求的 `⋯` → History 进入）。History 只列从这个请求发出的运行记录（按请求 ID 或这个文档关联，不按 URL 匹配），点一条在响应区只读显示，可以 Open as draft，× 或再次 Send 回到自己的响应；Examples 点一条在 Examples tab 打开。M1 已补齐状态／来源筛选，以及 Run details… 中的备注、基线和对比。
   - 3.3 抓到的请求就是请求文档（完成）：选中抓到的 HTTP 请求，检查器就是它的请求文档：顶部 `Captured · 时间 · 进程` 和抓到的状态，下面是 method、URL、Send 和 Save to collection（检查器不足 600 时只留图标），编辑器只有一排标签页（Headers、Params、Body、Auth，其余在 More 里），抓到的请求直接填进这些标签页；只有改过或发过之后，More 里才出现 Original，用来读抓包当时的样子。旁边是抓到的响应，发送后换成这次的响应。第一次编辑或发送时才为这条抓包留一份草稿（copy on write），头部和行里有 Edited 标记，抓包本身不变；检查器 `⋯` → Discard edits 回到原样。Edit & replay、Back to traffic、Source #N 都去掉了；落盘的大请求体点 Edit request 才读，流和隧道只读。检查器在列表旁边时请求在上、响应在下；在列表下方（上下布局，或窗口不足 1000 时）或占满窗口时左右并排，不再按宽度切换。窄检查器里 Send 窄到 64，发送中只留 Cancel。
   - 3.4 收尾（完成）：
     - `workspace`／`captureSection`／`requestsSection` 合成一个 `page`（Live、Session、Request、API、Runs）。
     - 去掉文档 tab 栏，一次只显示一个请求：顶部是面包屑（集合 / 名字，点它改名或移动）、环境、Save、`⋯` 和 ×；新建的请求放在 COLLECTIONS 顶部的 Unsaved 里，改过没保存的已保存请求在集合里带圆点；没改过也没发过的已保存请求离开屏幕就关掉。Ctrl+Tab 仍在打开的请求之间切换。
     - muted 文字收进 uikit：新增 `CountedTabs`（带计数的 tab，请求编辑器和响应 tab 共用）、`MoreMenu`（`⋯` 菜单按钮）、`Glyph`（标记用的小图标）；说明文字改用 Hint／Meta，流量表的次要列用 Hint，调查板的 Request／Response 切换改用 `uikit.Segmented`。剩下 4 处是控件自己的状态色（格式下拉、遮住的密钥、协议分段），保留。
     - 截图矩阵：LIVE 的状态（`capture-*`）同时渲染左右和上下两种布局（上下的文件名带 `-stacked`），× Paper／Ink；单独的 `capture-stacked` 去掉。
   - 3.5 macOS 质感：
     - 3.5a uikit（完成）：高度 20／24／28，tab 行和面板头 28，表格行 24；macOS 上界面用系统字体；`Tabs` 改成强调浅底圆角块，新增 `SubTabs`，检查器的 Request／Response 面板和调查板的预览用它；`Segmented` 改成无边框凹槽加浮起滑块；`SecondaryButton`、`DangerButton`、`CompactButton`、`Select` 去掉边框、用中性面；`Input` 中性填充加聚焦光晕；`RowButton` 和流量表的行去掉 hover（流量表的客户端图标随行高改成 14）；侧栏分组标题不再全大写。
     - 3.5b 窗口和界面（完成）：
       - 侧栏占满窗口高度，红绿灯和品牌在它顶部；标题栏、内容和状态栏移到右边一栏，标题栏没有色带和底边框，录制 pill 居中在这一栏。红绿灯按标题栏中线对齐（v3 起标题栏 40，`TrafficLightPosition` Y 13）。
       - macOS 上窗口用 `VibrancySidebar`，根节点透明（在 `view` 最后设，因为每次 `SetTheme` 都会重画根节点），侧栏盖 90% 的 chrome 色，内容区不透明；其他平台没有材质，侧栏是平涂的 chrome 色。
       - 窗口在别的窗口后面时（`OnFocus`／`OnBlur`），侧栏和挑选列表的选中变灰。
       - 外观改成「跟随系统／浅色／深色」三选一，默认跟随系统；旧设置里的 `dark: true` 读成深色。
       - 流量表的数据改成 12 号等宽（`TextData`）；类型筛选改成和 `Tabs` 一样的浅底块，没有凹槽；流量搜索框和 URL 栏用 `FieldFace`（中性填充、细线、聚焦时强调色细边加光晕）；标题栏的布局切换按钮改成 24。
       - 侧栏占满高度只在 macOS（红绿灯在左上角）；Windows、Linux 的窗口按钮在右边，标题栏横跨整个窗口，品牌在它左侧，录制 pill 居中在窗口上。
       - 还没统一的：Settings 的分类列表（Settings 已是独立窗口，排版随 Settings 重新分区，M1）。
4. **标志性组件：** 录制 pill 的速率曲线、查询语法筛选、活动柱状图、耗时条、响应结论、瀑布视图（Table／Waterfall 切换）。
   - 4.1 录制 pill（完成，方案 A）：一个 24 高、细线边框的 pill，依次是状态点、状态（Recording／Paused／Proxy stopped）、等宽监听地址（设备接入时加 `· LAN`）、最近 60 秒的速率曲线（56×14，2 秒一个点，平滑曲线下面一层向下渐隐的浅色阴影）、最近 10 秒的平均速率，竖线后面是 20 的暂停／继续按钮；点左边部分打开 Settings → Proxy，代理停止时只有红点、文字和 `›`。暂停时代理照常转发，但不保留外部流量（连同它的 WebSocket／SSE 消息），编辑器发出的请求和重放仍然保留；曲线变平、不显示速率。命令面板里有 Pause／Resume recording。
   - 4.2 耗时条（完成，方案 C）：不加新列，Duration 单元格（112 宽）从右往左铺一块 19 高的浅底，至少盖住整个数字，再往左多出的长度按耗时的对数（0 到 60 秒），这样边缘不会切过数字；≥ 1 秒是慢请求，底是 Danger 16%，数字用正文色；侧栏的 Slow 也按 1 秒算（原来是 120 ms）。
   - 4.3 响应结论（完成；v3 收进面板头）：响应面板头一行 32 高，左边是状态（如 `200 OK`），正文字号、500 字重，只有错误状态用语义色，前面 6px 的状态点；右边是 11px 等宽的耗时和大小，悬停耗时显示分段卡片。面板窄于 420 时省掉耗时和大小，留给标签页。抓包和发送后的响应共用同一个面板头。没有状态码显示 No response，连接失败显示 Failed，手动发送的代理错误保留 proxy response 标记，不冒充上游 HTTP 错误。原始响应打开时去掉上方重复摘要；查看其他视图或发送后的新响应时仍保留原抓包摘要，便于区分来源。Protobuf 的消息类型、搜索框和 Schema 按钮统一 24 高、中性填充，输入框聚焦时加光晕；类型和 Schema 保持同组，搜索框只在空间不足时换行，消息说明单行省略并支持悬停全文；短面板滚动可达消息选择和复制操作。
   - 4.4 Activity strip（完成，方案 A；v3 起 24 高一行）：只占列表宽度，不加卡片外框；摘要、堆叠柱和结束时间共一行 24 高（v3 之前是 52 高、带三处时间刻度）。最近五分钟分成 60 个五秒桶，柱高表示总请求数，正常／304 中性，3xx 灰蓝、4xx 赭黄、5xx 和连接失败朱红；异常只染自己的堆叠部分，不把整根柱染红。搜索、协议和正文等条件同步影响背景柱，拖选只筛选表格，保留完整五分钟背景；选中后固定时间轴，区间外变淡，两个手柄调整边界，Clear／Escape 清除后恢复跟随。方向键平移、Shift+方向键调整右边界，时间条件也可在查询栏移除或 Reset all。选区按视图独立，不写入保存的筛选，替换会话时清除；停止或导入的历史流量以最新请求时间为准，实时捕获（包括暂停录制）继续按当前时钟衰减；空图不可新建选区，但已有空选区仍可调整和清除。
   - 4.5 Query bar（进行中，方案 A：chips 在前、输入在后）：
     - 第一步（完成）：语法解析和 chip。搜索框里的普通文字照旧匹配 host 和 path；`status:`（`404`、`>=400`、`4xx`、`200-299`，逗号表示或）、`host:`、`app:`、`method:`、`duration:`、`size:`、`header:`、`body:`、`color:`、`note:` 和单独的 `slow`（≥ 1 秒）是语法，值里有空格时用双引号。其他 `key:value`（比如 `localhost:8080`）仍是普通文字。
     - token 后面打了空格或按 Enter 才变成 chip，写进筛选对话框和右键菜单用的同一组条件，所以保存的筛选、视图和 Reset all 都不用改。没打完的 token 既不算条件，也不当 URL 文字，表格不会在输入时变空。值不合法时 token 留在框里、文字变红，悬停说明原因。同一个 key 再输一次会替换旧值，条件之间都是 AND。
     - chip 用等宽字、强调浅底，按语法原样显示（`status:>=400`、`app:"Google Chrome"`、`slow`）；点文字把它变回框尾的文字再编辑，× 移除。侧栏范围、协议和时间区间没有对应语法，只能移除。框内不再重复显示 URL 文字的 chip。chip 太多时横向滚动，滚动条贴在框的下边，工具栏保持一行。
     - 第二步（完成）：自动补全，用 MyGo 的 `ComboboxBase`，浮层挂在输入框下方，盖在 Activity strip 上面，最多 8 行，左边等宽的 token、右边次要说明或计数。补全的是光标所在的 token：没有冒号时补 key（`status:`、`slow` 等），选中后光标停在冒号后，接着列出值；有冒号时列出本次抓包里的值：`status` 给 2xx／3xx／4xx／5xx／>=400，`host`、`app`、`method` 取当前流量（按数量），开头匹配的排在前面。选中值等于打完一个 token，直接变成 chip。
     - 输入值时第一项高亮，Enter 直接取；输入 key 时不高亮，按 ↓ 才进入列表，所以 `st` 加 Enter 仍是搜索文字并跳到请求列表。浮层没有选项时关闭；关闭时按 ↓ 照旧跳到请求列表，点击空框会列出全部 key。Escape 关闭浮层。
   - 4.6 Waterfall（暂缓）：先不做列表的 Table／Waterfall 切换。改为响应结论第二行的耗时可悬停：卡片顶部一条各阶段占比的细条，下面逐行列出 DNS 查询（仅本请求自己查询时）、等待、下载的毫秒数和 Total，最后一行说明连接是否复用、连接和 TLS 是否算在等待里。DNS 单列出来后从等待里扣掉，几段相加等于服务器用时。抓包和编辑器发出的响应共用。现有数据只能分这三段；Connect／TLS 单独计时随 M5。
5. **随里程碑落地：** 首次启动引导和 Settings 独立窗口随 M1，Host 方案随 M2，Tools 窗口随 M4（见第 10 节）。
   - 5.1 首次启动引导（完成，方案 A：竖向清单）：`~/.vscroxy` 不存在时，流量列表的位置换成 520 宽的四步清单（开始抓包、信任证书、选择要解密的对象、完成），完成的步骤收起打勾，当前步骤展开并用强调浅底；打开状态存在 settings 的 `guide`，跳过或完成后关闭，命令面板 Show setup guide 重新打开。每一步按 app 观察到的状态完成，没有“下一步”按钮：抓到任何流量或系统代理指向本机算第 1 步，证书检查通过算第 2 步，有任一解密规则算第 3 步，第一条解密的 HTTPS 请求出现时关闭引导并选中它。第 1 步给监听地址、Turn on system proxy 和 Capture one app only…（打开 Client setup）；第 2 步 Install and trust；第 3 步列出已看到的前 6 个 App 和域名，勾选立即写入 HTTPS 规则，等待第 4 步时仍可继续勾选。抓包停止时第 1 步只给 Diagnose capture…。
   - 5.2 Settings 独立窗口（完成，第一步）：Settings 从弹窗改为单独的原生窗口（760×560，最小 600×420，`StateKey` 记住位置），左侧分类、右侧内容和原弹窗相同；Ctrl/Cmd+, 和齿轮按钮打开，已打开时只前置，关闭后再打开回到上次的分类。两个窗口共用同一份状态：Settings 里改外观等设置主窗口立即重画；证书和系统代理检查完成时两边都更新；Settings 在前面时提示消息显示在 Settings 里。会在主窗口打开对话框的按钮（Manage rules…、Ignored hosts…、Connect a device… 等）把主窗口前置，Settings 保持打开。Linux／Windows 上和主窗口一样隐藏菜单栏。没有主窗口时（无头测试）仍用原弹窗，内容是同一个函数。重新分区见 5.3。
   - 5.3 Settings 重新分区（完成）：分类改为 General／Appearance／Proxy／HTTPS／Capture／Devices，每页用 `ui.Form`／`ui.Fieldset`／`ui.Field`（标签右对齐成一列，控件在右，说明在控件下）。General：Manage rules…、Reload configuration、Show setup guide。Proxy：Listener（地址、端口）、System proxy、Upstream（原地编辑，Apply 或回车立即生效，不再跳到弹窗）。HTTPS：Certificate、Decryption（Every domain 开关立即生效、Manage domains…）、Wireshark（TLS key log）。Capture：No Caching 开关、Ignored hosts／apps（显示条数）。Devices：Connect a device…、Set up a local client…。改动立即生效；端口和 TLS key log 需要重启，和启动时的值不同时显示“Restart to apply”标签，改回去标签消失。样式按 macOS System Settings：侧栏分类带图标；每页顶部是标题和一句说明；每组字段是一张圆角卡片（`uikit.SettingsPage`／`uikit.SettingsGroup`，仍是 `ui.Form`，整页标签对齐），内容最宽 640；状态文字和按钮分成两个字段，禁用的按钮不再把 Status／Trust 标签变灰。
   - 5.4 允许列表（完成）：Settings → Capture 新增 Allow list 组：开关 “Only show allowed traffic”（立即生效，记在 settings.json），Allowed hosts…／Allowed apps… 用和忽略名单相同的编辑弹窗，存在 `allow.txt`／`allow-apps.txt`。开启后只记录匹配的 host（含子域名）或 App，其余照常代理；忽略名单优先。开关打开但两个列表都空时，开关下用红字提示“什么都不会显示”。开启期间流量表上方有 “Allow list” 工具 chip：点 chip 打开 Settings → Capture，点 × 关闭。
   - 5.5 内存上限（完成）：Settings → Capture 新增 Memory 组：Keep up to（1,000／5,000／20,000／50,000 条，默认 5,000，即原来的固定上限）和 Memory limit（128 MB–2 GB，默认 512 MB，估算请求头、保留的 body 和消息），以及 Disk limit（1–20 GB，默认 5 GB，溢出到临时文件的 body 和消息）。超出任一上限先删最旧的请求，Pin 的保留；被删请求的落盘文件一起删（只删本次抓包的 spill 目录里的文件）。改动立即生效并记住，In use 显示内存估算、磁盘占用和条数。
   - 非首次启动、列表为空时，空状态只留一句话和两个按钮：Turn on system proxy（监听中才有）和 Diagnose capture…；Client setup、设备和 HTTPS 在侧栏底部。
6. **第三方主题：** 第 8 节的映射、内置主题和导入。
7. **设计系统 v3（进行中）：** 设计稿见第 2 节。屏幕的布局和信息架构不变，只换表面、颜色、尺寸和组件。
   - 7.1 基础层（完成，2026-11-11）：
     - `Palette` 换成 v3 的角色：`Chrome`／`Canvas`／`Well`／`Hairline` 三种表面加细线，`Fill`／`FillHover`／`FillPressed`（文字色的 6%／10%／14%，Ink 是 8%／12%／17%），`Shadow`；`Background`／`Panel`／`Hover`／`Pressed`／`Border`／`Bar` 删掉，屏幕改用新名字。
     - `Theme()` 不再把 MyGo 的 Surface 设成 Background：Background 是 canvas，Surface 是 chrome，所以表头和 MyGo 自己的栏是 chrome，输入框由 uikit 自己画（canvas 上是 well，chrome 上是 canvas 加细线）。
     - token：`TextHeadline`（18）删掉，`Mono` 改成 12；`HeightCompact`（20）和 `RadiusDialog`（14）删掉，新增 `HeightStatus`（24）和 `RadiusInner`（4）；`HeightBar` 44 → 40，`HeightTabs` 28 → 32。
     - 字重只有 400 和 500：uikit 的 `Heading`、`Label`、`DialogTitle`、`SettingsPage` 和屏幕里的 600 全部改成 500。
     - 组件：`Tabs` 的选中改成中性面（不再强调浅底）；`StatusPill` 不再填底；`Segmented` 的凹槽是中性面、滑块是 canvas 加细线；`QueryChip` 改成中性面；`Input`／`FieldFace` 是 well 加 1px 强调色内边和 3px 浅底光晕，新增 `ChromeFieldFace`；`PopoverPanel`、`Modal`、`SettingsGroup` 用 canvas 和 `Shadow`；`Selected`、`ListTheme` 失焦换成中性面，标记线换成 faint；`MoreMenu` 恢复成没有面的 `⋯`。
     - 屏幕只做和 token 直接相关的替换：侧栏和栏的背景改成 `Chrome`，耗时条的底改成 `Fill`，协议筛选的选中改成中性面，过滤栏 40、状态栏 24，红绿灯 Y 13。
   - 7.2 检查器的标签页（完成，2026-11-11）：
     - 每个面板只剩一排标签页：请求是 Headers／Params／Body／Auth，响应是 Body／Headers／Cookies／Timing（流另有 Messages）；Scripts、Checks、Examples、Docs、Settings、Raw、Cookies、Original 收进请求的 More，响应的 Raw／Checks／Console 收进它右侧的 `⋯`。从 More 选中的视图作为最后一个 chip 出现在同一行，所以面板永远知道自己显示的是什么。
     - 去掉 Original tab：抓到的请求直接进这组标签页，是它的文档；改过或发过之后 More 里才出现 Original（抓包当时的原始报文）。
     - 面板头 32 高：标题在左、标签页紧跟、元信息在右；响应面板的标题就是状态（`200 OK`，正文色，只有错误状态上色，前面 6px 状态点），耗时和大小是右侧 11px 元信息。18px／48 高的响应结论行删掉。
     - Request 与 Response 默认 45:55（设计稿的 5:6）；检查器窄于 760 时上下堆叠（列表右侧的窄列就是这种情况）。请求编辑器页用同一条规则：够宽并排、窄了堆叠；并排时 45:55，堆叠时请求占 55% 的高度。
     - 面板头按量出来的宽度取舍（第七阶段取代了「窄于 430」的阈值）：放不下的先让耗时／大小——它们从面板头消失，进 `⋯` 菜单的第一行，状态悬停也显示同样的文字，四个 tab 因此完整可见、不靠横向滚动。
     - 空的 header 表不再是带框的表格：Key／Value 表头加一行 Add header；有行时也是无框的行，行内单元格没有分隔线。Key 列至少 140 宽，常见 header 名不会被截断。空响应是居中的一句话加一行提示。
     - Body 视图有读取开关（Read as）：Edit 是编辑器，其余按内容类型和字节列出 Pretty、Hex、Protobuf、表单／XML／MessagePack 等，读的是草稿自己的字节、只读不改；原来的 Original 里能读的结构化内容因此仍能看到。
     - 请求头的右键筛选菜单从读取器搬到了编辑器表格的 Key 单元格，功能不变。
     - 请求侧的读取器（结构化／Protobuf／表单视图、每侧独立的 body 格式、响应头的右键菜单）只在只读抓包（流、隧道、带 trailer、落盘 body）和响应侧可用：编辑器把请求体当字节编辑。这是这一步已知的功能缺口，见 7.3。
   - 7.3 侧栏（完成，2026-11-11）：
     - 侧栏在 chrome 面上分成两个固定区域，中间一条细线，各自滚动：上区 Traffic＋Sources，下区 Collections（搜索框、集合树、树末尾的 Sessions）；页脚是 Client setup、HTTPS rules、Diagnostics。上区最高占侧栏的 55%，下区吃掉剩下的高度，所以 20 个域名加一棵长树时两边都还在屏幕上、各自滚动。
     - 行：24 高，左右内缩 6，圆角 6，左边一个固定宽度的图标槽（16，MyGo 的 source list 条目按字号画图标），所以带图标、带圆点、带箭头的行文字都对齐在同一条线上；树在图标槽之前按层级缩进 12。分区标题 11 号、次要色、500，右端可以放开关或加号。
     - Sources：Apps｜Domains 是一个小号 `Segmented`（20 高，字 11），默认 Domains；只列前五项（按请求数），超过时末尾一行 Show all／Show fewer。主机名、App 名和所有标签用界面字体，方法标签仍是等宽（`MethodLabel`）。
     - 选中仍是强调浅底，窗口在后台时换成中性 `Fill`，形状不变。
     - 旧入口的新位置：Views（View 1、New view、保存的筛选按钮）作为 Traffic 里的行；Diagnostics 进页脚；Collections 的 `⋯` 在标题行加号左边；Sessions 变成集合树末尾可折叠的一行，里面的会话和 Open session… 缩进一级。
   - 7.4 标题栏和列表（完成，2026-11-11）：
     - 第一条请求上方只有四行 chrome：标题栏 40、过滤栏 32（`HeightTabs`，控件 24）、活动行 24、表头 24。原来的「已启用工具栏」28 高一行去掉，工具 chip 进标题栏。
     - 标题栏从左到右：品牌（非 macOS）、录制 pill、Host 方案 chip、已启用工具 chip（`Fill` 面、无边框，带 ×；放不下时横向滚动，右侧渐隐）、命令栏（canvas 底加 1px 细线，左边放大镜、右边 `⌘K`/`Ctrl K`，点它打开命令面板）、布局切换、Tools、Settings、`⋯`。原来的命令按钮就是这条命令栏。
     - 活动行 24 高：摘要（`Last 5 min · 2 requests`）在左，堆叠柱在右（14 高），最右是结束时间（11 号等宽）。原来那行三处时间刻度去掉（设计稿没有）。柱色：正常是中性面，3xx 蓝、4xx 赭黄、5xx／连接失败和慢请求（≥ 1 秒）朱红；摘要那一段固定 240 宽，拖选时柱不会在指针下移动。
     - 流量表：Host、Client、Method 和备注用界面字体（方法仍是 12、500、方法色），等宽只留给状态码、路径、耗时、大小、时间和数字；行高 24；选中行失焦时保留形状、换成中性 `Fill`，和侧栏一致（`ListTheme`）。
     - 状态栏 24（`HeightStatus`），各页面同一条 `footer`。
     - 集合树去掉文件夹图标：展开箭头就是行首的图标槽，名字紧跟其后，每层缩进 8；请求行没有空槽，method 直接从缩进后开始，名字因此从 x≈74 起（原来 x≈112）。Sessions 行同样只有箭头和名字，「Open session…」保留时钟图标。树改用 MyGo 的 `TreeBase`（无外观）自己画行，保留键盘导航、右键菜单和展开状态。
   - 7.5 细节打磨（完成，2026-11-11）：布局和结构不变，只减少视觉噪音、统一控件、补交互反馈。依据是 Linear 的"导航不该抢内容的注意力、结构要被感觉到而不是被看到"和 Apple HIG 的侧栏约定（工具栏项目精挑、侧栏可隐藏、关键入口不放在侧栏底部）。
     - 分隔线：Capture 页只留区域之间的线（侧栏与内容、列表与检查器、请求面板与响应面板、状态栏上沿）。抓包信息行、URL 行、请求编辑器面包屑行、只读提示行、两个面板头、调查板的面板条、连接详情的面板条都去掉了底线；表格表头没有列分隔线，拖动列宽的命中区保留，指针停在列边缘时才画强调色细线（表头自己的下沿改为逐格画，替代表格主题的 Border）。
     - 输入框只剩两种：chrome 上是 `ChromeFieldFace`（canvas 加细线），canvas 上是 `FieldFace`（well）。命令入口改成 `ChipButton` 后，同一屏不再有第三种。
     - 命令栏改成 24 高的按钮：放大镜加 `⌘K`／`Ctrl K`，点击和快捷键都打开命令面板，省出的宽度留给工具 chip。uikit 新增 `ChipButton`。
     - 侧栏退后：未选中行的文字用 `TextMuted`、图标用 `TextFaint`，悬停和选中回到 `Text`；悬停色差一帧（图标色在构建时取，MyGo 没有逐属性的过渡），用 `Local` 记住上一帧并请求补一帧。计数为 0 不显示。对比度：`TextMuted` 在 canvas／chrome／well 上是 5.94／5.37／5.09（浅）和 6.76／6.13／7.20（深），都过 4.5:1，所以没有改调色板；`TextFaint` 只用于图标（装饰，随行的文字标签），在 chrome 上是 2.39（浅）／2.94（深）。
     - 流量表去装饰：去掉耗时列的底色块和状态列的圆点（`statusIcon` 列一起删掉），正常行只有文字；慢请求（≥ 1 秒）的耗时和 4xx／5xx 的状态用信号色，3xx 按次要文字读。
     - 集合树的方法槽按当前可见的最长方法定宽、右对齐：名字对齐在同一条线上，`GET` 和名字之间只剩行的 8px 间距（原来有 24）。
     - 侧栏分区可折叠：Traffic、Sources、Collections 的标题点击收起或展开，悬停时显示箭头，折叠状态存进 settings 的 `sidebarClosed`，下次启动保留。reveal（`⌘1`／`⌘2`、命令面板打开集合、打开会话）会展开对应的分区。
     - 侧栏可隐藏：标题栏新增按钮（`PanelLeftOpen`／`PanelLeftClose` 图标，提示带 `⌘B`），`⌘B` 和命令面板里的 Toggle sidebar 仍在；窗口窄于 `sidebarMinWidth`（860）时自动让开、变宽后回到用户原来的选择，太窄时按钮禁用并说明原因。
     - 空流量表的引导：没有抓到请求时，空状态给两个动作——启动代理（监听没在跑时）和 Client setup；代理在跑时是 Client setup 和 Diagnose capture…。侧栏页脚的入口保留。`startCapture` 会一直阻塞到监听关闭，所以按钮在 goroutine 里调用它。
     - 交互反馈：`DurationHover`（120ms）是全 kit 唯一的过渡时长，toast 进出用它；MyGo 的控件面按构建状态画，悬停和按下立即变色，没有可设的时长，这一点写在 6.3。行内操作（视图行的 ×、树行的 `⋯`）只在悬停或键盘聚焦时出现，用透明度隐藏、不挤动行内其他内容。有快捷键的图标按钮在提示里带快捷键：命令（`⌘K`）、侧栏（`⌘B`）、Settings（`⌘,`）、Host 方案（`⌘⇧H`）、关闭请求（`⌘W`）、搜索框（`⌘F`）。
## 10. 新界面

功能路线图带来的新界面，都按本文和 UX 规范设计。每个界面在对应里程碑开始前先出设计稿，确认后再实现。

| 界面 | 要点 | 里程碑 |
| --- | --- | --- |
| 首次启动引导 | 嵌在流量列表空状态里的 4 步引导（UX 规范第 9 节）；每步一个主按钮，完成的步骤打勾，可跳过 | M1 |
| 录制 pill 和暂停 | 三种状态（录制中、已暂停、代理停止），暂停按钮在 pill 内 | M1 |
| Settings 窗口 | 独立窗口，左侧分类（通用、外观、代理、HTTPS、抓包、设备），右侧 FormRow；改动立即生效，需要重启的项目标明 | M1 |
| Sessions（已完成） | 侧栏集合树末尾可折叠的 Sessions 行和多会话 tab；payload 只读并标记历史，Pin／颜色／备注可保存，跨会话 Diff；启动只读标题，打开时才加载，可删除 | M1 |
| History（已完成） | 请求的 History／Examples 中间栏；状态／来源筛选，Run details… 进入基线、备注和 Diff；各入口共用 experiments.json | M1 |
| 连接诊断（已完成） | 一个页面：上方输入目标 URL，下方按"监听 → 系统代理 → 证书 → 解密规则 → 实际流量"逐项打勾或打叉，每项一个修复按钮 | M1 |
| Host 方案（已完成） | 标题栏下拉框：列出方案和开关，底部"管理方案…"；Tools 窗口里左侧方案列表（拖动排序、开关），右侧 hosts 编辑器（语法高亮、冲突提示）。设计稿：[下拉框](https://ampcode.com/user-content/attachments/89b87b593e44b850fa2c15f43d2c8555826507b08af5ee1b4e5c2e2a358d5f44-file.png)、[管理界面](https://ampcode.com/user-content/attachments/29b10b4e637e7374b61763cd22d7a305ae8e17bb43be0fbd59696a3d31db3c75-file.png)。Tools 窗口先只开 Host profiles，其余分类灰显，随 M4 开放；Hosts 按钮在录制 pill 右侧，窗口太窄时 pill 让开 | M2 |
| 整理成集合向导 | 大弹窗，顶部步骤条（范围 → 合并接口 → 变量 → 结构 → 示例 → 完成），每步左侧设置、右侧结果预览 | M3 |
| 对应请求提示 | 请求文档头部一个小链接"对应请求：Get user"，点开或存为示例 | M3 |
| Tools 窗口（规则页已完成） | 独立窗口，左侧分类（规则、场景、脚本、Host 方案、模拟），中间列表（开关、名称、匹配条件），右侧详情。规则页：列表上方是规则测试框和当前 Scenario 菜单；行是开关、类型图标、名称、等宽的 method 和匹配条件，次要色的 Scenario 名和命中数（0 次不显示）；测试框有内容时，会作用的规则按作用顺序标 1、2、3，其余变淡、悬停说明原因，命中数让位给序号；拖动行或 `⋯` → Move up／down 调整顺序。右侧是表单（标签右对齐），底部 Save rule。匹配条件：URL 一行是分段控件（Contains／Wildcard／Regex）加等宽输入框，说明随方式变化；Apps 一行逗号分隔；Headers 每条一行（名称、比较方式、值、×），下面是 Add header condition。列表第二行写 method、URL（正则加 `/…/`）、`@App` 和 `+N headers`；测试框写成 `GET URL @ App; Header: value`，也接受 ws／wss。表单顺序是类型、匹配条件、动作、Scenario；Map Remote 的动作是 Send to（含端口）、Rewrite path（两个等宽输入框，中间 →）和 Keep the original Host header；Block 的动作是分段控件 Refuse／Drop connection／Hide，说明随动作变化，隐藏的规则在列表里用 eye-off 图标；Map Local 的动作依次是 Response from（文件或文件夹，File…／Folder…）、Body（180 高的代码编辑器，按内容高亮）、文件夹时的 Strip path、Status 和 Headers。设计稿：[方案 A 三栏](https://ampcode.com/user-content/attachments/88eb21206f0403c54299e1294651d9f6f40ad495272474fd3b652cdcb997e7a1-file.png)，吸收 [方案 B](https://ampcode.com/user-content/attachments/ede052aa7be29ff829fe9a6c7b9bd93abda9c6e57577df13523bdd3d718e4e7a-file.png) 的 Scenario 标签和命中数、[方案 C](https://ampcode.com/user-content/attachments/209eaaab6a75f3674a54eb76e6552e33d7d755f9a62429dad6ee67934ecb49a5-file.png) 的作用序号；原则是默认状态和 A 一样安静，多出来的信息只在需要时出现 | M4 |
| 规则脚本编辑器（已完成） | Tools → Scripts：左侧脚本列表（钩子、被几条规则使用、解析错误），列表上方分段控件切换 Rule scripts／Addons（addon 行显示 `from "名字"` 和被几个脚本引用，没有 Console），右侧代码编辑器（解析错误下划线）、Insert snippet，下方 150 高的 Console；规则表单的 Script 一行选脚本、Edit script 跳过去。Scenarios 页是场景列表（开关、规则数）和场景内的规则；Mocks 页是只列 Map Local 的规则页 | M4 |
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

截图（输出目录必须不存在或为空）：

```bash
# 组件总览：Paper／Ink 各一张总览，加按钮的 hover／pressed
VSCROXY_UIKIT_SNAPSHOTS="$PWD/.amp/in/artifacts/uikit" \
  go test ./internal/uikit -run '^TestOverviewSnapshots$' -count=1

# 界面矩阵：浅／深色 × 1280／960 等宽度，覆盖各界面、菜单、对话框和状态
VSCROXY_SNAPSHOTS="$PWD/.amp/in/artifacts/ui" \
  go test ./internal/app -run '^TestUISnapshots$' -count=1
```

- 评审外观先看组件总览，再看界面矩阵；每一步附前后对比。
- 菜单、hover、tab、拖动分栏这类交互，另外在真实窗口里录屏检查。
- 现有 UI 测试按无障碍标签点击和查找，是行为不变的保障。
- macOS、Windows 的原生窗口、读屏软件还没有实机验收（Windows 没有 runner）。

## 12. 决策记录

1. Windows、Linux 内嵌 Geist 和 Geist Mono；macOS 用系统的 SF Pro 和 SF Mono；中文用系统字体（3.5，原来三个平台都内嵌 Inter 和 JetBrains Mono）。
2. 强调色是墨：Paper `#1a1a18`、Ink `#e9e8e4`（v1 的靛蓝、v3 前五阶段的油墨绿都已取代）。
3. 外观默认跟随系统，可选浅色或深色（3.5b）。
4. 行高 24，控件 24，URL 栏和 Send 28（3.5；原来是 28／28／32）。
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
15. 形状靠近 macOS：圆角 6／10／14，嵌套同心，浮层柔和阴影；不做胶囊按钮（3.0）。毛玻璃只在 macOS 侧栏，且盖 90% 不透明的 chrome 色（3.5）。
16. 抓到的请求原地编辑：第一次编辑或发送才留草稿，草稿跟着抓包，只在本次会话，清掉抓包就没了；保存到集合要先起名、选集合（3.3）。
17. 去掉请求文档的 tab 栏，一次只显示一个请求，未保存的新请求在 COLLECTIONS 顶部 Unsaved 里（3.4）。
18. tab 用方案 B：第一层是强调浅底圆角块（借 Telegram 的选中色，不要它的胶囊轨道），第二层只有强调色文字（3.5）。第一层的文字后来改成正文色、不加粗，和选中行一致：浅底已经表示选中，再叠强调色和粗体是重复（unslop 检查，2026-11-01）。
19. 按钮和输入框不画边框，面是文字色的低透明度叠加；列表和表格的行没有 hover；菜单高亮是强调色实底（3.5）。
20. 强调色不跟随系统的强调色（3.5）。
21. macOS 上侧栏占满窗口高度，红绿灯和品牌在侧栏顶部，录制 pill 居中在侧栏右边那一栏；Windows、Linux 的标题栏横跨窗口（3.5b）。
22. 录制 pill 用方案 A（暂停按钮在 pill 内）；耗时条用方案 C（铺在 Duration 单元格里，不占新列，尽量利用空间）；慢请求的线是 1 秒（4）。
23. 响应结论用方案 A（完整状态在第一行），尺寸允许调整：落地用 18px／48 高，而不是概念稿的 22px／60 高，给正文留空间；相邻的 Protobuf 工具栏同时按 24 高的控件规范协调（4.3）。
24. 不引入 Geist Serif；排版优化集中在行高和层级：`Prose`／`Hint` 用 1.5 的行高承载长句和中文，`Meta` 用 1.45 并默认 `tnum`，标题用 1.3；`Body` 和 `Mono` 保持字体自带行高，避免撑高控件和 24 高的表格行。残留的全大写分组标签改成句首大写（2026-11-04）。
25. v3 用三种表面：chrome（侧栏、栏、表头）、canvas（数据）、well（canvas 上的输入框）；chrome 上的输入框是 canvas 加 1px 细线。MyGo 的 Background 是 canvas、Surface 是 chrome，输入框的面由 uikit 自己画，不再和窗口同色（2026-11-11）。
26. 选中、主操作和已开启用强调色：标签页、过滤器、命令面板的选中是中性面；`StatusPill` 不填底，状态色和方法色只染文字，例外是慢请求的耗时标签（2026-11-11；"绿色"的角色在第六阶段由墨色接管）。
27. 控件不描边，面是文字色的 6%／10%／14%（Ink 8%／12%／17%）；选中失焦时保留形状、换成中性面（2026-11-11）。
28. 尺寸只有三档栏高（40／32／24）、两档控件（24，URL 和 Send 28）、四档字号（15／13／12 等宽／11）、两个字重（400／500）、三档圆角（4／6／10）。`HeightCompact`、`RadiusDialog`、`TextHeadline` 删掉（2026-11-11）。
29. 等宽只用于线上数据：路径、header 值、URL、数字、body；host、client 和标签用界面字体（2026-11-11）。
30. Paper 的 warning 从设计稿的 `#a15c07` 深到 `#9c5906`：前者在 well 上只有 4.44:1，其余信号色和设计稿一致；设计稿的信号色在按下态是 3.6–4.5:1，测试对叠在表面上的面按 UI 组件的 3:1 计（2026-11-11）。
31. 每个面板只有一排标签页，面板头 32 高：标题、标签页、右侧元信息。请求的 Headers／Params／Body／Auth 是 tab，其余视图进 More；响应的 Body／Headers／Cookies／Timing 是 tab，Raw／Checks／Console 进 `⋯`。从 More 选中的视图作为最后一个 chip 回到同一行，不再开第二排（2026-11-11）。
32. 去掉 Original tab：抓到的请求直接是它的文档，改过之后 More 里才有 Original（原始报文）。请求侧原来的结构化／Protobuf／表单读取回到 Body 视图的读取开关（Read as），读草稿的字节；每侧独立的 body 格式只属于只读抓包的读取器（2026-11-11）。
33. 响应面板的标题就是状态，正文色、500，只有错误状态上色，前面 6px 状态点；耗时和大小是右侧 11px 元信息。18px／48 高的响应结论行删掉（2026-11-11）。
34. 检查器里 Request 与 Response 默认 45:55，请求编辑器页并排时相同、堆叠时 55% 高度；检查器窄于 760 时上下堆叠。面板头始终保持一行，且标签页优先：放不下时耗时和大小让位（进 `⋯` 和状态悬停），四个 tab 完整可见（2026-11-11；取舍在第七阶段改成按实测宽度）。
35. 侧栏分成两个固定区域（上：Traffic 和 Sources；下：Collections 和树末尾的 Sessions），中间一条细线，各自滚动，上区最高 55%；流量再多也不会把集合推出屏幕（7.3，2026-11-11）。
36. 侧栏每行 24 高、内缩 6、左边一个固定宽度的图标槽，没有图标的行放一个圆点，所以文字都对齐；树的缩进加在图标槽之前。分区标题 11 号、次要色、500（7.3，2026-11-11）。
37. Sources 用一个 Apps｜Domains 的小号分段开关只显示一类，默认域名，列前五项，超过时末尾一行 Show all；Views 作为 Traffic 里的行，Diagnostics 进页脚，Sessions 是集合树末尾可折叠的一行（7.3，2026-11-11）。
38. 面板放不下时，Body 的格式菜单也像耗时和大小一样让位，进 `⋯` 菜单（7.2 的补遗，2026-11-11；第七阶段起按实测宽度决定）。
39. 标题栏里放已启用的工具 chip 和命令栏：原来的「已启用工具栏」28 高一行去掉，第一条请求上方只剩标题栏 40、过滤栏 32、活动行 24、表头 24。chip 放不下时横向滚动、右侧渐隐；命令栏是 canvas 底加细线、点开命令面板，取代原来的命令图标按钮（7.4，2026-11-11）。
40. 活动柱状图缩成 24 高的一行：摘要在左、柱在右、结束时间在最右，三处时间刻度去掉；正常柱是中性面，3xx／4xx／5xx 和慢请求用信号色。摘要那一段固定 240 宽，拖选时柱不在指针下移动（7.4，2026-11-11）。
41. 流量表里 Host、Client、Method、备注用界面字体，等宽只留给状态码、路径、耗时、大小、时间和数字；方法仍是 12、500、方法色（7.4，2026-11-11）。
42. 集合树用 MyGo 的 `TreeBase` 自己画行：展开箭头占行首的图标槽，没有文件夹图标，请求行没有空槽（method 直接从缩进后开始），每层缩进 8；Sessions 行同样只有箭头和名字，「Open session…」保留图标（2026-11-11）。
43. 线只画在区域之间：侧栏与内容、列表与检查器、请求面板与响应面板、状态栏上沿。面板头、抓包信息行、URL 行、面包屑行都没有底线；表头没有列分隔线，拖动列宽的命中区保留、指针停在边缘时才显示（7.5，2026-11-11）。
44. 输入框只有两种：chrome 上是 canvas 加细线，canvas 上是 well。标题栏的命令入口改成 24 高的按钮（放大镜加 `⌘K`／`Ctrl K`），不再和过滤栏的搜索框长得一样（7.5，2026-11-11）。
45. 侧栏退后：未选中行文字次要色、图标 faint，悬停和选中回到正文色，计数为 0 不显示。`TextMuted` 在三种表面上都过 4.5:1（浅 5.94／5.37／5.09，深 6.76／6.13／7.20），所以调色板没动；图标是装饰，浅色 chrome 上 2.39（7.5，2026-11-11）。
46. 流量表去装饰：耗时没有底色块，状态没有圆点（`statusIcon` 列删掉），慢请求（≥ 1 秒）的耗时和 4xx／5xx 的状态才带信号色，3xx 按次要文字读（7.5，2026-11-11）。
47. 集合树的方法槽按当前可见的最长方法定宽并右对齐：名字对齐，`GET` 和名字之间只剩行的 8px（7.5，2026-11-11）。
48. 侧栏的 Traffic／Sources／Collections 可以折叠，状态存进 settings；侧栏可以隐藏（标题栏按钮、`⌘B`、命令面板），窗口窄于 860 时自动让开、变宽后回到原来的选择（7.5，2026-11-11）。
49. 空流量表的主要操作是启动代理和 Client setup；Diagnostics 留在侧栏页脚（7.5，2026-11-11）。
50. `DurationHover`（120ms）是全 kit 唯一的过渡时长；MyGo 的控件面按构建状态画，悬停和按下立即变色，没有逐属性的过渡（7.5，2026-11-11）。
51. 强调色改成墨：Paper `#1a1a18`、Ink `#e9e8e4`，悬停 `#33332f`／`#ffffff`，按下 `#474743`／`#cfcdc8`；中性色去掉绿调、带一点暖（Chrome `#f4f3f0`／`#1f1f1d`，Canvas `#ffffff`／`#171716`，Well `#ecebe7`／`#101010`，Hairline `#e3e1dc`／`#2e2e2b`）。应用图标是黑猫，强调色跟着走（第六阶段，2026-11-12）。
52. `Success` 从强调色里拆出来，是独立的绿（Paper `#26734a`、Ink `#7cc492`）：绿色只表示"通过"和"正在录制"。原来借 `Success` 当色相的耗时分段（Download／Receive）和消息方向改用 `Socket` 的青色；"Applied actions"这类说明文字改成次要色。测试把 `Success` 按信号色断言，并加了一条"Success 不等于 Accent"的回归（第六阶段，2026-11-12）。
53. 选中浅底 `AccentTint` 比设计稿给的值再深一档（Paper `#d8d4cb`、Ink `#45443f`）：强调色变墨以后，`#e0ded8`／`#3b3a37` 和悬停面只差 1.10／1.16 倍亮度，一眼分不出"选中"和"悬停"。聚焦时是浅底加 2px 墨色标记线，失焦时是中性 `Fill` 加 faint 标记线，两种状态都保留形状（第六阶段，2026-11-12）。
54. 文字选区的透明度从 30% 降到 24%：墨色 30% 在浅色下是一整块很重的灰。24% 时选中文字在 Paper 上 11.8:1、Ink 上 7.0:1，都还清楚（第六阶段，2026-11-12）。
55. 面板头的取舍按实测宽度而不是固定阈值：面板用 `ui.Handle` 记住上一帧的宽，标题、标签页、`⋯` 的量好的宽度扣掉，剩下的分给耗时／大小和面板自己的控件；标签页和状态不参与取舍。原来是「面板窄于 430 就把耗时藏起来」的阈值，遇到编辑器那种还带 Pretty、Decode 和动作按钮的面板就不准了，1280 宽下 Cookies 和 Timing 会被切掉（第七阶段，2026-11-12）。
56. 编辑器里的语法着色不再用绿色或青色：键名用 `Text`，字符串用 `MethodPatch` 的梅紫，数字和布尔值用 `Info` 蓝；库默认主题画成绿色的正则、标签、插入文本也换成这三种（正则随字符串、标签和插入文本用 `Info`）。Ink 的 `Socket` 从 `#7cc4c0` 挪到 `#6fc3d6`，12 号字下和 `Success` 的绿分得开（第七阶段，2026-11-12）。
57. 请求头的 Key／Value 两列都给最小宽度（108／120）：常见 header 名和 `application/json` 这样的值读得完整，超过 20 个字符的值在悬停时给全文。列表在窄于 860 时先去掉 Remote IP 列、窄于 740 时再去掉 Client 列，耗时列不再被列表的边切掉（第七阶段，2026-11-12）。
58. 过滤栏的搜索框放不下整句占位文字时只显示「Search host or path」，不再截半句（第七阶段，2026-11-12）。
59. 外壳只有一块底色：标题栏、侧栏、底栏都是 `Chrome`，之间不画线；内容是一张抬起的 `Canvas` 面板（`RadiusPanel`、1px `Hairline`、`Shadow(0,1,3)`），右边 8，侧栏收起时左边也 8（第八阶段，2026-11-12）。
60. Ink 的 `Chrome` 和 `Canvas` 对调：外壳 `#171716`、面板 `#1f1f1d`，`Well` 仍是 `#101010`。对调前面板比外壳暗，看起来是凹下去的；Paper 不动。对调后对比度断言全过，最低仍是 Paper 的 Info 在 well 上 4.52:1（第八阶段，2026-11-12）。
61. 标题栏：左边是开始／暂停录制的按钮和品牌，中间是监听 pill（`Success` 圆点 + `Listening on` + 等宽的地址，未运行时是停止状态、没有绿点），右边是 `⌘K`、侧栏开关、布局切换、Tools、Settings、`⋯`。Host 方案和已启用的工具 chip 从标题栏移到底栏；pill 不再画请求速率曲线（第八阶段，2026-11-12）。
62. 底栏 28 高（`HeightFooter`），取代 24 的状态栏：左边 Clear／Filter／Auto-follow（Auto-follow 跟随最新时是选中态，有新请求时按钮上写「N new」），中间计数（有选中时是「1 of 6 selected」），右边是已启用的工具和 Host 方案的 chip。chip 按量出来的宽度排，放不下的折成 `+N`，点开是同一批 chip 的列表，不横向滚动、不用渐隐。toast 抬高一个底栏的高度，不盖住它（第八阶段，2026-11-12）。
63. MyGo 只有一个 `Surface`：它画表格头、拖影和 source list。第八阶段起 `Surface` 跟面板走（canvas），外壳的 chrome 由应用自己画；侧栏里临时把 `Surface` 换成 chrome，免得 source list 在框上挖出一块 canvas（第八阶段，2026-11-12）。
64. 输入框聚焦时那 1px 边从 `Accent` 换成 `TextMuted`：强调色变墨以后，黑边和旁边的 Send 主按钮抢视线。3px 的 `AccentTint` 光晕保留，聚焦和未聚焦仍然一眼分得开；`TextMuted` 在 well／canvas／chrome 上分别是 5.51／6.58／5.93（Paper），都过 3:1。按钮和列表行的键盘焦点框不动（第八阶段，2026-11-12）。
65. 编辑器里标点（`TokenOperator`）从库默认的红改成 `TextMuted`：黑白界面里红标点像报错，红色只留给真正的错误（第八阶段，2026-11-12）。
66. 动效的时长进 uikit，成了四个 token（`DurationFast` 80／`DurationHover` 120／`DurationStandard` 160／`DurationSlow` 200）加一个无回弹弹簧（`MotionSpring`，200ms）：界面代码只写 token。MyGo v0.8.1 有 `AnimateColor`、`AnimateTo`／`Spring`、`AnimatePose`、`Transition` 和 `ContentTransition`，第五阶段"没有逐属性过渡"的结论作废（第九阶段，2026-11-12）。
67. 悬停和按下的面用 120ms 的颜色过渡，但只在**离开**时：进入即时，指针扫过一排按钮时面不会落后于指针。过渡每一帧都挂着，悬停帧的时长是 `-1`（没有时间）：MyGo 只在上一帧也有过渡时才混色，按"只在没悬停的帧加过渡"写，离开的那一帧就成了新元素，颜色直接跳回。`ToolbarButton`、`FooterButton`、tab、分段控件和底栏按钮都走这条（第九阶段，2026-11-12；离开的淡出在第十阶段修正）。
68. 弹层（`PopoverPanel`：菜单、toast、面板里的浮层）淡入加上移 4px，进 120、出 80；对话框和命令面板（`Modal`）淡入，160。放大不做：3% 的缩放把面板画出来的像素挪离布局位置，开场 160ms 内的点击会落到控件旁边（两个既有测试立刻失败）。对话框的淡入标了 `NonMotion`，所以系统要求减少动态效果时它仍然淡入，而不是直接出现（第九阶段，2026-11-12）。
69. 侧栏分区收起展开用 `RegionMotion`（`Collapse` + 弹簧），面板和分栏改变尺寸用 `PaneMotion`（`Size` + 同一个弹簧）：尺寸变化以 200ms 的无回弹弹簧落地，不跳（第九阶段，2026-11-12）。
70. 底栏的计数和活动行的摘要用 `ContentTransition{Numeric}`：数字变化时逐位变形，160（第九阶段，2026-11-12）。
71. 录制中、且这一秒有流量时，监听 pill 的圆点以 1.2s 的周期在 55%–100% 不透明之间轻微脉动；没有流量或暂停时静止，不常驻抖动。脉动画在圆点自己的 `Draw` 里（`AnimationFrameRate(30)`，从 `p.Now()` 算），只重绘、不重跑视图：按原来的写法在构建视图时读 `Loop`，抓包时几乎每秒都会把整个视图（含流量表）重建 60 次（第九阶段，2026-11-12；第十阶段改为绘制）。
72. 不做动效的四处是刻意的：流量表新行进入、行选中和键盘上下移动、输入、自动跟随滚动。流量在持续进来时，这些动效会让列表掉帧（第九阶段，2026-11-12）。
73. 编辑器页的历史列按实测宽度让位：`requestTabsFit` 用当前的 45:55 分栏和面板头的实际宽度（标题、tab、`⋯`）算出四个 tab 能否整行放下，放不下就收起历史列，宽回来再恢复；不写死窗口宽度，1280 下四个 tab 都在行上（第九阶段，2026-11-12）。

74. 菜单的面、工具提示和分段控件统一到 uikit：`MenuSelect` 用 v0.9 的 `Panel` 给菜单换面和进出动效；`uikit.Tip` 用 `TooltipBase` 画和弹层一致的提示框（并保留 MyGo 的延迟、关闭和对读屏的描述），应用里每一处 tooltip 都走它；`Segmented` 的选中面按 tab 的规则淡入淡出。滑块式移动要自己定位一个 thumb，`SegmentedBase` 不提供（第十一阶段，2026-11-13）。`TooltipBase` 0.8.1 就有，之前在第 13 节写「工具提示接不上」是错的。
75. 图标改成 Icon Composer 文档（`assets/AppIcon.icon`，`macos.icon` 接进去，`actool` 编成 `Assets.car`）：macOS 26 给普通 `.icns` 加浮雕和灰底板，分层文档才按层合成玻璃。`resources/icon.png` 仍是 Windows、Linux 和没有 Xcode 的构建用的图。小尺寸单独画了 `assets/icon-small.svg`，但 `.icon` 格式没有按尺寸的槽位，macOS 26 在 16／32 仍渲染同一套层（第十一阶段，2026-11-13）。

## 13. 仍与设计稿不一致的地方

v3 六个阶段落地后，和设计稿仍有这些差别，都是有意保留的：

1. **侧栏的图标槽是 16 而不是 14。** MyGo 的 source list 条目按字号（16）画图标，槽跟着宽 2px；行文字仍在同一条线上。
2. **侧栏分区标题有 8px 上边距**（第一个也有），所以侧栏顶部留白 12；设计稿第一个标题没有上边距。
3. **Collections 标题行多了 `⋯`**（设计稿只有加号）：导入、环境、历史、Runner、APIs、Vault、Cookies 都在里面。
4. **Sources 的 Show all 展开后变成 Show fewer**；设计稿只画了未展开的样子。
5. **Sessions 行带会话数**，`Open session…` 带时钟图标（设计稿没有这两行）。
6. **树里的请求行没有空图标槽**：method 直接从缩进后开始，所以同一层里文件夹名和请求名不在同一条线上；设计稿的 mock 也是这样（`.slot` 14 + `.mth` 30）。
7. **上区最高 55%**：设计稿没有这个数，是"两个固定区域、各自滚动"的实现细节。
8. **标题栏比设计稿多几个按钮**（命令按钮、侧栏开关、布局切换的两个、Tools、`⋯`）：这些入口都要能到达；设计稿只画了布局切换和 Settings。
9. **标题栏的工具 chip 会横向滚动**：设计稿只画了一个 Host chip，实际同时可能有六七个工具开着。
10. **活动行摘要那一段固定 240 宽**：为了让拖选时柱不在指针下移动，短摘要和柱之间会留一段空白；设计稿是紧挨着的。
11. **活动柱的堆叠比设计稿细**：设计稿只有"正常／高柱"两档，这里保留 3xx／4xx／5xx／慢请求四段，慢请求和 5xx 同为朱红。
12. **流量表的列比设计稿多**（#、SSL、Remote IP、Client、耗时、大小、时间、备注、自定义列）：列宽和顺序沿用现有实现，v3 只改字体、行高和选中色；表头的字重仍是 MyGo 表格的 600（设计只有 400／500），没有 API 改。
13. **状态栏右侧的 Auto-follow 是按钮**（设计稿只是一行文字）。
14. **macOS 上的品牌在侧栏顶部**，标题栏里没有品牌；设计稿的 mock 是整窗布局。
15. **侧栏图标用 faint**：未选中行的图标比文字再低一档，浅色 chrome 上是 2.39:1，低于非文本 3:1；它旁边的文字标签承担信息，图标是装饰。
16. **标签页的选中块不是滑动**：四个 tab 各自带背景，选中块从一个位置"滑"到另一个位置需要一层共享元素或绝对定位的滑块，MyGo 的 `TabsBase` 不提供。点的那个 tab 正在指针下，选中底瞬间出现；被取消选中的那个不在指针下，按 120ms 淡出，所以看起来是"新的出现、旧的退去"。
17. **绿色还剩两处橄榄绿**：POST 的方法色（`#5b6b1c`／`#b5c27a`）和编辑器里字符串的着色（`EditorTheme` 用 `MethodPost`）都偏绿。方法色按用户决定保留原值；它们不是 `Success`，但小字号下和 `Success` 的绿（`#26734a`／`#7cc492`）放得比较近。
18. **选中浅底和设计稿不同**：`AccentTint` 用了 `#d8d4cb`／`#45443f`，比设计稿给的 `#e0ded8`／`#3b3a37` 深一档，否则和悬停面分不开（决策 53）。
19. **面板头里少一个按钮是常态**：编辑器的响应面板在 1280 宽（历史列打开时约 530 宽）只放得下状态、标签页、耗时和 `⋯`，格式菜单、Decode 和保存／示例／历史都在 `⋯` 里；960 宽（没有历史列）时格式菜单和动作按钮回到行上，只有 Decode 留在 `⋯` 里。
20. **编辑器并排时请求面板的 tab 会挤掉历史列**：窗口 1280、历史列打开时请求面板约 270 宽，四个 tab 放不下。第九阶段起历史列像侧栏一样让位：按实测宽度算出请求面板放不下整行的 tab 时，历史列自动收起（Send 历史仍可从响应面板的 `⋯` 打开），窗口变宽后回到用户原来的选择。放不下的 tab 仍从右往左进 More，行上永远是完整的词。
21. **底栏没有上下行速率**：应用现在只有请求计数（`rateWindow`），没有字节计数，所以设计稿里的 `↑ 27 KB/s ↓ 219 KB/s` 没有画。要显示它需要 `capture.Server` 在读写请求／响应体时累计字节（例如 `up`／`down` 两个 atomic，或按秒的环形桶），由底栏每秒取差值；现在的 `flow.bytes` 是完成后的响应体大小，只能算出「每次请求完成时的突发」，不是实时速率。
22. **侧栏显隐没有滑入滑出**：侧栏在 MyGo 的 `Split` 里，宽度由用户拖动的分隔条拥有，`Split` 没有开合动画的入口；收起时它整块出现／消失，旁边的内容面板按 `PaneMotion` 弹簧改变尺寸。要做侧栏自身的滑动，得把 `Split` 换成自绘分隔条的 `Row`，属于结构改动。同理，检查器在左右并排与上下堆叠之间切换时，两侧面板的内容是重新挂载的，只有外层面板的尺寸在动。
23. **库画的部分仍是瞬时**：菜单的面和工具提示现在由 uikit 画（`MenuSelect` 的 `Panel`、`TooltipBase`），进出和弹层一样；剩下的只有菜单项自己的高亮（库画的强调色实底）和 `ui.Segmented` 内置的滑块——应用里没有再用后者，`uikit.Segmented` 是 `SegmentedBase` 自绘的。
24. **侧栏内部还剩一条线**：Traffic／Sources 与 Collections 之间有一条 `Divider`，设计稿的示意图里没有；它分的是侧栏里两个各自滚动的区域，属于侧栏自己的结构，这一阶段没动。Collections 与页脚之间那条已经去掉——那里现在是侧栏和底栏的交界，外壳是一整块。
