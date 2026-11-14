# 设计审计：uikit 与画廊页面

状态：**审计完成，2026-11-14；本轮只写审计，不改任何现有页面的布局。**

> 2026-11-15：uikit 的前五条（FormRow／InputState、DataList、Modal 标题、Ring、DestructiveButton）已在 `uikit-q` 实现；下面的“现状”保留审计时的样子，作为改动依据。
基线：`gallery-plates`，已变基到 `origin/feat/mygo-app`（`ecfced7`）。图版见 `docs/design/`，由 `internal/gallery` 从 `internal/app/gallery.go` 的假数据导出；浅色、深色各一张。

参考：

- **shadcn/ui**（<https://ui.shadcn.com>，源码 `github.com/shadcn-ui/ui`，`apps/v4/registry/new-york-v4/ui/`）：按钮的变体和尺寸、Form/Field 的标签与错误、Card/Dialog/Sheet/Tabs/Table/Empty 的间距与层级、`--radius`／`--muted`／`--accent`／`--border`／`--ring`。
- **Base UI**（<https://base-ui.com>，源码 `github.com/mui/base-ui`）：`data-highlighted`、`data-selected`、`data-disabled`、`data-popup-open`、`data-pressed`、`data-open`／`data-closed`、`data-starting-style`／`data-ending-style`、`data-side`／`data-align`，以及菜单、选择器、标签页和对话框的键盘与焦点约定。
- **Anthropic `frontend-design` skill**（`github.com/anthropics/skills` 的 `skills/frontend-design/SKILL.md`）：从题材出发、排版承担个性、结构即信息、克制用色、把大胆留在一个地方、文案只做一件事。

优先级：**P1** 影响理解或操作；**P2** 视觉或交互不一致；**P3** 打磨。

---

## (a) uikit 层

### a.1 结论

我们的 kit 不是 CSS，不能按 shadcn 的类名逐条对照。真正值得比较的是**语义**：一个角色是否只有一个意思、一个状态是否有唯一的画法、表单的错误是否和控件绑在一起、列表的选中是否只有一种形状。按这个标准，uikit 的结构是成立的：三种表面、一个强调色、两种输入框、四种字号、两档字重、三档圆角，这些都比 shadcn 的默认值更适合桌面抓包工具。问题集中在**表单错误、列表选中、对话框命名和几个命名/状态约定**上，不在颜色。

### a.2 tokens

| 参考 | 我们的角色 | 现状 | 判断 |
| --- | --- | --- | --- |
| `--radius`（0.625rem，派生 sm/md/lg/xl） | `RadiusInner`／`RadiusControl`／`RadiusPanel` = 4／6／8（`internal/uikit/tokens.go:54-56`） | 固定三档，不派生 | **保留**。桌面控件的圆角不该随一个基数缩放；三档已经够用。 |
| `--muted` + `--muted-foreground` | `TextMuted` + `Fill`（`internal/uikit/theme.go:20-21`） | 没有单独的 muted 面；控件面用文字色 7% 叠加 | **保留**。`Fill` 在三种表面上都成立，比一个固定的 muted 面更稳。 |
| `--accent`（悬停面） | `Accent`（主操作、焦点、选中标记，`internal/uikit/theme.go:22`） | 语义和 shadcn **相反**：shadcn 的 accent 是 hover 背景，我们的 Accent 是墨色主操作 | **不改名，写清楚**。在 6.1 或本文件加一行映射，避免后来的人按 shadcn 的习惯把 `Accent` 当 hover。 |
| `--border` + `--input` | `Hairline`（`internal/uikit/theme.go:16`） | 只有一个边线角色；输入框在 canvas 上是 well，在 chrome 上是 canvas + hairline | **保留**。桌面工具不需要第三种边线。 |
| `--ring` | **没有**；焦点用 `Accent`（`internal/uikit/theme.go:103`），输入框聚焦用 `TextMuted` 边 + `AccentTint` 光晕（`internal/uikit/components.go:259-274`） | 按钮、tab、列表行的焦点环和主操作同色；输入框的焦点又是另一套 | **建议加 `Ring`**。一个 `Palette.Ring` 角色（Light 可用 `TextMuted`，Dark 同理），`Theme` 里 `t.Focus = p.Ring`；输入框保留现在的 muted 边 + 光晕。成本低（一个字段、两套值、对比度测试加一组），收益是“焦点”和“主操作”不再抢同一个颜色。P2。 |
| `--destructive` | `Danger` + `DangerButton`（`internal/uikit/components.go:194-197`） | `DangerButton` 是中性面上的红字；清空对话框又直接用 `PrimaryButton(...).Background(t.Danger)`（`internal/app/app.go:1767`） | **建议统一**。加一个 `DestructiveButton`（危险色实底、反色文字），或规定“危险确认一律用 `DangerButton`，不填充”。现在是两种写法。P2。 |
| `TextFaint` | `#8a8f88`／`#6d766e`（`internal/uikit/theme.go:49,69`） | 用于图标、禁用、占位和失焦标记线；浅色 chrome 上约 2.39:1（`docs/ui-redesign.md:399`） | **保留但收紧**。装饰性图标可以；**可点击的图标按钮不要用 TextFaint**，用 TextMuted（≥3:1）。这条要写成规则，否则树箭头、行内 × 这类唯一入口会低于非文本对比度。P2。 |
| 控件高度 | `ControlHeight` 28／`ControlHeightSmall` 22；`HeightControl` 24／`HeightLarge` 28（`internal/uikit/tokens.go:37-51`） | 四个名字，两组值；`ControlHeight` 和 `HeightControl` 只差词序，`HeightLarge` 和 `ControlHeight` 同为 28。设计规范第 6.3 节仍写“控件 24（URL 和 Send 28）”（`docs/ui-redesign.md:204`），和代码不一致 | **建议改名**：`HeightControl`／`HeightControlSmall` 管控件，`HeightRow` 管行。或者至少把文档改成 28／22，并在 tokens 注释里说明 24 只用于行。成本是一次大范围改名，建议在下一轮集中做；P2。 |

### a.3 组件

#### 按钮

- 现状：`PrimaryButton`、`SecondaryButton`、`DangerButton`、`CompactButton`、`LinkButton`、`ToolbarButton`、`IconButton`、`ChipButton`、`RowButton`、`SwatchButton`（`internal/uikit/components.go:96-243`）。没有 outline；没有尺寸参数；高度由角色决定。
- shadcn：`default`／`destructive`／`outline`／`secondary`／`ghost`／`link`；尺寸 `xs` 24／`sm` 32／`default` 36／`lg` 40／`icon` 36（`apps/v4/registry/new-york-v4/ui/button.tsx`）。Base UI 的 Button 只给状态，不给外观。
- 建议：**不加 outline**。我们的设计明确“控件不描边”（`docs/ui-redesign.md:120` 一带），outline 会引入第四种面。**不加尺寸参数**：28／22 已经覆盖桌面密度，再加 32／36 会让表格和工具栏重新长出两套高度。**要补的是危险变体**（见 a.2）和 `LinkButton` 的禁用态：`LinkButton` 在 hover 时直接换面（`internal/uikit/components.go:159-161`），没有检查 `IsDisabled()`；虽然 MyGo 的 `Hovered()` 对禁用元素通常为 false，但这是靠库的隐含行为，不是 kit 的约定。P3。
- 值不值得做：危险变体值得（低）；outline／尺寸参数不做；LinkButton 的禁用检查值得（极低）。

#### 表单：Label、Description、Error

- 现状：`SettingsRow` 只负责“标签左、控件右、hint 在控件下”（`internal/uikit/components.go:1081-1128`）；错误信息没有角色，页面各自画 `ui.Text(...).TextColor(t.Danger)`，全仓库约 85 处。`Input` 也没有错误态参数（`internal/uikit/components.go:245-257`）。
- 参考：shadcn 的 `FormItem` 是 `grid gap-2`；`FormLabel` 在错误时 `data-error=true` 并变危险色；`FormControl` 设 `aria-describedby` 和 `aria-invalid`；`FormDescription` 是 14px muted；`FormMessage` 是 14px 危险色，空时不占位（`apps/v4/registry/new-york-v4/ui/form.tsx`、`field.tsx`）。Base UI 的 Field 也是 Root/Label/Control/Description/Error 一组，`data-disabled`／`data-invalid` 由根传下去。
- **MyGo 已经提供了等价能力**：`ui.Field(c, label, control).Description(...).Error(...)`、`.Invalid(bool)`、`.DescribedBy(...)`、`.ErrorMessage(...)`；有错误时控件会被标为 invalid，文本输入画危险色边（`$MYGO/docs/ui/form.md`）。我们的 `SettingsRow` 绕开了 `ui.Field`，所以这些接线全部丢了。
- 建议：加 `FormRow(label, control, description, err)`，内部用 `ui.Field`，把 `Description`／`Error` 交给库；`Input` 加 `Invalid(bool)` 或 `InputState`，让 `FieldFace` 在 invalid 时画危险色边（而不是只靠外面的红字）。先在证书编辑器、规则表单、Mocks 表单、Client setup 四处用起来，再逐步替换手写红字。
- 值不值得做：**最值得**。成本中等（一个组件 + 一个输入态 + 四处迁移），收益是错误信息和控件真正绑定，读屏能读，视觉也不再是“控件下面飘一行红字”。P1。

#### 对话框

- 现状：`Modal` 用 `ui.DialogBase`，面板 20 内边距、12 间距、圆角 8、hairline + 阴影（`internal/uikit/components.go:709-737`）；`DialogTitle` 只是 15px/500 的文本（`internal/uikit/components.go:739-741`）；`DialogFooter` 有上边线（`internal/uikit/components.go:513-517`）。
- shadcn：`p-6 gap-4 rounded-lg border shadow-lg`，标题 18px semibold，描述 14px muted，footer 右对齐无上边线，右上角有 X（`apps/v4/registry/new-york-v4/ui/dialog.tsx`）。Base UI 的 Dialog 默认把焦点移入、关闭后还给 opener，`initialFocus`／`finalFocus` 可调。
- 建议：**对话框要有名字**。MyGo 的 `DialogBase` 把面板标成 `RoleDialog`，但没有 Label；`DialogTitle` 是普通文本，所以读屏看到的是“未命名对话框”。最小改法：给 `Modal` 加一个 `title string` 参数（或 `ModalLabel`），在 `panel.Label(title)` 上设名字；调用方把现有的 `DialogTitle` 文案传进去。成本低，收益明确。P1。
- 次要：footer 的上边线是设计里“线只画在区域之间”的一个例外；shadcn 的 footer 没有线。可以去掉，靠间距分组。P3。
- 值不值得做：命名值得（低）；footer 线可做可不做。

#### Card、Panel、Sheet

- 现状：没有通用 Card。`SettingsGroup` 是“标题 + 一条下边线”，不是卡片（`internal/uikit/components.go:1055-1071`）；但 Sessions 的右栏、Runner 的列表、向导的预览各自画 `Border(1) + Radius`，内边距 8／12 不等。
- shadcn：Card 是 `gap-6 py-6 px-6 rounded-xl border shadow-sm`，Header/Content/Footer 有固定内边距（`apps/v4/registry/new-york-v4/ui/card.tsx`）。Sheet 是侧滑面板，`w-3/4 sm:max-w-sm`，header/footer `p-4`。
- 建议：**不要引入 Card 堆叠**，那会推翻“三种表面、线只画在区域之间”的方向。需要的是一个 `Panel`：`RadiusPanel` + 1px Hairline + 统一 12 内边距，只给“确实要框住一块”的地方（Sessions 右栏、Runner 结果、向导预览、MCP catalog）。Sheet 不需要：Tools 是独立窗口，Sessions 是三栏，不是抽屉。
- 值不值得做：Panel 值得（中）；Card/Sheet 不做。

#### Tabs、Segmented

- 现状：`Tabs` 用 `TabsBase`，选中是 `Fill` 面 + 正文色，高度 22（`internal/uikit/components.go:765-800`）；`SubTabs` 只变文字色；`Segmented` 是凹槽 + 浮起滑块，24 高（`internal/uikit/components.go:927-948`）。
- shadcn：`TabsList` 是 `h-9 p-[3px] bg-muted rounded-lg`，trigger 选中 `bg-background shadow-sm`；另有 `line` 变体（下划线）。Base UI 的 Tabs 用 `data-active`、`data-disabled`、`data-orientation`，方向键切换、Home/End 到两端。
- 建议：**保留两套**。Tabs 是“换一个视图”，Segmented 是“换一个互斥模式”，语义不同；我们的 `SubTabs` 已经解决了两级标签页。真正的问题是页面把两个 Segmented 叠在一起（Sessions，见 b 节），不是 kit 缺变体。不加 `line` 变体：面板头已经够密，下划线会增加一条水平线。P3。
- 值不值得做：不加。

#### Table

- 现状：行高 24，无 hover，选中是 `AccentTint` + 2px 标记线（`internal/uikit/components.go:961-991`）；表头由 MyGo 画，字重仍是 600（`docs/ui-redesign.md` 第 13 节第 12 条承认）。
- shadcn：行有 `border-b` 和 `hover:bg-muted/50`，选中 `bg-muted`，表头 `h-10 px-2 font-medium`。
- 建议：**保留无 hover、24 行高**；这是桌面抓包工具的密度，shadcn 的 40 行高不适合。表头 600 是唯一违反“两个字重”的地方；MyGo 没有暴露表头字重的 API，先记录，等库或自绘表头再改。P3，不做。
- 值不值得做：不做。

#### Empty

- 现状：`EmptyState` 是一个居中的 Column：16px 图标、`Heading`（15）、`Hint`（13）、一个 `actions func()`（`internal/uikit/components.go:634-644`）。
- shadcn：`Empty` 是 `flex-1 center gap-6 p-6 md:p-12`，`EmptyHeader` 限宽 `max-w-sm`，`EmptyMedia` 有 `default`／`icon` 两种（icon 是 40px 圆角块），`EmptyTitle` 18px medium，`EmptyDescription` 14px muted，`EmptyContent` 限宽并排按钮（`apps/v4/registry/new-york-v4/ui/empty.tsx`）。
- 建议：**不照搬 40px 图标块**，设计规范明确空状态图标 16px、不加框。可以补一个 `EmptyActions`（水平 gap-2、居中）和一个 `EmptyContent`（限宽 320），让两个按钮的空状态不用每次手写 Row。成本极低。P3。
- 值不值得做：可选，低。

#### Toast（变基后）

- 现状：`Toasts` 的面板宽 360，图标 + 标题/描述一列，动作是同行的 22pt ghost link，关闭按钮在后（`internal/uikit/components.go:672-703`）。变基后的图版 `components-toasts-light.png` 和 `-dark.png` 里，“Request deleted / Undo” 在同一行，符合新提交 `a27cdb7` 的意图。
- 问题：动作在静止时用 `TextMuted`（`internal/uikit/components.go:689`），和关闭按钮同色；在深色 toast 上它读起来像禁用链接，而不是“撤销”。shadcn 的 toast action 用前景色/主色，关闭才是 muted。
- 建议：动作静止用 `t.InverseText`（toast 是反色面），hover 再用 `t.InverseText` 加 12% 反色面；关闭保持 `InverseText` 的 70% 不透明度。成本一行。P2。**已实施。**
- 值不值得做：值得，极低。

#### 列表选中：`ListTheme` 必须由调用方记得

- 现状：`ListTheme` 把 MyGo 列表的 `Accent` 换成 `AccentTint`（失焦换成 `Fill`），行自己画 `RowMarker`（`internal/uikit/components.go:961-991`）。这是“数据列表”的唯一正确画法，但它是一个**可选调用**。
- 反例：MCP catalog 的 `ui.List` 直接 `c.SetTheme(t)`，没有套 `ListTheme`（`internal/app/mcp_ui.go:783-802`）。结果选中行是实心 `Accent`：浅色下黑底白字，深色下白底黑字，比页面里任何东西都响，而且描述文字失去 muted 色。图版 `screens-mcp-catalog-light.png` / `-dark.png` 一眼能看到。
- 建议：加一个 `DataList(c, state, n, rows)` 包装，内部强制 `ListTheme` + `RowMarker`；或者至少把 `ListTheme` 改名成 `DataListTheme` 并在 `ui.List` 的每个调用点做一次审计。成本低，收益是这类“菜单式选中”不会再出现。P1。
- 值不值得做：值得。

### a.4 状态与无障碍（对照 Base UI）

- **状态属性**：Base UI 把状态放在 `data-*` 上：`data-highlighted`（指针/键盘高亮）、`data-selected`、`data-disabled`、`data-popup-open`、`data-pressed`、`data-open`／`data-closed`、`data-starting-style`／`data-ending-style`、`data-side`／`data-align`（`packages/react/src/utils/popups/Common*DataAttributes.ts`、`select/item/SelectItemDataAttributes.ts`、`tabs/tab/TabsTabDataAttributes.ts`）。MyGo 是原生控件，状态是 Go 方法（`Hovered`、`Pressed`、`IsDisabled`、`Checked`、`Expanded`），角色和名字由 `Label`／`Role` 给。我们的 kit 只建模了 selected／disabled／hover／pressed，**没有建模“键盘光标”**，也没有建模“触发器的 popup 打开态”。
  - 后果一：脚本列表里 `Broken` 行有浅底、`Debug headers` 行是选中底，两行都像被选中（图版 `screens-tools-scripts-light.png`）。这是 MyGo 列表的 cursor 和我们的 `Selected` 各画各的。
  - 后果二：`MenuSelect` 打开时触发器不保持高亮；Base UI 的 `data-popup-open` 会让触发器在菜单打开期间保持选中面。Tools 的 Scenario 菜单、body 格式菜单都受影响。
  - 建议：定义 `Cursor` 面（1px accent inset 或 `Fill` 无圆角）并写进 6.5；菜单触发器在 `Expanded()` 时用 `FillHover`。成本低到中；P2。
- **键盘**：Base UI 的约定是 Select 支持方向键、Home/End、typeahead、Enter/Space、Escape；Menu 支持方向键、Home/End、typeahead、Escape、Tab 关闭；Tabs 方向键自动激活、Home/End；Dialog 焦点陷阱、Escape、关闭后还原。MyGo 的 `TabsBase`、`ListBase`、`MenuBase`、`SelectBase`、`DialogBase` 已经实现了这些；`uikit.SidebarList` 自己实现了 ↑↓／Home/End／首字母跳转（`internal/uikit/sidebar.go`）。**不需要重写**。要补的是自定义行：`RowButton` 是 ButtonBase（Enter/Space 可用），`QueryChip` 的编辑/移除是两个独立焦点，读屏会读成两个按钮——这是对的，但视觉上它们贴在一起，Tab 顺序需要确认。
- **禁用**：设计规则是“禁用放在外层”（`docs/ui-redesign.md:6.1`）。`RowButton` 检查 `IsDisabled()`（`internal/uikit/components.go:223`），`SwatchButton`、`QueryChip`、`LinkButton` 没有显式检查；目前靠 MyGo 的 `Hovered()` 对禁用元素返回 false。建议加一个 `disabledFace` 帮手，或至少在组件注释里写“调用方必须外层 Disabled”。P3。
- **命名**：`DialogTitle` 不命名对话框（见 a.3）；`EmptyState` 的标题不是 `RoleHeading`；`SettingsRow` 的标签没有和控件绑定（见 a.3 表单）。这三条是读屏最直接的损失，前两条成本低，第三条和 FormRow 一起做。

### a.5 不建议照搬的地方

1. **outline 按钮**：我们的控件不描边，边线只属于输入框、浮层和对话框。加 outline 会破坏“三种表面”的规则。
2. **填充式 destructive 到处用**：红色只表示错误/失败；危险确认用一个明确的 `DestructiveButton` 就够，不要每个删除按钮都填红。
3. **Card 堆叠和 `shadow-sm`**：桌面工具靠表面和间距分层，不靠一叠一样的圆角卡片。
4. **shadcn 的 36px 控件**：桌面密度是 24 行、28 控件；36 会让表格和工具栏松掉。
5. **Sheet**：Tools 是独立窗口，Sessions 是三栏，不需要侧滑抽屉。
6. **`--accent` 的 hover 语义**：我们的 `Accent` 是墨色主操作；照搬会把所有 hover 面变黑。

---

## (b) 页面层

每个新页面一节。图版路径都在 `docs/design/`；行号是 `gallery-plates` 变基到 `ecfced7` 后的。每条先写最严重的问题。

### 1. Tools · Rules

图版：`screens-tools-rules-light.png` / `-dark.png`。

- **P1 规则列表的第二行被截断。** 图版中部 `Hide analytics` 的 `@Goo…`、`Mock user profile` 的 `GET api.example...` 都只剩半句；`internal/app/rules.go:635` 的名字是 `SingleLine`，下面的匹配条件/App 也没有换行或 tooltip。改法：第二行允许两行，或把匹配条件放到右侧详情的标题下；至少给整行一个 `Tip` 显示全文。P1，因为用户靠这一行分辨规则。
- **P2 测试框太窄。** 图版顶部 `Test: GET https://api.example.com` 在 1040 宽的 Tools 窗口里被切；`internal/app/rules.go:450-490` 的测试行和 Scenario 菜单挤在一行。改法：测试框独占一行，Scenario 菜单放它下面；或测试框可展开成多行。P2。
- **P2 Body 编辑器空白过大。** `{"ok": true}` 占 180 高，下面全是空；`internal/app/rules.go:758`。改法：按内容自适应（最小 120），空内容时显示占位而不是一块空编辑区。P3。
- **P3 标题重复。** 窗口标题 `Tools`、页标题 `Rules`、左栏选中 `Rules` 三处；`internal/app/tools.go:133-150`。改法：窗口里不再画页标题，或左栏分类不重复。P3。

### 2. Tools · Host profiles

图版：`screens-tools-hosts-light.png` / `-dark.png`。

- **P1 冲突注解被裁。** 图版第 3 行右侧 `overrides Default line 1 (10.1.0...` 被编辑器边缘切掉；`internal/app/tools.go:455-475` 把冲突写成 inlay，编辑器只有约 480 宽。改法：inlay 改成短标记（如 `!`）加悬停全文，或在编辑器下方列出冲突；P1，因为“谁覆盖谁”是 hosts 的核心信息。
- **P2 编辑器太窄。** 三栏 180 + 268 + 编辑器，在 1080 窗口里编辑器约 480；长行和注解都放不下。改法：Tools 窗口允许收起分类栏，或把冲突列表放到下方；P2。
- **P2 错误文字叠在行尾。** 第 7 行 `10.0.3.4O is not an address` 是红色文字直接跟在行内容后，和代码抢注意力；`internal/app/tools.go:444-449`。改法：用 diagnostic underline + 行下注释，或状态栏一行；P3。

### 3. Tools · Scripts

图版：`screens-tools-scripts-light.png` / `-dark.png`。

- **P1 代码行横向被裁。** 图版里 `sign(pm.request.bod`、`Date.now() - Number(pm.variabl` 都在编辑器右边缘被切；`internal/app/scripts.go:750-752` 的 `editor.View` 放在固定宽 Box 里，没有 soft wrap 或横向滚动。改法：soft wrap，或横向滚动 + `EdgeFade`；P1，因为脚本的可读性就是这个页面的全部。
- **P2 两行同时像选中。** `Broken` 行有浅底、`Debug headers` 行是选中底；`internal/app/scripts.go:639-670` 的 `Selected` 和 MyGo 列表的键盘 cursor 各画各的。改法：定义 cursor 样式（1px accent inset），或让 cursor 跟随选择；P2。
- **P2 关键字是红色。** `import`、`export function`、`console.log` 在 `EditorTheme` 里偏红，和真正的错误红混在一起；`internal/app/scripts.go:749`。改法：关键字用 `Text` 或 `Info`，红色只给诊断；P2。
- **P3 三级标题。** `Tools` → `Scripts` → `Rule scripts / Addons`；`internal/app/tools.go:133-150`、`internal/app/scripts.go:579-610`。改法：去掉页标题，保留 tabs；P3。

### 4. Tools · Mocks

图版：`screens-tools-mocks-light.png` / `-dark.png`。

- **P1 响应文件路径被裁。** 图版 `Response from` 显示 `/home/demo/.vscroxy/mocks/user.j`；`internal/app/rules.go:748` 的输入框和 File/Folder 按钮同排，字段太窄。改法：字段最小宽度 + 末尾省略 + tooltip 全文，或显示 basename 与目录两行；P1，因为 mock 的来源文件是关键信息。
- **P2 Body 标签浮在高编辑器中间。** `Body` 出现在 180 高编辑器的垂直中间，而不是第一行；`internal/app/rules.go:758` 的 `ui.Field` 在 Form 行里按单行控件居中。改法：用多行字段（`SettingsRowMultiline` 的等价物）让标签对齐第一行；P2。
- **P2 列表第二行和测试框同样被裁。** 图版 `GET api.example.... · Offline demo` 和顶部测试框；`internal/app/rules.go:635`、`:450-490`。改法同 Rules；P2。
- **P3 `Save rule` 是唯一主操作，好。** 不需要改。

### 5. Tools · Scenarios

图版：`screens-tools-scenarios-light.png` / `-dark.png`。

- **P2 场景名是输入框但看起来像标题。** 图版顶部 `Offline demo` 没有标签，右侧紧挨开关；`internal/app/scenarios.go:162-165`。改法：显示标题文本，双击或 `⋯` 重命名；或加 `Name` 标签；P2。
- **P2 状态文案不清楚。** `On: its rules apply, with the rules in none.`（`internal/app/scenarios.go:184-188`）读不懂；改法：`On: its rules apply. Rules with no scenario also apply.`；P2。
- **P3 底部说明离列表太远。** `Rules keep their place in the Rules order...` 在页面最底部（`internal/app/scenarios.go:215`），和规则列表之间隔着整屏空白；改法：放到列表头或 tooltip；P3。
- **P3 每行一个 `Edit`。** 规则行右侧重复 `Edit`（`internal/app/scenarios.go:208-211`）；改法：行点击打开，`Edit` 只在 hover 出现；P3。

### 6. Client setup

图版：`screens-client-setup-light.png` / `-dark.png`。

- **P1 唯一主操作是禁用的。** 抓包停止时 `Open terminal` 是灰色（`internal/app/client_setup_ui.go:63-76`、`:105-130`），页面没有“启动抓包”的入口。图版顶部还有 `Capture stopped` 徽标。改法：停时给 `Start capture` 主操作，或一个跳到标题栏 pill 的链接；`Open terminal` 继续禁用；P1。
- **P2 一段四行的技术说明。** `SSL_CERT_FILE, REQUESTS_CA_BUNDLE, ...` 占四行（`internal/app/client_setup_ui.go:117-126`），把页面压得很长；改法：收进 `What gets set` 折叠或 tooltip；P2。
- **P2 CA bundle 的警告没有出路。** 图版黄色 `System CA bundle unavailable...`（`internal/app/client_setup_ui.go:117-120`）；真实首次启动也会这样，但页面没说怎么生成。改法：`Start capture to create it` 或隐藏该行；P2。
- **P3 底部三个 secondary。** `Phone / tablet setup…`、`Diagnose…`、`Close` 同权重；`internal/app/client_setup_ui.go:37-52`。改法：Close 用右上角 X 或 LinkButton，保留前两个；P3。

### 7. Diagnostics

图版：`screens-diagnostics-light.png` / `-dark.png`。

- **P1 第 2 步通过、第 1 步失败，自相矛盾。** 图版 `Listener` 红叉 `Capture is stopped`，下一行 `System proxy` 绿勾 `HTTP and HTTPS point at this app`；`internal/app/diagnostics.go:122-150` 的 system proxy 检查只看 OS 配置，不看 listener。改法：listener 停止时 system proxy 标成 `Points at this app, but capture is stopped`，或直接 fail；P1，因为诊断页的信任建立在步骤一致上。
- **P2 失败行没有展开箭头。** 整行可点，但只有悬停 tooltip `Show details`（`internal/app/diagnostics.go:310-340`）；图版第 1 行展开、其余收起，看不出能点。改法：行首 chevron，展开时旋转；P2。
- **P2 顶部和底栏重复“read-only”。** 顶部 `Nothing changes until you press a fix`（`internal/app/diagnostics.go:273`），底栏 `Connection diagnostics · read-only until you press a fix`（`internal/app/shell.go:337-338`）。改法：只留一处；P3。
- **P3 下方大片空白。** 5 行检查后没有内容；改法：把失败证据/连接日志放进展开区；P3。

### 8. HTTPS rules

图版：`screens-https-rules-light.png` / `-dark.png`。

- **P2 三个名字指同一个东西。** 侧栏/命令是 `HTTPS rules`（`internal/app/app.go:2423-2425`），对话框标题是 `SSL proxying`（`internal/app/app.go:4273`），Settings 分类是 `HTTPS`。改法：统一成 `HTTPS decryption` 或 `HTTPS rules`，对话框标题跟入口一致；P2。
- **P2 域名文本域空白过多。** 100 高只有 3 行（`internal/app/app.go:4281`）；改法：高度随内容 3–6 行，或改成一行一个 domain 的 chips；P3。
- **P2 分组线不一致。** 域名和 Local apps 之间有 `Divider`，`Decrypt every domain` 和域名标题之间没有（`internal/app/app.go:4274-4283`）；改法：统一分组；P3。

### 9. API · endpoints

图版：`screens-api-endpoints-light.png` / `-dark.png`。

- **P1 端点详情只有原始 JSON。** 图版右侧 `OPERATION · schemas and references` 下面是 operation 的 JSON（`internal/app/apis.go:263-273`），没有参数表、请求体、响应码。改法：结构化 Parameters / Request body / Responses 三组，原始 JSON 进 `Show source`；P1，因为“Endpoints”页面的职责就是回答这个操作要什么、回什么。
- **P2 端点列表的 method 列不齐。** 图版 `/projects GET`、`/projects/{id} DELETE` 的方法终点不齐；`internal/app/apis.go:227-235` 把 method 放在 `SidebarItem` 的 children 里，宽 48 但左对齐。改法：method 固定列放在路径前，或右对齐；P2。
- **P2 长说明夹在标题和 schema 之间。** `Uses the first effective server...`（`internal/app/apis.go:261`）是一整行；改法：移到 `Open in Requests` 的 tooltip 或页脚；P2。
- **P3 每行前的圆点没有意义。** `SidebarItem` 没有图标时放 `SidebarDot`；API 端点列表的槽位应该给 method，不是圆点；P3。

### 10. Runs · records

图版：`screens-runs-records-light.png` / `-dark.png`。

- **P1 页面叫 History，入口叫 Runs。** 图版标题 `History`（`internal/app/experiments_ui.go:281`），但 `pageRuns` 的注释是 run records（`internal/app/app.go:55`），侧栏/命令也叫 Runs。改法：统一命名，或标题写 `Runs · retained attempts`；P1，因为用户不知道自己在哪。
- **P1 底栏说了无关的话。** 图版底栏 `4 requests · Local environment`（`internal/app/shell.go:347-355` 的 fallback），和运行记录无关。改法：`pageRuns` 返回 `N runs · baseline #1` 或留空；P1，错误的状态信息比没有更糟。
- **P2 顶部隐私说明太长。** `Private local snapshots may contain credentials...`（`internal/app/experiments_ui.go:298`）一整行；改法：收进 info tooltip；P2。
- **P2 左列表只有两行，右栏大片空白。** 行里只有 `#2 · 时间`（`internal/app/experiments_ui.go:315-325`），method/URL/status/duration 都在右侧；改法：行内显示 method + status + duration，或加对比摘要；P2。

### 11. Runs · running

图版：`screens-runs-running-light.png` / `-dark.png`。

- **P1 运行中真正可用的动作是 secondary。** `Run collection` 是 primary 但禁用，`Cancel run` 是 secondary 且可用（`internal/app/runner.go:368-380`）；图版底栏四个按钮，主操作是灰的。改法：运行时把 Cancel 变成主/危险操作，Run 换成 `Running…` 状态；P1。
- **P2 Iterations 没有标签。** 图版只显示 `3`（`internal/app/runner.go:267`）；改法：加 `Iterations` 标签或 stepper；P2。
- **P2 底栏长句。** `Running… · displaying latest 1000 results; closing keeps a run active`（`internal/app/runner.go:363-367`）；改法：只留 `Running…` 和计数，说明进 tooltip；P3。
- **P3 结果区一行 + 大片空白。** 改法：显示当前步骤/进度条；P3。

### 12. Runs · results

图版：`screens-runs-results-light.png` / `-dark.png`。

- **P1 失败原因被压在一个全宽按钮下面。** 图版 `Inspect retained run` 是全宽 secondary，失败详情 `✕ Available / expected 200, received 503` 在它下面的 125 高滚动区（`internal/app/runner.go:338-360`）。改法：选中失败行时，把失败详情放在结果列表右侧或上方，`Inspect` 移到行尾；P1，因为“为什么失败”是这个页面的重点。
- **P2 四个底栏按钮同权重。** `Load test…`、`Close`、`Cancel run`、`Run collection`（`internal/app/runner.go:368-380`）；改法：Close 用 X，Cancel 只在运行时出现；P2。
- **P3 结果行摘要右对齐、11px。** 名称和 `201 · 43 B · 47 ms · 1/1 tests` 之间距离很大；改法：固定列（status / duration / tests）；P3。

### 13. Sessions

图版：`screens-sessions-light.png` / `-dark.png`。

- **P1 两排 Segmented 看起来同级。** 图版 `Request | Response` 和 `Headers | Body | Messages | Connection` 上下相邻（`internal/app/sessions_ui.go:214-225`），实际是两级。改法：第一级用 Tabs，第二级用 SubTabs；或合并成 `Response · Body` 一行；P1。
- **P2 标题和备注输入框没有可见标签。** `after.json` 和 `Cache behaviour...` 填了值后像按钮/标题（`internal/app/sessions_ui.go:74`、`:211`）。改法：保留 label，或加编辑图标；P2。
- **P2 顶部四个动作同权重。** `Unpin`、`Use as left`、`Compare with left`、`Reveal credentials`（`internal/app/sessions_ui.go:169-193`）。改法：`Compare` 一个主操作，其余进 `⋯`；P2。
- **P3 右栏大片空白。** 改法：body 编辑器填满，或显示 exchange metadata；P3。

### 14. Collection wizard

图版：`screens-collection-wizard-light.png` / `-dark.png`。

- **P2 右侧预览大半空白。** 图版右栏只有 `GET /v1/projects`、origin、一行 `/v1/projects 200 47 ms`（`internal/app/collection_wizard_ui.go:91-130`）；改法：显示将生成的请求、变量、示例，或 response 预览；P2。
- **P2 左侧 Merge endpoints 行信息太少。** 只有 method、path、`×1`（`internal/app/collection_wizard_ui.go:120-160`）；改法：加 host/status 或折叠重复；P3。
- **P3 步骤条 6 步在 1080 宽下每步很窄。** `internal/app/collection_wizard_ui.go:23-58`；改法：只显示当前/已完成/下一步，或缩短标签；P3。
- **P3 `Split` 在只有一个 endpoint 时禁用，没有说明。** 改法：tooltip；P3。

### 15. Postman import

图版：`screens-postman-import-light.png` / `-dark.png`。

- **P1 只有 OK，没有“查看导入结果”。** 图版底栏只有一个 `OK`（`internal/app/postman_ui.go:268-272`）；导入完成后用户无法跳到新集合。改法：主操作 `Review collection`，`OK` 改 `Done` secondary；P1。
- **P2 五条 bullet 全文，无分组。** `internal/app/postman_ui.go:262-266`；改法：按 Auth / Body / Scripts / Variables / Examples 分组，每组一行 + 数量，展开看详情；P2。
- **P2 标题是状态句。** `Imported “Workspace API” · 14 requests`（`internal/app/postman_ui.go:261`）；改法：标题 `Import review`，状态作副标题；P2。
- **P3 每条 1–2 行。** 改法：每条 ≤1 行 + `Details`；P3。

### 16. Variables · environment

图版：`screens-variables-environment-light.png` / `-dark.png`。

- **P1 两个 “Local” 控件没有标签。** 图版上面是环境选择（带 chevron），下面是环境名输入框（`internal/app/app.go:3657`、`:3665`）；两个都显示 `Local`，用户不知道哪个是选、哪个是改名。改法：选择器加 `Environment` 标签，输入框加 `Name` 标签；或把改名放进 `⋯`；P1。
- **P1 所有 Current 值默认掩码。** 图版 `projectId` 也显示 `••`；`internal/app/variables_ui.go:137-142` 对每个 Current 都 `Password()`，只有 `Initial` 用 `secret` 判断（`:126-132`）。改法：Current 也只对 `r.Credential || sensitiveVariable(r.Name)` 掩码；P1/P2，因为非敏感值被藏起来，表格读不了。
- **P2 行尾三个图标被表格右边缘裁掉。** 图版最右的第三个图标只露一半；`internal/app/variables_ui.go:147-176` 的动作列宽 `HeightControl * 4`，贴着表格边。改法：预留 4×24 + padding，或把删除放进 `⋯`；P2。
- **P2 长说明。** `Initial values are shared...`（`internal/app/app.go:3667`）；改法：tooltip；P3。

### 17. Certificates · editor

图版：`screens-certificates-editor-light.png` / `-dark.png`。

- **P1 Save 在首屏之外。** 560 高的 Settings 窗口里，图版在 `Add client certificate` 处就结束了，`Save certificates` / `Discard changes` 在 Trusted roots 之后（`internal/app/certificate_ui.go:38-180`）。改法：Settings 页底部 sticky footer，或把 Save 放到窗口工具栏；P1，因为编辑器的主操作不可见。
- **P2 每个 client 用四行 stacked label。** Host / Certificate file / Key file / Passphrase 各一行（`internal/app/certificate_ui.go:56-112`），和 Settings 其他页的左标签右控件不一致；改法：每个 client 一行摘要 + 展开编辑，或 `SettingsRowMultiline`；P2。
- **P2 PEM/PKCS#12 放在 Host 行右侧。** `internal/app/certificate_ui.go:57-72`；它决定下面字段的形态，却和 Host 并排。改法：格式单独一行，或作为字段前缀；P2。
- **P2 两个 helper 文案不同。** `Sealed locally; never exported` 和 `Saved passphrase in use`（`internal/app/certificate_ui.go:100-114`）；改法：统一一个 hint + 状态；P3。

### 18. Protobuf inspector

图版：`screens-protobuf-inspector-light.png` / `-dark.png`。

- **P1 解码内容在窗口底部被裁。** 图版右下 `attrs: {` 被切在 800 处；`internal/app/protobuf_ui.go:219-235` 的 detail 是 `Fill()` + `Scroll`，但 pane 没有给底部留出 footer 的高度。改法：detail 明确 `Grow(1).MinHeight(0)`，pane 底部留 8–12 间距；P1，因为长消息看不全。
- **P2 请求面板占一半高度但只有一行 header。** 图版上半只有 `Content-Type: application/grpc`；改法：请求无 body 时把 split 调向响应，或折叠请求面板；P2。
- **P2 `Copy decoded` 是全宽 secondary。** 它压在消息详情上方（`internal/app/protobuf_ui.go:220-226`）；改法：放到面板头 `⋯` 或图标按钮；P2。
- **P2 URL 在 1280 宽下被裁。** 图版 `https://api.exam`；captured header 的 URL 字段没有横向滚动/省略。改法：URL 可横向滚动，或缩小 Send 按钮；P2。

### 19. SSE session

图版：`screens-sse-session-light.png` / `-dark.png`。

- **P2 标题把状态拼进去。** `SSE session · Connected`（`internal/app/sse_session_ui.go:51`）；改法：标题 `SSE session`，状态用 `StatusPill`；P2。
- **P2 `Copy event ID` 是全宽按钮。** 它在 payload 上方（`internal/app/sse_session_ui.go:121-123`），像一个主操作；改法：图标按钮或放进 `⋯`；P2。
- **P2 连接中仍显示禁用的 `Connect SSE`。** `internal/app/sse_session_ui.go:54-69`；改法：连接中隐藏 Connect，给 `Reconnect` / `Stop`；P2。
- **P3 底部技术脚注很长。** `Latest 200 events · 64 KiB preview...`（`internal/app/sse_session_ui.go:139-148`）；改法：tooltip；P3。

### 20. Investigation board

图版：`screens-analysis-board-light.png` / `-dark.png`。

- **P1 方法学段落比数据还长。** `Recorded per-capture durations, not elapsed session time. P50 uses nearest rank...`（`internal/app/analysis.go:125`）占三行；改法：删掉或放 info tooltip；P1，因为摘要页应该先给结论。
- **P2 顶部三个同权重按钮。** `Summary`、`Compare two`、`Inspect focused`（`internal/app/analysis.go:84-97`）；`Summary` 是当前视图却像按钮。改法：Summary/Compare 用 Tabs，Inspect 用主按钮或行点击；P2。
- **P2 批量工具栏四个按钮。** `Replay…`、`Export HAR…`、`Export CSV…`、`Clear selection`（`internal/app/workbench.go:123-132`）；改法：Replay 主，Export 进 `⋯`，Clear 用 LinkButton；P2。
- **P3 右栏大片空白。** 改法：加耗时分布或让慢请求列表占满；P3。

### 21. Setup guide

图版：`screens-setup-guide-light.png` / `-dark.png`。

- **P1 第一步没有“开始抓包”。** 图版第 1 步只有 `Diagnose capture...`（`internal/app/guide.go:168-190`），标题却是 `Start capturing`。改法：代理停时给 `Start capture` 主操作，Diagnose 作为次操作；P1。
- **P2 列表 chrome 还在。** 图版上方仍有搜索框、`All / HTTP / HTTPS / ...` 协议 tabs、Traffic 行（`internal/app/workbench.go:116-140` 的 captureToolbar + guide 分支）；改法：guide 时隐藏过滤栏和协议 tabs，只留标题栏；P2。
- **P2 `Skip guide` 是卡片外的文字按钮。** `internal/app/guide.go:104-113`；改法：卡片底部右对齐的 LinkButton，或标题栏；P2。
- **P3 标题和状态重复。** `Start capturing` + `Capture is stopped`；改法：标题保留，状态一行；P3。

### 22. MCP catalog

图版：`screens-mcp-catalog-light.png` / `-dark.png`。

- **P1 选中行是实心 Accent 条。** 浅色黑底白字、深色白底黑字；`internal/app/mcp_ui.go:783-802` 没有套 `ListTheme`，描述文字也失去 muted 色。改法：`c.SetTheme(uikit.ListTheme(t, focused))` + `RowMarker`，或统一用 a.3 的 `DataList` 包装；P1，因为这是全屏最响的元素，且和其他数据列表不一致。
- **P1 三套 Tools/Resources/Prompts 导航。** 请求面板 tabs（`internal/app/mcp_ui.go:249-286`）、响应面板 tabs（`:524-560`）、catalog 的 Segmented（`:768-775`）都在同一屏。改法：catalog 只保留一套筛选，去掉响应 tab 里的 Catalog 或请求 tab 的重复；P1/P2，因为用户不知道哪一套在控制什么。
- **P2 `Call tool` 和 `Refresh` 分居两侧。** 左边工具行尾是 `Call tool`，右边 catalog 头是 `Refresh`（`internal/app/mcp_ui.go:769-774`、`:249-260`）；改法：Refresh 放 catalog 标题行，Call tool 作为唯一主操作；P2。
- **P3 左栏参数编辑器只有 3 行。** 下面大片空白；改法：参数区可伸展，或显示 input schema；P3。

### 23. Request editor · Socket.IO

图版：`screens-editor-socketio-light.png` / `-dark.png`。

- **P2 `Copy shown message` 是全宽按钮。** 它在 payload 上方（`internal/app/socketio_ui.go:226-245`），和 SSE 的 `Copy event ID` 是同一个问题；改法：图标按钮或 `⋯`；P2。
- **P2 左栏 Event 表单只有一行 arguments。** 下面大片空白（`internal/app/socketio_ui.go:120-190`）；改法：把 session 信息/ACK 队列放进来，或让 arguments 编辑器伸展；P2。
- **P2 选中消息的身份太弱。** `In · bytes-back` 是 11px muted（`internal/app/socketio_ui.go:230-235`）；改法：用 `Body` + 方向色；P2。
- **P3 底栏和页面无关。** `4 requests · Local environment`（`internal/app/shell.go:347-355`）；改法：`pageRequest` 的 footer 说 `Socket.IO · Connected` 或省略；P3。

---

## 重排顺序（按使用频率）

“重排”指下一轮改页面的顺序，不是本轮动手。频率从高到低：

1. **请求编辑器 · Socket.IO / 协议编辑器**：调试实时协议时每次都看；先修消息身份、复制动作、左栏空白（P2）。
2. **Tools · Rules**：日常开关规则；先修列表第二行截断（P1）。
3. **Tools · Scripts**：写脚本的人每天用；先修代码行裁切（P1）和两行选中（P2）。
4. **Tools · Mocks**：日常 mock；先修响应文件路径（P1）和 Body 标签（P2）。
5. **Diagnostics**：出问题时的高 urgency 页面；先修 listener/system proxy 矛盾（P1）。
6. **Tools · Host profiles**：偶尔但高影响；先修冲突注解（P1）。
7. **Sessions**：复查证据时用；先修两级 Segmented（P1）。
8. **Runs · running / results / records**：批量测试时用；先修运行中动作层级（P1）、失败详情位置（P1）、History/Runs 命名和底栏（P1）。
9. **API · endpoints**：合同工作；先修结构化参数/响应（P1）。
10. **Variables · environment**：配置时用；先修两个 Local 和掩码（P1）。
11. **Client setup**：接入新客户端时用；先修停时主操作（P1）。
12. **HTTPS rules**：接入时用；先修命名（P2）。
13. **Certificates · editor**：接入 mTLS 时用；先修 Save 可见性（P1）。
14. **Protobuf inspector**：协议调试时用；先修底部裁切（P1）。
15. **SSE session**：协议调试时用；先修标题/动作层级（P2）。
16. **MCP catalog**：MCP 调试时用；先修选中行（P1）和三套导航（P1）。
17. **Collection wizard**：整理集合时用；先修预览空白（P2）。
18. **Postman import**：迁移时用；先修 Review collection（P1）。
19. **Investigation board**：调查时用；先修方法学段落（P1）。
20. **Setup guide**：只用一次；先修 Start capture（P1），但优先级可以低于日常页面。
21. **Tools · Scenarios**：偶尔；P2 命名和状态文案。
22. **Certificates · editor** 已在 13；**Postman import** 已在 18。

前五条（协议编辑器、Rules、Scripts、Mocks、Diagnostics）覆盖了每天都会打开的界面，建议下一轮先做这五页的 P1，再做 uikit 的 FormRow 和 DataList，最后回头处理一次性页面。

---

## 附：本轮没有画出来的页面

- **Tools 没有单独的“实验”标签页。** Tools 的五个分类是 Rules、Scenarios、Scripts、Host profiles、Mocks（`internal/app/tools.go:140-150`）；“实验”在本应用里是 Runs 的运行记录，不是 Tools 的分类。因此 Tools 的五个图版对应真实的五个标签页，Runs 的两个状态单独出图。
- **Postman 的文件选择器是原生对话框**，无法在画廊里画；图版画的是导入后的 review 对话框（`internal/app/postman_ui.go:254-274`）。
- **证书编辑器**已经有 `settings-certificates` 图版；本轮新增的 `certificates-editor` 是同一个页面的“已保存口令 + 额外根证书 + 未保存修改”状态，不是另一个页面。
- **MCP catalog** 画的是响应面板的 Catalog 标签页；MCP 的工具调用、时间线和 Result 在已有的 `editor-*`／`requests-mcp-*` 快照矩阵里，不在本轮画廊条目里。

---

## (c) uikit-q 实施记录（2026-11-15）

uikit 前五条已经实现，页面层只重排了 Rules／Scripts／Mocks 三页。

- **表单错误**：新增 `FormRow`（`ui.Field` + `Description`／`Error`，错误会标 `Invalid`）和 `FormRowTall`（高控件标签顶部对齐）；`InputState` 画 `Danger` 边。规则表单 23 个字段、Replay 4 个、筛选 2 个、代理／集合／Cookie／网络预设 4 个，共 33 个 `FormRow`、1 个 `FormRowTall`、6 个 `InputState`。8 处字段级红字迁到控件错误；其余 76 处 `TextColor(t.Danger)` 是页面级状态、连接日志、列表行错误或信号色，不是字段错误，保留并在下节说明。
- **数据列表**：新增 `DataList`／`DataListIndexed`，固定 `ListTheme` 和 `RowMarker`；gRPC、MCP 事件、MCP 三个目录、Protobuf、Runs、Socket.IO、MQTT、Messages 共 10 处换用。Scripts／Scenarios／Rules／Host profiles 是“选择列表”，仍用圆角 `Selected`，不是数据列表。
- **对话框名字**：`Modal` 现在接收 `title` 并设 `panel.Label(title)`；50 个调用点全部传入标题；新增无障碍树测试 `TestModalNamesItsPanel`。
- **Ring**：`Palette.Ring` Light `#636e61`、Dark `#9aa89a`；`Theme.Focus`、`InputState`／`TextArea` 聚焦边都用它。Light 对 canvas／chrome／well 为 5.19／4.73／4.48:1，Dark 为 7.44／8.06／6.59:1；与 Accent 不同色。
- **危险操作**：新增 `DestructiveButton`（`Danger` 实底、`AccentText`，对比 5.22:1／5.89:1）；清空抓包、删除资源、清空实验记录、重置 Vault 四个确认对话框使用。删除 `DangerButton` 和 `PrimaryButton.Background(Danger)`；设备“停用访问”改为普通次级按钮（可逆），反向代理“移除”改为菜单里的红色危险项。
- **Toast**：动作静止改用 `InverseText`、13 号 500 字重，hover 用 12% 反色面；关闭按钮保持 70% 反色，不再和动作同色。
- **页面**：Rules 列表第二行移到整行宽度、可换行并有 Tip；Scripts 代码编辑器 `SoftWrap`、只保留一个选中行；Mocks 增加中间省略的完整路径行和 Tip，Body 标签用 `FormRowTall` 顶部对齐。三页表单都改用 `FormRow`／`NumberField`。

仍未迁的红字类别：连接／协议页的 `problem`（WS、SSE、MQTT、gRPC、Socket.IO、MCP、Protobuf 等），页面级保存或校验错误（Rules、Vault、Certificates、Client setup、API、Sessions、Experiments、Collection wizard 等），列表行错误（Sessions、Scripts 解析错误），以及诊断／日志文本。它们不是某个控件的值错误，硬塞进 `Field.Error` 会把页面错误误读成字段错误。
