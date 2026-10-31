# vscroxy UI 改版计划

状态：已定稿，实施时细化。方向：概念图 A（精修 Proxyman 的 Capture）+ C（Postman 式的 Requests）。

- 概念 A，Capture：https://ampcode.com/user-content/attachments/6f08b3d269bd9a4951a014aaabc83df3d412b6e7c786f46f94ef9df3ea0919ac-file.png
- 概念 C，Requests：https://ampcode.com/user-content/attachments/7769f0d30970d12d35ff0e3c52e86e854e86aad8eda587eeb12ec9c8c0d64621-file.png

概念图只用来定方向，里面的数据和个别细节是生成的，不是像素级的稿子。

相关文档：[功能路线图](feature-roadmap.md)、[UX 规范](ux-guidelines.md)（交互和文案）、[基础设施方向](architecture.md)。

## 1. 目标

1. 整个 app 看起来像一个精心打磨的原生桌面工具（Proxyman、Linear 这个级别），而不是拼起来的表单。
2. 所有界面共用一套视觉规范：同样的字号、间距、控件高度、圆角和颜色语义。
3. 信息密度更高：减少层层叠叠的标题栏，把屏幕留给流量列表、请求和响应体。
4. 第 5 节的改版阶段只改外观和布局，不改功能。现有的每个功能、快捷键和无障碍标签都保留。
5. 功能路线图带来的新界面（第 6 节）也按同一套规范设计，随各自的功能里程碑落地。

不在改版阶段范围内：新增功能、改数据模型或存储格式、改 `internal/request`、`internal/vault` 等非 UI 包、换掉 MyGo。

## 2. 现状问题

数据来自 `internal/app` 的非测试代码：

| 问题 | 现状 |
| --- | --- |
| 字号没有规范 | 写死的 `FontSize` 有 700 多处，用了 9、10、11、12、13、14、15、16、17 共 9 种字号；主题设的是 13，但最常用的是 11（331 处）和 12（299 处），整体又小又挤 |
| 圆角、高度没有规范 | `Radius` 混用 3/4/5/6/7/10；控件高度有 22/24/26/28/30/32/34/36 八种 |
| 同类控件写法不一 | 手搭 `ui.ButtonBase` 58 处；tab 有 `flatTabs`、`ui.Tabs`、`ui.TabsBase`、`ui.Segmented`、`ui.SegmentedBase` 五种，所以 Request 和 Response 两边的 tab 长得不一样 |
| 强调色过多 | 侧栏图标、选中行、主按钮、tab 下划线、checkbox 全是饱和蓝，互相抢视线 |
| 颜色写死 | method 和状态码的颜色在 `methodColor`、`statusColor` 里写死，不跟深色模式变化 |
| 纵向浪费 | Capture 在表格上方叠了标题栏、搜索行、协议行、表头约 160px；inspector 在内容上方叠了摘要、面板标题、tab 三层约 100px |
| 编辑器像网页表单 | Requests 的 Key/Value 每格都是独立的填充输入框，加一个带边框的 ×；Method 选择器无边框而 URL 框有边框；侧栏底部散落 4 个文字链接 |
| 两个工作区导航不一致 | Requests 有左侧图标栏，Capture 没有 |
| Linux 字体 | 走系统字体（常见是 DejaVu Sans），又宽又笨重；三个平台的观感不一致 |
| 对话框 | 约 40 个 modal，各自排版，标题、按钮位置、宽度都不统一 |

## 3. 设计原则

1. **内容优先**：chrome（工具栏、标题、边框）尽量退后，流量和 body 是主角。
2. **一个强调色**：靛蓝只用于焦点、有焦点时的选中、主操作。其他一律用中性灰和语义色。
3. **语义色只表达语义**：绿、橙、红、青只出现在状态码、method、校验结果、错误上。
4. **同一种东西只有一种样子**：tab、按钮、面板头、表格各一种规范写法。
5. **密而不挤**：表格和编辑器保持桌面工具的密度，但最小可读字号是 11，正文 13。
6. **保持原生行为**：只封装"这个 app 的样子"，键盘、焦点、无障碍继续交给 MyGo 自带的组件。

## 4. 设计系统

全部放在 `internal/app` 里：主题继续在 `appearance.go`，新增 `style.go` 放 token 和共用组件。不建独立的 package，不做组件文档站。

### 4.1 字体

- 内嵌 Inter（界面）和 JetBrains Mono（URL、header、body、method），用 `ui.RegisterFont` 注册，设到 `t.Font`。两者都是 OFL 许可，需要补进 `THIRD_PARTY_NOTICES.md`。
- 只带要用的字重：Inter Regular/Medium/SemiBold，JetBrains Mono Regular/Bold。阶段 1 实际带入的原始静态 TTF 共 1,800,412 字节（1.72 MiB），另有 8,779 字节的 OFL 许可；这是资源量，不是最终打包后二进制增量测量。
- 表格里会变化的数字（耗时、大小、时间、计数）开 `FontFeatures("tnum")`，让数字等宽、列能对齐。
- **中文回退**：Inter 和 JetBrains Mono 都没有中文字形。主题字体写成一个列表，中文用各平台的系统字体：
  - 界面：`Inter, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`。
  - 等宽：`JetBrains Mono, PingFang SC, Microsoft YaHei UI, Noto Sans CJK SC`（中文没有好的等宽字体，用界面字体回退）。
  - 不内嵌中文字体：一个字重就要 10 MB 以上。Linux 上没有 Noto Sans CJK 时，在 Settings → 外观提示安装（Debian/Ubuntu 是 `fonts-noto-cjk`）。
  - 中文界面的行高和按钮宽度要用中文截图单独检查：中文字形比英文高，按钮文字通常更短。

### 4.2 字号

| Token | 大小 | 用途 |
| --- | --- | --- |
| `textMeta` | 11 | 计数、时间戳、提示、状态栏、分组标题（大写，加字距） |
| `textBody` | 13 | 默认：正文、表格、输入框、按钮、tab |
| `textTitle` | 15 | 对话框标题、inspector 头部的请求名 |

不再使用 9、10、12、14、16、17。等宽字体同样走这三档。

### 4.3 尺寸、间距、圆角

- 间距：直接用 MyGo 的 `t.Space(n)`，`Spacing` 保持 4，只允许 4/8/12/16/24。
- 控件高度：`heightCompact` 24（表格内、工具栏图标按钮）、`heightControl` 28（按钮、输入框、tab、侧栏条目）、`heightLarge` 32（URL 栏、Send）。
- 行高：流量表格和 KV 表格 28。
- 圆角：`radiusControl` 4（按钮、输入、pill）、`radiusPanel` 6（弹层、菜单、卡片）、`radiusDialog` 10（对话框）。
- 分隔：统一 1px 细线 `t.Border`，不靠背景色块分区。
- 阴影：只有菜单、弹层、对话框有阴影，两档。

### 4.4 颜色

在现有 `ui.Theme` 字段的基础上调整，再加一组语义色，浅色和深色各一套：

- **中性色**：背景、侧栏背景（比背景略灰）、hover、pressed、边框、正文、次要文字、第三级文字（禁用、占位）。
- **强调色**：靛蓝，浅色 #5b5bd6，深色 #7c7cf0。不再用系统蓝，因为它和 GET 的蓝是同一色相。
- **选中行**：窗口有焦点且列表有焦点时用实心强调色；否则用浅色强调底（约 12% 透明度）加正常文字颜色。
- **语义色**：成功、警告、危险、信息四种，每种有前景色和浅底色两档（给 pill 用）。
- **Method 色**：GET 蓝、POST 绿、PUT 橙、DELETE 红、PATCH 洋红（#c026d3，原来是紫，会和靛蓝撞）、WS 青；改成从主题取值，深色模式提高亮度。
- **状态码色**：2xx 绿、3xx 青、4xx 橙、5xx 红、304 和 Tunnel 灰，同样从主题取值。
- 侧栏图标改成次要文字色，不再用蓝色。

**为以后接入第三方主题做准备**（Tokyo Night、Dracula 这类现成色板）：

- 所有界面代码只引用语义 token（背景、表面、边框、文字、强调、成功/警告/危险/信息、method、状态码、语法高亮），不直接写色值。
- 一套主题就是"语义 token → 色值"的一张表。内置浅色和深色两张；以后再加一个映射层，把外部色板（比如 Tokyo Night 的 `bg`/`fg`/`blue`/`magenta`）填进这张表，代码编辑器的语法高亮也一起取值。
- 本次只做到 token 齐全、全部从主题取值，不做主题选择界面和导入功能。

### 4.5 图标

继续用 Lucide。尺寸只有 14（行内、按钮）和 16（rail、空状态）两档；颜色跟随文字，不单独上色。

### 4.6 共用组件

只封装反复出现、需要统一样子的东西：

| 组件 | 替换的现状 | 规范 |
| --- | --- | --- |
| `toolbarButton` | 各处手搭的 `ButtonBase` | 高 28，透明底，hover 灰底，可带图标；激活态（如断点开启）用浅色强调底 |
| `iconButton` | 同上 | 24 或 28 的正方形，必须有 tooltip 和无障碍标签 |
| `primaryButton` / `secondaryButton` | `ui.Button` 的各种样式 | 一屏最多一个主按钮 |
| `tabs` | 五种 tab 写法 | 一种下划线 tab，高 32，可带计数（`Headers 3`）；segmented 只用于真正互斥的模式切换 |
| `paneHeader` | inspector 和响应区的标题行加 tab 行 | 一行：标题、tab、右侧操作 |
| `statusPill` | 纯文字状态 | 语义浅底色加前景色文字，`201 Created` |
| `methodLabel` | 各处 method 文字 | 等宽字体、method 色，宽度对齐 |
| `kvGrid` | 请求编辑器里的 KV 输入框，inspector 的 header 列表 | 细线表格、无边框单元格、启用列、hover 时才出现删除按钮、末行是"添加"占位行；只读模式给 inspector 用 |
| `sidebarItem` / `sidebarSection` | 两个侧栏各自的写法 | 高 28，图标、名称、右侧计数；分组标题用 11 号大写 |
| `emptyState` | 各处空状态 | 图标、一句标题、一句说明、最多一个主操作和一个次操作 |
| `appModal` / `dialogTitle` / `dialogFooter` | 已有前两者，footer 各写各的 | 固定宽度档（480/640/840）、标题栏、可滚动内容、右下角按钮（取消在左，主操作在右） |

### 4.7 交互规范

- **Hover**：可点的元素 hover 时背景变为 `SurfaceHover`，文字颜色不变；不可点的元素没有 hover 效果。颜色立即变化，不做过渡动画。
- **按下**：背景变为 `SurfacePressed`。
- **焦点环**：键盘焦点显示 2px 强调色外框，鼠标点击不显示。所有可交互元素都必须有焦点环。
- **禁用**：文字用第三级文字色，不响应 hover；tooltip 说明为什么不可用。
- **动效**：只用于说明变化（展开收起、面板出现、toast 进出），150–200ms，ease-out，只改变透明度和位移；尊重系统的"减少动效"设置。列表滚动、新流量到达不做动画。
- **拖拽**：可拖的分隔线 hover 时变成强调色细线，鼠标指针变成调整大小的样式；双击分隔线恢复默认位置。
- **右键**：任何有动作的对象都支持右键，菜单内容见 UX 规范第 6 节。
- **选中**：单击选中；Cmd/Ctrl 单击加选；Shift 单击连选；Enter 进入检查器；Esc 返回。

### 4.8 状态规范

每个列表、面板、编辑器都要设计这四种状态：

| 状态 | 样子 |
| --- | --- |
| 空 | `emptyState`：16px 图标、一句标题、一句说明、最多一个主操作和一个次操作，垂直居中偏上 |
| 加载 | 超过 300ms 才显示；列表用占位行，面板用居中的小转圈，按钮内显示转圈并禁用 |
| 错误 | 面板内显示错误图标、一句说明、"重试"和"详情"；不要用弹窗报错 |
| 部分数据 | 数据被截断、不完整时，在内容上方显示一条警告条，说明原因（现有的截断提示改成这个样式） |

**Toast**：右下角，宽 360，最多同时 3 个，从下往上堆叠。四个级别用左侧图标区分（不用彩色背景）。行为见 UX 规范第 8 节。

**已启用工具栏**：标题栏下方 32px 高，背景比标题栏略深；每个工具一个 chip（图标、名称、×）。行为见 UX 规范第 5 节。

### 4.9 文案

文案的语气、长度、省略号用法和术语表见 [UX 规范](ux-guidelines.md) 第 11 节。改版时同步替换界面上超过一行的说明文字（现在有 47 段超过 120 个字符）。

### 4.10 品牌和 app 图标

- 现在的 logo 直接用 Lucide 的 `Route` 图标，需要一个专属的品牌标识。
- 产出：一个简单的几何图形标识（靛蓝为主色），用于标题栏（16px 单色版）和 app 图标（macOS 圆角矩形、Windows、Linux 各尺寸，1024px 源文件），放在 `internal/appicon` 旁边的资源目录里。
- 先出 2–3 个方向供选择，在阶段 2 之前定下来。

## 5. 分阶段计划

每个阶段单独可验证、可回退，可以单独合并。一次只做一件事：先换样式不动布局，再改布局。

### 阶段 0：基线与截图工具

- 写一个可选开启的截图测试（例如设置 `VSCROXY_SNAPSHOTS=dir` 时才运行）。它用 `ui.NewTester` 加固定的假流量和假请求库渲染关键状态，再用 `tt.Image()` 存成 PNG，浅色深色各一套。
- 覆盖的状态包括：Capture 空状态、有流量且选中一行、各个 body 视图；Requests 空状态、编辑中、有响应；设置对话框；命令面板；主要菜单。
- 先存一份改版前的基线。之后每个阶段都出前后对比图。

验收：一条命令就能生成全套截图，结果是确定的（同样的代码出同样的图）。

#### 阶段 0 使用方式

截图测试在 `internal/app/snapshots_test.go`。从仓库根目录运行：

```bash
VSCROXY_SNAPSHOTS="$PWD/.amp/in/artifacts/ui-current" go test ./internal/app -run '^TestUISnapshots$' -count=1 -v
```

输出目录必须不存在或为空；测试不会覆盖已有基线。换一个目录名即可保留下一轮截图。必须带 `-count=1`，避免测试缓存跳过生成。普通 `go test ./...` 会跳过这项测试，不生成图片。

改版前基线保存在 `internal/app/testdata/ui-baseline/`：

- `light/`、`dark/` 各 28 张 PNG，共 56 张；默认 1280 × 800，Capture 选中态、Requests 编辑态和响应态另有 960 × 800 版本。
- 覆盖 Capture 空状态、选中、筛选、Headers/Raw/Timing；Auto/Pretty/JSON/Hex/HTML Preview/Protobuf/图片预览；Requests 空状态、Headers 编辑、Body 编辑、有响应；Settings 通用页和命令面板。
- 六类原生菜单（Capture 更多、布局、body 格式、inspector 更多、Request 更多、流量右键）各有宿主截图 `*.host.png` 和菜单条目 `*.menu.json`。**MyGo 的原生弹出菜单不在 `tt.Image()` 中**；条目清单只能核对文案、顺序和子菜单路径，不能验证菜单外观、勾选或禁用态。菜单外观与交互仍需后续真实窗口检查，不能把宿主截图当成菜单截图。
- `manifest.json` 记录运行平台、Go 版本、缩放比例和每个产物的 SHA-256。只有全部场景成功才写入；不自动判定视觉变化是否正确。

基线环境：Linux amd64、Go 1.27.3、MyGo v0.7.3、Onevra source v0.2026.16，1× 渲染，DejaVu Sans / DejaVu Sans Mono。生产 UI 来源是 `59a1442fc83106db613e397dc64d742a85445614`，本阶段不改生产代码。固定 UTC 时间、英文 locale、假流量、请求库和环境；不读取个人设置，不启动代理，不发送网络请求。Requests 的 Send 在无代理实例的测试夹具中为禁用态，不代表实际发送行为的验证。

确定性范围是**相同代码、依赖、平台和字体环境**；现阶段仍使用系统字体，不能要求跨平台逐像素一致。已在两个独立测试进程中逐文件比较产物。后续阶段保留此基线，将新产物生成到另一个目录，再做浅色/深色对比；跨平台检查仍按阶段 7 执行。

```bash
# 同环境下核对全部文件；无输出且退出码为 0 表示完全相同。
diff -rq internal/app/testdata/ui-baseline .amp/in/artifacts/ui-current
```

### 阶段 1：设计系统落地，不改布局

- 新增 `style.go`：第 4 节的 token 和共用组件。
- `appTheme` 按 4.4 调整颜色，补语义色；`methodColor`、`statusColor` 改成从主题取值。
- 内嵌字体。
- 只替换 shell（标题栏、状态栏）和 Capture 主界面里的写死数值和手搭控件，用来检验这套规范；布局保持不变。

验收：Capture 截图前后对比，布局一致，观感统一；所有现有测试通过；`go tool mygo vet .` 通过。

#### 阶段 1 实施记录

- `style.go` 已加入三档字号、控件尺寸、圆角、图标和字体 token，以及工具栏按钮、图标按钮、主次按钮、状态 pill、method 标签、原生侧栏条目和只读 KV 行；已有 `flatTabs` 统一为 32px 下划线 tab。保留 MyGo 的键盘、焦点、禁用和无障碍行为。
- Shell 和 Capture 的工具栏、流量表格、Inspector、Headers/Params、Body/Raw/JSON/Hex 和 Timing 已接入这套样式。搜索／协议两行工具栏、列顺序、Inspector 摘要／面板标题／tab 的分层、分栏方向和比例不变；没有合并任何一行。全局主题和共用 tab 会影响其他界面的字体、颜色和 tab 外观，但没有迁移 Requests 或 Settings 布局。
- 浅深色主题使用靛蓝强调色和主题语义色；列表与窗口都有焦点时选中行用实心强调底，否则用 12% 浅底和正常文字。新增浅／深色焦点回归测试，覆盖窗口失焦、重新获得焦点和转到搜索框。
- Inter 4.1（400/500/600）和 JetBrains Mono 2.304（400/700）已内嵌，来源、版本、校验和与许可见 `internal/app/fonts/README.md`。注册失败只记录一次日志并回退，不阻止启动。为避免回退到 CJK 字体后英文变宽、ASCII 失去等宽，平台拉丁字体放在 CJK 字体前；中文仍由系统字体提供。MyGo 没有 `Theme.FontMono`，等宽读数和编辑器显式使用字体 token。
- **行高限制**：MyGo v0.7.3 的原生 `ui.Table` 最小行高／表头为 `t.Space(8)`，当前公共 API 无行高参数。阶段 1 保留 32px；不修改依赖或缩小全局 Spacing 来强行实现目标 28px。后续 Capture 阶段需另行解决，28px 目标不变。
- **留给后续阶段**：合并式 `paneHeader`、可编辑 `kvGrid`、侧栏分组重排、统一空状态结构、modal footer／宽度档和全局剩余字面量清理，随各自布局阶段迁移；本阶段不加入未使用的组件骨架。中文字体缺失的 Settings 提示也不在本次新增。

验证仍使用阶段 0 的截图矩阵，保留原基线；本阶段检查浅／深色、1280／960 宽度、各 Body 格式、空状态和原生窗口 hover、菜单、tab、焦点。另检查了包含中文 client、URL 路径和 JSON 值的 960px 夹具；这不是整套中文本地化界面验收。macOS／Windows 实机外观留给阶段 7。

当前 orb 的 `~/.cache/mygo` 是文档源码软链且不可写，会阻止 MyGo 写入字体缓存。要验收内嵌字体而非系统回退，使用独立的可写缓存（正常启动失败回退路径也已测试）：

```bash
XDG_CACHE_HOME=/tmp/vscroxy-ui-cache VSCROXY_SNAPSHOTS="$PWD/.amp/in/artifacts/ui-phase1" go test ./internal/app -run '^TestUISnapshots$' -count=1 -v
```

### 阶段 2：外壳

- 标题栏 44px：品牌、`Capture | Requests` 切换、居中的录制状态 pill（状态点、地址；暂停按钮在 M1 录制开关做好后加上）、右侧命令面板、Tools、Settings 图标按钮，`⋯` 菜单保留。Host 方案下拉框在 M2 加入，位置预留在录制 pill 右侧。
- 已启用工具栏（4.8）：先显示现有的断点、限速、No Caching、Scenario、上游代理。
- 状态栏统一样式：左侧计数，右侧 auto-follow 开关、环境等上下文信息。
- 命令面板、右键菜单、`⋯` 菜单、toast 统一样式。toast 依赖基础设施的通知层，在它完成前先统一现有消息条的样式。
- 新品牌标识和 app 图标（4.10）。

#### 阶段 2 实施记录（保留现有图标）

- 标题栏改为 44px，工作区切换移到品牌旁，监听状态 pill 按整个窗口居中，左右分别避让平台窗口按钮；右侧保留命令面板、Tools、Settings 和更多入口。状态仍明确称为 Proxy running/stopped，不把监听误报为已验证的 HTTPS 录制，也没有提前加入 M1 暂停、M2 Host 或 M4 Tools 窗口。
- 32px 已启用工具栏在两个工作区共享，无工具时不占空间；现有断点、限速、No Caching、Scenario 和上游代理均有编辑入口和独立关闭按钮。关闭直接更新原有配置；Scenario 必须保存成功后才更新运行配置，失败则保留 chip 并通知。上游 chip 不显示可能带凭据的地址。状态栏左侧统一计数，右侧保留 auto-follow、环境等上下文。Capture 和 Requests 内部布局不变。
- app 自己构建的右键／菜单按钮统一使用 MyGo 的 `PopoverMenu`／`PopoverMenuButton`，由主题绘制；保留原有动作、快捷键、勾选、禁用和子菜单。系统应用菜单以及 MyGo 内建的编辑器／表头菜单仍由框架负责。命令面板接入 13/11 字号、32px 行高和焦点选中规范，列表视口显示完整行。
- 现有通知实际上已使用 MyGo Toast，因而用 `ToastViewportBase`／`ToastBase` 统一外观：右下角 360px 中性卡片，最多三个，图标区分已有 Type，200ms 位移／淡入淡出并尊重减少动效。计时、悬停暂停、无障碍通知、动作和关闭仍由 MyGo 管理；不新增通知业务层，也不靠猜测消息文字分级。弹窗位于通知之上，保存按钮不会被通知遮住。
- 截图矩阵扩展到浅深色共 76 张，新增工具栏、Tools／更多／子菜单、不可用命令和四类通知，工具栏也覆盖 960px。新菜单直接包含在 PNG 中，不再输出宿主截图和原生菜单清单；阶段 0 基线保持不变。新增测试覆盖左右原生按钮避让、通知动作／关闭，以及两个工作区关闭工具、重新开启和 Scenario 保存失败；Linux 真实窗口另检查指针、菜单、命令搜索、工作区切换和通知动作。macOS／Windows 的真实窗口仍待跨平台验收。
- 按用户最新决定，保留现有 Route 品牌标识及 app 图标；不实施 A/B/C 新品牌方案，也不将新图标作为继续阶段 3 的前置条件。

### 阶段 3：Capture

- **侧栏**：灰色单色图标、大写分组标题、计数对齐；Views 和 Investigations 的"+ ⋯"行和缩进不齐的条目改成规范的侧栏条目；底部的 Client setup、HTTPS rules 保留但改成同样的样式。
- **工具栏合成一行**：搜索框（内嵌筛选 chip）、协议 segmented（All/HTTP/HTTPS/WS/SSE/JSON）、筛选、断点、布局。"Rules…" 收进图标按钮或菜单。
- **流量表格**：状态列（圆点加状态码）、Method、Host（次要色）、Path（主文字）、Client（小图标加名称）、耗时、大小、时间；行高 28；数字等宽；选中样式按 4.4；列宽和列选择（`columns.go`）保持可用。
- **Inspector**：一行头部（`POST /graphql`、状态 pill、耗时和大小、Replay、Copy cURL、`⋯`）；Request 和 Response 两个面板各自把标题和 tab 放在同一行；header 用只读 `kvGrid`；Pretty、格式菜单放在面板头右侧。上下分栏、左右分栏的布局选项保留。
- **Body 视图**（`body.go`）、Timing、WebSocket/SSE 消息列表、CONNECT 连接视图、调试历史、对比视图：统一面板头和间距。
- **空状态**：用 `emptyState`；没选中请求时，inspector 不再占半屏空白（收起或只显示一行提示）。

#### 阶段 3 实施记录

- Capture 工具栏合为 44px 一行，位于流量表格／Inspector 分栏之外；搜索、内嵌可移除条件、协议 segmented、筛选、断点、布局和更多入口并列。条件较多时在搜索框内横向滚动，不再新增条件行；全部条件仍按原有 AND／OR 语义工作。Images／HTML、保存筛选和 Debugging rules 保留在更多菜单。
- 侧栏分组标题改为大写和 11px，计数使用等宽数字；Views 的新建入口改为带文字的条目，保存筛选入口保留在同行右侧；新建 Investigation、历史会话和底部连接入口的图标／文字左对齐。不重做 Investigations 看板，也不替换品牌图标。
- 流量表格分开显示弱化的 Host 和主要 Path；保留 `#`、列拖动、列宽、隐藏列和自定义列。Path 沿用持久化 ID `url` 及原有完整 URL 排序，避免清掉用户保存的布局；新增 Host 列按主机名排序且可隐藏。正常 HTTP 状态显示圆点和状态码，Tunnel／Failed 不误称为 HTTP 成功。表格局部 `Spacing=3.5` 配合 16px 单行文本框实现 28px 行高；全局 Spacing 仍为 4，依赖未修改。
- Inspector 摘要使用 method／path、状态、耗时／大小、Replay、Copy cURL 和更多菜单；完整 URL 保留在辅助标签和 tooltip。窄 Inspector 缩短状态并把 Copy 收为图标，历史入口移入更多菜单。Request／Response 各自的标题、tab 和格式菜单合到同一行；只读 Headers／Params 继续复用 `kvRow`，保留凭据遮盖、复制和筛选。底部、右侧、自适应和聚焦模式保留，宽度判断扣除实际侧栏。
- Body、JSON／Hex、Timing、WS／SSE、CONNECT、调试历史和对比阅读器统一字级、控件和间距；CONNECT 标题与其 tabs 也合到同一行。完整内容、截断提示、消息过滤／暂停／导出及历史证据语义不变。空列表使用 `emptyState`，Inspector 仅留一行提示；搜索框不随分栏挂载变化，输入无匹配再恢复时保留焦点。
- 验证：`go build ./...`、`go vet ./...`、`go tool mygo vet .`、`go test ./...` 和 `CGO_ENABLED=0 go test ./...` 通过。新增 UI 回归检查覆盖 960／1280px 单行工具栏、28px 行高、空结果恢复和菜单专属协议条件；原有选择、滚动、分栏、筛选、重放和列布局测试继续通过。
- 浅／深色截图矩阵为 98 张，新增右侧 Inspector、无匹配、多选、CONNECT／失败、WS／SSE、Diff 和调试历史。16 张 Requests 截图与阶段 2 基线逐字节一致。Linux 原生窗口另录制并检查了指针悬停、协议键盘导航、搜索归零／恢复、重置、Headers／Raw／Timing、左右分栏和深色切换；未使用真实流量、用户配置或系统代理。macOS／Windows 原生窗口仍待跨平台验收。

### 阶段 4：Requests

- **左侧 rail**：图标加小标签，选中态用浅底，不用蓝色块。
- **集合树**：文件夹、请求前的 method 小标签、hover 时出现操作；面板头放"+"和"⋯"；底部的 Import cURL、Vault、Runner、Cookies 收进"+"或"⋯"菜单。
- **环境侧栏**：同样的树和表格样式。
- **文档 tab 栏**：method 色标签、未保存圆点、关闭按钮；右侧是正式的环境下拉框（带颜色点）和 Save。
- **请求行**：Method 和 URL 合成一个带边框的控件，`{{变量}}` 显示成 chip；Send 用 32 高的主按钮。
- **编辑区 tab**：带计数（Headers 3、Checks 2）。
- **Params、Headers、Form、变量**：换成可编辑 `kvGrid`；Bulk edit 保留成切换按钮。
- **Auth、Body、Scripts、Checks、Examples、OAuth、Protobuf**：统一表单行（标签在左、控件在右）、统一代码编辑器边框。
- **响应区**：一行头部，包含 tab（Body、Headers 5、Cookies、Checks 2/2、Timing）、状态 pill、耗时、大小；Send history、对比入口放在头部右侧。

#### 阶段 4 实施记录

- 保留现有品牌和 app 图标。Requests rail 使用浅色选中底和小标签；集合树使用 28px 行、文件夹、method 标签和 hover／行内键盘焦点时出现的操作菜单。操作始终挂载且保留 Tab 顺序，不因隐藏而丢失键盘入口；不悬停时不为操作预留额外的名称宽度。集合头部的「+」和「⋯」容纳新建、Import、Import cURL、Vault、Runner、Cookies。
- 文档 tab 保留 method、未保存标记、关闭和 Source 入口；环境选择改为带颜色点的正式下拉框，位于 Save 等操作之前。环境侧栏改为树形样式，不改变原有环境编辑行为。
- Method 和 URL 合为一个边框，Send 为 32px。URL 未聚焦时绘制字面量 `{{引用}}` chip，聚焦时使用原生输入框的文字范围样式；始终是同一个原生输入框负责选择、复制、撤销和 IME，不展开变量，也不读取 vault 值。Unicode 和未闭合引用不改写。
- Params、Headers、Form 和变量表格使用细线分隔、28px 行、静止时无边框的单元格和末尾添加入口；保留重复／禁用行、显式凭据显隐、文件附件及 Bulk edit 往返。Auth／OAuth 共用标签左、控件右的表单；Body、Scripts、Bulk Headers 和 Examples 共用代码编辑器样式，Checks／Examples 同步整理。Protobuf 沿用阶段 3 已统一的共享阅读器，不另建 Requests 专用版本。
- 编辑区显示 Params、Headers、Checks、Examples 计数；Headers 按启用行计数，不去重。计数与无障碍名称分离；Examples 使用稳定名称 `Request editor: Examples`，不再把数量放进名称。响应头合并 tab、格式菜单、状态 pill、耗时／大小和操作；Tests 视觉文案为 Checks，保留 `Response tab: Tests` 无障碍名称和原有枚举顺序。收紧 tab 留白，保证当前 960px 夹具的 Console 完整显示；更拥挤时仍可横向滚动。不隐藏取消、失败、历史实验和来源证据。
- 验证：`go build ./...`、`go vet ./...`、`go tool mygo vet .`、`go test ./...` 和 `CGO_ENABLED=0 go test ./...`。回归检查覆盖 960／1280px 的 URL 原文复制与撤销、环境切换、重复／禁用 Header 计数、Checks 计数和最右侧响应 tab；窄屏裁切检查在恢复较宽留白时失败。
- 浅／深色截图矩阵扩展到 198 张，覆盖七类 Auth、Params、Multipart、Bulk Headers、Scripts、Checks、Examples、变量／环境、菜单、凭据遮盖／显式显示和键盘焦点。与阶段 3 共有的 80 张非 Requests 截图逐字节一致。Linux 原生窗口另录制并检查了移动指针、集合菜单、hover 行操作、凭据显隐、URL 选择／复制、环境切换、响应页签和深色窄窗口；夹具不读取用户配置、不启动代理、不发送请求，Send 保持禁用。实际发送行为由现有代理集成测试覆盖；macOS／Windows 外观仍留给阶段 7。

### 阶段 5：次级视图

Runner、History（合并后的 Run records）、Sessions（合并后的 Investigations 和 Historical sessions）、APIs（OpenAPI）、Tools 窗口（规则、Scenario、脚本、Host 方案）：套用同样的侧栏、表格、面板头和空状态组件。

功能合并（History、Sessions）和 Tools 窗口属于 [功能路线图](feature-roadmap.md) 的 M1 和 M4。按路线图会被去掉的界面（Investigations 看板、Regression 分享对话框、Recent requests）不做改版，直接随合并删除。

### 阶段 6：对话框

约 40 个 modal 按类型统一：

- **设置类**：Settings 改成左侧分类、右侧表单行的布局，分区为通用、外观、代理（端口、系统代理、上游、绕过）、HTTPS（CA、解密名单）、抓包（白名单、忽略、No Caching、内存上限）、设备。Upstream、SSL、Ignored hosts/apps 这几个独立对话框并进 Settings 后删除；DNS 映射升级为 Host 方案，放进 Tools 窗口。Client setup 和 Diagnostics 合并成"连接诊断"。
- **编辑类**：Rules、Breakpoint、Vault、Cookies、Variables、Columns、Folder、Rename。
- **流程类**：Import、Postman 导入报告、cURL 导入、Code、HAR 导出、WebSocket session、Replay。
- **确认类**：Delete、Clear、关闭未保存请求。使用统一的确认对话框。

全部使用 `appModal` 的宽度档、标题、可滚动内容和 footer。

### 阶段 7：深色模式、跨平台与可访问性

- 用截图检查全部状态的深色版本，调整对比度：正文至少 4.5:1，次要文字至少 3:1。
- macOS（红绿灯、标题栏）、Windows、Linux 各检查一遍标题栏和窗口控件位置。
- 键盘：Tab 顺序、焦点环可见、所有图标按钮都有 tooltip 和标签。

### 阶段 8：收尾

- 清理剩余的写死字号、圆角、高度和颜色。目标是 `internal/app` 里只有 `style.go` 和 `appearance.go` 出现数字字号和 `ui.Hex`。
- 加一个简单的检查（测试或 `go vet` 风格的脚本），防止新代码再写死这些值。
- 更新 `docs/guide.md` 里与新界面不符的描述（控件名称、位置）。

## 6. 新界面

功能路线图带来的新界面，都按第 4 节的规范和 UX 规范设计。每个界面在对应里程碑开始前先出设计稿（Painter 概念图或 amp-view 交互原型），确认后再实现。

| 界面 | 要点 | 里程碑 |
| --- | --- | --- |
| 首次启动引导 | 嵌在 Capture 空状态里的 4 步引导（UX 规范第 9 节）；每步一个主按钮，完成的步骤打勾，可跳过 | M1 |
| 录制 pill 和暂停 | 三种状态（录制中、已暂停、代理停止），暂停按钮在 pill 内 | M1 |
| Settings 页面 | 独立窗口，左侧分类（通用、外观、代理、HTTPS、抓包、设备），右侧表单行（标签左、控件右、说明在控件下方一行）；改动立即生效，需要重启的项目标明 | M1 |
| Sessions | Capture 侧栏的一个分组：最近打开的会话文件；打开的会话以视图 tab 显示，tab 上有"历史"标记，只读 | M1 |
| History | Requests 资源栏的一项：按时间倒序的发送记录列表，顶部按请求、来源、状态筛选；选中后右侧显示请求和响应，可以设为基线、对比、打开成草稿 | M1 |
| 连接诊断 | 一个页面：上方输入目标 URL，下方按"监听 → 系统代理 → 证书 → 解密规则 → 实际流量"逐项打勾或打叉，每项一个修复按钮 | M1 |
| Host 方案 | 标题栏下拉框：列出方案和开关，底部"管理方案…"；Tools 窗口里左侧方案列表（拖动排序、开关），右侧 hosts 编辑器（语法高亮、冲突提示） | M2 |
| 整理成集合向导 | 大弹窗，顶部步骤条（范围 → 合并接口 → 变量 → 结构 → 示例 → 完成），每步左侧是设置、右侧是结果预览 | M3 |
| 对应请求提示 | 检查器头部一个小链接"对应请求：Get user"，点开或存为示例 | M3 |
| Tools 窗口 | 独立窗口，左侧分类（规则、场景、脚本、Host 方案、模拟），中间列表（开关、名称、匹配条件），右侧详情编辑；顶部规则测试输入框 | M4 |
| 规则脚本编辑器 | Tools 窗口右侧的代码编辑器，下方 Console；和 Requests 的 Scripts 用同一个组件 | M4 |
| JSON 筛选、快速解码 | Body 面板头部的筛选输入框（JSONPath / jq 切换）；选中文字后出现的小浮层，显示 Base64、JWT、时间戳解码结果 | M5 |
| 耗时瀑布图 | Timing 标签：横向分段条（DNS、连接、TLS、等待、下载），每段颜色不同，下方表格列出数值 | M5 |
| 协议客户端 | gRPC、SSE、Socket.IO、MCP、MQTT 客户端都作为 Requests 的请求类型，共用请求 tab、地址栏和响应区，只有编辑区和消息时间线不同 | M8 |

## 7. 验证

- 每个阶段：`go build ./...`、`go vet ./...`、`go test ./...`、`go tool mygo vet .`。
- 每个阶段都附阶段 0 生成的浅色和深色前后对比截图。
- 涉及交互的改动（菜单、hover、tab、拖动分栏）另外在真实窗口里录屏检查。
- 现有 UI 测试（28 个测试文件、约 250 处按标签点击和查找）是行为不变的保障。改版必须保留无障碍标签；确实需要改名的，同一次提交里同步改测试，并在提交说明中写清楚。

## 8. 风险

| 风险 | 应对 |
| --- | --- |
| 改布局时不小心丢功能或快捷键 | 阶段 1 只换样式；后续阶段对照菜单、命令面板和快捷键清单逐项检查，现有测试兜底 |
| 无障碍标签变化导致测试大面积失败 | 共用组件强制传入标签，标签沿用现有文字 |
| 内嵌字体增加体积 | 只带用到的五个字重；当前 TTF 资源量为 1.72 MiB |
| 改动面大，难以评审 | 按阶段、按区域拆成小提交，每个都附截图 |
| 次级界面长期停留在旧样式 | 阶段 8 的检查脚本会列出剩余的写死值，作为待办清单 |

## 9. 决策记录

1. ~~是否内嵌 Inter 和 JetBrains Mono？~~ 已决定：三个平台都内嵌。
2. ~~强调色？~~ 已决定：靛蓝 #5b5bd6（深色 #7c7cf0），PATCH 改成洋红。
3. ~~默认主题？~~ 已决定：外观设置改成"跟随系统 / 浅色 / 深色"三选一，默认跟随系统。现有的深色开关迁移过来。
4. ~~行高？~~ 已决定：28。桌面调试工具需要密度，Proxyman 也是这个量级。
5. ~~全局图标栏（方案 B）？~~ 已决定：不做。保留 Capture / Requests 两个工作区；规则、Host 方案、脚本、Mock 集中到 Tools 窗口（见功能路线图第 2 节）。
6. ~~网络设置对话框是否并进 Settings？~~ 已决定：并进，见阶段 6。
