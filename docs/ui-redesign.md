# vscroxy UI 设计规范与改版计划

状态：**方向 v3，2026-11-11 定稿；v3 的四个阶段（uikit 基础层、检查器的标签页和面板头、侧栏、标题栏与列表）、第五阶段（细节打磨）、第六阶段（强调色从绿色改成墨黑，Success 拆成独立信号色）、第七阶段（面板头的裁切、编辑器语法色、Dark 的 Socket）和第八到十二阶段（外壳一张卡片、底栏、动效 token、菜单面和工具提示、应用图标）全部完成；第十三阶段按 UI/UX 审计（宏观与微观）删解释性文字、合并入口、修选中和键盘。仍与设计稿不一致的地方列在第 13 节。** 本文是界面的视觉规范和改版进度；交互和文案见 [UX 规范](ux-guidelines.md)，功能范围见 [功能路线图](feature-roadmap.md)，代码基础设施见 [基础设施方向](architecture.md)。

v1 改版（靛蓝强调色、Capture／Requests 两个工作区，阶段 0–8）和 v2（暖纸色 Light／Dark、油墨绿强调色）都已经完成并被 v3 取代：v3 保留 v2 的信息架构和密度，把暖纸色换成中性灰白，并把界面收成三种表面、一套控件规则。第六阶段再把强调色从绿色换成墨黑（Dark 下是白），中性色去掉绿调、带一点暖，绿色只剩 Success。它们的规范和各阶段实施记录见 git 历史里的本文件。

## 1. 方向

v1 统一了结构和 token，但界面仍然是一张普通表格加一堆表单：同一种控件有好几种样子，异常要靠眼睛一行行扫，Capture 和 Requests 之间要来回搬运请求。v2 从 UX 规范第 1 节的前两项核心任务出发：**让时间和异常一眼可见，让抓到的请求和保存的请求是同一种东西。**

目标：

1. 看起来像一个精心打磨的原生桌面工具（Proxyman、Linear 这个级别），而不是拼起来的表单。
2. 所有界面只用一套设计系统 `internal/uikit`：同样的字号、间距、控件、颜色语义。
3. 信息密度高：chrome 退后，屏幕留给流量、请求和响应体。
4. 改版不丢功能：现有的功能、快捷键和无障碍标签都保留；确实要改名的标签，在同一个提交里同步改测试。

不在范围内：改数据模型或存储格式、改 `internal/request`、`internal/vault` 等非 UI 包、换掉 MyGo。

## 2. 设计稿

设计稿就是这个仓库：`cmd/vscroxy-gallery` 打开设计画廊，左边是目录、右边是画布，分 Foundations（token）、Components（uikit 的每个公开组件及其状态）和 Screens（抓包页、请求编辑器、设置、命令面板等）三组；画布顶部切换 Light／Dark 和文字 1.0×／1.35×。

```bash
go run ./cmd/vscroxy-gallery
```

每个条目都用真代码画：token 读自 `internal/uikit`，组件是 uikit 的公开组件，屏幕是 `internal/app` 的真实视图加假数据（`internal/app/gallery.go` 构造，不启动抓包服务器）。悬停和按下随指针出现，焦点用 Tab；导出的图版会把指针停在悬停样张上、把焦点移到焦点样张上。`VSCROXY_GALLERY_ENTRY=<条目 id>` 直接打开一个条目，`VSCROXY_GALLERY_DARK=1` 以深色打开，用来截图。

图版在 `docs/design/`，一个条目一张，命名 `<组>-<条目>-<light|dark>.png`，例如 `screens-capture-traffic-light.png`。重新生成：

```bash
VSCROXY_GALLERY_OUT="$PWD/.amp/in/artifacts/gallery" go test ./internal/gallery -run '^TestGallerySnapshots$' -count=1
```

测试按 Light 和 Dark 各写一张 PNG；不设置 `VSCROXY_GALLERY_OUT` 时它只检查每个条目都能渲染、不 panic。把导出的图缩到 1600 宽、112 色再放进 `docs/design/`，总量控制在 3 MB 以内：

```bash
for f in .amp/in/artifacts/gallery/*.png; do
  magick "$f" -resize '1600x>' -dither None -colors 112 -depth 8 -strip -define png:compression-level=9 "docs/design/$(basename "$f")"
done
```

`-dither None` 是为平涂界面加的：默认的 Riemersma 抖动会在文字和面板边缘撒一层噪点，既放大文件，也在截图里多出并不存在的颜色。112 色足够容纳两套主题的全部 token；128 色时 142 张图约 3.04 MB，超出总量，112 色约 2.98 MB，肉眼无差。

画廊不启动抓包服务器，所以抓包页标题栏的 pill 读作 Proxy stopped，表格里是假数据。屏幕条目在画廊里占满整个窗口（目录收进左上角的 Catalog 抽屉），因为 `internal/app` 的视图按窗口尺寸排版，不能放进比窗口窄的一栏。

Screens 组现在覆盖：抓包（空、流量、错误、隧道、刷选）、请求编辑器（HTTP、MQTT、gRPC、WebSocket、Socket.IO）、Settings 的七个分类和证书编辑状态、命令面板、收起侧栏、Tools 的五个分类（规则、场景、脚本、Host 方案、Mock）、Client setup、Diagnostics（Listener 失败和全部通过）、HTTPS decryption、API 的端点页、Runs（记录、运行中、结果）、Sessions、集合向导、Postman 导入报告、环境变量、Protobuf 检查器、SSE 会话、调查板、首次启动引导和 MCP 目录。画廊里的入口都构造自假数据，不启动代理、不读写设置、不发请求；需要文件的检查器（Protobuf schema）写进临时目录，关闭时删除。

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
| 主工作区 · Light／Dark 配色 | https://ampcode.com/user-content/attachments/4264e67bf143883b93aeb4f42f8c4a95410859a42a81bceda68f6293e9fd335d-file.png | https://ampcode.com/user-content/attachments/60501cbdcdd682610c79458646ac794c4b17389f1a5881d836585b8888110756-file.png |

uikit 组件总览设计稿（实现后以第 11 节的总览截图为准）：

| 总览 | Dark（深色） | Light（浅色） |
| --- | --- | --- |
| 控件 | https://ampcode.com/user-content/attachments/900406bfa1a1edc21aed8a1797a9b2b4ee67b47a31c6bf6571efade3b54b909e-file.png | https://ampcode.com/user-content/attachments/8d629c20f8e012b5227369f81b842995009326d280089a1a57d8bb80a4083fd6-file.png |
| 结构和反馈 | https://ampcode.com/user-content/attachments/7994da9f07c86a3f3c3866e1d99c73af2a197a848381afbc90d05d89c3703c9e-file.png | https://ampcode.com/user-content/attachments/f007c7d55451f9a1c97f9f7d7bc0401172770a7b6cda3c841cf004ced536cc15-file.png |
| 标志性组件 | https://ampcode.com/user-content/attachments/ce3837e7e7e6d41c58c45c2e7972bf29ba5cbc55810b146a5aa8386a81365e3c-file.png | https://ampcode.com/user-content/attachments/8fb1ae2fd8231030e32f8f65b3d6b982434c64255534472029fa88767d1dc1ee-file.png |

3.5「macOS 质感」（密度、tab、侧栏材质、控件形状以这组为准，数值以第 6.3 节为准；Painter 画不准像素，图里的行高仍比 24 高）：

| 设计稿 | 链接 |
| --- | --- |
| 主工作区 · Light | https://ampcode.com/user-content/attachments/1f7855404ee0e721a439d66473c9d87476cc8fab892dedeb6fd4e77de80d422e-file.png |
| 主工作区 · Dark | https://ampcode.com/user-content/attachments/e21b22a0870927c6301a6794325b063d5c51fe16bba20dfa96de911a0c92272f-file.png |
| tab 三种方案（选 B） | https://ampcode.com/user-content/attachments/aaf96d6a6a15e2fbd9c5ef07aec1ff425162053eafd461bf79c41a4db454f2ea-file.png |
| 组件总览 · Light | https://ampcode.com/user-content/attachments/b61d8c6facc74d8de884ad0d85334bee3f1bdeb9fab6ce280d292b1e19a0c47a-file.png |
| 组件总览 · Dark | https://ampcode.com/user-content/attachments/6d731fdff658c99fbc162178fce775f625d94dfa1a381c4f7c61127218a27511-file.png |

3.5 的两张组件总览早于 tab 方案 B 和 24 档高度：里面的 tab 和 28 高的控件以第 6 节为准；Dark 组件总览里对话框的 Delete 应该是危险色；Dark 主工作区响应区的 Body 多了下划线。

设计稿里和规范不一致、实现时以规范为准的地方：

- Dark 主工作区里的耗时分段全是灰色；五段要可区分。
- 浅色"已保存请求"的录制 pill 被画成了 `Staging`，响应区多了一个重复的 Save to collection；浅色引导页的说明文字误用了等宽字体；表格里有 `/v1/prders` 之类的拼写错误。
- Light「结构和反馈」的面板头把标题和 tab 分成了两行；规范是一行。
- 空状态图标偏大；规范是 16px、不加框。
- 「标志性组件」里的校验结果画成了带底色的卡片；规范是行内的一行结果加一行原因。
- 按钮 hover／pressed 以第 5 节的颜色为准，焦点环一律画在控件外侧。

## 3. 信息架构：一个工作区

- **去掉 Capture／Requests 切换。** 抓到的请求就是"还没保存的请求"：点开是可编辑的文档，顶部有来源标记（`Captured · 12:03:42 · Safari`），可以直接 Send，或者 Save to collection。Edit & replay、Save as request 这类搬运动作并入这个文档，不再是跨工作区的跳转。
- **侧栏三段，只有中间滚动：** 顶部是固定的 Traffic——All requests、Errors、Slow、Pinned、Overview 各带计数（0 不显示）、保存的视图和快速筛选，标题右端是新建视图的加号；窗口太矮时这一组在自己的列表里滚动，不挤走下面。中间是可折叠的 Sources（标题右侧 Apps｜Domains 开关，只列前五项，超过时末尾一行 Show all）、Sessions 和 Collections，它们按 PaneView 的方式分配高度：每段保留自己的标题，上面的段保持自己的高度，最后一个展开的段（默认是 Collections）占满剩余空间，分隔条可拖动、双击恢复默认；折叠状态和每段高度记在 settings 里。底部固定的 Client setup、HTTPS decryption、Diagnostics 不滚动。侧栏本身可以隐藏（标题栏的按钮、`⌘B`、命令面板），窗口窄于 860 时自动让开、变宽后回到原来的选择。环境、APIs、Runner 从集合的 `⋯` 菜单和命令面板进入，不再占一条资源栏。
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

v3 把界面收成三种表面和一套控件规则（设计稿见第 2 节）。表面负责结构；强调色是墨（Dark 下是白），负责"选中、主操作、已开启"；绿色只表示"通过"和"正在录制"。

- **三种表面：** **chrome** 框住窗口（侧栏、各种栏、表头），**canvas** 放数据（表格、编辑器、面板），**well** 是 canvas 上的输入框（比 canvas 深一档）。chrome 上的输入框反过来，是 canvas 底加 1px 细线。Hairline 只分隔同一表面的两个区域，不靠色块分区。
- **外壳一整块，内容抬起来（第八阶段）：** 标题栏、侧栏、底栏是同一块 chrome，三者之间不画线。流量列表、过滤栏、活动行、检查器（以及请求编辑器页的全部内容）合成一张 canvas 面板：圆角 `RadiusPanel`（8）、1px hairline 加一层很浅的阴影，右边留 8 的 chrome，上下紧贴标题栏和底栏；侧栏收起时左边也留 8。面板内部的区域线（列表｜检查器、请求｜响应）保留，过滤栏在面板里、下沿一条 hairline。
- **强调色是墨，绿色只表示通过和录制。** 主按钮、焦点、选中标记、开关和复选框的开启态、活动行选区的把手、表头拖动线用 `Primary`（Light `#1a1a18`，Dark `#e9e8e4`）；检查通过、录制点、信任状态、向导里已完成的步骤用 `Success`（Light `#26734a`，Dark `#7cc492`）。标签页、过滤器和命令面板的高亮一律中性底，一屏不再有好几块彩色。
- **控件不描边。** 按钮、tab、chip 的面是文字色的 6%（静止）／10%（悬停）／14%（按下），叠在什么表面上都对，所以同一个按钮放在窗口、chrome 或选中行里都一样。只有输入框在 chrome 上有 1px 细线，浮层和对话框有细线边框加阴影。
- **状态色和方法色不填充背景。** 2xx 和 304 是普通的次要文字，前面没有圆点；4xx（赭黄）和 5xx（朱红）只染状态文字本身，3xx 也按次要文字读。慢请求（≥ 1 秒）的耗时用朱红文字，没有底色块。method 是纯色文字，不做 chip（表格里是界面字体，侧栏和树里是等宽）。
- **选中保留形状。** 列表失焦时，选中底从强调浅底换成中性面（6%），表格行同时把标记线换成 faint 色，位置和形状都不变。
- **等宽只用于线上数据：** 路径、header 值、URL、数字、body。host、client、标签用界面字体。会变的数字开 `tnum`。
- **六档字号，两个字重：** 20（展示）／16（标题）／15（Amp 的 md 标尺档）／13（正文和控件）／12（次要文字和等宽数据）／11（元信息），只有 400 和 500，没有 600。界面常用前五档里的 20／16／13／12／11。
- **三档栏高，两档控件：** 栏 40／32／24，控件 `HeightControl` 28／`HeightControlSmall` 22，行 `HeightRow` 24；工作区圆角 4／6／8，浮层再取 10／12／16；间距 4／8／12／16／24，左右边距统一 12。
- **线只画在区域之间。** 侧栏与内容、列表与检查器、请求面板与响应面板、状态栏上沿；区域内部靠间距和表面区分，面板头、抓包信息行、URL 行都没有底线。流量表表头没有列分隔线，拖动列宽的命中区还在，指针停在那一列边缘时才画出强调色细线。
- **输入框只有两种：** 在 chrome 上的是 canvas 底加 1px 细线（`ChromeFieldFace`），在 canvas 上的是 well 填充（`FieldFace`）。标题栏的命令入口不是输入框，是一个 24 高的按钮（放大镜加 `⌘K`／`Ctrl K`）。
- **侧栏退后。** 未选中行的文字用次要色、图标再低一档（faint），悬停和选中时回到正文色；计数为 0 不显示。侧栏的四个分区标题（Traffic、Sources、Sessions、Collections）都可以点击折叠，折叠状态和中间三段的高度记在 settings 里。
- **常驻的状态信息：** 列表上方一条 24 高的活动柱状图：摘要（`Last 5 min · 138 requests`）在左、柱在右、最右是结束时间（等宽）；最近 5 分钟，按状态码上色（正常中性面，3xx／4xx／5xx 和慢请求用信号色），可拖选时间范围。筛选是查询语法 chip（`status:>=400`、`host:`、`app:`、`slow`），带自动补全；每行带一根耗时条，慢请求是朱红。
- **选中分两种形状，都是选中浅底（`Accent`）、正常文字颜色：**
  - 选位置或选东西的行（侧栏、集合树、各种挑选列表）是离边缘内缩的圆角块，不画标记线，和 macOS 的 source list 一样。侧栏的选中表示"现在在哪"，不是键盘光标，焦点在别处时仍是选中浅底。
  - 表格和数据列表（流量表、消息、Protobuf、Runner 结果）的行占满宽度，左侧加 2px 强调色标记线。列表失焦时浅底变成 `Muted`、标记线变成 faint 色，靠标记线和中性底区分开。选中浅底 `Accent` 是正文色 7% 的 wash（Amp 的 `--accent`），不再靠比悬停更深来区分选中。
  - 浮动的选择列表（菜单、`Select`、`Combobox` 的弹层）的高亮是同一张 7% 墨色 wash，文字保持墨色。命令面板和数据列表的选中也是 `Accent`，不混用。
- **形状靠近 macOS：** 圆角 4／6／10（第 6.3 节），嵌套的圆角同心（内层 = 外层 − 内边距），浮层是细边框加柔和的大阴影。不做胶囊按钮：控件高 24 时胶囊显得像玩具。
- **macOS 上的原生感（3.5，保留）：** 参考了 onegit／onevra、kero、mygo-cloudmusic 三个 MyGo 应用、Telegram 和 Apple HIG，共同点是窗口外壳原生、控件用中性色叠加、少画线。
  - 只在 macOS 上：界面字体用系统字体（SF Pro）。侧栏仍挂着 `VibrancySidebar`，但外壳的每一块（标题栏、底栏、侧栏、内容）都画自己的实色，桌面不透出来：标题栏曾经因为没有自己的底色而让壁纸透上来，标题栏和其余外壳不是一个色（2026-11-13 修）。Windows、Linux 用内嵌的 Geist 和平涂。
  - 标题栏不画色带和底边框，红绿灯对齐标题栏中线（40 高，Y 13）。
  - 强调色不跟随系统，是墨黑（Dark 下是白）。
- **每个面板只有一排标签页：** 面板头 32 高，标题在左、标签页紧跟、元信息在右。请求面板是 Headers／Params／Body／Auth，响应面板是 Body／Headers／Cookies／Timing；Scripts、Checks、Examples、Docs、Settings、Raw、Original、Bulk edit 收进请求面板的 More，响应的 Raw／Checks／Console 收进它右侧的 `⋯`。选中的 tab 是中性面，不再有强调色文字。
- **面板头按量出来的宽度取舍，不按猜的阈值：** 面板记下自己上一帧的宽度，标题和标签页的量好的宽度从中扣掉，剩下的才是元信息和控件的位置。放不下的按顺序进 `⋯`——先是耗时和大小，然后是面板自己的控件（编辑器里是格式菜单、动作按钮，Decode 最先让），标签页和状态永远不动。所以窄面板里不会出现半个词，只会少一个按钮。
- **同一种东西只有一种样子：** tab、按钮、面板头、表格各一种写法，都来自 uikit。
- **密而不挤：** 表格和编辑器保持桌面工具的密度，最小字号 11，正文 13，行高 24。

## 5. 主题：Light／Dark

两套主题取 Amp 默认主题（Daydream）的原色：浅色 **Light**（近白的绿调灰），深色 **Dark**（近黑）。强调色是墨，绿色只留给 Success。只用平涂色，不做渐变、纹理和做旧。外观跟随系统，也可以手动选浅色或深色。主题名保留 Light／Dark。

下表是 `internal/uikit/theme.go` 里的实际值：

| Token | Light | Dark | 说明 |
| --- | --- | --- | --- |
| `Window` | `#eff3ee` | `#070a08` | 外壳：标题栏、侧栏、底栏，以及内容卡片四周 2px 的缝；标题栏和侧栏是一块底，左上角没有接缝 |
| `Background` | `#f7fbf6` | `#101612` | 内容卡片，也是 MyGo 的 Background 和 Surface |
| `Input` | `#e8ede5` | `#171f1a` | canvas 上的输入框；Light 也带 Daydream 的绿调，不再是纯灰 |
| `Border` | `#dde4d9` | `#1b2324` | 面板的 1px 边、同一表面的分隔线、表头边框、对话框边缘 |
| `Muted`／`MutedHover`／`MutedPressed` | 文字色 7%／11%／15% | 文字色 7%／11%／15% | 控件、tab、chip 的面；失焦选中底用 `Muted` |
| `Foreground` | `#0b0d0b` | `#f6fff5` | 正文 |
| `MutedForeground` | `#595959` | `#9ca49c` | 次要文字、2xx 的颜色，也是输入框聚焦时那 1px 边和编辑器里的标点 |
| `Faint` | `#8a8f88` | `#6d766e` | 只用于图标、禁用、占位和失焦标记线，不承载信息 |
| `Primary` | `#171917` | `#eaf3e9` | 主按钮、焦点、选中标记、开关、复选框的开启态、活动行把手、表头拖动线 |
| `PrimaryHover`／`PrimaryPressed` | `#2f312f`／`#2f312f` | `#d3dbd2`／`#d3dbd2` | Web Amp 实测：hover 和 pressed 共用一张面，Light 提亮、Dark 压暗；禁用只叠 50% 透明度 |
| `PrimaryForeground` | `#fafaf8` | `#0b0d0b` | 主按钮文字，也是 Success 面（向导的完成标记）上的文字 |
| `Accent` | 正文色 7% | 正文色 7% | 选中浅底、活动工具、活动行区间和菜单项高亮；失焦选中用 `Muted` |
| `Ring` | `#636e61` | `#9aa89a` | 键盘焦点环和输入框聚焦边；和墨色强调分开 |
| `SwitchOn` | `#171917` | `#8a998a` | 开关打开时的轨道；Dark 不用近白的墨色，白色滑块仍有 3:1 边缘 |
| `Success` | `#2c784d` | `#54b879` | 检查通过、录制点、信任状态、向导里已完成的步骤 |
| `Info`／3xx／GET | `#2f6ea5` | `#8fb3d1` | |
| `Warning` | `#86600f` | `#d6a038` | 慢请求、4xx 的提示色 |
| `Danger` | `#b9423c` | `#d67670` | 5xx、连接失败、错误 |
| `MethodPost` | `#5b6b1c` | `#b5c27a` | POST 的方法色（橄榄绿） |
| `MethodPatch` | `#7a3e6d` | `#c99abd` | PATCH 的方法色（梅紫） |
| `Socket` | `#2b6868` | `#6fc3d6` | WebSocket 的协议色 |
| `EditorForeground`／`EditorKeyword`／`EditorEntity`／`EditorTag` | `#24292e`／`#d73a49`／`#6f42c1`／`#22863a` | `#e1e4e8`／`#f97583`／`#b392f0`／`#85e89d` | `@pierre/diffs` 的 `--diffs-token-light/dark`（GitHub Light/Dark）；文件 diff 编辑器里的 token 色 |
| `EditorVariable`／`EditorConstant`／`EditorString`／`EditorRegexp`／`EditorComment`／`EditorInvalid` | `#e36209`／`#005cc5`／`#032f62`／`#032f62`／`#6a737d`／`#b31d28` | `#ffab70`／`#79b8ff`／`#9ecbff`／`#dbedff`／`#6a737d`／`#fdaeb7` | 同一套 token 色的其余角色；都不是状态色 |
| `DiffAdded`／`DiffRemoved`／`DiffMeta` | `#2f8052`／`#c44f4a`／`#a07013` | `#4ba86e`／`#d67670`／`#d6a038` | Amp `--diff-*` 的前景/meta；MyGo 由它们合成 diff 行底与字色 |
| `Inverse`／`InverseText` | `#0b0d0b`／`#f7fbf6` | `#f6fff5`／`#101612` | 提示条和 toast 的反色面 |
| `Scrim` | 黑 35% | 黑 35% | 对话框遮罩 |
| `ShadowPopover`／`ShadowDialog` | 1 px 8% 黑环 + `0 4 6 -1` + `0 2 4 -2`／`0 10 15 -3` + `0 4 6 -4` | Dark 白环；菜单再加深 | 详见 6.3 阴影；工作区不加阴影 |

2026-11-17：表面色按 Amp 的明度层级收了一档，仍保留少量绿味。`Window` → `Background` 的对比度 Light 为 1.072、Dark 为 1.085，接近 Daydream 的 1.073／1.084；布局、密度、圆角和 `Border` 没有动。命名整体对齐 Amp：`Text` → `Foreground`、`Chrome` / `Canvas` / `Well` / `Hairline` → `Window` / `Background` / `Input` / `Border`、`Fill*` → `Muted*`、旧 `Accent*` → `Primary*`、旧 `AccentTint` / `Selection` → `Accent`。选中面从实色改为正文色 9% 的 wash；录制 pill 的绿底从 14% 收到 10%，叠在新外壳上接近 Daydream 的 `success/15` 在原外壳上的效果。历史事件条目里的 token 名按现名书写，值仍是当时的值。

`Bar` 这个 token 已删掉：耗时条的底就是 `Muted`，和设计稿的 `.dur` 一样。

**Success 与 Accent 是两回事。** 第六阶段之前 `Success` 直接等于 `Primary`，强调色一变黑，"通过"就没有颜色了。现在 `Success` 是独立的一支绿，只出现在"这件事通过了"和"正在录制"：检查通过、录制 pill 的点、`https` 的信任状态、向导和引导里已完成的步骤、toast 的成功图标。其余界面操作走 `Primary`（墨）或中性面；耗时分段和消息方向用 `Socket` 的青色。代码语法与 diff 是数据色，各有自己的 `Editor*`/`Diff*`，不借状态色表达结论。

对比度由 `internal/uikit/theme_test.go` 检查（线性 sRGB 相对亮度，合成半透明底后计算）：正文、信号色（Info／Warning／Danger／Accent／Success）、method 色、主按钮文字在 canvas／chrome／well 三个表面上 ≥ 4.5:1，次要文字 ≥ 3:1，TextFaint 不参与；叠在表面上的 6%／10%／14% 面和选中浅底上，正文仍 ≥ 4.5:1，信号色和 method 色按 UI 组件的 3:1 计。第六阶段的实测最低值是 Light 的 Info 在 well 上 4.52:1，信号色在按下态最低 3.39:1（Light 的 Info 在 well 的 14% 面上）；`Success` 在三个表面上分别是 Light 5.78／5.21／4.84、Dark 8.69／7.99／9.21，`PrimaryForeground` 在 `Success` 面上 5.78（Light）／9.1（Dark）。Light 的 warning 从设计稿的 `#a15c07` 深到 `#9c5906`，其余信号色与设计稿一致。第七阶段把 Dark 的 `Socket` 改成 `#6fc3d6`，它作为 method 色在三种表面上仍是 9.0:1 以上。

## 6. 设计系统：`internal/uikit`

本节是 uikit 的完整规范：每个 token 的值、每个组件的用途、变体、尺寸、状态和不要做的事。值都从代码里读出（`internal/uikit/tokens.go`、`theme.go`、`components.go` 等），代码和本节不一致时以代码为准，并改本节。

### 6.1 规则

- **独立的包，** 只依赖 MyGo（`ui`、`ui/editor`、`ui/chart`），不碰 app 状态和业务包。
- **主题 = 语义 token 表。** `Palette` 是一张表，Light、Dark 是内置的两张，`Theme(dark)` 由它生成 `ui.Theme`，`Colors(t)` 取回当前表。界面代码只引用语义 token，不能假设强调色是哪种颜色。字号、高度、圆角、间距由 uikit 导出，主题不能改（唯一的例外是 `Spacing` 网格，表格局部主题可以收紧）。
- **界面只用 uikit。** `internal/app` 不直接调用 `ui.Button`、`ui.PrimaryButton`、`ui.ButtonBase`、`ui.TextInput`、`ui.Select`，不写 `ui.Hex`、数字字号和手写悬停／按下。uikit 的组件建在 MyGo 的 Base 上，键盘、焦点和无障碍由 MyGo 负责；每个可交互组件都要传无障碍标签。
- **确实要用裸 MyGo 控件的地方，** 在调用处写一行 `// Bare:` 注释，说明 uikit 为什么画不了它；没有注释的裸控件是缺陷。第 6.8 节列出全部裸控件和理由。
- **组件按变体设计，** 调用方选角色和变体，不调样式。布局（`Grow`、`Width`、`SingleLine`、`Tooltip`）照常在返回值上链式设置。
- **每个可交互组件都有这些状态：** 静止、悬停、按下、焦点、禁用；需要时有忙碌（`*Busy`）和无效（`Invalid`、`Danger` 边）。状态由组件自己决定，调用方不画。
- **禁用由组件自己处理：** uikit 按钮在构建时就决定 hover 外观，所以组件在构建时检查 `IsDisabled()`（`LinkButton`、`SwatchButton`、`QueryChip` 和 `ToolbarButton` 都这样做）。忙碌中的按钮用 `PrimaryButtonBusy`／`SecondaryButtonBusy`：它禁用自己，并让转圈代替标签、保留标签的宽度。外层 `ui.Row(c).Disabled(x)` 仍可用于一组控件，但不要在返回值上再调 `Disabled`。
- **控件不描边。** 按钮、tab、chip 的面是文字色叠出来的（见 6.3 的颜色）；只有输入框在 chrome 上有 1 pt 细线，浮层和对话框有细线边框加阴影。
- **颜色只有语义。** 强调色是墨（Light 近黑，Dark 近白），绿色只表示通过和录制（`Success`）。危险、警告、信息各有一个语义色，不用来装饰。
- **字重只有 400 和 500；字号共六档，其中 15 是标尺档**（6.3）。没有 600，也没有别的字号。`WeightMedium`（500）是唯一的字重常量，组件里不写 `FontWeight(500)`。
- 不做防回退的检查脚本；写死值靠评审和组件总览（`internal/gallery`）发现。

### 6.2 字体

- 内嵌 Geist 1.9.0（400／500／600，界面）和同家族的 Geist Mono 1.9.0（400／700，数据），给 Windows、Linux 用，在 `internal/uikit/fonts`，来源和许可见那里的 README 和 `THIRD_PARTY_NOTICES.md`。注册失败只记录一次日志并回退。
- macOS 上用系统字体：界面 `FontUI = "system-ui"`（SF Pro），数据 `FontMono = "monospace"`（SF Mono），中文走系统的级联。在 `font_darwin.go`；其他平台在 `font_other.go`。
- **不引入 Geist Serif。** 它只有常规一个字重，是展示字体，没有界面角色；需要时单独引入，不要混进 uikit 的文字角色。
- **SF Pro／SF Mono 只在 macOS 上以系统字体使用。** Apple 的字体文档不允许打包系统字体；运行时用 `system-ui`／`monospace` 取系统的那一份是允许的。
- 中文不内嵌（一个字重就要 10 MB 以上），用系统字体回退。拉丁回退字体放在 CJK 字体前面，避免英文变宽、等宽失效：
  - 界面（Windows、Linux）：`Geist, Arial, DejaVu Sans, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
  - 等宽（Windows、Linux）：`Geist Mono, Menlo, Consolas, DejaVu Sans Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
- 中文的成段说明用 `Prose`／`Hint`，行高沿用正文的 20/13；单行标签仍用 `Body` 的行高。`Hint` 的 12 号字在 20/12 行高下比单行更松。组件总览的 Chinese 一节固定渲染中英混排，作为每次改字体后的检查样张。

### 6.3 Token

下面每一项都是 `internal/uikit` 导出的常量。Scaled 的值随桌面字号缩放（`SetTextScale`，1 为常用大小，1.5 为大一半）；其余是固定点数。

#### 文字

| Token | 值 | 用途 |
| --- | --- | --- |
| `TextMeta` | 11 | 元信息：计数、时间、大小、标签 |
| `TextData` | 12 | 等宽数据：URL、路径、header 值 |
| `TextSmall` | 12 | 次要文字、弹层说明、提示 |
| `TextBody` | 13 | 正文、标签、控件文字 |
| `TextMedium` | 15 | Amp 的 md 标尺档，不用于界面文字 |
| `TextTitle` | 16 | 对话框、面板、检查器里的请求名 |
| `TextDisplay` | 20 | 首次引导、命令面板的大标题 |

字号六档（11、12、13、15、16、20），其中 15 只为与 Amp 的标尺对齐而存在；字重只有 400 和 500。

| 行高 | 值（行高／字号） | 对应 |
| --- | --- | --- |
| `LineMeta` | 13.6／11 | `TextMeta` |
| `LineSmall` | 16／12 | `TextSmall`、`TextData` |
| `LineBody` | 20／13 | `TextBody` |
| `LineMedium` | 20／15 | `TextMedium` |
| `LineProse` | 20／13（同 `LineBody`） | 成段说明（中文和长句） |
| `LineTitle` | 24／16 | `TextTitle` |
| `LineDisplay` | 28／20 | `TextDisplay` |

#### 栏和控件高度

| Token | 值 | 用途 |
| --- | --- | --- |
| `HeightBar` | 40 | 标题栏 |
| `HeightTabs` | 32 | 面板头和过滤栏 |
| `HeightStatus` | 24 | 活动柱状行、侧栏分区标题 |
| `HeightFooter` | 28 | 底栏（窗口动作、计数、已启用的工具） |
| `HeightControl` | 28 | 按钮、输入框、下拉、搜索框、表单控件 |
| `HeightControlSmall` | 22 | 小按钮、tab、chip、底栏动作、行内动作 |
| `HeightRow` | 24 | 表格行、列表行、侧栏条目、分段控件的轨道 |
| `HeightKbd` | 18 | 快捷键键帽 |
| `HeightRowAction` | 20 | 行内的图标动作和 `⋯`（`RowIconButton`、`MoreMenu`），以及侧栏标题和面板头里的同款动作；离行的上下左右边各 `RowActionInset`（2），即 `(HeightRow − HeightRowAction)／2` |
| `HeightChip` | 18 | 查询 chip（`QueryChip`），居中于 `HeightRow`（24）的字段内，上下各留 3；字段两侧留 `SpaceM`（8），与 `SearchField` 相同 |
| `MenuRowHeight` | 28 | 菜单行（MyGo 当前画 24，见第 12 节） |
| `SettingsControlHeight` | 36 | 设置页开关的宽（轨道 36×20） |
| `SettingsRowHeight` | 44 | 设置卡片行的最小高 |
| `CommandPaletteInputRow` | 48 | 命令面板的输入行 |
| `CommandPaletteRow` | 40 | 命令面板的结果行 |
| `TabRuleHeight` | 2 | 子标签下的强调色下划线 |
| `DotSize` | 6 | 状态点、图例点、单选的圆点 |
| `ProgressBar` 高 | 4 | 进度条（圆角 2） |

栏高只有三档（40、32、24）加底栏 28：标题栏、面板头、活动行。控件高度是 28／26／22：主操作、Amp 次级按钮、密集控件；行是 24。

#### 间距

间距的名字乘主题网格 `Spacing`（默认 `SpaceGrid` 4，`t.Space(name)` 读取）。Amp Web 用 Tailwind 的 `0.25rem`（4）网格和 `px-1.5`／`px-2`／`px-2.5`／`px-3` 这样的数字类名，也就是 6／8／10／12；我们的 `SpaceS`／`SpaceM`／`SpaceCompact`／`SpaceL` 就是同一组步进的名字，不是另一套标尺。界面主要用 4、6、8、10、12、16、24；2 是细缝。

| Token | 网格倍数 | 点数 | 用途 |
| --- | --- | --- | --- |
| `SpaceHair` | 0.5 | 2 | 细缝：tab 之间、Segmented 轨道的内边距 |
| `SpaceXS` | 1 | 4 | 图标和文字之间、行内的小间距 |
| `SpaceS` | 1.5 | 6 | 控件内部、标题和元信息之间 |
| `SpaceM` | 2 | 8 | 同一行的控件之间 |
| `SpaceCompact` | 2.5 | 10 | tooltip 和 toast 的内部间距 |
| `SpaceL` | 3 | 12 | 面板内边距、组内间距 |
| `SpaceXL` | 4 | 16 | 分组之间 |
| `SpaceXXL` | 5 | 20 | 大组的间距 |
| `Space2XL` | 6 | 24 | 区块之间 |
| `Space3XL` | 8 | 32 | 页面的各部分之间 |

`t.Space(n)` 只在 uikit 内部出现；`internal/app` 只写名字。

#### 内边距（inset）

组件内部使用，屏幕不自己加 padding。

| Token | 值（左右／上下） | 用在哪里 |
| --- | --- | --- |
| `ControlPaddingX` | 12 | 默认控件的左右 |
| `ControlPaddingXSmall` | 8 | 小控件、chip、行的左右 |
| `PanelPadding` | 12 | 面板和面板头的两侧 |
| `TipPaddingX`／`TipPaddingY` | 4／2 | tooltip（Amp Web 的 `px-1 py-0.5`） |
| `CardTipWidth`／`CardTipPaddingX`／`CardTipPaddingY`／`CardTipPaddingBottom` | 320／10／10／8 | 富提示（hover 卡片） |
| `ToastPaddingX`／`ToastPaddingY` | 16／8 | toast |
| `PopoverPadding` | 16 | 弹层（时序卡） |
| `DialogPadding` | 24 | 对话框 |
| `DialogGap` | 16 | 对话框内的分组间距 |
| `CardPaddingX`／`CardRowPaddingY` | 16／4 | 设置卡片行 |
| `KbdPaddingX` | 4 | 键帽左右 |
| `WindowMargin` | 16 | 浮层和窗口边缘的距离（每侧） |
| 输入框的垂直内边距 | 8 | `TextArea` 的上下（`ControlPaddingXSmall`），左右 `ControlPaddingX` |

#### 宽度

| Token | 值 | 用途 |
| --- | --- | --- |
| `WidthNumber` | 88 | 数字字段：端口、超时、计数 |
| `WidthNarrow` | 120 | 窄字段，也是参数表的 key 列最小宽 |
| `WidthPicker` | 140 | 选择器的最小宽度 |
| `WidthField` | 160 | 常用字段 |
| `WidthFieldWide` | 200 | 宽字段 |
| `WidthWide` | 240 | 宽字段、弹层、时序卡 |
| `DialogSmall`／`DialogMedium`／`DialogLarge` | 480／640／840 | 对话框宽度档，随窗口收缩 |
| `SettingsWidth` | 620 | 设置页的内容宽（`SettingsCardPage`） |
| `SettingsLabelWidth`／`SettingsLabelMax` | 112／180 | 设置标签列：最窄 112，按组里最宽的标签放宽到 180 |
| `SettingsHintWidth` | 400 | 设置说明的最大宽 |
| `CardKeyWidth` | 52 | 卡片提示里的 key 列 |
| `TipMaxWidth`／`CardTipWidth`／`ToastWidth` | 450／320／360 | tooltip 最大宽、hover 卡片宽、toast 最大宽 |
| `CommandPaletteWidth` | 720 | 命令面板 |

**参数表的 key 列。** 它原来是 `t.Space(27)`，即 108 点；现在是 `WidthNarrow`（120 点），所以 key 列由 108 放宽到 120，能放下常见的请求头名。

**变量表的动作列。** 行尾的动作（揭示、Persist、Reset、Remove）都是 `HeightRowAction`（20）的正方形，相邻之间留 `SpaceXS`（4），列宽是 `HeightRow*4 + SpaceXS`（100）。没有揭示按钮的行用同样 20 的占位；原来是 `HeightRow`（24），会让这一行的 Remove 比其他行偏 4 点。Remove 离列的右边 8 点，各行一致。

**整行的设置字段。** 设置页里占整行的字段（如项目目录）用 `MinWidth(260).Grow(1)` 占满块宽，保存按钮靠右；原来的 `Width(260)` 是固定宽，长路径没有空间。

#### 圆角

圆角随嵌套同心：内层 = 外层 − 内边距，所以工作区是 8，里面的控件是 6，再里面的面是 4。

| Token | 值 | 用在哪里 |
| --- | --- | --- |
| `RadiusInner` | 4 | 控件内的面、键帽、徽标、复选框的方框、`LinkButton` |
| `RadiusControl` | 6 | 按钮、输入框、侧栏条目、分段轨道、色块 |
| `RadiusPanel` | 8 | 工作区面板、tooltip、画廊的 `Panel`、tab 面 |
| `RadiusMenu` | 6 | 菜单面（Amp Web `rounded-md`） |
| `RadiusCard` | 12 | 弹层（时序卡）、无内边距的模态（命令面板） |
| `RadiusDialog` | 16 | 对话框、toast |

#### 图标

只用 Lucide。尺寸只有两档；颜色跟随文字，不单独上色。例外：开关类条目开着时图标用强调色（第 3 节）。

| Token | 值 | 用在哪里 |
| --- | --- | --- |
| `IconInline` | 14 | 行内、按钮内、输入框前的放大镜 |
| `IconLarge` | 16 | 复选框的方框、Callout 的图标、空状态、徽标的高、侧栏的图标槽 |

#### 边框（hairline）

| Token | 值 | 用途 |
| --- | --- | --- |
| `HairlineWidth` | 1 | 所有细线：面板的边、区域之间的分隔线、表头线、浮层边、字段的边、复选框的边 |

颜色用 `Palette.Hairline`（分隔、面板边）和 `Palette.Ring`（焦点、未选中的复选框和单选的边）。只有区域之间画线，同一表面里的两块靠间距区分。

#### 阴影（elevation）

工作区没有阴影。浮起的东西照 Amp Web 的 `--shadow-sm/md/lg` 与 `shadow-popover` 分层；Dark 把 1 px 描边从 8% 黑换成 10% 白，菜单的下两层加深到 80%/60%。

| Token | 层 | 用在哪里 |
| --- | --- | --- |
| `ShadowSmall` | `0 1 3 0` + `0 1 2 -1`，两层 10% 黑 | 小面 |
| `ShadowMenu` | 1 px 8% 黑环 + `0 4 6 -1` 10% + `0 2 4 -2` 10%；Dark 是白环 + 80%/60% 深影 | 菜单面（`MenuPanel`） |
| `ShadowPopover` | 与菜单亮色相同；Dark 只有白环变化 | 弹层、时序卡、toast、tooltip、hover card |
| `ShadowDialog` | `0 10 15 -3` + `0 4 6 -4`，两层 10% 黑 | 对话框（`Modal`、`ModalFlush`） |

#### 动效（motion）

- **uikit 不写过渡。** 悬停、选中、展开、切换 tab 都立即到位。没有任何组件定义过渡时长。
- **浮层的出现由 MyGo 决定。** 对话框由 MyGo 的 `DialogBase` 打开，`ModalFlush` 只在第一次显示时把它的动画值 `modal-in` 跳到 0。
- **提示的延迟：** 指针停留 `TooltipDelay` 120 ms 就显示（Web Amp 实测）；提示消失后 MyGo 的 warm 窗口（约 80 ms）内，相邻目标立即显示；键盘焦点立即显示。
- **忙碌的动态：** `Spinner` 和不确定的 `ProgressBar` 的转动和流动由 MyGo 绘制。这是唯一的持续动画。
- 用户自己拖动的分隔条、滚动和拖选不属于动效。

#### 浮层和对话框

| 面 | 圆角 | 内边距 | 阴影 | 备注 |
| --- | --- | --- | --- | --- |
| 菜单（`MenuPanel`） | 6 | MyGo 的 4 px 内边距 | 三层 `shadow-popover` | 行高 28、圆角 4、字号 12；高亮是正文色 7% wash |
| 弹层（`PopoverPanel`） | 12 | `PopoverPadding` 16 | `ShadowPopover` | 时序卡 |
| 对话框（`Modal`） | 16 | `DialogPadding` 24，间距 `DialogGap` 16 | `ShadowDialog` | 遮罩 `Scrim`（黑色 35%），离窗口边 `WindowMargin` 16 |
| toast（`ToastPanel`） | 16 | `ToastPaddingX／Y` 16／8 | `ShadowPopover` | 宽不超过 `ToastWidth` 360；底边离底栏 `HeightFooter` + 16 |
| tooltip（`Tip`） | 0 | `TipPaddingX／Y` 4／2 | 三层 `shadow-popover` | 最大宽 `TipMaxWidth` 450，12 号正文；方角、无描边 |
| hover 卡片（`CardTip`） | 6 | `CardTipPaddingX/Y/Bottom` 10／10／8 | 三层 `shadow-popover` | 宽 `CardTipWidth` 320，13 号标题、11 号元信息，分隔线下的键值行；约 650 ms 出现 |

#### 颜色（语义表）

颜色不是 token 尺寸，但每个组件只用这些名字。Light 与 Dark 的值在 `theme.go`。

| 名字 | 用途 |
| --- | --- |
| `Window` | 外壳：标题栏、侧栏、底栏、画廊面板 |
| `Background` | 内容：表格、编辑器、面板、弹层 |
| `Input` | canvas 上的输入框（比 canvas 深一档） |
| `Border` | 分隔线、面板边、表头线 |
| `Muted`／`MutedHover`／`MutedPressed` | 文字色叠出来的控件面：静止 7%、悬停 11%、按下 15% |
| `Foreground`／`MutedForeground`／`Faint` | 正文、次要、图标和占位符 |
| `Primary`／`PrimaryHover`／`PrimaryPressed`／`PrimaryForeground` | 墨：主按钮、开关开、复选框选中、浮动列表的高亮 |
| `Accent` | 列表、导航和活动工具的选中浅底（对应 Amp `--accent` / `--sidebar-accent`） |
| `Ring` | 1 pt 焦点边（控件外框、字段边外一圈）；未选中的复选框和单选的边 |
| `Success` | 通过和录制，只有这两件事 |
| `Info`／`Warning`／`Danger` | 信息、警告、错误（文字和图标） |
| `Editor*`／`Diff*` | 文件 diff 编辑器的代码 token 与增删行数据色，不表达通过/失败（对应 `--diffs-token-*` / Amp `--diff-*`） |
| `Scrim`／`Shadow` | 遮罩和阴影 |
| `Inverse`／`InverseText` | tooltip 的反色 |

#### 其他

- `Scaled(size)`：把字号按桌面字号缩放。`SetTextScale` 设定缩放，进程内只有一个值。
- `MethodColor(method, t)`、`StatusColor(status, t)`：方法和状态码的颜色。2xx 和 304 是次要文字色；3xx 是信息色；4xx 是警告色；5xx 是危险色。
- `EditorTheme(t)`：把 `Editor*` / `Diff*` 映射到 MyGo 的 token kinds 与 diff 墨色。按 `@pierre/diffs` 的 GitHub Light/Dark token 色，关键字和运算符是 `EditorKeyword`（红），实体（函数名、类型）是 `EditorEntity`（紫），标签是 `EditorTag`（绿），变量是 `EditorVariable`（橙），属性、常量、数字是 `EditorConstant`（蓝），字符串是 `EditorString`、正则是 `EditorRegexp`，注释是 `EditorComment`。这些风格只有颜色，没有斜体或粗体：`--diffs-token-*` 只传颜色。未改动的 diff 行没有底色；增删行带 diff 底色和行首符号。语法里的颜色只是代码数据，不是 `Success`。
- `MarkColors`：请求标记的六种颜色（红、橙、黄、绿、蓝、紫），饱和色，不随主题变化，带白色对勾。

### 6.4 文字角色（`text.go`）

调用方说“这段文字是什么”，角色决定大小、字重和颜色。布局（`Grow`、`SingleLine`、`Tooltip`）照常链式设置。`internal/app` 里不再有裸 `ui.Text` 和行内 `FontSize`（只有 `// Bare:` 的两处，见 6.8）。

| 角色 | 样子 | 用途 | 不要做 |
| --- | --- | --- | --- |
| `Display` | 20，500，行高 28／20 | 首次引导、命令面板的大标题 | 不用于面板标题 |
| `Heading` | 16，500，行高 24／16 | 对话框、面板、检查器里的请求名 | 不用于正文 |
| `Body` | 13，颜色继承，行高 20／13 | 普通文字；在按钮和选中行里也对 | 不写死颜色 |
| `BodyStrong` | 13，500 | 一组的引导词、一行里最要紧的值 | 不要整段都用 |
| `Prose` | `Body`，行高 20／13 | 成段的说明、设备设置步骤、导入提示 | 不用于单行标签 |
| `Small` | 12，行高 16／12 | 弹层说明、菜单里的第二行 | 不用作表单字段的标签 |
| `Hint` | 12 的说明，次要色，行高 20／12 | 控件下面或空面板里的一句说明 | 不用于错误 |
| `Caption` | 11，正文色，行高 13.6／11 | 与前后文同属一组的一行事实（不是注释） | 不用来表示警告（用 `Callout`） |
| `Meta` | 11，次要色，`tnum` | 计数、时间、大小；数字默认等宽对齐 | 不用于正文 |
| `Label` | `Meta` 加 500 | 字段名、分组标题（标题式大小写，不全大写） | 不用于数据 |
| `Mono` | 12 等宽 | 线上数据：URL、路径、数字 | 不用于说明文字 |
| `DataBody` | 13 等宽 | 数据在正文的大小：header 值、字段的等宽文字 | 不用于说明 |
| `DataMeta` | 11 等宽 | 表格里作为数据的计数、大小、单元格 | 不用于说明 |
| `Code` | 12 等宽，行高 16／12，可多行 | 多行的线上数据：header 块、原始报文、要复制的片段（环境变量列表） | 不用于一句话 |
| `ErrorText` | 12，危险色，行高 16／12 | 页面或面板里没有字段可挂的错误句子 | 字段的错误走 `FormRow` |

等宽的次要信息写成 `Meta(c, s).Font(uikit.FontMono)`，或用 `DataMeta`。`TextData`（12）只给等宽数据；界面文字最小 11、正文 13。

### 6.5 组件

每个组件一行：用途、变体和尺寸、状态、不要做的事。除非另注，组件的高度、内边距、圆角由 uikit 决定。

#### 按钮

| 组件 | 用途 | 变体和尺寸 | 状态 | 不要做的事 |
| --- | --- | --- | --- | --- |
| `PrimaryButton` | 一屏或一个对话框的主操作 | 28 高，`ControlPaddingX` 12，13／500，强调色实底，圆角 6 | 静止、悬停（`PrimaryHover`）、按下（`PrimaryPressed`）、焦点、禁用 | 不放进行内或菜单；一屏只放一个 |
| `PrimaryButtonState` | 只用于图版，画出指定的悬停或按下面 | 同 `PrimaryButton` | 悬停、按下 | 界面代码不调用 |
| `PrimaryButtonBusy` | 启动长任务的主操作 | 同 `PrimaryButton`；忙碌时转圈代替标签，标签保留宽度 | 忙碌（禁用）、静止 | 忙碌时不要只禁用 |
| `SecondaryButton` | 次要操作 | Amp Web：26 高，左右 8，12 字，圆角 4，1 pt hairline，透明面 | 静止、悬停/按下（正文色 5% wash）、焦点、禁用（整体半透明） | 不用强调色；不把面默认填成灰 |
| `SecondaryButtonBusy` | 启动长任务的次要操作 | 同 `SecondaryButton`；忙碌时转圈，宽度不变 | 忙碌（禁用）、静止 | 同上 |
| `DestructiveButton` | 破坏性确认对话框的确认钮 | 28 高，危险色实底，文字是 `PrimaryForeground`（Light 近白） | 静止、悬停、按下、禁用 | 只放在对话框底部；行内和菜单里的破坏性动作先问 |
| `CompactButton` | 密集的行和栏里的次要操作 | 22 高，`ControlPaddingXSmall` 8，11 字，面同 `SecondaryButton` | 同 `SecondaryButton` | 不要当主操作用 |
| `ChipButton` | 中性面的小按钮，内容由调用方画（图标加快捷键） | 22 高，8 内边距，间距 6，圆角 6 | 静止、悬停、按下、禁用 | 不要只放文字（用 `CompactButton`） |
| `LinkButton` | 行内动作，如“添加属性” | 22 高，11 字，文字是次要色，可带 14 的图标，圆角 4；无面 | 静止（无面）、悬停（中性面，文字正文色）、禁用 | 不要当主要操作；不要放进表单的控件位 |
| `ToolbarButton` | 工具栏和标题栏的动作 | 28 高，`ControlPaddingX` 12，13 字，圆角 6；active 是强调浅底 | 静止（次要文字色）、悬停（中性面）、按下、active、禁用 | 不要用来代替主按钮 |
| `IconButton` | 只有图标的动作 | 28 正方形，图标 14 | 同 `ToolbarButton` | 必须带 `tip`（它是唯一的说明）；行内用 `RowIconButton` |
| `RowIconButton` | 行内、侧栏标题、列表头和搜索框里（清除、筛选）的图标动作 | `HeightRowAction`（20）正方形，图标 14，圆角 `RadiusInner`，离行边 `RowActionInset`（2） | 同 `ToolbarButton` | 不用于工具栏（用 `IconButton`）；必须带 `tip` |
| `FooterButton` | 底栏的动作 | 22 高，8 内边距，11 字，圆角 4 | 静止（只有文字）、悬停、按下、on（凹进的选中面） | 不要用于底栏之外 |
| `RowButton` | 列表中的整行，如命令面板的结果 | 最小 28 高，内边距 4×8，13 字，圆角 6，撑满宽 | 按下时出面；没有悬停面（列表不随指针亮） | 不要自己画选中；选中用 `Selected` 或 `DataList` |
| `SwatchButton` | 挑选颜色 | 28 正方形，圆角 6，颜色本身不变 | 选中（对勾）、悬停（1 pt 文字色描边）、禁用 | 不改颜色本身 |
| `Kbd` | 快捷键键帽，只用于菜单项和命令旁边 | 18 高，11 字 500，圆角 4，1 pt `Border`，左右 4 | 无 | 不要嵌进句子里（写文字，不写键帽） |

**窄处的动作只留图标。** 请求体的工具行（模式、读取方式、Format）在响应并排时只有编辑区的一半宽：两个 `Select`（各 `WidthPicker` 140）加上带字的 Format 放不下，所以那里的 Format 用 `IconButton`（28 正方形），带同样的标签和提示；宽处仍是 `ToolbarButton` 带字。MQTT 消息详情的头部同理：Text／JSON／Hex 的 170 宽 `Tabs` 保留，复制改成 `IconButton`（与协议和 SSE 视图的复制一致）。

#### 输入

| 组件 | 用途 | 变体和尺寸 | 状态 | 不要做的事 |
| --- | --- | --- | --- | --- |
| `Input` | 单行文本 | 28 高，左右 12，13 字，圆角 6，canvas 上为 `Input` 面 | 静止：正文色 15% 边；hover 不变；焦点：边外再加 1 pt `Ring`；禁用（整体半透明） | 不在 chrome 上直接用（用 `ChromeFieldFace` 包） |
| `InputState` | `Input`，带无效面 | 同 `Input` | 无效：1 pt `Danger` 边（焦点时边外加 1 pt `Danger`） | 无效的说明用 `FormRow` 的 `errText` |
| `InputControl` | 不带面的输入，给要自己画文字段的字段 | 同 `Input`，无面 | 同 `Input`（面由外层给） | 不单独使用 |
| `FieldFace` | 给自带控件的行画字段面（搜索框、URL 栏） | 行高由调用方定，圆角 6，裁切子元素 | 15% 墨边；焦点加 1 pt `Ring` | 不给 `Input` 再包一层 |
| `ChromeFieldFace` | chrome 上的字段面：canvas 底，边与 `FieldFace` 共用 | 同 `FieldFace` | 同 `FieldFace` | 不在 canvas 上用 |
| `NumberField` | 整数，后面跟单位，如 `[60] s` | 宽 `WidthNumber` 88，等宽，单位是 `Meta` | 无效（超范围或非数字）：`Danger` 边；空白时保留旧值 | 需要 ± 步进时用 MyGo 的 `NumberInput`（6.8） |
| `Combobox` | 可输入也可从建议里选 | 28 高，圆角 8，canvas 色的字段面；弹层的高亮是 `Accent` 7% wash | 静止：15% 墨边；hover 不变；焦点：边外加 1 pt `Ring`；禁用 | 只能选一项时用 `Select` |
| `TextArea` | 多行文本 | 上下 8、左右 12，圆角 6，canvas 上为 `Input` 面，焦点边 `Ring` | 静止、焦点、禁用 | 不用于单行 |
| `SearchField` | 在列表里查找 | 28 高，左右 8，放大镜 14，间距 4 | 静止、焦点（`FieldFace`，`onChrome` 时为 `ChromeFieldFace`）、空时显示占位 | 不用于其他文字字段 |
| `FormRow` | 表单字段：标签、控件、说明和错误绑定到控件 | MyGo 的 `Field`：不在 `ui.Form` 里时标签在控件上方；在 `ui.Form` 里时标签在左列并与控件首行对齐 | 错误：控件 `Danger` 边、控件下方的红字；说明：控件下方的次要字 | 不要在控件下面自己写说明（用 `description`） |
| `FormRowTip` | 说明放在标签的提示里 | 标签上方，控件在下 | 同 `FormRow` | 说明很重要时不用（看不见） |
| `FormRowTall` | 比一行高的控件（代码编辑器）的表单行 | 标签在控件首行，说明和错误在控件下 | 同 `FormRow`；错误只画一次 | 不用于单行控件 |
| `Select` | 从几个选项里选一个（选项就是显示的文字） | 触发器 28 高，左右 12，圆角 8，最小宽 `WidthPicker` 140，chevron 10（`SpaceCompact`）；弹层离触发器 4，面板内边距 4，高亮是 7% 墨色 wash | 静止：canvas 面 + 15% 墨边；hover/按下不变；焦点：边外加 1 pt `Ring`；禁用 | 选项是标识符而显示是另一种语言时用 `SelectLabels` |
| `SelectLabels` | `Select`，选项与显示文字分开 | 同 `Select` | 同 `Select` | 同 `Select` |
| `MenuSelect` | 用菜单做的选择（如 body 格式） | 触发器同 `Select`，弹层是 `MenuPanel` | 同 `Select` | 不用于切换显示模式（用 `Segmented`） |
| `SettingsRow`／`SettingsRowMultiline` | 设置行：左列标签，右边控件，下面一行说明 | 标签列按组宽（112–180）；多行版的标签与控件首行对齐 | 说明可带 tip；控件自己的状态 | 不用于非设置的表单（用 `FormRow`）；不放进 `SettingsCard`：它不带卡片的内边距，标签会贴着卡边。卡里用 `SettingsCardBlock`／`SettingsCardRow`（HTTPS 的「永不解密」原来是 `SettingsRow`，标签落在卡边上，现在改为块） |
| `Switch`／`SettingsSwitch` | 开关 | 轨道 36×20，圆角 10；`SettingsSwitch` 带标签 | 关：正文色 20% 轨道 + `MutedForeground` 滑块；开：`SwitchOn` 轨道 + 浅滑块（Dark 保留浅轨白滑块）；hover 不变；焦点、禁用（半透明） | 不要用来做互斥选择 |

**单行文字的边距。** `Input` 与 `InputControl` 的文字左右各留 `ControlPaddingX`（12）。左边是内边距；右边是一条透明的边框（`uikit.TextInset`），因为 MyGo 把输入的文字裁到边框的外沿，而不是内边距。这条边框在各状态下都画不出来（`InputControl` 建字段时把 Accent、Border、Text、Danger 设为透明，MyGo 按状态给边框上色时取的就是这些颜色）。超出字段的文字裁在这条线上，没有省略号（MyGo 的输入不做省略）；所以字段的占位文字或长值要按宽度留够，或由调用方缩短。字段的焦点边和无效边由 `fieldFaceRadius` 经 `fieldEdge` 用 `Draw` 画在整个边框盒上（1 pt，圆角随字段：文字 6、选择器 8），包括这条透明边框的一侧；原先的 `InsetShadow` 只画到内边距的边缘，右侧 12 点没有边（`TestFieldEdgeReachesItsRightSide` 守住这一点）。带 `fieldFace` 的字段，其 `Draw` 归 kit 所有：MyGo 的 `Draw` 只有一个槽，边会替换调用方设的 `Draw`，之后再设的 `Draw` 会把边去掉。URL 栏是裸的 `ui.TextInputBase`，同样用 `uikit.TextInset`，右边留 `SpaceM`（8），与它的左边相同。`Combobox` 的文字停在箭头前 `ControlPaddingX`（12）：字段的 `Gap` 就是文字与箭头之间的距离（箭头宽 10，字段内边距 12），弹层的高亮仍是 `Ring`。

**字段的说明在控件下面。** 数字字段的单位说明（超时的“秒”、重定向的“HTTP 跳数”）放在 `NumberInput` 那一行的下一行，宽度给整列；并排放在控件右边时，表单的字段列只剩几十点，说明会被裁成半个词。

#### 选择

| 组件 | 用途 | 变体和尺寸 | 状态 | 不要做的事 |
| --- | --- | --- | --- | --- |
| `Checkbox` | 布尔值，带标签 | 16 的方框（圆角 4，1 pt `Primary` 边），14 的对勾，标签 13 | 未选：透明面、`Primary` 边；选中：`Primary` 实底、对勾为 `PrimaryForeground`；悬停和按下不变；禁用：不切换（整体半透明） | 不用于互斥选择（用 `Radio` 或 `Segmented`） |
| `Radio`／`RadioGroup` | 从两到四个互斥选项里选一个 | 16 的圆（1 pt `Primary` 边），选中的 6 点圆点为 `PrimaryForeground`；`RadioGroup` 竖排，间距 `SpaceS` | 同 `Checkbox`；`RadioGroup` 只占一个 Tab 停靠点 | 超过四项不用；当前没有接入界面（6.8） |
| `Segmented` | 互斥的显示模式（如 Pretty／Raw） | 轨道 24 高（`HeightRow`），内边距 2，段 11 字，圆角 6 和 4 | 选中：canvas 面加 hairline；悬停：文字正文色；按下 | 不用于导航或动作；超过四段不用 |
| `Tabs`／`CountedTabs` | 面板内的标签页（切换视图或过滤） | 32 高，左右 10，12 字，圆角 8；计数是 `Meta` | 静止（次要色）、悬停/按下（正文色 5% wash）、选中（canvas 面加 hairline，正文色）；照 Amp Web 的顶部标签 | 不用强调色；不要两层同名 |
| `TabStrip` | 放不下时横向滚动的标签行 | 同 `Tabs`；缺的一侧边缘渐隐（`EdgeFade`） | 同 `Tabs` | 不要在宽处使用 |
| `SubTabs` | 第二层标签（在一个 tab 里面，或列表头） | 22 高，标签间距 8，选中加 2 pt 强调色下划线（只在字宽下） | 静止、悬停（正文色）、选中（下划线） | 不要和 `Tabs` 放在同一层 |
| `MoreButton`／`MoreMenu`／`TabMenu` | 放不下的选项收进菜单 | `MoreButton` 是标题栏和过滤栏的 28 正方形 `IconButton`，画图标 `⋯`，直接打开菜单（不带 chevron）；`MoreMenu` 是行、侧栏标题和面板头里的 `⋯`，`HeightRowAction`（20）正方形、圆角 `RadiusInner`，离行边 `RowActionInset`（2）；`TabMenu` 是长得像 tab 的菜单入口 | 同菜单 | 不要收进主要动作 |

#### 反馈

| 组件 | 用途 | 变体和尺寸 | 状态 | 不要做的事 |
| --- | --- | --- | --- | --- |
| `Callout` | 页面或区块里一句需要读的通知：警告、错误、信息、成功，一句话加行尾动作 | 四种：`CalloutInfo`、`CalloutSuccess`、`CalloutWarning`、`CalloutDanger`；图标 16，首行对齐；正文 13；没有底色。进行中（如上游代理在加载）用 `CalloutInfo`，不用 `CalloutDanger`；页脚的状态芯片同理，`danger` 只在真正失败时为真 | 静止；动作是按钮（`SecondaryButton` 等） | 不用于字段错误（用 `FormRow`）；不做成卡片 |
| `ErrorText` | 没有字段可挂的错误句子（如连接失败的细节） | 12，危险色 | 静止 | 不用作字段错误；不用来显示状态词（用 `Meta` 加危险色） |
| `Spinner` | 未知长度的忙碌 | 正文次要色，标签是名字 | 转动（MyGo） | 按钮里的忙碌用 `*Busy` |
| `ProgressBar` | 进度：0 到 1 的确定进度，或不确定（负值） | 4 高，圆角 2，轨道 `Muted`，标签由调用方在旁边写 | 确定、不确定 | 没有进度的长任务用 `Spinner` |
| `Badge` | 计数或状态的小标签 | Amp Web：18 高，11 号 500，tnum，圆角 4，左右 6；面是文字色 10% | 颜色同时给文字和面；错误徽标即 `Danger` 与 `Danger/10` | 不做成卡片；静默状态别用彩色徽标 |
| `EmptyState` | 空面板：一句标题、一句说明、动作一行 | 图标 16（不加框），图标与标题之间 8 | 无 | 不用大图标；动作排成一行，不换成按钮堆 |
| `Tip`／`TipKeys`／`TipSurface` | 悬停和焦点的说明，可带快捷键 | 方角、4×2 内边距，最大宽 450，12 号；快捷键是 11 号 500 的次要色纯文本（Amp） | 指针停 120 ms 出现；键盘焦点立即出现；Escape 关闭；上方不够就翻到下方 | 不在提示里放按钮或输入框 |
| `CardTip`／`CardTipBeside` | 富提示：标题、一行元信息、分隔线和键值行 | Amp：320 宽、6 圆角、10／10／8 内边距；键 11 次要色，值 12（含 `://` 的用等宽）；`CardTipBeside` 的顶边与行齐平，开在行的右侧，离行 `SpaceS` | 约 650 ms 出现；弹层自动避让边缘，放不下就翻到另一侧 | 只用于侧栏的集合请求行和抓包行；侧栏的请求行用 `CardTipBeside`，不盖住上面的行 |

#### 结构

| 组件 | 用途 | 变体和尺寸 | 状态 | 不要做的事 |
| --- | --- | --- | --- | --- |
| `Panel` | 框住一块数据区（预览、目录、阅读区） | chrome 背景，1 pt `Border`，圆角 8 | 无 | 不要给每个分组都加 |
| `PaneHeader` | 面板头：内容在左，动作在右 | 32 高（`HeightTabs`），左右 12（`PanelPadding`），间距 8 | 无（不画底线） | 不在面板头里放第二行标签（标签和标题同一行） |
| `PaneTitle` | 面板名，带状态点，点它把共享读取器交给这一侧 | 22 高，点 6，名字 13 | 悬停、按下 | 不用来做导航 |
| `Modal` | 对话框：标题、内容、底栏 | 宽 `DialogSmall`／`DialogMedium`／`DialogLarge`，内边距 24，间距 16，圆角 16，遮罩 `Scrim`，阴影 `ShadowDialog` | 打开、Escape 关闭、焦点回到打开它的控件 | 不在对话框里再开对话框 |
| `ModalFlush` | 没有内边距的对话框（命令面板） | 同 `Modal`，圆角 12，内容撑满 | 同 `Modal` | 不用于普通表单 |
| `DialogTitle` | 对话框的名字 | `Heading`，正文色 | 无 | 不用正文字号代替 |
| `DialogColumn` | 对话框内容的列 | 宽度由调用方给（档位常量） | 无 | 不在列里放全宽的表格 |
| `DialogFooter` | 对话框底部的动作：右对齐，顶上 12 的间距和一条 hairline | 间距 8，换行 | 无 | 破坏性动作不要放在底部以外 |
| `Toasts`／`ToastPanel` | 短暂的通知（底边离底栏 16） | 宽不超过 360，内边距 16×8，圆角 16，阴影 `ShadowPopover` | 出现、消失（由调用方控制） | 不放需要读完的长文本 |
| `PopoverPanel` | 悬浮的面（时序卡、复杂提示） | 圆角 12，阴影 `ShadowPopover`；内边距由调用方给（`PopoverPadding` 16） | 无 | 不放菜单（用 `MenuPanel`） |
| `MenuPanel`／`MenuButtonOptions` | 菜单的面（MyGo 画条目） | 圆角 6，三层 `shadow-popover`，行高 28、行圆角 4、字号 12 | 行的高亮是正文色 7% wash，文字仍是墨色；`MenuFace` 在建按钮时把这张面传给 MyGo | `ContextMenu` 和 `PopoverMenu` 的面是 MyGo 的固定样式（第 12 节） |
| `EdgeFade` | 被裁切的一侧渐隐到表面色 | 宽 24（`Space2XL`） | 无 | 不用作装饰 |
| `KVRow` | 只读的键值行，内容由调用方排 | 不画底线 | 无 | 不用于可编辑的表 |
| `StatusPill` | 状态文字（如“已停止”） | Amp Web 的安静过滤 pill：28 高，4×6 内边距，11 号，全圆角；只有文字色，不填底 | 颜色由调用方给（2xx 和 304 用次要色） | 不给状态加底色或圆点；可点击的版本用按钮 |
| `MethodLabel` | HTTP 方法 | 12 等宽 500，方法色文字 | 无 | 不做成 chip（列表里是界面字体，树里是等宽） |
| `QueryChip` | 过滤语法的 chip（`status:>=400`） | `HeightChip`（18）高，圆角 `RadiusInner`，居中于 24 高的字段内，字段两侧留 `SpaceM`（8）；chip 左右内边距 2，标签的左右内边距 4；× 与 chip 同高 | 静止、悬停、按下 | 不做成输入框 |
| `Glyph` | 装饰图标 | 尺寸由调用方给（14 或 16） | 无 | 不带焦点 |
| `Divider`（MyGo `ui.Divider`） | 分隔线：横向，或在横排里变成竖线 | 颜色即 `Border`，1 pt | 无 | 不要用色块分区 |

查询 chip 的字段方案（图版 `query-chip-options`）：原来字段两侧是 4，chip 离边 4、文字离边 10，看上去贴边。考虑过三种：A 保持 4；B 两侧改为 `SpaceM`（8），与 `SearchField` 相同，chip 离边 8、文字离边 14，占位文字随之内移，字段高度不变，选用 B；C 把字段加高到 28，会比同一栏的协议分段（24）高，整栏高度不一致，不选。只缩小 chip 不能给文字留出位置，也不选。过滤栏里的 ⋯ 原是 `MoreMenu` 的 20 正方形，比断点按钮窄，协议分段随之右移；现在用 `MoreButton`（28），与断点按钮同高。字段内的清除和筛选原是 28 高的 `IconButton`，被 24 高的字段裁成 24，现在用 `RowIconButton`（20）。

#### 列表和侧栏

| 组件 | 用途 | 变体和尺寸 | 状态 | 不要做的事 |
| --- | --- | --- | --- | --- |
| `DataList`／`DataListIndexed` | 数据列表：流量、消息、Protobuf、运行结果 | 行高由行决定（表格和数据列表用 `HeightRow` 24），撑满宽；选中是 `Accent` 浅底加左侧 2 pt 标记线；列表失焦时浅底变中性面、标记线变 `Faint` | 悬停、选中、失焦 | 不要在行上再画第二个光标或标记 |
| `ListTheme` | 给 MyGo 的表格、列表、树设选中色 | 选中浅底；失焦时中性面 | 聚焦、失焦 | 不在行里自己画选中底 |
| `RowMarker` | 数据行的左侧标记线（宽 4 的命中框，画 2 pt） | 2 pt，上下各离 6 pt（不超过行高的四分之一） | 聚焦（强调色）、失焦（`Faint`） | 只给 `DataList` 的行用 |
| `Selected` | 圆角选中块，用于选位置或选东西的行（挑选列表、规则、Git 文件、场景） | 选中浅底（失焦为中性面）；圆角由调用方的行给，即 6 | 聚焦、失焦 | 不与 `RowMarker` 同时用在同一行 |
| `SidebarList` | 侧栏的一个列表（有键盘选择和字母跳转） | 行高 24，内缩 6 | 选中（圆角浅底，不描边）、焦点、字母匹配 | 不要用强调色做选中 |
| `SidebarItem`／`SidebarItemImage`／`SidebarItemLetter` | 侧栏条目：图标或字母方块、名字、可带计数 | 24 高，内缩 6，图标 16 占一个槽，圆角 6；未选中文字次要色、图标 faint | 选中（圆角浅底）、悬停（正文色）、计数为 0 时不显示 | 不要在侧栏里放多于一个选中项 |
| `SidebarButton` | 做动作而不是选中的侧栏行 | 同 `SidebarItem` | `on` 时图标为强调色（开关类） | 不用来表示“现在在哪” |
| `SidebarHeading` | 侧栏分区标题（可折叠，右端可带开关或加号） | 24 高，11 号次要色 500 | 折叠箭头、悬停 | 不做成按钮 |
| `SidebarDot`／`LetterSquare` | 没有图标时占槽的圆点；字母方块（集合或环境） | 点 6，方块 `size`，圆角 6 | 无 | 不用于选择 |
| `SidebarPanes` | 侧栏的中间三段，按分配的高度排，中间的分隔条可拖 | 分隔条命中 7 高，线 1 pt | 悬停（ns 调整光标）、拖动、双击恢复 | 不要让固定的分区滚动 |
| `ActivityStrip` | 列表上方的活动柱状行：摘要、柱、结束时间 | 24 高（`HeightStatus`），柱 14 高，最窄 6 pt 一根，最矮 5 pt | 拖选时间范围、清除按钮（固定宽 48） | 不要把柱的颜色用于正常流量（只有异常着色） |
| `ResponseMeta` | 响应面板头右侧的耗时和大小 | 等宽，14 行高；耗时悬停显示时序卡 | 悬停（时序卡）、窄面板里省略（悬停看全文） | 不要在面板头里放第二行 |
| `Sparkline` | 录制 pill 的速率曲线 | 56×14，1.25 pt 线；少于两个点时只画基线 | 无 | 不用于数据表 |

**带分隔线的行。** 行与行之间各画 1 pt 的分隔线，最后一行不画，它的底边由卡片的边给出。最后一行也画时，分隔线与卡片底边只差几点，看上去是双线（HTTPS 的「本地应用」列表原来如此）。

**项目符号。** 符号与文字分两列（`Row`，符号在前），换行的文字对齐到文字起点，而不是符号（Postman 导入的说明原来把续行排在符号下面）。

**侧栏的空状态提示。** 侧栏里的说明（如「Apps and domains show here…」）允许换行，不加 `MaxLines(1)`；原来的单行截断在词中间加省略号，读成半句。

**运行中的状态。** 运行中，过去的运行选择器不能切换，当前运行的名字作为状态行（`Spinner` 加正文次要色的名字），不画禁用的 `SelectLabels`：禁用的选择器仍画成面加 chevron，读作控件。

**带展开箭头的行。** 同一组的行共用箭头的位置：不能展开的行留出同宽（`IconInline` 14）的空位，状态图标和标题才会在同一列（诊断的步骤）。 展开的细节放在同样的箭头槽和状态槽之后（`Row`），正文列与标题对齐，不用手调左边距。

#### 主题和文字之外的组件

`Palette`、`Theme`、`Colors`、`Shadow`、`Scaled`、`SetTextScale`、`TextScale`：见 6.3。它们不画东西。

### 6.6 图标

只用 Lucide。尺寸只有 14 和 16 两档；颜色跟随文字，不单独上色。例外：开关类条目开着时图标用强调色（第 3 节）。图标画在 uikit 里的（复选框的对勾、Callout 的三种图标、搜索、chevron、关闭、展开）都从同一套 Lucide 取。

### 6.7 工具提示

所有提示都走 `uikit.Tip`（带快捷键用 `TipKeys`，富内容用 `CardTip`），没有地方自己画。

- 面：Amp Web 的 canvas 方角面板，无描边，三层 `shadow-popover`，内边距 4×2（`TipPaddingX／Y`），最大宽 450。
- 字：12 号（`TextSmall`），普通文字色；长文本自然换行，URL 和超过 32 字符的连续串按 `/ ? & = - _ .` 断行（`tipBreakRun`）。快捷键是 11 号 500 的次要色纯文本，修饰键直接相连（`⌘B`），命名键前留空格（`Ctrl S`），不拼进句子。
- Markdown 视图（`uikit.MarkdownTheme`）：正文 13／20，段间 `.75em`、列表项 `.375em`；H1 1.5 倍/700、上方 2.5em，H2 1.25 倍/600、上方 2em，H3 原大/600、上方 1.5em，标题下方都是 `.5em`。MyGo 的 Markdown 主题按 em 读这些值，窗口切主题时由调用方重新取当前主题的视图。
- 富卡片（`CardTip`）：Amp 的 320 宽、6 圆角、10／10／8 内边距，约 650 ms 才出现；标题 13／500、一行元信息 11（次要色）、一条分隔线和键值行（键 11 次要色、值 12，含 `://` 的值用等宽）。用在侧栏的集合请求行（名字、方法、集合、完整 URL）和抓包行（`GET /path`、状态、耗时、大小、URL、Host、Client）。它仍是 tooltip 语义，不是 popover。
- 时序：指针停留 120 ms 显示（`TooltipDelay`，Web Amp 实测）；提示消失后 warm 窗口内移到相邻目标立即显示；键盘焦点立即显示，Escape 关闭，上方不够翻到下方。
- 锚点：不要把 tip 包在 `Combobox` 或其他字段外面。tip 包住 `Combobox` 与放在整行上，同一面板里解码体的值列相差约 8 点（单独渲染：包住字段时值列起于 x 967，放在整行上时起于 975；像素差 1858，布局框不变）。这是 MyGo tooltip 包裹的副作用，没有追到 MyGo 里的具体一行。连续渲染整个画廊时方向相反，所以这一列的位置不是稳定的量。
- 截断：截断的标签要有完整文字的 tip。检查器头的时间行，它的卡片元信息里是完整来源行；`Matches` 链接的 tip 是完整标签加说明。静态文字不能获得键盘焦点，所以它的 tip 只在悬停时显示。

### 6.8 没有接入界面的组件，和留在 MyGo 上的控件

按 `// Bare:` 的约定，下面每一处在调用处都有一行注释说明原因。

**文档化但没有接入界面的组件：**

- `Radio`／`RadioGroup`。组件本身做完（有无障碍名、禁用不切换、焦点、Light 和 Dark 的图版），但界面里没有一处是“从两到四个互斥选项里选一个，且单选比现有的 `Select`／`Segmented` 更好”。按评审决定，它留作文档化的基础组件，不为接入而接入。

**留在 MyGo 上的裸控件（`internal/app`）：**

| 控件 | 位置 | 理由 |
| --- | --- | --- |
| `ui.NumberInput` ×7 | 请求设置的超时、重定向上限、文件夹超时；`Expiry`、`wait`、WebSocket 回复窗口 | 带 ± 步进（箭头键和按钮）；`NumberField` 不画步进，换成它会改变行为 |
| `ui.TextInputBase` ×2 | URL 栏（面由整行画，与方法和 Send 同一个面）；方法输入（宽度随文字） | 面和高度由所在的行决定，`InputControl` 的内边距与之不同；URL 栏的右边用 `uikit.TextInset` 留 `SpaceM`（8），与左边相同 |
| `ui.SelectBase` ×1 | 环境 chip | chip 带状态点，不是 28 高的字段，没有 kit 的触发器对应 |
| `ui.SearchField` ×1 | 消息搜索 | 要读输入框的 `Changed()` 来跟随数据流；`uikit.SearchField` 的行没有暴露它 |
| `ui.TextInput` ×1 | 命令面板的输入 | 48 高、16 字，专用的行高（`CommandPaletteInputRow`），不是 28 高的字段 |
| `ui.Autocomplete` ×1 | 集合名字段（保存为集合时） | 自由文本加建议；`uikit.Combobox` 只接受选项。换成 `Combobox` 会让 `TestSaveChosenRowsToCollection` 失败，所以保留 |
| `ui.Table` ×2 | 结构化字段表（检查器）、流量表 | kit 没有表格组件；行和单元格由调用方画 |
| `ui.ComboboxBase` ×1 | 请求列表的查询字段（`workbench.go`） | 字段自带 token 和补全弹层；`uikit.Combobox` 只做选项列表，不建模 token |
| `ui.SegmentedBase` ×1 | 协议 chip（`workbench.go`） | 每个 chip 有计数和短标签两种字；`uikit.Segmented` 只接受一段纯文字标签 |
| `ui.Link` ×1 | 「匹配的已保存请求」链接（`request_match.go`，放在 Tip 里） | 运行动作的文字链接，不是按钮；`uikit.LinkButton` 带按钮面和高度，不是文字链接 |

共 18 处，每处在调用处都有 `// Bare:` 注释。

**不是裸控件，但值得知道：** `Divider` 直接用 MyGo 的 `ui.Divider`（颜色即 `Border`）；`ui.Scroll`、`ui.Tooltip` 的底层由 uikit 的组件包住。

**已知的未完成项（见 `design-audit.md` 第 (f) 节）：** 设置行的标签没有用 `LabelledBy` 绑定到控件；原因写在那一节。

## 7. 交互和状态

- **Hover：** 按钮、工具栏按钮、tab 的背景变 Hover（第二层 tab 和分段控件只把文字变成正文色），立即变化，不做过渡。和 macOS 一样，列表、表格、侧栏的行没有 hover；不可点的也没有。
- **窗口失焦：** 窗口在别的窗口后面时，侧栏和挑选列表的选中浅底变成中性灰，表格的标记线变次要色。
- **按下：** 背景变 Pressed。
- **焦点环：** 键盘焦点显示 1 pt `Ring`、贴着控件边（`FocusRingWidth=1`、`FocusRingOffset=0`），鼠标点击不显示，所有可交互元素都要有。输入框静止就有 15% 墨边，聚焦时同样在边外加 1 pt `Ring`，不画外框和光晕；输入框的聚焦边和主操作不再同色。对话框关闭后焦点回到打开它的控件，`Modal` 会请求一帧重绘让焦点环显示出来。
- **禁用：** 文字用 TextFaint，不响应 hover；tooltip 说明为什么不可用。
- **动效：没有。** 悬停和按下的面立即变化，菜单、弹层、工具提示、toast 和对话框立即出现／消失，侧栏分区折叠和面板尺寸立即到位，计数、状态码和耗时直接换成新值。保留的只有两处，它们不是过渡：录制中且有流量时监听 pill 的状态点脉动（1.2s，55%–100%，画在 `Draw` 里，只重绘不重跑视图），以及用户自己拖动的分隔条和滚动（MyGo 的滚动惯性、`Split` 的拖动）。系统要求减少动态效果时 MyGo 仍然把滚动直接送到终态；应用没有过渡需要它结束。
- **拖拽：** 分隔线 hover 时变成强调色细线，指针变成调整大小；双击恢复默认。
- **右键：** 任何有动作的对象都支持右键，菜单内容见 UX 规范第 6 节。菜单的高亮项是正文色 7% wash、文字仍是正文色（Web Amp 的 `data-[highlighted]:bg-accent`）。
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
| `colors` 的 `editor.background`、`sideBar.background`、`list.hoverBackground`、`list.activeSelectionBackground`、`focusBorder`、`button.background`、`input.border` 等 | Canvas、Chrome、Fill、Selection、焦点、Accent、Hairline |
| `terminal.ansiRed`／`ansiYellow`／`ansiBlue`／`ansiGreen`／`ansiMagenta`／`ansiCyan` | 状态码、method、耗时分段 |
| `tokenColors`（TextMate scope） | body 编辑器的语法高亮 |

约束：

1. 主题只换颜色，不换字号、密度和形状。
2. 映射后按第 5 节的对比度标准检查；不达标的组合自动调整，并在 Settings → 外观里提示。
3. 内置主题（候选：One Dark Pro、Dracula、Tokyo Night、Catppuccin、GitHub Light/Dark、Solarized）逐个确认许可证，写进 `THIRD_PARTY_NOTICES.md`；同时支持导入 `.json` 主题文件。

## 9. 实施顺序和进度

每一步都能单独合并，附 Light／Dark 前后截图；涉及交互的另外录屏。

1. **uikit 和主题（完成）：** 建 `internal/uikit`（token、Light／Dark、字体、共用组件、对比度检查、组件总览截图），删掉 `internal/app/style.go`。不改布局。
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
     - 截图矩阵：LIVE 的状态（`capture-*`）同时渲染左右和上下两种布局（上下的文件名带 `-stacked`），× Light／Dark；单独的 `capture-stacked` 去掉。
   - 3.5 macOS 质感：
     - 3.5a uikit（完成；当时高度 20／24／28，现在是 `ControlHeight` 28／`ControlHeightSmall` 22 和 `HeightControl` 24）：tab 行和面板头 28，表格行 24；macOS 上界面用系统字体；`Tabs` 改成强调浅底圆角块，新增 `SubTabs`，检查器的 Request／Response 面板和调查板的预览用它；`Segmented` 改成无边框凹槽加浮起滑块；`SecondaryButton`、`DangerButton`、`CompactButton`、`Select` 去掉边框、用中性面；`Input` 中性填充加聚焦光晕；`RowButton` 和流量表的行去掉 hover（流量表的客户端图标随行高改成 14）；侧栏分组标题不再全大写。
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
     - `Palette` 换成 v3 的角色：`Window`／`Background`／`Input`／`Border` 三种表面加细线，`Muted`／`MutedHover`／`MutedPressed`（文字色的 7%／11%／15%，两个主题一样），`Shadow`；`Background`／`Panel`／`Hover`／`Pressed`／`Border`／`Bar` 删掉，屏幕改用新名字。
     - `Theme()` 不再把 MyGo 的 Surface 设成 Background：Background 是 canvas，Surface 是 chrome，所以表头和 MyGo 自己的栏是 chrome，输入框由 uikit 自己画（canvas 上是 well，chrome 上是 canvas 加细线）。
     - token：`TextHeadline`（18）删掉，`Mono` 改成 12；`HeightCompact`（20）和 `RadiusDialog`（14）删掉，新增 `HeightStatus`（24）和 `RadiusInner`（4）；`HeightBar` 44 → 40，`HeightTabs` 28 → 32。
     - 字重只有 400 和 500：uikit 的 `Heading`、`Label`、`DialogTitle`、`SettingsPage` 和屏幕里的 600 全部改成 500。
     - 组件：`Tabs` 的选中改成中性面（不再强调浅底）；`StatusPill` 不再填底；`Segmented` 的凹槽是中性面、滑块是 canvas 加细线；`QueryChip` 改成中性面；`Input`／`FieldFace` 是 well 加 1px 强调色内边和 3px 浅底光晕，新增 `ChromeFieldFace`；`PopoverPanel`、`Modal`、`SettingsGroup` 用 canvas 和 `Shadow`；`Selected`、`ListTheme` 失焦换成中性面，标记线换成 faint；`MoreMenu` 恢复成没有面的 `⋯`。
     - 屏幕只做和 token 直接相关的替换：侧栏和栏的背景改成 `Window`，耗时条的底改成 `Muted`，协议筛选的选中改成中性面，过滤栏 40、状态栏 24，红绿灯 Y 13。
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
     - 侧栏在 chrome 面上分成两个固定区域，中间一条细线，各自滚动：上区 Traffic＋Sources，下区 Collections（搜索框、集合树、树末尾的 Sessions）；页脚是 Client setup、HTTPS decryption、Diagnostics。上区最高占侧栏的 55%，下区吃掉剩下的高度，所以 20 个域名加一棵长树时两边都还在屏幕上、各自滚动。
     - 行：24 高，左右内缩 6，圆角 6，左边一个固定宽度的图标槽（16，MyGo 的 source list 条目按字号画图标），所以带图标、带圆点、带箭头的行文字都对齐在同一条线上；树在图标槽之前按层级缩进 12。分区标题 11 号、次要色、500，右端可以放开关或加号。
     - Sources：Apps｜Domains 是一个小号 `Segmented`（20 高，字 11），默认 Domains；只列前五项（按请求数），超过时末尾一行 Show all／Show fewer。主机名、App 名和所有标签用界面字体，方法标签仍是等宽（`MethodLabel`）。
     - 选中仍是强调浅底，窗口在后台时换成中性 `Muted`，形状不变。
     - 旧入口的新位置：Views（View 1、New view、保存的筛选按钮）作为 Traffic 里的行；Diagnostics 进页脚；Collections 的 `⋯` 在标题行加号左边；Sessions 变成集合树末尾可折叠的一行，里面的会话和 Open session… 缩进一级。
   - 7.4 标题栏和列表（完成，2026-11-11）：
     - 第一条请求上方只有四行 chrome：标题栏 40、过滤栏 32（`HeightTabs`，控件 24）、活动行 24、表头 24。原来的「已启用工具栏」28 高一行去掉，工具 chip 进标题栏。
     - 标题栏从左到右：品牌（非 macOS）、录制 pill、Host 方案 chip、已启用工具 chip（`Muted` 面、无边框，带 ×；放不下时横向滚动，右侧渐隐）、命令栏（canvas 底加 1px 细线，左边放大镜、右边 `⌘K`/`Ctrl K`，点它打开命令面板）、布局切换、Tools、Settings、`⋯`。原来的命令按钮就是这条命令栏。
     - 活动行 24 高：摘要（`Last 5 min · 2 requests`）在左，堆叠柱在右（14 高），最右是结束时间（11 号等宽）。原来那行三处时间刻度去掉（设计稿没有）。柱色：正常是中性面，3xx 蓝、4xx 赭黄、5xx／连接失败和慢请求（≥ 1 秒）朱红；摘要那一段固定 240 宽，拖选时柱不会在指针下移动。
     - 流量表：Host、Client、Method 和备注用界面字体（方法仍是 12、500、方法色），等宽只留给状态码、路径、耗时、大小、时间和数字；行高 24；选中行失焦时保留形状、换成中性 `Muted`，和侧栏一致（`ListTheme`）。
     - 状态栏 24（`HeightStatus`），各页面同一条 `footer`。
     - 集合树去掉文件夹图标：展开箭头就是行首的图标槽，名字紧跟其后，每层缩进 8；请求行没有空槽，method 直接从缩进后开始，名字因此从 x≈74 起（原来 x≈112）。Sessions 行同样只有箭头和名字，「Open session…」保留时钟图标。树改用 MyGo 的 `TreeBase`（无外观）自己画行，保留键盘导航、右键菜单和展开状态。
   - 7.5 细节打磨（完成，2026-11-11）：布局和结构不变，只减少视觉噪音、统一控件、补交互反馈。依据是 Linear 的"导航不该抢内容的注意力、结构要被感觉到而不是被看到"和 Apple HIG 的侧栏约定（工具栏项目精挑、侧栏可隐藏、关键入口不放在侧栏底部）。
     - 分隔线：Capture 页只留区域之间的线（侧栏与内容、列表与检查器、请求面板与响应面板、状态栏上沿）。抓包信息行、URL 行、请求编辑器面包屑行、只读提示行、两个面板头、调查板的面板条、连接详情的面板条都去掉了底线；表格表头没有列分隔线，拖动列宽的命中区保留，指针停在列边缘时才画强调色细线（表头自己的下沿改为逐格画，替代表格主题的 Border）。
     - 输入框只剩两种：chrome 上是 `ChromeFieldFace`（canvas 加细线），canvas 上是 `FieldFace`（well）。命令入口改成 `ChipButton` 后，同一屏不再有第三种。
     - 命令栏改成 24 高的按钮：放大镜加 `⌘K`／`Ctrl K`，点击和快捷键都打开命令面板，省出的宽度留给工具 chip。uikit 新增 `ChipButton`。
     - 侧栏退后：未选中行的文字用 `MutedForeground`、图标用 `Faint`，悬停和选中回到 `Foreground`；悬停色差一帧（图标色在构建时取，MyGo 没有逐属性的过渡），用 `Local` 记住上一帧并请求补一帧。计数为 0 不显示。对比度：`MutedForeground` 在 canvas／chrome／well 上是 5.94／5.37／5.09（浅）和 6.76／6.13／7.20（深），都过 4.5:1，所以没有改调色板；`Faint` 只用于图标（装饰，随行的文字标签），在 chrome 上是 2.39（浅）／2.94（深）。
     - 流量表去装饰：去掉耗时列的底色块和状态列的圆点（`statusIcon` 列一起删掉），正常行只有文字；慢请求（≥ 1 秒）的耗时和 4xx／5xx 的状态用信号色，3xx 按次要文字读。
     - 集合树的方法槽按当前可见的最长方法定宽、右对齐：名字对齐在同一条线上，`GET` 和名字之间只剩行的 8px 间距（原来有 24）。
     - 侧栏分区可折叠：Traffic、Sources、Collections 的标题点击收起或展开，悬停时显示箭头，折叠状态存进 settings 的 `sidebarClosed`，下次启动保留。reveal（`⌘1`／`⌘2`、命令面板打开集合、打开会话）会展开对应的分区。
     - 侧栏可隐藏：标题栏新增按钮（`PanelLeftOpen`／`PanelLeftClose` 图标，提示带 `⌘B`），`⌘B` 和命令面板里的 Toggle sidebar 仍在；窗口窄于 `sidebarMinWidth`（860）时自动让开、变宽后回到用户原来的选择，太窄时按钮禁用并说明原因。
     - 空流量表的引导：没有抓到请求时，空状态给两个动作——启动代理（监听没在跑时）和 Client setup；代理在跑时是 Client setup 和 Diagnose capture…。侧栏页脚的入口保留。`startCapture` 会一直阻塞到监听关闭，所以按钮在 goroutine 里调用它。
     - 交互反馈：uikit 不定义过渡时长（第 6.3 节的动效）；MyGo 的控件面按构建状态画，悬停和按下立即变色。行内操作（视图行的 ×、树行的 `⋯`）只在悬停或键盘聚焦时出现，用透明度隐藏、不挤动行内其他内容。有快捷键的图标按钮在提示里带快捷键：命令（`⌘K`）、侧栏（`⌘B`）、Settings（`⌘,`）、Host 方案（`⌘⇧H`）、关闭请求（`⌘W`）、搜索框（`⌘F`）。
8. **第十三阶段：审计的前十条（进行中，2026-11-13）。** 依据是 UI/UX 审计（宏观与微观），目标是去掉「AI slop」：解释性文字、同一功能的多个入口、不分主次的控件堆砌，方向是删而不是加。
   - 选中行可见（8.1 之后）：MyGo 的表格只按交给它的选择集合画选中行，不看键盘指向的那一行；应用自己移动选中行（第一条流量、导入、链接、筛选后剩下的邻居）时没有同步选择集合，检查器显示的那一行看起来和别的一样。现在应用移动选中行时把它放进选择集合，等于点了它一下；底栏只在一行以上时才报「N of M selected」，一行是正在读的那一行，不是批量选择。曾试过的「画的时候把 picked 塞进 chosen」会清掉多选，已回退。
   - 键盘：LIVE 打开时流量列表拿键盘（窗口自己的列原先拿着焦点，方向键、Home、End、Enter 都不动），Tab 仍然能离开列表去侧栏和标题栏；搜索框里 Esc 清掉输入的文字，`⌘D` 复制正在编辑的请求（⋯ 菜单的 Duplicate）。⌘⌫ 删除单条抓包没有做：UX 规范第 7 节要求直接执行并给撤销、同时进最近删除，那是功能而不是打磨。Space 预览也没有单独做：检查器本来就一直显示选中的那一行，Space 只是 Enter 的重复。
   - 筛选只有一个入口：漏斗从过滤栏、底栏和侧栏 New view 旁收进搜索框右端；搜索框前的「Text」模式下拉删掉，三种模式改成写在文字里的语法——普通文字是包含，`/…/` 是正则，带 `*`／`?` 是通配，都作用于整个 host＋path，正则永远不会被当成 `key:value` chip。视图和保存的筛选里存的就是当时输入的查询。
   - 计数只说一次：侧栏保留计数；活动条只剩柱子和结束时间（清除时间范围的按钮留一个固定宽度的槽，拖选时柱子不在指针下移动）；底栏只在隐藏了行时说「8 of 13」。
   - 隧道（CONNECT）详情：一行「Encrypted, not decrypted」加一个「Decrypt <host>」；失败的握手保留记录下来的错误；域规则、App 规则和信任状态收进 Connection 标签页，给整个 App 放宽权限的动作进面板头的 `⋯`。面板头不再拼出「Unidentified client」，有客户端时是它的图标和名字；Explain 和 Focus 两个文字按钮进 `⋯`，不再被窄检查器切成半个词。表格里隧道的 Status 是锁图标、Path 留空、CONNECT 用次要色。
   - 底栏：Clear 和 Auto-follow 静止时是文字、没有面，悬停才出面，开启时才用强调浅底；Hosts chip 在没有方案开启时不显示，开启时显示方案名。
   - 文案：空列表不再解释条件怎么组合，只说要隐藏多少行和两个出口；重放行不再解释链接存在哪；抓包停止时不再提示去检查监听。
9. **第十四阶段：审计的 4、5、6、10–12、13–16 条（完成，2026-11-13）。**
   - 侧栏容器换成自己的列表（第 81 条）：集合树独立滚动，Apps 行能画 `.app` 图标，Traffic 区里多了 Sessions 分区，键盘和读屏行为与 MyGo 的 source list 一致。
   - 表格：默认按开始顺序（`#`）排，慢请求用琥珀色；行下方的空白区仍是空白（第 13 节）。
   - 标题栏：右边四个控件，侧栏开关在红绿灯右边，Settings 进 ⋯。
   - 证书横幅：未信任时过滤栏下方一条可关闭的提示，隧道面板不再重复。
   - 活动条：轴固定五分钟，柱子从右往左走。
   - 侧栏内容：Sessions 进 Traffic 区、页脚去掉 `›`、空集合给一行「+ New request」、Domains 用首字母方块、视图按条件命名并能双击改名。
10. **第十五阶段：审计的 17–19、22–23、25–31 条（部分完成，2026-11-13）。**
   - 已做：编辑器的两个空状态只留图标和发送键；历史列的两个下拉只在有记录时出现；面板头去掉重复的「Request」「• Response」，状态移到行尾；系统代理用一个开关；路径改用界面字体。
   - 没做：22（设置窗口的行式表单）、25（右键菜单统一）、26（双击打开编辑器，见第 13 节的证据）、27（拖拽保存）、28（悬停操作通查）、29（图标线宽）、31（字号层级）。

## 10. 新界面

功能路线图带来的新界面，都按本文和 UX 规范设计。每个界面在对应里程碑开始前先出设计稿，确认后再实现。

| 界面 | 要点 | 里程碑 |
| --- | --- | --- |
| 首次启动引导 | 嵌在流量列表空状态里的 4 步引导（UX 规范第 9 节）；每步一个主按钮，完成的步骤打勾，可跳过 | M1 |
| 录制 pill 和暂停 | 三种状态（录制中、已暂停、代理停止），暂停按钮在 pill 内 | M1 |
| Settings 窗口 | 独立窗口，左侧分类（通用、外观、代理、HTTPS decryption、抓包、设备），右侧 FormRow；改动立即生效，需要重启的项目标明 | M1 |
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
# 组件总览：Light／Dark 各一张总览，加按钮的 hover／pressed
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
2. 强调色是墨：Light `#1a1a18`、Dark `#e9e8e4`（v1 的靛蓝、v3 前五阶段的油墨绿都已取代）。
3. 外观默认跟随系统，可选浅色或深色（3.5b）。
4. 行高 24（`HeightControl`），控件 28／22（`ControlHeight`／`ControlHeightSmall`）；URL 栏和 Send 与其它控件同为 28（3.5；原来是 28／28／32）。
5. 一个工作区（取代 Capture／Requests 两个工作区）；不做全局图标栏；规则、Host 方案、脚本、Mock 集中到 Tools 窗口。
6. 网络相关的设置对话框并进 Settings。
7. 组件放在独立的 `internal/uikit`，只依赖 MyGo；界面只用 uikit 的组件和 token。
8. 默认主题是 Light／Dark，平涂、不做纸纹。
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
27. 控件不描边，面是文字色的 7%／11%／15%（两个主题一样，Amp 配色时定下）；选中失焦时保留形状、换成中性面（2026-11-11）。
28. 尺寸只有三档栏高（40／32／24）、两档控件（`ControlHeight` 28／`ControlHeightSmall` 22）和 24 高的行（`HeightControl`）、五档字号（`TextDisplay` 20／`TextTitle` 16／`TextBody` 13／`TextSmall` 12／`TextMeta` 11）、两个字重（400／500）、六档圆角（4／6／8／10／12／16）。`HeightCompact`、`TextHeadline` 删掉（2026-11-11；字号和圆角在第十六阶段扩展）。
29. 等宽只用于线上数据：路径、header 值、URL、数字、body；host、client 和标签用界面字体（2026-11-11）。
30. Light 的 warning 从设计稿的 `#a15c07` 深到 `#9c5906`：前者在 well 上只有 4.44:1，其余信号色和设计稿一致；设计稿的信号色在按下态是 3.6–4.5:1，测试对叠在表面上的面按 UI 组件的 3:1 计（2026-11-11）。
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
45. 侧栏退后：未选中行文字次要色、图标 faint，悬停和选中回到正文色，计数为 0 不显示。`MutedForeground` 在三种表面上都过 4.5:1（浅 5.94／5.37／5.09，深 6.76／6.13／7.20），所以调色板没动；图标是装饰，浅色 chrome 上 2.39（7.5，2026-11-11）。
46. 流量表去装饰：耗时没有底色块，状态没有圆点（`statusIcon` 列删掉），慢请求（≥ 1 秒）的耗时和 4xx／5xx 的状态才带信号色，3xx 按次要文字读（7.5，2026-11-11）。
47. 集合树的方法槽按当前可见的最长方法定宽并右对齐：名字对齐，`GET` 和名字之间只剩行的 8px（7.5，2026-11-11）。
48. 侧栏的 Traffic／Sources／Collections 可以折叠，状态存进 settings；侧栏可以隐藏（标题栏按钮、`⌘B`、命令面板），窗口窄于 860 时自动让开、变宽后回到原来的选择（7.5，2026-11-11）。
49. 空流量表的主要操作是启动代理和 Client setup；Diagnostics 留在侧栏页脚（7.5，2026-11-11）。
50. `DurationHover`（120ms）是全 kit 唯一的过渡时长；MyGo 的控件面按构建状态画，悬停和按下立即变色，没有逐属性的过渡（7.5，2026-11-11）。
51. 强调色改成墨：Light `#1a1a18`、Dark `#e9e8e4`，悬停 `#33332f`／`#ffffff`，按下 `#474743`／`#cfcdc8`；中性色去掉绿调、带一点暖（Chrome `#f4f3f0`／`#1f1f1d`，Canvas `#ffffff`／`#171716`，Well `#ecebe7`／`#101010`，Hairline `#e3e1dc`／`#2e2e2b`）。应用图标是黑猫，强调色跟着走（第六阶段，2026-11-12）。
52. `Success` 从强调色里拆出来，是独立的绿（Light `#26734a`、Dark `#7cc492`）：绿色只表示"通过"和"正在录制"。原来借 `Success` 当色相的耗时分段（Download／Receive）和消息方向改用 `Socket` 的青色；"Applied actions"这类说明文字改成次要色。测试把 `Success` 按信号色断言，并加了一条"Success 不等于 Accent"的回归（第六阶段，2026-11-12）。
53. 选中浅底 `Accent` 比设计稿给的值再深一档（Light `#d8d4cb`、Dark `#45443f`）：强调色变墨以后，`#e0ded8`／`#3b3a37` 和悬停面只差 1.10／1.16 倍亮度，一眼分不出"选中"和"悬停"。聚焦时是浅底加 2px 墨色标记线，失焦时是中性 `Muted` 加 faint 标记线，两种状态都保留形状（第六阶段，2026-11-12）。
54. 文字选区的透明度从 30% 降到 24%：墨色 30% 在浅色下是一整块很重的灰。24% 时选中文字在 Light 上 11.8:1、Dark 上 7.0:1，都还清楚（第六阶段，2026-11-12）。
55. 面板头的取舍按实测宽度而不是固定阈值：面板用 `ui.Handle` 记住上一帧的宽，标题、标签页、`⋯` 的量好的宽度扣掉，剩下的分给耗时／大小和面板自己的控件；标签页和状态不参与取舍。原来是「面板窄于 430 就把耗时藏起来」的阈值，遇到编辑器那种还带 Pretty、Decode 和动作按钮的面板就不准了，1280 宽下 Cookies 和 Timing 会被切掉（第七阶段，2026-11-12）。
56. 编辑器里的语法着色不再用绿色或青色：键名用 `Foreground`，字符串用 `MethodPatch` 的梅紫，数字和布尔值用 `Info` 蓝；库默认主题画成绿色的正则、标签、插入文本也换成这三种（正则随字符串、标签和插入文本用 `Info`）。Dark 的 `Socket` 从 `#7cc4c0` 挪到 `#6fc3d6`，12 号字下和 `Success` 的绿分得开（第七阶段，2026-11-12）。
57. 请求头的 Key／Value 两列都给最小宽度（108／120）：常见 header 名和 `application/json` 这样的值读得完整，超过 20 个字符的值在悬停时给全文。列表在窄于 860 时先去掉 Remote IP 列、窄于 740 时再去掉 Client 列，耗时列不再被列表的边切掉（第七阶段，2026-11-12）。
58. 过滤栏的搜索框放不下整句占位文字时只显示「Search host or path」，不再截半句（第七阶段，2026-11-12）。
61. 试验：卡片外壳 + Amp 原色（2026-11-13）。外壳一种底色，侧栏和内容各是一张卡片，卡片之间和四周只隔 2px；卡片不描边、不加阴影，圆角 8，只圆朝外的角。**后改为只有内容是卡片**：侧栏直接画外壳色，和标题栏、底栏连成一块底，左上角没有分界（`Palette.Sidebar` 已删）；内容卡片仍只圆朝外的角，侧栏收起时四角都圆。配色取 Amp 默认主题（Daydream）的变量，半透明色按叠在卡片上的效果换成实色：外壳 `#edf3ea`／`#070807`、侧栏卡片 `#f3f8f0`／`#0c1313`、内容卡片 `#f9fdf6`／`#0c1516`、`Input` `#ebebeb`／`#192121`、`Border` `#ebefe8`／`#1b2324`、`Foreground` `#0b0d0b`／`#f6fff5`、`MutedForeground` `#595959`／`#9ca49c`、`Primary` `#141614`／`#eaf3e9`、`Accent` `#d6dcd6`／`#2f372f`、`Success` `#2c784d`／`#54b879`、`Warning` `#86600f`／`#d6a038`、`Danger` `#b9423c`／`#d67670`。对比度契约不变；Amp 的 Light Danger `#c44f4a` 和 Success `#2f8052` 在 canvas、外壳和 well 上都不到 4.5，按原色相和饱和度压明度到刚好过线（`#b9423c`、`#2c784d`），Dark 保持原值。`Faint`、`PrimaryHover`／`PrimaryPressed`、`PrimaryForeground`、`Muted` 的百分比按同一套明度关系推出来。
59. 外壳只有一块底色：标题栏、侧栏、底栏都是 `Window`，之间不画线；内容是一张抬起的 `Background` 面板（`RadiusPanel`、1px `Border`、`Shadow(0,1,3)`），右边 8，侧栏收起时左边也 8（第八阶段，2026-11-12）。
60. Dark 的 `Window` 和 `Background` 对调：外壳 `#171716`、面板 `#1f1f1d`，`Input` 仍是 `#101010`。对调前面板比外壳暗，看起来是凹下去的；Light 不动。对调后对比度断言全过，最低仍是 Light 的 Info 在 well 上 4.52:1（第八阶段，2026-11-12）。
61. 标题栏：左边是开始／暂停录制的按钮和品牌，中间是监听 pill（`Success` 圆点 + `Listening on` + 等宽的地址，未运行时是停止状态、没有绿点），右边是 `⌘K`、侧栏开关、布局切换、Tools、Settings、`⋯`。Host 方案和已启用的工具 chip 从标题栏移到底栏；pill 不再画请求速率曲线（第八阶段，2026-11-12）。
62. 底栏 28 高（`HeightFooter`），取代 24 的状态栏：左边 Clear／Filter／Auto-follow（Auto-follow 跟随最新时是选中态，有新请求时按钮上写「N new」），中间计数（有选中时是「1 of 6 selected」），右边是已启用的工具和 Host 方案的 chip。chip 按量出来的宽度排，放不下的折成 `+N`，点开是同一批 chip 的列表，不横向滚动、不用渐隐。toast 抬高一个底栏的高度，不盖住它（第八阶段，2026-11-12）。
63. MyGo 只有一个 `Surface`：它画表格头、拖影和 source list。第八阶段起 `Surface` 跟面板走（canvas），外壳的 chrome 由应用自己画；侧栏里临时把 `Surface` 换成 chrome，免得 source list 在框上挖出一块 canvas（第八阶段，2026-11-12）。
64. 输入框聚焦时那 1px 边从 `Primary` 换成 `MutedForeground`：强调色变墨以后，黑边和旁边的 Send 主按钮抢视线。3px 的 `Accent` 光晕保留，聚焦和未聚焦仍然一眼分得开；`MutedForeground` 在 well／canvas／chrome 上分别是 5.51／6.58／5.93（Light），都过 3:1。按钮和列表行的键盘焦点框不动（第八阶段，2026-11-12）。
65. 编辑器里标点（`TokenOperator`）从库默认的红改成 `MutedForeground`：黑白界面里红标点像报错，红色只留给真正的错误（第八阶段，2026-11-12）。
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

76. 搜索框是筛选取值的唯一入口，它自己没有模式：普通文字包含、`/…/` 正则、`*`／`?` 通配，都作用于整个 host＋path。原来的「Text／Wildcard／Regex」下拉删掉，因为同一个功能有两个入口（下拉和语法）；漏斗从过滤栏、底栏和侧栏收进搜索框右端，底栏只留 Clear 和 Auto-follow（第十三阶段，2026-11-13）。

77. uikit A/B（2026-11-16）：工具提示换成浮层表面（canvas、hairline、`ShadowPopover`、`RadiusPanel`、6×10、12 号正文、360 宽），快捷键用 `Kbd`，新增 `CardTip` 富卡片（侧栏请求行和抓包行）；延迟设成 500ms。删掉所有装饰性过渡和动效 token（悬停淡出、浮层和 toast 进出、对话框淡入、分区折叠、面板尺寸、数字滚动），只留录制点的脉动和用户自己的拖动／滚动。侧栏改成 PaneView：Traffic 固定，中间 Sources／Sessions／Collections 可折叠、最后一个展开的段占满剩余、分隔条可拖、双击恢复，折叠状态和段高记在 settings（`SidebarPaneHeights`，可选字段；开发阶段不升格式版本）。Client setup 对话框高度跟内容、动作按钮不再整行宽；HTTPS decryption 的「Domains and subdomains」进标题的 Tip。菜单关闭后补一帧重绘，让焦点环在 Escape 的那一帧出现。
77. MyGo 的表格只按 `ListState.Selection` 画选中行，`Selected` 只是键移动的起点。所以应用自己移动选中行时必须同时把它放进选择集合（`choosePicked`）；「画的时候强行塞进去」会清掉多选，不是解法（第十三阶段，2026-11-13）。
78. 计数只说一次：侧栏保留计数，活动条只有柱子和结束时间，底栏只在隐藏了行时说「8 of 13」，批量选择时仍报「N of M selected」（第十三阶段，2026-11-13）。
79. 隧道详情只留一行状态和一个「Decrypt <host>」，其余（域规则、App 规则、信任）进 Connection 标签页；给整个 App 放宽权限的动作进面板头的 `⋯`。表格里隧道行用锁图标表示 Status、Path 留空、CONNECT 用次要色（第十三阶段，2026-11-13）。
80. 底栏按钮静止时是文字、没有面，悬停才出面；Hosts chip 在没有方案开启时不显示（第十三阶段，2026-11-13）。

81. 侧栏容器自己实现（`uikit.SidebarList`／`SidebarItem`／`SidebarItemImage`／`SidebarItemLetter`）：MyGo 的 `ui.Sidebar` 自己拥有滚动，所以里面的集合树不能单独滚动；它的行只收 `*ui.SVG`，Apps 行放不进 `mygo.Shell.FileIcon` 的位图。新列表只做列表：焦点在它上面，↑↓／Home／End／首字母跳转，`RoleTree` + ActiveDescendant，悬停换字色用 `Local` + 重绘一帧，选择仍是强调浅底、后台窗口时中性。滚动交给外面的 `Scroll`（Traffic 区）或里面的树（Collections），所以集合树现在能独立滚动（第十四阶段，2026-11-13）。
82. 表格默认按 `#`（请求开始顺序）排序，`#` 列带排序箭头：请求在开始时编号、结束时到达，按到达顺序排就会出现 6 在 5 前面。慢请求的耗时从朱红改琥珀：慢不是错误，红色只留给失败（第十四阶段，2026-11-13）。
83. 标题栏右边最多四个控件（命令栏、布局切换、Tools、`⋯`）；侧栏开关移到标题栏开头、红绿灯右边；Settings 图标删掉，⋯ 菜单新增「Open settings…」，应用菜单的 ⌘, 和命令面板里本来就有（第十四阶段，2026-11-13）。
84. 证书未信任时，抓包页在过滤栏下方显示一条可关闭的琥珀横幅「HTTPS content is not decrypted · Install certificate」；隧道面板不再重复安装证书的按钮。信任检查在启动时跑一次，横幅才有话可说（第十四阶段，2026-11-13）。
85. 活动条的轴固定为最近五分钟：原来按流量裁短轴，每来一条请求所有柱子都会挪位，刷选时指针下的时间也会变（第十四阶段，2026-11-13）。
86. Sessions 是流量的历史而不是集合，从集合树移到 Traffic 区的第三个分区（Sources 之下），最后一行是「Open session…」；侧栏页脚的三个入口都是打开窗口或页面，去掉 `›`（第十四阶段，2026-11-13）。
87. 空集合时集合树只给一行「+ New request」，表头的加号在这一行存在时让开，同一个动作只有一个入口（第十四阶段，2026-11-13）。
88. 视图按它筛的东西命名：`api.example.com · 4xx`，条件清空后才是「View N」；双击视图行弹「Rename view」，用户起的名字优先于条件名，清空名字回到条件名（第十四阶段，2026-11-13）。
89. 面板头不再重复标签页说的话：请求面板没有「Request」、响应面板没有「• Response」，响应状态（200 OK 和它的灯）在行尾、和数字在一起。原来点标题会把共享读取器交给那一侧，现在改成在面板里选标签页（第十五阶段，2026-11-13）。
90. 两个空状态不再解释自己：历史列和响应面板只留一个淡色图标加发送键（`⌘↩ Send`）。历史列的两个下拉只在有记录时出现，设了筛选时保留，免得筛选把列表清空后没法撤销（第十五阶段，2026-11-13）。
91. 系统代理用一个开关（`Use this app’s proxy`）代替 Enable／Disable 两个按钮：开关显示 OS 的状态，翻过来就是改变；「Check again」留着，重读机器和改变它是两件事（第十五阶段，2026-11-13）。
92. 等宽只给要对齐比较的数字和代码：路径改成界面字体，状态码、耗时、大小、`#` 仍是等宽（第十五阶段，2026-11-13）。
93. 宽松档接进 app（第十六阶段，2026-11-15）：命令面板改成 720 宽的无标题浮层，输入 48/16、结果 40/14，选中行内缩 4 加 `RadiusControl`，快捷键和底部提示用 `Kbd`；`actionMenu` 用 `Content` 把每个动作的快捷键画成 `Kbd`，`PopoverMenuButton` 统一走 `MenuPanel`。七个 Settings 页改成 620 宽的 `SettingsCardPage`，导航 188，行最小 44，多行内容和证书编辑器用 `SettingsCardBlock`。`ContextMenu` 和 `PopoverMenu` 的面板仍由 MyGo 内部画（没有 Panel 入口），但其中的动作快捷键已经用 `Kbd`；要连面板一起统一，得替换 MyGo 的菜单或等上游开 API。MyGo 画的行高固定为 24，`MenuPanel` 只能换面板、圆角和投影；宽松档的菜单行 28 是替换菜单后的目标，当前不硬改。
94. 侧栏按 PaneView 分层（第十七阶段，2026-11-16）：Traffic 和页脚是固定 chrome，Sources／Sessions／Collections 是中间的可折叠区；折叠后最后一个展开区占满剩余高度，展开区之间有可拖的分隔条，双击复位，折叠状态和高度仍存 `SidebarPaneHeights`。窗口太矮时三个区先压到最小（标题进入紧凑档，去掉 8px 上边距），仍不够才让整个中间滚动，Traffic 的每一行和页脚永远完整。Collections 至少保留标题、搜索和几行。
95. 抓包表的单元格都挂同一张 `CardTip`（方法、状态、Host、Path）：MyGo 0.9 的 `Table` 没有行级 tip 入口，所以把整行卡片挂到每列的内容上；卡片给完整 URL、Host、Client 和错误，不再用只重复单元格文字的短 tip。
96. Certificates 的 Save 移到 Settings 页的 sticky footer（第十七阶段，2026-11-16）：滚动的是卡片列表，Save／Discard 和密封说明固定在窗口底部；560 高的 Settings 窗口首屏就能看到主操作。
97. Protobuf 读取器按视口高度布局（第十七阶段，2026-11-16）：reader 的高度取上一帧的实际视口，split 和 Copy decoded 图标留在 pane 内；请求面板不在 Body 标签时 split 给响应 75%，不再让解码内容跑到窗口边缘。
98. SSE 会话对话框重排（第十七阶段，2026-11-16）：标题固定为 `SSE session`，状态用 `StatusPill`；连接中显示 `Reconnect`／`Stop SSE`，不显示禁用的 `Connect SSE`；`Copy event ID` 是 meta 行尾的图标，技术脚注收进信息图标的 Tip。

99. uikit AD（2026-11-16）：MCP catalog 去掉自己的 Tools/Resources/Prompts 分段，目录跟随请求面板的 tab，只留一套导航；集合向导 Endpoints 预览显示将生成的请求和捕获响应；Postman 导入报告的主操作改成 Review collection，Done 次要；SSE 连接中 Stop 是主操作、Reconnect 次要，事件列表和 payload 放进填满高度的 Panel；Protobuf 在窄 inspector 改成上下布局，宽时仍左右；Certificates 滚动区底部留出 footer 高度。设置格式回到未版本化，开发阶段直接改格式（见 AGENTS.md）。

100. uikit AE（2026-11-16）：调查板先给慢请求列表，方法学收成一行、全文进 Tip；首次启动引导打开时占据整个内容区，不再露出抓包工具栏和列表外框，第一步的 `Start capture` 是主操作，`Skip guide` 收进卡片底部；Scenarios 的名称改成带可见标签的 `FormRow`，状态文案改成 `On: its rules apply. Rules with no scenario also apply.`，规则顺序说明移到标题下；SSE 和 WebSocket 的事件列表改用 `DataList`，文字左对齐，只有选中行有底色和 `RowMarker`。重放结果通知的测试改为等待提示真正出现，不再假定一帧就够。

101. Daydream 收口（2026-11-16）：按 frontend-design skill 复查配色，保留 Amp Daydream（试过一套冷钢灰的自有配色，DA DA 决定保留 Daydream），只修它在本应用里的三处弱点：Light 的 `Input` 从纯灰 `#ebebeb` 改成同绿调的 `#e9eee5`，输入框和表面是一种材料；外壳 `Window` 从 `#f2f6ee` 改回 Daydream 自己的外壳色 `#edf3ea`，`Border` 从 `#ebefe8` 深到 `#dde4d9`，内容面板的边缘不再几乎看不见；Dark 的外壳从中性黑 `#070807` 改成带面板青调的 `#050a0b`，外壳和面板不再是两种色相。对比度契约不变，`theme_test` 全部通过。录制 pill 是外壳里唯一花颜色的地方：正在录制时面是 `Success` 14%，停止和暂停仍是中性面，只靠圆点说明原因。

102. 编辑器 token 改用 `@pierre/diffs` 的色板（2026-11-18）：先按 Amp Web 的 `--syntax-*` 样式表改过一次，随后在浏览器里展开 Changes 文件 diff，发现它由 `<diffs-container>` 的 shadow DOM 渲染，token 颜色是行内的 `--diffs-token-light/dark`，来自 GitHub Light/Dark 的主题色，和聊天 Markdown 的 `--syntax-*` 是两套。实际的未改动行（`data-line-type=context`）没有 diff 底色，代码按这套 token 色画；增删行才由 Amp 的 `--diff-*` 覆盖成增删底色。旧的 `Syntax*` Palette 字段换成 `Editor*`，按 GitHub 的角色重新映射 MyGo token kinds，并断言具体色值和「只有颜色、没有字形」。MyGo 的 diff 行底仍从 `Added`／`Deleted` 推导，不能逐像素使用 Amp 的显式背景与强调色。

103. 选中行不再叠两层底（2026-11-18）：`Theme.Accent` 改成实色主按钮墨以后，MyGo 的列表给选中行刷的实色透出来，`uikit.Selected` 的 7% wash 又画在它上面，正文色落在实色底上，Light／Dark 对比度只有约 1.1:1。Scripts、Rules、Scenarios 和 Git 状态列表共用这条路径。现在 `uikit.Selected` 先把 wash 和表面合成成实地再画，完整盖住列表自己的选中底；加了用 Scripts 画廊量选中行底色的回归测试，旧画法在两套主题下都失败。

104. JSON 树的 value 也用编辑器 token 色（2026-11-18）：Body 的 Auto／Pretty 走 MyGo 的 JSON lexer，已经是完整的 GitHub token 配色；显式 JSON 模式是可折叠、可查询的自定义树，原来只有 key 借了 `TokenProperty` 的颜色，value 一律正文色。现在字符串用 `TokenString`，数字、布尔和 null 用 `TokenConstant`，与 Pretty 对同一段 JSON 的上色一致；回归测试在 Light／Dark 下量文本像素，旧画法全部失败。

## 13. 仍与设计稿不一致的地方

v3 六个阶段落地后，和设计稿仍有这些差别，都是有意保留的：

- **悬停只重绘，做不到（2026-11-13）。** 悬停底色要在 `Draw` 里读指针才能只重绘；但 `p.AnimationFrame()` 请求的是只重绘不重建的帧，这种帧里 `Element.Hovered()` 用的还是上一轮构建的元素，MyGo 判它过期并 panic：`ui: element from pass 143 used in pass 146; keep a ui.Handle instead`。`ui.Handle` 只有 `Focused`／`Bounds`，没有 `Hovered`，所以换 handle 也读不到。23 处悬停仍在构建期读，当时用 `hoverMotion` 保留 120 ms 淡出；这一批把淡出去掉了，悬停面立即变化，不再有构建期的颜色过渡。
- **表格行不能拖进集合（2026-11-14）。** MyGo 0.9.0 有 `DragRows`／`Drop`，tester 里的合成拖动也能通过，但在真机上按住表格行拖动时，表格从不开始拖动会话（没有幽灵行，集合行也没有 `DragOver`），所以松手不会保存。拖放已整条回退，等 MyGo 提供可在真机工作的拖动源，或改用自绘的拖动层。
- **Darwin 上第三方进程读不到 TCP 控制块（2026-11-14）。** `/usr/sbin/netstat` 带有 `com.apple.private.network.statistics` 权限，从终端直接运行时能看到每个连接的进程和 pid；但由 Go／C 子进程（甚至 `/bin/sh -c` 子进程）启动时输出 0 字节，`netstat -anv -p tcp` 的 pid 列不会给第三方 responsible process。`sysctl -b net.inet.tcp.pcblist_n` 在终端返回 170064 字节，而 Go 的 `unix.SysctlRaw` 和 C 的 `sysctlbyname` 都只拿到 48 字节的 `xinpgen` 头。M6 的 PCB 读取因此删掉，客户端归属仍走 lsof 批量查询；只有 `lsof` 能在本机实际拿到 socket 的 pid。
- **侧栏 Apps 的行只能画字形图标，画不了应用自己的图标（2026-11-13）。** `ui.SidebarItem` 的图标槽收 `*ui.SVG`，而我们自己 `.Children(...)` 加的内容会排在文字之后；`mygo.Shell.FileIcon` 能取到 `.app`／exe 的位图，但没有地方把它放进行首。流量表的 Client 列不受影响（那里是我们自己排的行）。

- **表格行下方的一大片空白还在（2026-11-13）。** 审计建议画斑马空行延续网格，或让最后一行之后的点击取消选中。前者要 MyGo 的 `Table` 给出滚动偏移或一个空白区的绘制钩子，它两个都没有，画在表格矩形里的横线滚动后会穿过行；后者和「永远有一行被选中、检查器跟着它」冲突（第 77 条）。
- **表格行双击打不开编辑器（2026-11-13）。** MyGo 0.9.0 的 `Table` 把回车和双击都报成 `Submitted()`，而 `Element.RowDoubleClicked()` 在最小复现里从不触发（tester 里两张表都是 0），所以「双击在编辑器打开、回车只检查」没法实现也没法验证。现在回车和双击都聚焦检查器，编辑器仍从行菜单的「Edit request…」或 `⌘⌥↩` 进入。
- **Domains 行没有 favicon（2026-11-13）。** 首字母方块代替站点图标：抓 favicon 要网络请求、缓存和失败回退，属于功能而不是打磨（第 13 条的另一半）。
- **Sources 列表在自己的分区里滚动（2026-11-16）。** 中间是一个 `Scroll`，Sources／Sessions／Collections 各自也有内层滚动；Traffic 与页脚不参与。窗口太矮时中间整体滚动，而不是把 Traffic 的行压掉。
- **侧栏的键盘选择只在上区（Traffic／Sources／Sessions）和下区（Collections）各自一份**：两个区域各是一个 `SidebarList`，焦点跟着指针或 Tab 走，不跨区域连续。
- **没有做的那几条（第十五阶段，2026-11-13）**：设置窗口的「左标签右控件」行式表单（22）、表格行／侧栏行／集合树的右键菜单统一与补齐（25）、把抓包行拖进集合（27）、行内操作只在悬停出现的通查（28）、图标线宽与尺寸统一（29）、字号层级从五档收到四档（31）。这些是下一阶段的事，现状仍是设计稿允许的实现。

1. **侧栏的图标槽是 16 而不是 14。** MyGo 的 source list 条目按字号（16）画图标，槽跟着宽 2px；行文字仍在同一条线上。
2. **侧栏分区标题有 8px 上边距**（第一个也有），所以侧栏顶部留白 12；设计稿第一个标题没有上边距。
3. **Collections 标题行多了 `⋯`**（设计稿只有加号）：导入、环境、历史、Runner、APIs、Vault、Cookies 都在里面。
4. **Sources 的 Show all 展开后变成 Show fewer**；设计稿只画了未展开的样子。
5. **Sessions 行带会话数**，`Open session…` 带时钟图标（设计稿没有这两行）。
6. **树里的请求行没有空图标槽**：method 直接从缩进后开始，所以同一层里文件夹名和请求名不在同一条线上；设计稿的 mock 也是这样（`.slot` 14 + `.mth` 30）。
7. **很矮的窗口里中间滚动，Traffic 不滚**：Traffic 和页脚是固定 chrome，Sources／Sessions／Collections 先压到最小，仍不够才让整个中间滚动；设计稿没有这个数，是"顶部固定、中间滚动"的实现细节。
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
18. **选中浅底和设计稿不同**：`Accent` 用了 `#d8d4cb`／`#45443f`，比设计稿给的 `#e0ded8`／`#3b3a37` 深一档，否则和悬停面分不开（决策 53）。
19. **面板头里少一个按钮是常态**：编辑器的响应面板在 1280 宽（历史列打开时约 530 宽）只放得下状态、标签页、耗时和 `⋯`，格式菜单、Decode 和保存／示例／历史都在 `⋯` 里；960 宽（没有历史列）时格式菜单和动作按钮回到行上，只有 Decode 留在 `⋯` 里。
20. **编辑器并排时请求面板的 tab 会挤掉历史列**：窗口 1280、历史列打开时请求面板约 270 宽，四个 tab 放不下。第九阶段起历史列像侧栏一样让位：按实测宽度算出请求面板放不下整行的 tab 时，历史列自动收起（Send 历史仍可从响应面板的 `⋯` 打开），窗口变宽后回到用户原来的选择。放不下的 tab 仍从右往左进 More，行上永远是完整的词。
21. **底栏没有上下行速率**：应用现在只有请求计数（`rateWindow`），没有字节计数，所以设计稿里的 `↑ 27 KB/s ↓ 219 KB/s` 没有画。要显示它需要 `capture.Server` 在读写请求／响应体时累计字节（例如 `up`／`down` 两个 atomic，或按秒的环形桶），由底栏每秒取差值；现在的 `flow.bytes` 是完成后的响应体大小，只能算出「每次请求完成时的突发」，不是实时速率。
22. **侧栏显隐没有滑入滑出**：侧栏在 MyGo 的 `Split` 里，宽度由用户拖动的分隔条拥有，`Split` 没有开合动画的入口；收起时它整块出现／消失，旁边的内容面板立即改变尺寸。要做侧栏自身的滑动，得把 `Split` 换成自绘分隔条的 `Row`，属于结构改动。同理，检查器在左右并排与上下堆叠之间切换时，两侧面板的内容是重新挂载的，只有外层面板的尺寸在动。
23. **库画的部分仍是瞬时**：菜单的面和工具提示现在由 uikit 画（`MenuSelect` 的 `Panel`、`TooltipBase`），没有进出动效；剩下的只有菜单项自己的高亮（库画的强调色实底）和 `ui.Segmented` 内置的滑块——应用里没有再用后者，`uikit.Segmented` 是 `SegmentedBase` 自绘的。
24. **侧栏内部还剩一条线**：Traffic／Sources 与 Collections 之间有一条 `Divider`，设计稿的示意图里没有；它分的是侧栏里两个各自滚动的区域，属于侧栏自己的结构，这一阶段没动。Collections 与页脚之间那条已经去掉——那里现在是侧栏和底栏的交界，外壳是一整块。
25. **活动条没有摘要文字**：审计要求计数只说一次，所以活动条只剩柱子和结束时间；设计稿第 4.4 节画的「Last 5 min · N requests」没有了。选中时间范围时，范围读在搜索框的 `Time:` chip 上，活动条里只有清除按钮（第十三阶段，2026-11-13）。
26. **底栏没有计数，除非视图隐藏了行**：设计稿画的是「N captured · N shown」，现在只在隐藏时说「8 of 13」，都显示时什么也不说，因为侧栏已经数过（第十三阶段，2026-11-13）。
27. **搜索框的语法比下拉更隐晦**：`/…/` 和 `*` 要用户知道才会用，补全只在 `key:` 上有帮助；换来的是一个入口和更少的控件（第十三阶段，2026-11-13）。
28. **⌘⌫ 删除单条抓包、Space 预览没有做**：前者要撤销和最近删除（UX 规范第 7 节），后者和 Enter 重复，因为检查器一直显示选中的那一行（第十三阶段，2026-11-13）。

