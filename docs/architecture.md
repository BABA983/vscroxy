# vscroxy 基础设施方向

状态：已定稿，实施时细化。本文规划代码层面的基础设施：要改什么、改成什么样、哪个里程碑之前必须完成。功能范围见 [功能路线图](feature-roadmap.md)，界面见 [UI 设计规范](ui-redesign.md) 和 [UX 规范](ux-guidelines.md)。

## 1. 现状

### 做得好的，要保留

- **包边界清楚**：`internal/request`（请求如何变成流量，不依赖 MyGo）、`internal/vault`、`internal/postman`、`internal/capture`、`internal/dnsmap` 各管一件事。
- **测试扎实**：`go test ./...` 全部通过，约 50 秒；UI 测试用 MyGo 的 `ui.NewTester` 按标签点击，行为覆盖面广。
- **数据安全意识强**：私有文件权限（0600）、原子写、读不了的文件绝不覆盖、Vault 用系统凭据存储密钥、系统代理有独立的恢复进程。
- **集合落盘成目录**：可以直接放进 Git，并且能检测外部修改。

### 需要改的

| 问题 | 证据 |
| --- | --- |
| `app` 是一个巨大的结构体 | 198 个字段，其中 26 个是"某个弹窗开没开"的 bool；`app.go` 3493 行 |
| 导航用整数表示 | `workspace int`、`captureSection int`、`requestSide int // 0 collections, 1 runs, 2 environments, 3 contracts` |
| 动作分散 | 共享动作表 `desktopAction` 只覆盖 3 个请求动作和命令面板；93 个菜单项各自构建 |
| 存储没有统一层 | `~/.vscroxy` 下 17 个文件，txt 和 json 混用，没有格式版本号；21 个各自独立的读写函数；原子写的 helper 只有一个 `writePrivateFile`，其他地方 5 处直接 `os.WriteFile` |
| 匹配逻辑重复 | 至少 4 套：`capture/rules.go` 的 URL 子串、`capture.hostMatches`（HTTPS 解密名单和忽略名单）、`capture.matchesBreak`、`dnsmap.nameMatches` |
| 后台任务没有统一模式 | 36 处临时写的 `go func`；流量用"互斥锁 + 待处理队列 + `Invalidate`"送到 UI，其他地方有的用 `Window.Update` |
| 反馈只有一行文字 | 168 处 `setMessage`，没有级别、动作、详情 |
| 文案写死 | 约 380 处 `ui.Text(c, "...")`，还有大量按钮、菜单、标签文字 |
| 测试文件过大 | `ui_test.go` 3706 行 |
| 没有 CI | 仓库里没有 `.github/workflows` |
| 没有日志文件和发布流程 | 61 处日志调用只打到标准输出；没有打包、签名、更新的流程 |

## 2. 目标结构

```text
cmd/vscroxy/            入口（不变）
internal/
  app/                  UI 和应用状态；按功能拆分状态和文件（仍是一个包）
  capture/              代理引擎（不变），规则执行改为调用 rules
  rules/                新：匹配器、规则、处理管线、规则测试
  hosts/                新：Host 方案（从 dnsmap 的规则部分演进）
  store/                新：带版本的本地存储、迁移、备份
  request/              不变；规则脚本复用它的 JS 沙箱
  vault/ postman/ ...   不变
  control/              M9 新：本地控制接口，供命令行和 MCP 使用
```

原则：

- **引擎不依赖 UI**：`capture`、`rules`、`hosts`、`store`、`request` 都不导入 MyGo，可以单独测试，也能被命令行和 MCP 复用。
- **`app` 保持一个包**：拆分成多个 UI 包会带来大量改动和循环依赖。改为每个功能拥有自己的状态结构和文件，`app` 只负责组合。
- **一次只做一件事**：重构不改行为，改行为不夹带重构；重构前后现有测试必须不改就能通过。

## 3. 工作项

### 3.1 状态和导航

- 导航改成有类型的值，比如 `type workspace int` 加常量，`type requestSection int` 加常量，替换现在的裸整数和注释。
- 每个功能一个状态结构：`captureState`、`requestsState`、`toolsState`、`settingsState`……`app` 只持有它们。已有的 `desktopWorkbench`、`deviceSetup`、`clientSetup` 就是这个方向，继续推广。
- 弹窗统一管理：用一个"当前弹窗"值（有类型的枚举加参数）替代 26 个 bool，天然保证"弹窗不叠弹窗"（UX 规范第 4 节）。
- 不需要大爆炸式重写：UI 改版改到哪个区域，就顺手拆哪个区域的状态。

### 3.2 动作层

把 `desktopAction` 扩展成全局的动作表：

```go
type action struct {
	id       string                 // 稳定 ID，比如 "flow.replay"，命令行和 MCP 也用它
	label    string
	group    string                 // 右键菜单分组，见 UX 规范第 6 节
	shortcut shortcut
	enabled  func() (bool, string)  // 不可用时给出原因，命令面板显示
	run      func()
}
```

- 右键菜单、`⋯` 菜单、原生菜单、命令面板、快捷键、工具栏按钮都从这张表生成。
- 每个对象类型（流量、请求、集合、规则……）有一个"给定目标，返回动作列表"的函数，现在的 `requestActions` 就是雏形。
- 动作 ID 是公开契约：M9 的命令行和 MCP 直接按 ID 调用。

### 3.3 存储层（`internal/store`）

提供一个通用的"带版本的文档"：

- **格式版本**：每个文件有 `version` 字段；读取时依次运行迁移函数升到当前版本。迁移是纯函数，每个都有测试数据。
- **原子写**：所有写入都走临时文件加重命名，权限 0600（把现在的 `writePrivateFile` 推广到所有地方）。
- **自动备份**：每次迁移前、每次覆盖重要文件前，保存到 `~/.vscroxy/backups/`，每个文件保留最近 10 份。
- **外部修改检测**：记录读取时的内容哈希，保存前比较；被外部修改时拒绝覆盖并提示重新加载（集合已经这样做了，推广到全部）。
- **读失败不覆盖**：读不了的文件进入只读状态，界面显示错误和路径（现有行为，保持）。
- **导入导出**：整个配置（设置、规则、Host 方案、脚本）打包成一个文件，用于备份和分享（路线图 M4）。

目标布局：

| 现在 | 之后 |
| --- | --- |
| `settings.json`、`ssl.json`、`ignore.txt`、`ignore-apps.txt` | `settings.json`（v2，分区：通用、外观、代理、HTTPS、抓包；白名单也在这里） |
| `rules.json` | `tools/rules.json`（v2，新的匹配器格式） |
| `script.js` | `tools/scripts/*.js`，每条规则脚本一个文件 |
| `dns.txt` | `tools/hosts/*.hosts`，每个 Host 方案一个文件，可以手改、放进 Git |
| `experiments.json`、`history.json` | `history/`（合并后的 History） |
| `investigations.json` | 迁移成 `sessions/` 下的会话文件 |
| `views.json` | 并进 `settings.json` |
| `collections/`、`env.json`、`vault.json`、`vault.key`、`apis.json`、`protobuf.pb` | 不变，只加版本号 |

迁移只做一次：新版本第一次启动时迁移，原文件移到 `backups/` 并保留一个版本周期。

### 3.4 规则引擎（`internal/rules`）

- **统一的匹配器**：URL 子串（兼容旧规则）、通配符、正则、method、App、header、GraphQL operation 名。HTTPS 解密名单、忽略名单、白名单、Host 方案、规则、断点全部使用同一个匹配器，替换现在的 4 套实现。
- **处理管线**：每个请求按固定顺序经过：白名单 → 忽略名单 → Block → Map Local / Map Remote → 规则脚本 → 断点 → 上游。每一步可以单独测试。
- **规则测试**：给一个 URL 和 method，返回每条规则是否命中、为什么（现有的 "Test URL + method" 迁移过来，并扩展到所有名单）。
- **规则脚本**：复用 `internal/request` 的 JS 沙箱（goja、`pm` API、内置库），抓包脚本和请求脚本共用一套库和 Console。
- `capture` 只负责代理和调用管线，不再自己判断匹配。

### 3.5 后台任务和 UI 更新

- 写一个小的任务工具：按 key 启动任务，同一个 key 的新任务自动取消旧任务；结果通过 `Window.Update` 送回 UI，并带代号检查，过期结果直接丢弃（MyGo 使用指南的要求）。
- 流量和 WebSocket 消息这种高频数据，保留"互斥锁 + 批量队列 + `Invalidate`"的做法（它就是为了合并高频更新），但只允许这一处例外，并在代码里说明原因。
- 现有的 36 处 `go func` 在改到相关区域时迁移。

### 3.6 通知和错误

- `notify(level, text, ...action)` 替代 `setMessage`：级别（信息、成功、警告、错误）、可选动作（撤销、查看、重试）、可选详情。展示规则见 UX 规范第 8 节。
- 面向用户的错误类型：一句话说明加原始错误，原始错误放在"详情"里并写入日志。

### 3.7 日志和诊断

- 用 `log/slog` 写到 `~/.vscroxy/logs/vscroxy.log`，按大小轮转，保留 5 份。不记录请求内容和凭据。
- "导出诊断包"：版本信息、平台、脱敏后的设置、最近的日志、连接诊断结果，打成一个 zip，用于提交问题。

### 3.8 国际化

- 所有界面文字（包括无障碍标签和 tooltip）都经过一个翻译函数，以英文原文作为 key。
- 先支持英文和简体中文；语言设置在 Settings → 通用，默认跟随系统。
- 测试固定使用英文，所以现有按标签查找的 UI 测试不受影响。
- 中文字体回退见 UI 设计规范 6.2。

### 3.9 主题

- 主题是"语义 token → 色值"的一张表（UI 设计规范第 5、6 节，`internal/uikit` 的 `Palette`）。界面代码只引用 token。
- 界面迁移完成后支持 VS Code 主题的映射和导入，同时覆盖代码编辑器的语法高亮（UI 设计规范第 8 节）。

### 3.10 测试和 CI

- **CI**：GitHub Actions，在 Linux 上跑 `go build ./...`、`go vet ./...`、`go test ./...`、`go tool mygo vet .`；macOS 和 Windows 至少跑编译。每个 PR 必须通过。
- **截图基线**：UI 截图矩阵和 uikit 组件总览（UI 设计规范第 11 节）接入 CI，改版期间每个 PR 附前后截图。
- **测试数据构造**：写一个构造假流量和假请求库的 helper，取代测试里手写的大段结构体。
- **拆分测试文件**：`ui_test.go` 按功能拆开，和被测的功能文件一一对应。
- **时间预算**：全量测试保持在 1 分钟左右；变慢时优先找出慢的测试，而不是减少覆盖。

### 3.11 性能

- 抓包数量和内存的上限、自动清理（路线图 M1），落盘文件有总量上限。
- 基准测试：10 万条流量时列表滚动、筛选、搜索的耗时；作为回归指标放进 CI（只报告，不阻塞）。

### 3.12 发布

- 用 `go tool mygo build` 打包三个平台；打 tag 时由 GitHub Actions 构建并发布到 GitHub Releases。
- app 内"检查更新"：启动时（可关闭）查询 GitHub Releases，有新版本时提示，不自动安装。
- 有证书后再加 macOS 签名和公证、Windows 签名；在那之前文档说明如何放行未签名的构建。

### 3.13 本地控制接口（M9）

- 只监听本机回环地址或 Unix socket，需要随机令牌（保存在 0600 文件里）。
- 暴露动作表（3.2）和只读查询（流量列表、单条流量、规则、集合）。
- 命令行和 MCP server 都是它的客户端，不直接访问内部状态。

## 4. 和里程碑的关系

| 工作项 | 必须在什么之前完成 | 理由 |
| --- | --- | --- |
| CI（3.10） | M0 | 之后每一步都要靠它兜底 |
| 动作层（3.2） | M1 | 已启用工具栏、对象动作、撤销都依赖它 |
| 通知（3.6） | M1 | 撤销 toast 依赖它 |
| 存储层和迁移（3.3） | M1 | 合并历史和留证需要迁移旧数据 |
| 日志（3.7） | M1 | 迁移出问题时需要排查 |
| 状态和导航（3.1） | 跟随 UI 改版逐步完成 | 改到哪里拆到哪里 |
| 后台任务（3.5） | 跟随 UI 改版逐步完成 | 同上 |
| 规则引擎（3.4） | M2 用匹配器，M4 完成管线 | Host 方案需要统一的匹配器；规则升级需要管线 |
| 国际化（3.8） | UI 改版完成后 | 改版后文字集中在共用组件里 |
| 性能（3.11） | M1 | 和内存上限一起做 |
| 发布（3.12） | 第一个公开版本之前 | |
| 主题映射（3.9） | M9 | |
| 控制接口（3.13） | M9 | 依赖动作层 |

调整后的顺序：

1. **M0**：CI → UI 改版第 1–3 步（UI 设计规范第 9 节）。
2. **M1 前置**：动作层、通知、存储层、日志、性能上限。
3. 之后按路线图推进，状态拆分和后台任务迁移随 UI 改版穿插完成。
