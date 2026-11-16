# VS Code 参考：PaneView、悬停与动效

这份报告读的是 `microsoft/vscode` 的 `main`（2026-11-16，`--depth 1` 克隆在仓库外），
重点是 `src/vs/base/browser/ui/splitview/{paneview,splitview}.ts`、
`src/vs/base/browser/ui/hover/`、`src/vs/platform/hover/`、`workbench.reduceMotion`
和仓库的分层规则（`eslint.config.js` 里的 `local/code-layering`）。结论只用于
vscroxy 这一批的三件事：侧栏分区、工具提示、动效；不是要搬 VS Code 的架构。

## 1. 整体分层：值得借的是边界，不是容器

VS Code 把代码分成三层：`base`（无业务的可复用件）、`platform`（服务与协议）、
`workbench`（窗口与功能），每层再按 `common`／`browser`／`node` 分运行时；ESLint
的 `local/code-layering` 规定一个目录只能 import 同层或更下层，跨层只能走
`platform` 的 service 接口。依赖注入（`createDecorator` + `InstantiationService`）
让 workbench 不 new 服务，`Disposable`／`DisposableStore` 让监听器跟着对象一起
结束，`Emitter`／`Event` 让变化从服务流向界面。

vscroxy 已经有同样的边界，只是用 Go 的方式表达：

- `internal/uikit` 是 base：只依赖 MyGo，不 import 应用包。
- `internal/request`、`internal/imports`、`internal/httpfile`、`internal/vault`、
  `internal/upstream` 是 platform：不 import MyGo，也不 import `internal/app`。
- `internal/app` 是 workbench：它 new 服务、拥有窗口状态、把事件排进每帧的
  `drain`。这就是依赖注入，只是构造发生在 `app` 的字段里，而不是容器里。

值得借鉴的具体一点：把这条边界变成会失败的检查。AGENTS.md 已经写明
`uikit` 只依赖 MyGo、`request`／`imports`／`httpfile` 不 import MyGo，但今天
没有测试守着它。可以在 `internal/uikit` 加一个只读 `go list -deps` 的测试，
或在 CI 里加一行 `go list`，让越层 import 直接红。这比引入 DI 容器便宜得多。

不适合搬的：`InstantiationService` 的装饰器与生命周期容器、`DisposableStore`
的树（MyGo 的元素是每帧重建的，跨帧状态在 `app` 的字段和 `ui.Local` 里，Go 的
GC 负责释放）、`Emitter` 的事件图（即时模式 UI 在构建期读状态最稳；`app` 的
队列 + 每帧 `drain` 避免了“事件在画到一半时改状态”）。VS Code 的
`IView`／`Pane` 是长期存在的 DOM 对象，vscroxy 的分区是每帧重新构建的函数，
照搬类层次只会多一层壳。

## 2. PaneView / SplitView：侧栏该学的行为

`Pane` 的组成是“标题 + 内容”，关键行为：

- 标题永远在，内容只在展开时渲染；`minimumSize = 标题高 + 最小内容高`，
  折叠后分区仍占标题那一行。`expandedSize` 记住展开时的高度，重新展开回到
  原来的尺寸。
- 标题是 `role="button"`、`aria-expanded`，Enter／Space 切换，左右箭头收起／
  展开；点击标题切换。标题可聚焦，这是键盘可达的全部代价。
- `SplitView` 的尺寸是像素：每个 view 有 `minimumSize`／`maximumSize`，
  拖动分隔条时 `resize` 把增量分给两侧、按最小／最大夹住；`distributeEmptySpace`
  把剩余空间给最后一个（或低优先级）能长的 view，高优先级的先拿。
- 双击分隔条把两侧拉平（`onDidSashReset`）。
- 折叠／展开只有 200ms 的 CSS 过渡，`workbench.reduceMotion` 打开时由
  `.monaco-pane-view.animated` 的类直接关掉。

vscroxy 现在的问题正对着这几条：整个侧栏一个 `Scroll`，`Sources` 和
`Sessions` 的标题会叠在一起（`trafficRegion` 的 `MaxHeightPercent(70)` 与
内部列表的溢出没有对齐），分区没有独立高度，折叠只是把内容从流里拿掉，
下面的分区不会“长到底”。`ui.Split` 只有两栏，也没有“折叠到标题”的概念。

建议的落地（这一批已按此做）：在 `uikit` 里加一个只做纵向分区的组件，
每个分区是“标题 + 内容”，折叠只留标题；展开的分区按记住的像素高度排，
最后一个展开的分区吃掉剩余高度，所以 Collections 会一直长到底；只有相邻两个
都展开时才有可拖的分隔条；拖动用 `Bounds()` 量当前两段的高度，按像素增量改
两侧的高度，按最小高度夹住；双击分隔条恢复默认。折叠状态用设置里已有的
`SidebarClosed`，高度是新增字段，按 AGENTS.md 给 `store.Settings` 加版本步进
和测试数据。不做：分区拖拽重排（`PaneDraggable`）、横向 PaneView、
正交分隔条、CSS 动画类。

## 3. 悬停：延迟、位置、富内容、键盘

VS Code 的 hover 服务（`platform/hover/browser/hoverService.ts`）：

- 延迟是 `workbench.hover.delay`：Linux／Windows 默认 500ms，macOS 1500ms
  （注释说这是为了贴近系统原生提示）；需要更快反馈的场景用
  `workbench.hover.reducedDelay`（默认 500）。**同一个 groupId 的 hover 已经
  显示时，下一个跳过延迟立即显示**；相邻目标之间移动不会重新等。
- 键盘焦点带来的 hover 立即显示；Escape 关闭；指针离开目标与提示都关闭；
  提示本身可以接指针（`hideOnHover`），富内容里可以点链接。
- 位置默认在目标下方，空间不够翻到上方；`position` 可以指定，`alignment`
  控制对齐；提示的最大宽度默认 500（`--vscode-hover-maxWidth`），内边距
  4×8，内容按 Markdown 渲染，可以带状态栏动作。
- 同时只显示一个，嵌套最多 3 层；`showAndFocusLastHover` 把焦点送进提示，
  让键盘用户读到富内容。

vscroxy 的 `uikit.Tip` 已经建在 MyGo 的 `TooltipBase` 上，而 MyGo 0.9 已经
实现了这套时序：0.6s 延迟、提示消失后 0.4s 内下一个立即显示（“warm”窗口）、
键盘焦点立即显示、Escape 关闭、上方不够翻到下方、同时只显示一个、指针穿过
提示。所以不需要自己写 hover 服务；要改的是**外观**和**内容**：

- 外观从“黑底反色、等宽 11、圆角 6”改成浮层表面：`Canvas` 底、1px
  `Hairline`、`ShadowPopover`、`RadiusPanel`（6 与 10 之间）、6×10 内边距、
  正文 `TextSmall` 12、普通文字色、最大宽度 360；URL 仍按 `/ ? & = - _ .`
  断行，但不再整段用等宽。
- 快捷键用 `Kbd`，不再拼进正文。
- 富卡片：标题（13/500）、一行 meta（11、muted）、key/value 行（key 11
  muted、value 12）。用在侧栏的集合请求行和抓包行：标题是请求名或
  `GET /path`，meta 是方法／状态／耗时，行里是完整 URL、Host、Client。
- 延迟把 `Theme().TooltipDelay` 设成 500ms，贴近 VS Code 在 Linux／Windows
  的默认；warm 窗口随之为 333ms。macOS 的 1500ms 不跟：vscroxy 的提示是
  界面说明，不是编辑器里的类型信息，1.5 秒会让图标按钮显得没反应。

不适合搬的：Markdown／HTML hover 渲染器、hover 里的动作栏、鼠标位置 hover
（`setupDelayedHoverAtMouse`）、hover 栈的三层嵌套、`IHoverDelegate` 的
委托链。MyGo 的提示是元素树的一部分，富内容用 uikit 的组件画即可。

## 4. 减少动效：VS Code 的分寸

`workbench.reduceMotion` 有 `auto`／`on`／`off`，`auto` 跟随系统；打开后
workbench 的装饰性过渡（面板滑入、列表平滑滚动、光标动画）被关掉，但
**表达状态的动效**还在：进度条、加载转圈、录音指示。VS Code 不是“全关”，
而是“关装饰、留状态”。

vscroxy 这一批按用户的意见走得更彻底：默认没有装饰性过渡。具体是删掉
悬停淡出、浮层／toast／对话框的进出、分区折叠和面板尺寸的弹簧，以及
`MotionFast`／`MotionBase`／`MotionSlow`／`MotionEase`／`MotionSpring`／
`RegionMotion`／`PaneMotion` 这些 token；保留录制中的状态点脉动（它说明
“正在录”，不是过渡），保留用户自己拖动的分隔条和滚动。MyGo 的
`Preferences.ReduceMotion` 仍然生效，但已经没有我们自己的过渡需要它兜底。

## 5. 落地顺序（这一批）

1. `uikit.Tip` 换面、加 `CardTip` 富卡片、设 `TooltipDelay`；侧栏请求行和
   抓包行接上富卡片。
2. 删动效：token、helper、所有调用点、测试和文档；录制脉动留下。
3. 侧栏分区：`uikit` 的纵向分区组件、`app` 接入、`store.Settings` 版本 2
   与迁移测试、公开界面的折叠／占满测试。
4. Client setup 与 HTTPS decryption 两处对话框修正。
5. 画廊导出、快照断言、真实窗口截图。

## 6. 不搬的清单

- DOM／CSS 的布局与过渡、`contextview`、`hoverWidget` 的定位算法（MyGo 的
  `AttachTo` 已经做了翻转和夹边）。
- `Disposable` 树、`Emitter`、`InstantiationService`、装饰器注入。
- 虚拟滚动（MyGo 的 `List`／`Table` 自己只建可见行）。
- 分区的拖拽重排、折叠动画、PaneView 的横向模式。
- Markdown hover、hover 动作栏、hover 嵌套栈。
