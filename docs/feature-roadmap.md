# vscroxy 功能路线图

状态：已定稿的方向，具体实现时再细化。和 [UI 改版计划](ui-redesign.md) 配套：UI 改版只改外观，本文规划功能本身的增、删、合并，以及长期路线。交互和文案见 [UX 规范](ux-guidelines.md)，代码基础设施和各里程碑的前置条件见 [基础设施方向](architecture.md)。

## 1. 定位

**一个 app 走完"抓包 → 调试 → 整理成接口集合 → 回归"的全流程。**

- 抓包和调试：完整覆盖 Proxyman 的桌面端功能。
- 请求：覆盖 Postman 和"发请求、测请求"相关的功能。
- Host 切换：SwitchHosts 式的多方案切换，基于代理实现。
- 差异化：抓包和请求是同一份数据，流量可以一步整理成集合，集合里的示例可以反过来当 mock 用。

明确不做（见第 8 节）：云同步和团队空间、手机端 app、可视化编排（Postman Flows）、云端监控、app 内置 AI 助手。协作走 Git（集合本来就落盘成目录），AI 走对外的 MCP 接口。

依据：`docs/guide.md`、`internal/` 的代码、Proxyman 文档（docs.proxyman.com）、Postman v12 发布说明。

## 2. 已做的决策

| 问题 | 决定 | 理由 |
| --- | --- | --- |
| 历史和留证的 7 个概念 | 合并成 **History**（已保存请求的发送记录）和 **Sessions**（侧栏 SESSIONS 分组）（见 3.1） | 概念太多、入口重复，约 2700 行代码维护同一类功能 |
| Investigations | 去掉，能力并入 Sessions；已有数据自动迁移成会话文件，不丢数据 | Proxyman 用"保存会话 + Pin + 颜色 + 备注"就覆盖了这个需求 |
| Regression 分享/导入格式 | 去掉自有格式；脱敏变成所有导出（Postman、HAR、会话）都有的"脱敏"选项 | 分享统一走通用格式，脱敏能力保留并扩大适用范围 |
| 两套脚本 | 抓包脚本改成挂在规则上，API 对齐 `pm`，和请求脚本共用编辑器、Console 和内置库；`script.js` 迁移成一条"匹配全部"的规则 | 用户只需学一套 API；Proxyman 也是"规则 + 脚本" |
| Checks 和 pm.test | 都保留；导出 Postman 时 Checks 自动转成等价的 `pm.test` | 无代码断言有价值，导出不再丢失 |
| 旧版脚本 API（`req`、`res`、`vars`、`test`、`log`） | 下一个版本标记废弃并在 Console 提示，再下一个版本删除 | 减少维护面 |
| 诊断入口分散 | 合并成一个"连接诊断"，其他地方只放跳转 | 同一类问题只有一个答案 |
| Host 方案能否同时启用多个 | 默认可以同时开多个，列表里靠上的优先；可以切到"互斥模式" | 和 SwitchHosts 默认行为一致 |
| Host 方案和 Requests 环境联动 | 第一版不联动；之后作为方案上的可选项"切换时同时切换环境" | 先把两个功能各自做好，避免概念绑死 |
| Host 方案和系统 hosts 文件 | 不写系统 hosts 文件 | 代理方式已经覆盖本机 App 和设备，不需要管理员权限，也不影响全机器 |
| 抓包 → 集合向导 vs Host 方案 | Host 方案先做 | 更小、基于现有 DNS 映射；向导依赖 Requests 侧改版完成 |
| 导航结构 | 只有一个工作区：抓到的请求和保存的请求是同一种可编辑文档，侧栏分 LIVE、COLLECTIONS、SESSIONS（UI 改版计划 v2，2026-10-31 修订，原为保留 Capture / Requests 两个工作区）；规则、Host、脚本、Mock 集中到一个 **Tools** 管理窗口（标题栏和命令面板进入） | 去掉两个工作区之间的搬运动作；工具类功能高频开关，不适合塞进设置 |
| 设置结构 | Settings 只放配置：通用、外观、代理（端口、系统代理、上游、绕过）、HTTPS（CA、解密名单）、抓包（白名单、忽略、No Caching、内存上限）、设备 | 和 Tools 分开：配置很少改，工具经常开关 |
| 界面语言 | 加中文界面，在 UI 改版完成后做（M9 之前任何时候都可以插入） | 改版后文字集中在共用组件里，翻译成本最低 |

## 3. 现有功能的收敛

### 3.1 历史和留证

| 现在 | 之后 |
| --- | --- |
| Run records、Send history、Debug history | **History**（选中已保存请求时的中间栏）：每次 Send/Runner 一条记录，可以按请求、按抓包来源过滤，对比、标基线、备注、打开成草稿 |
| Recent requests（`history.json`） | 去掉，被 History 取代 |
| Investigations、Historical sessions | **Sessions**（侧栏 SESSIONS 分组）：保存/打开会话文件，多个会话以 tab 打开，用 Pin、颜色、备注留证，跨会话用 Diff 对比 |
| Regression 用例和分享格式 | 带 Checks 的普通请求放在集合里，用 Runner 跑；分享走 Postman 导出加"脱敏"选项 |

迁移：`investigations.json` 里每个调查转成一个会话文件；`experiments.json` 继续作为 History 的存储；`history.json` 保留只读一个版本后删除。

### 3.2 其他

- 诊断：Capture diagnostics、CONNECT 详情的 Explain、Client setup 的检查、设备接入的 Checks 合并成"连接诊断"。
- 界面里的大段说明文字（比如 DNS 映射对话框的整段说明）移到文档和 tooltip，界面只保留一句话。

## 4. 功能清单

状态：**已有**、**部分**（有但不完整）、**缺少**。里程碑见第 5 节。

### 4.1 抓包核心

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| HTTP / HTTPS 抓包，按域名、按 App 解密 | 已有 | | |
| HTTP/2（TLS） | 已有 | 底层引擎协商 h2；h2c 不支持 | |
| WebSocket、SSE | 已有 | | |
| gRPC / Protobuf 检查 | 已有 | 只限 HTTP body | |
| **录制开关（Record / Pause）** | 缺少 | 现在只能清空，不能暂停 | M1 |
| 清空会话 | 已有 | | |
| **只抓指定 host / App（Allow list）** | 缺少 | 现在只有忽略名单 | M1 |
| 长时间抓包的内存和磁盘上限、自动清理 | 缺少 | 指南写明落盘没有总量上限 | M1 |
| Block list：通配符、正则、按 App、"隐藏"或"拦截"、断开连接 | 部分 | 现在 Block 规则只回 403，忽略名单只按 host/App | M4 |
| 系统代理绕过名单（Bypass list） | 部分 | 保留系统原有的绕过设置，但不能在 app 里编辑 | M7 |
| SOCKS5 代理入口 | 缺少 | 现在只有 HTTP 代理入口（上游支持 SOCKS5） | M7 |
| 反向代理（本地端口直接转发到远端） | 缺少 | | M7 |
| 设备接入访问控制（IP 白名单） | 已有 | | |
| 自定义证书：上游客户端证书（mTLS）、导入自定义根证书 | 缺少 | | M6 |
| TLS key 记录（SSLKEYLOGFILE） | 已有 | | |
| 连接日志 | 已有 | | |
| 来源 App 识别和图标 | 已有 | Windows 没有图标 | |
| 透明代理（抓不走系统代理的 App） | 缺少 | 各平台机制不同，研究项 | 远期 |
| HTTP/3 / QUIC | 缺少 | 引擎不支持，研究项 | 远期 |

### 4.2 列表和检查器

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| 排序、多选、Pin、颜色标记和备注 | 已有 | | |
| 多个视图 tab、保存的筛选 | 已有 | | |
| 自定义列：header、query | 已有 | | |
| 自定义列：JSON body 字段、GraphQL operation、Remote IP | 缺少 | | M2 / M5 |
| 筛选：多条件 AND/OR 组合 | 部分 | 条件之间只能 AND | M5 |
| 搜索支持通配符和正则 | 缺少 | 现在只匹配 host 和路径的子串 | M5 |
| Body 全文搜索 | 已有 | | |
| Body 查看：Pretty、JSON 树、Hex、图片、HTML、Protobuf | 已有 | | |
| Body 查看：multipart 分段、表单字段表、MessagePack、XML 格式化 | 缺少 | | M5 |
| JSON 筛选：JSONPath、jq | 缺少 | | M5 |
| 快速解码：选中文字解 Base64、JWT、URL 编码、时间戳 | 缺少 | | M5 |
| Cookies 标签（解析 Cookie 和 Set-Cookie） | 缺少 | 抓包检查器里没有 | M5 |
| 耗时瀑布图（DNS、连接、TLS、等待、下载） | 部分 | 现在只分两段，连接日志里有数据 | M5 |
| Diff、两条对比、多选汇总 | 已有 | | |
| Copy as：URL、cURL、body | 已有 | | |
| Copy as：Markdown 表格、CSV、原始报文 | 缺少 | | M5 |
| 抓包流量直接生成代码 | 部分 | 要先 Edit & replay 成草稿 | M5 |
| 左右分屏（两个视图并排） | 缺少 | | M9 |
| 检查器拆成独立窗口 | 缺少 | | M9 |
| 命令面板 | 已有 | | |

### 4.3 调试工具（Tools）

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| 规则匹配：通配符、正则、method、App、header | 部分 | 现在只支持 URL 子串和 method | M4 |
| 规则测试（输入 URL 看命中哪条） | 已有 | | |
| Breakpoint：请求、响应 | 已有 | | |
| Breakpoint 模板（保存常用报文一键套用） | 缺少 | | M4 |
| Map Local：文件 | 已有 | | |
| Map Local：在 app 内直接编辑响应 | 缺少 | 现在要改文件 | M4 |
| Map Local：目录（按路径找文件，找不到回源） | 缺少 | | M4 |
| Map Remote：换 scheme 和 host | 已有 | | |
| Map Remote：换端口和路径、是否保留原 Host header、WebSocket | 缺少 | | M4 |
| Scenario（规则分组切换） | 已有 | | |
| 脚本：挂在规则上、app 内编辑、`pm` 风格 API | 部分 | 现在是全局 `script.js`，手改文件 | M4 |
| 脚本：异步发请求、规则间共享状态、读系统环境变量 | 缺少 | | M4 |
| 脚本：内置库（lodash、crypto-js 等）、代码片段库、自定义 addon | 缺少 | 和请求脚本共用 | M4 |
| 脚本：WebSocket 消息钩子 | 缺少 | | M8 |
| 网络条件：全局预设 | 已有 | 3G / 4G / DSL | |
| 网络条件：自定义预设、丢包、按 host/规则生效 | 缺少 | | M5 |
| No Caching | 已有 | | |
| DNS 映射（可按 App） | 已有 | 升级为 Host 方案 | M2 |
| 上游代理：HTTP、SOCKS5 | 已有 | 只能全局一个 | |
| 上游代理：PAC、绕过名单、按 host 选择、认证 | 缺少 | | M7 |
| Repeat、Edit & repeat、批量重放 | 已有 | | |
| 高级重放：重复 N 次、并发、间隔 | 缺少 | | M5 |
| GraphQL：按 operation 名匹配规则、格式化 query | 缺少 | | M5 |
| 规则和设置的导入导出（备份、分享） | 缺少 | | M4 |

### 4.4 导入、导出、分享

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| HAR 导入导出、会话文件 | 已有 | | |
| 抓包流量 → Postman 集合 | 已有 | 通过"保存到集合"再导出 | |
| 抓包流量 → OpenAPI | 部分 | 要先保存成集合 | M3 |
| 导出 CSV | 缺少 | | M5 |
| 导出时脱敏选项 | 部分 | 只有 Regression 分享有 | M1 |
| 导入 Charles 会话（.chls）、Proxyman 日志 | 缺少 | | M7 |
| 分享到 GitHub Gist | 缺少 | | M9 |

### 4.5 设备和接入

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| 系统代理开关和崩溃恢复 | 已有 | | |
| CA 安装和信任检查 | 已有 | | |
| 局域网设备接入（iOS/Android 引导） | 已有 | | |
| 本地客户端配置示例 | 已有 | | |
| 一键打开已配好代理的终端（设置 `HTTP_PROXY`、`NODE_EXTRA_CA_CERTS`、`REQUESTS_CA_BUNDLE`、`SSL_CERT_FILE` 等） | 缺少 | Proxyman 的 Automatic Setup | M7 |
| 一键打开已配好代理的 Chrome/Firefox（独立配置） | 缺少 | | M7 |
| Android 模拟器自动配置（adb 设代理、装证书） | 缺少 | | M7 |
| iOS 模拟器自动装证书（macOS，simctl） | 缺少 | | M7 |
| Java keystore 导入证书 | 缺少 | | M7 |
| Electron App 带代理参数启动 | 缺少 | | M7 |

### 4.6 请求（Postman 相关）

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| HTTP 请求编辑、tab、草稿恢复 | 已有 | | |
| Params、Headers（启用开关、批量编辑、敏感值遮罩） | 已有 | | |
| Body：raw、urlencoded、multipart | 已有 | | |
| Body：binary 文件 | 缺少 | Postman 导入时会丢 | M6 |
| Body：GraphQL（query + variables，schema 自省和补全） | 缺少 | | M6 |
| Auth：Inherit、Bearer、Basic、API key、OAuth 2.0、Digest、AWS、JWT | 已有 | | |
| Auth：OAuth 1.0、NTLM、Hawk、Akamai EdgeGrid | 缺少 | 按需求排 | M6 |
| 环境、全局变量、集合/文件夹变量、Vault | 已有 | | |
| 变量分"可提交的初始值"和"仅本地的当前值" | 缺少 | 对放进 Git 的集合很重要 | M6 |
| `{{变量}}` 自动补全、悬停预览、未定义高亮 | 缺少 | | M6 |
| 动态变量 | 部分 | 只有 12 个，Postman 有完整的 faker 集合 | M6 |
| `pm` 脚本 API、Console、测试 | 已有 | | |
| 脚本沙箱库（lodash、crypto-js、moment 等） | 缺少 | 很多现成集合依赖 | M6 |
| `have.jsonSchema` | 缺少 | | M6 |
| `pm.visualizer`（响应可视化） | 缺少 | | M9 |
| 可复用脚本库（Postman Packages） | 缺少 | | M9 |
| Checks（无代码断言） | 已有 | 导出时转 `pm.test` | M1 |
| 集合、文件夹、设置、落盘为目录 | 已有 | | |
| 请求描述、集合文档视图 | 缺少 | Postman 导入时会丢 | M6 |
| 单个请求的设置：超时、跟随重定向、证书校验 | 缺少 | Postman 导入时会丢 | M6 |
| 客户端证书（按域名）、自定义 CA | 缺少 | | M6 |
| Examples | 已有 | | |
| Examples 作为 Mock 响应（本地 mock） | 缺少 | 见 6.3 | M4 |
| 代码 Mock（脚本写的 mock 服务，Postman `pm.mock`） | 缺少 | | M9 |
| Runner：数据文件、迭代、setNextRequest | 已有 | | |
| Runner：性能测试（虚拟用户、时长、指标） | 缺少 | | M9 |
| 命令行运行集合（CI，JUnit/JSON 报告） | 缺少 | | M9 |
| Cookies 管理 | 已有 | 只在内存里 | |
| Cookies 可选持久化 | 缺少 | | M6 |
| 响应：Pretty、Raw、Hex、预览、测试结果、耗时 | 已有 | | |
| 响应保存到文件 | 缺少 | | M6 |
| 全局搜索（集合、请求、环境） | 缺少 | 并进命令面板 | M6 |
| 代码生成：cURL、fetch、axios、Python、Go、HTTPie | 已有 | | |
| 代码生成：Swift、Kotlin、Java、C#、PHP、Ruby、Rust | 缺少 | | M9 |
| 导入：Postman v2.0/2.1、环境、OpenAPI 3.x、cURL、HAR | 已有 | | |
| 导入：Postman Collection v3 | 缺少 | Postman v12 的新格式 | M6 |
| 导入：Swagger 2、Insomnia、Bruno、Hoppscotch | 缺少 | | M7 |
| 导入：PowerShell / CMD 风格的 cURL | 缺少 | | M7 |
| WebSocket 客户端（文本） | 已有 | | |
| WebSocket 客户端：二进制消息、子协议 | 缺少 | | M8 |
| SSE 客户端（持续接收事件） | 缺少 | | M8 |
| gRPC 客户端（导入 proto 或服务端反射） | 缺少 | | M8 |
| Socket.IO 客户端 | 缺少 | | M8 |
| MCP 客户端（调试 MCP server） | 缺少 | | M8 |
| MQTT 客户端 | 缺少 | 需求少，最后做 | M8 |

### 4.7 一体化（vscroxy 独有）

| 功能 | 状态 | 里程碑 |
| --- | --- | --- |
| 抓包 → Edit & replay、保存成请求、多选保存到集合 | 已有 | |
| **"整理成集合"向导**（6.1） | 缺少 | M3 |
| 保存时附带抓到的响应作为 Example | 缺少 | M3 |
| 抓到的流量识别出对应的已保存请求 | 缺少 | M3 |
| **录制到集合**（6.2） | 缺少 | M9 |
| Example → Mock 规则，集合 → Scenario | 缺少 | M4 |
| 登录 token 链路识别，建议生成提取脚本 | 缺少 | M9 |
| **Host 方案**（第 7 节） | 部分（DNS 映射） | M2 |

### 4.8 自动化和生态

| 功能 | 状态 | 里程碑 |
| --- | --- | --- |
| 本地控制接口（供命令行和 MCP 共用） | 缺少 | M9 |
| 命令行：开关工具、开关系统代理、导出流量、导入规则、运行集合 | 缺少 | M9 |
| MCP server：让 AI 工具读取流量、解释请求、创建规则、运行请求 | 缺少 | M9 |
| 中文界面 | 缺少 | M9 之前插入 |
| 第三方主题（Tokyo Night、Dracula 等） | 缺少 | M9 |

## 5. 里程碑

每个里程碑单独可发布。UI 改版的阶段编号见 [ui-redesign.md](ui-redesign.md)。

### M0：UI 基础

先接入 CI，再做 UI 改版阶段 0–2：截图工具、设计系统（token、内嵌字体、共用组件、靛蓝强调色）、外壳。之后所有新功能都在新组件上做，避免做两遍。

### M1：抓包基础补齐，概念收敛

前置（见基础设施方向第 4 节）：动作层、通知层、带版本的存储层和迁移、日志文件。

- 录制开关（标题栏状态 pill 里的暂停按钮）。
- Allow list（Settings → 抓包）。
- 内存和磁盘上限、自动清理。
- 合并历史和留证（3.1），迁移旧数据。
- 合并诊断入口。
- 导出时的脱敏选项。
- Checks 导出为 `pm.test`。
- 旧版脚本 API 标记废弃。
- Settings 重新分区。
- UI 改版阶段 3（Capture）。

### M2：Host 方案

第 7 节的全部内容，加上 Remote IP 列。

### M3：Requests 改版和"整理成集合"

- UI 改版阶段 4（Requests）。
- "整理成集合"向导（6.1），保存时附带 Example。
- 抓到的流量识别出对应的已保存请求。
- 抓包 → OpenAPI 串进向导。

### M4：规则引擎升级（Tools 窗口）

- Tools 管理窗口：规则、Scenario、脚本、Host 方案集中管理。
- 规则匹配：通配符、正则、method、App、header。
- Map Local：app 内编辑响应、目录映射。
- Map Remote：端口、路径、Host header、WebSocket。
- Block：隐藏或拦截、断开连接、按 App。
- Breakpoint 模板。
- 规则脚本：`pm` 风格 API、编辑器、异步请求、共享状态、环境变量、内置库、代码片段；`script.js` 迁移。
- Example → Mock 规则、集合 → Scenario。
- 规则和设置的导入导出。

### M5：检查器和调试增强

- JSONPath / jq、快速解码、Cookies 标签。
- multipart、表单、MessagePack、XML 查看。
- 自定义列：JSON 字段、GraphQL operation。
- GraphQL 感知：operation 列、按 operation 筛选和匹配规则、格式化 query。
- 耗时瀑布图。
- 筛选的 AND/OR 组合，搜索支持通配符和正则。
- 网络条件：自定义、丢包、按 host。
- 高级重放。
- Copy as Markdown/CSV、导出 CSV、抓包直接生成代码。

### M6：Postman 兼容补齐

- Body：binary、GraphQL。
- 请求描述和集合文档、单个请求的设置。
- 证书：请求侧的客户端证书和自定义 CA；抓包侧的上游 mTLS 和自定义根证书。
- 变量：初始值/当前值、自动补全和悬停、完整动态变量。
- 脚本：沙箱库、`have.jsonSchema`。
- Postman Collection v3 导入导出。
- OAuth 1.0 等补充认证。
- Cookies 持久化、响应保存到文件、全局搜索。
- UI 改版阶段 5–8 在 M3 到 M6 期间穿插完成。

### M7：接入和网络

- 一键接入：终端、浏览器、Android 模拟器、iOS 模拟器、Java、Electron。
- SOCKS5 入口、反向代理。
- 上游代理：PAC、绕过、按 host、认证；系统代理绕过名单。
- 导入：Swagger 2、Insomnia、Bruno、Hoppscotch、Charles、Proxyman 日志、PowerShell cURL。

### M8：协议扩展

- gRPC 客户端（proto 导入、服务端反射、四种调用方式）。
- WebSocket：二进制、子协议、消息断点、脚本钩子；WebSocket 消息的 Protobuf 解码。
- SSE 客户端、Socket.IO 客户端、MCP 客户端、MQTT 客户端。

### M9：自动化和生态

- 本地控制接口、命令行、MCP server。
- 命令行运行集合（CI 报告）、Runner 性能测试。
- 录制到集合、token 链路识别。
- 代码 Mock、`pm.visualizer`、可复用脚本库。
- 更多代码生成语言。
- 第三方主题导入。
- 左右分屏、检查器独立窗口、分享到 Gist。

### 远期研究

- 透明代理：Linux 用 nftables 重定向，Windows 用 WinDivert，macOS 用 Network Extension（需要签名和权限）。
- HTTP/3 / QUIC 抓包。
- Host 方案与 Requests 环境的联动。

## 6. 抓包 → 集合：一体化流程

### 6.1 "整理成集合"向导

入口：选中多条流量，或者在侧栏右键某个域名/App，选 "Create collection…"。每一步都能预览和返回：

1. **选范围**：选中的流量，或某个域名、App 下的全部流量；默认去掉静态资源（图片、js、css、字体）、CONNECT 和失败的请求。
2. **合并重复接口**：按 method + 路径模板合并，`/users/42` 和 `/users/7` 识别成 `/users/:id`，可以手动拆开或合并。
3. **提取变量**：
   - 每个 host 变成环境变量 `{{base_url}}`（多个 host 时是 `{{api_url}}`、`{{auth_url}}`），写入新建或已有的环境。
   - 相同的 `Authorization`、`Cookie`、API key 提取到集合级 Auth（请求用 Inherit），值放进环境变量的"当前值"或 Vault，不写进集合文件。
   - 路径参数变成 `{{id}}` 这样的变量。
4. **组织结构**：按 host、路径前缀分文件夹，可以拖动调整；自动命名请求（`GET /users/:id` → "Get user"）。
5. **附带响应**：每个接口抓到的响应保存成 Examples。
6. **可选**：同时生成 OpenAPI 文档。

### 6.2 录制到集合

开启 "Record into collection" 并设置过滤条件（host、App），之后符合条件的新流量按 6.1 的规则自动追加进指定集合，重复接口只更新 Example。适合"把 App 点一遍，接口集合就出来了"。

### 6.3 双向打通

- **集合 → 抓包**：任意 Example 一键"作为 Mock 响应"，生成一条 Map Local 规则；一个集合的全部 Examples 可以作为一个 Scenario 一起开关，这就是离线的 mock 服务。
- **抓包 → 已有请求**：流量匹配到已保存请求的 method + 路径模板时，检查器里显示"对应请求：Get user"，一键打开，或把这次响应存为新的 Example。
- **链路识别**：发现登录接口响应里的 token 被后续请求使用时，建议生成 post-response 脚本 `pm.environment.set("token", …)`。

## 7. Host 方案（SwitchHosts 式）

### 现状

已有 DNS 映射：`~/.vscroxy/dns.txt`，一行一条 `name = address`，支持子域名、`*.` 通配一层、`@ Chrome` 按 App 生效，可以从列表右键"Map this host…"添加，命中的流量有 DNS 标记。它只影响走代理的流量，保留原 host、Host header 和 SNI，不需要管理员权限，设备也能用。

### 要做的

1. **多个命名方案**：比如"本地"、"测试"、"预发"，每个方案是一组映射。现有 `dns.txt` 迁移成方案"默认"。
2. **开关**：每个方案独立开关，默认可以同时开多个，列表里靠上的优先；可以切到互斥模式。
3. **快速切换**：标题栏放方案下拉框（和录制状态并列），命令面板也能切换。界面说明切换只影响新连接，已建立的 keep-alive 连接不变；提供"断开现有连接"按钮让切换立即生效。
4. **编辑器**：支持标准 hosts 语法 `1.2.3.4 a.com b.com`（一行多个域名、`#` 注释、IPv6），同时兼容现有的 `name = address` 和 `@ App` 写法；语法高亮、错误提示、和其他启用方案冲突时提示。
5. **可见性**：列表增加 Remote IP 列；检查器显示"由方案'测试'映射到 10.0.0.5"。
6. **导入**：从系统 hosts 文件导入；订阅远程 hosts 地址，定时刷新。
7. **不写系统 hosts 文件**。

DNS 映射换的是 IP，请求保留原 host，适合"同一个域名指到不同机器"；Map Remote 换的是目标 host，适合"把请求转到另一个域名或端口"。两者都保留，在 Tools 窗口里把区别讲清楚。

## 8. 明确不做

| 不做 | 理由 |
| --- | --- |
| 云同步、团队空间、评论、Fork | 集合落盘成目录，协作用 Git |
| 手机端 app（Proxyman iOS/Android） | 桌面端 + 设备接入已覆盖调试需求 |
| Atlantis 式 SDK | 维护成本高，设备接入已覆盖 |
| Postman Flows（可视化编排） | 和定位无关 |
| 云端监控、定时运行 | 本地工具；命令行运行集合可以接 CI 定时 |
| app 内置 AI 助手 | 通过 MCP server 交给外部 AI 工具 |
| 写系统 hosts 文件 | 见第 7 节 |
| 自定义工具栏 | 命令面板已覆盖 |
