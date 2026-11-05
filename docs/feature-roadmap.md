# vscroxy 功能路线图

状态：已定稿的方向，具体实现时再细化。和 [UI 设计规范](ui-redesign.md) 配套：UI 改版只改外观，本文规划功能本身的增、删、合并，以及长期路线。交互和文案见 [UX 规范](ux-guidelines.md)，代码基础设施和各里程碑的前置条件见 [基础设施方向](architecture.md)。

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
| Investigations | 去掉，能力并入 Sessions；原型阶段不迁移旧数据 | Proxyman 用"保存会话 + Pin + 颜色 + 备注"就覆盖了这个需求 |
| Regression 分享/导入格式 | 去掉自有格式；脱敏变成所有导出（Postman、HAR、会话）都有的"脱敏"选项 | 分享统一走通用格式，脱敏能力保留并扩大适用范围 |
| 两套脚本 | 抓包脚本改成挂在规则上，API 对齐 `pm`，和请求脚本共用编辑器、Console 和内置库；`script.js` 迁移成一条"匹配全部"的规则 | 用户只需学一套 API；Proxyman 也是"规则 + 脚本" |
| Checks 和 pm.test | 都保留；导出 Postman 时 Checks 自动转成等价的 `pm.test` | 无代码断言有价值，导出不再丢失 |
| 旧版脚本 API（`req`、`res`、`vars`、`test`、`log`） | 已删除，只保留 `pm` 和 Postman 自己的旧全局（`tests`、`postman.*`、`responseBody` 等） | 减少维护面；原型阶段不做兼容 |
| 诊断入口分散 | 合并成一个"连接诊断"，其他地方只放跳转 | 同一类问题只有一个答案 |
| Host 方案能否同时启用多个 | 默认可以同时开多个，列表里靠上的优先；可以切到"互斥模式" | 和 SwitchHosts 默认行为一致 |
| Host 方案和 Requests 环境联动 | 第一版不联动；之后作为方案上的可选项"切换时同时切换环境" | 先把两个功能各自做好，避免概念绑死 |
| Host 方案和系统 hosts 文件 | 不写系统 hosts 文件 | 代理方式已经覆盖本机 App 和设备，不需要管理员权限，也不影响全机器 |
| 抓包 → 集合向导 vs Host 方案 | Host 方案先做 | 更小、基于现有 DNS 映射；向导依赖 Requests 侧改版完成 |
| 导航结构 | 只有一个工作区：抓到的请求和保存的请求是同一种可编辑文档，侧栏分 LIVE、COLLECTIONS、SESSIONS（UI 设计规范 v2，2026-10-31 修订，原为保留 Capture / Requests 两个工作区）；规则、Host、脚本、Mock 集中到一个 **Tools** 管理窗口（标题栏和命令面板进入） | 去掉两个工作区之间的搬运动作；工具类功能高频开关，不适合塞进设置 |
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

`experiments.json` 继续作为 History 的存储；`investigations.json` 和 `history.json` 不再读取（原型阶段不做迁移）。

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
| **录制开关（Record / Pause）** | 已有 | 标题栏录制 pill 的暂停按钮，命令面板 Pause／Resume recording | M1 |
| 清空会话 | 已有 | | |
| **只抓指定 host / App（Allow list）** | 已有 | Settings → Capture，按 host 和 App；忽略名单优先 | M1 |
| 长时间抓包的内存和磁盘上限、自动清理 | 已有 | Settings → Capture → Memory：条数、内存和磁盘上限，超出先删最旧的（Pin 的保留），连同落盘文件 | M1 |
| Block list：通配符、正则、按 App、"隐藏"或"拦截"、断开连接 | 已有 | Block 规则的动作：拒绝（403）、断开连接、隐藏（照常代理但不进表格）；匹配用规则的通配符／正则／App／header | M4 |
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
| 自定义列：JSON body 字段、Remote IP | 已有 | 请求/响应标量 JSON pointer，后台完整正文读取；Remote IP 显隐 | M2 / M5 |
| 自定义列：GraphQL operation | 已有 | 实际选中的 operation 类型／名称，后台完整正文解析，支持 batch | M5 |
| 筛选：多条件 AND/OR 组合 | 已有首版 | 内容字段可选全部／任一匹配，含完整正文；外层范围仍 AND，暂不支持嵌套组 | M5 |
| 搜索支持通配符和正则 | 已有 | 显式 Text／Wildcard／Regex 模式，匹配 host＋路径（含查询串）；错误模式不回退，随视图／保存筛选保留 | M5 |
| Body 全文搜索 | 已有 | | |
| Body 查看：Pretty、JSON 树、Hex、图片、HTML、Protobuf | 已有 | | |
| Body 查看：multipart 分段、表单字段表、MessagePack、XML 格式化 | 已有 | 后台只读解析，保留重名/整数/二进制证据；见使用指南限制 | M5 |
| JSON 筛选：JSONPath、jq | 已有 | 后台只读有界子集，支持取消；详见使用指南 | M5 |
| 快速解码：选中文字解 Base64、JWT、URL 编码、时间戳 | 已有 | 本地只读预览；JWT 不验证签名或 claims | M5 |
| Cookies 标签（解析 Cookie 和 Set-Cookie） | 已有 | 抓包两侧和已发送响应；保留重名，默认遮罩 | M5 |
| 耗时瀑布图（DNS、连接、TLS、等待、下载） | 已有 | 请求级实际偏移，支持重叠、失败／未结束、复用；代理观测边界见使用指南 | M5 |
| Diff、两条对比、多选汇总 | 已有 | | |
| Copy as：URL、cURL、body | 已有 | | |
| Copy as：Markdown 表格、CSV、原始报文 | 已有 | 抓包与多选；表格用可见列，CSV 按 RFC 4180 并防公式注入；原始报文为 HTTP/1.1 文本，二进制／超大正文有明确标记 | M5 |
| 抓包流量直接生成代码 | 已有 | 抓包就是请求文档，Request options／命令面板的 Generate code… 直接可用；生成器限制见使用指南 | M5 |
| 左右分屏（两个视图并排） | 缺少 | | M9 |
| 检查器拆成独立窗口 | 缺少 | | M9 |
| 命令面板 | 已有 | | |

### 4.3 调试工具（Tools）

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| 规则匹配：通配符、正则、method、App、header | 已有 | URL 可选包含／通配符／正则；App 用和忽略名单相同的模式；header 支持存在、等于、包含、正则 | M4 |
| 规则测试（输入 URL 看命中哪条） | 已有 | | |
| Breakpoint：请求、响应 | 已有 | | |
| Breakpoint 模板（保存常用报文一键套用） | 已有 | 断点对话框里 Save as template… 和模板菜单；按请求／响应分开，不改暂停时的 URL | M4 |
| Map Local：文件 | 已有 | | |
| Map Local：在 app 内直接编辑响应 | 已有 | Tools 规则编辑器里的 Body，保存写回文件；没有文件时建在 mocks 里；二进制和超过 8 MiB 的除外 | M4 |
| Map Local：目录（按路径找文件，找不到回源） | 已有 | 可去掉路径前缀，/ 结尾读 index.html | M4 |
| Map Remote：换 scheme 和 host | 已有 | | |
| Map Remote：换端口和路径、是否保留原 Host header、WebSocket | 已有 | 端口写在目标 URL 里；路径前缀改写；WebSocket（含解密的 wss）跟随第一条匹配的 Map Remote。原主机连不上、且有启用的 Map Remote 可能匹配它时，改用生成的证书 | M4 |
| Scenario（规则分组切换） | 已有 | | |
| 脚本：挂在规则上、app 内编辑、`pm` 风格 API | 已有 | Script 规则运行 `tools/scripts/*.js` 的 onRequest／onResponse；Tools → Scripts 编辑，带 Console；全局 `script.js` 已删除 | M4 |
| 脚本：异步发请求、规则间共享状态、读系统环境变量 | 已有 | `pm.sendRequest` 直连服务器；`pm.variables` 跨同一请求的脚本，`pm.globals` 跨规则并保存；`process.env` | M4 |
| 脚本：内置库（lodash、crypto-js 等）、代码片段库、自定义 addon | 已有 | lodash、crypto-js 4.2.0、chai 和 Insert snippet 与请求脚本共用；规则脚本和 addon 是 ES 模块（esbuild 转成 CommonJS 后由 goja 运行，source map 指回原行号，不支持顶层 await），addon 在 `tools/addons/*.js`，规则脚本 `import`、请求脚本 `require` 加载，Tools → Scripts → Addons 编辑，CLI 用 `--addons` | M4 |
| 脚本：WebSocket 消息钩子 | 已有 | `onMessage(message)` 双向文本／二进制编辑、单条 drop；连接级 locals 串行执行；WS／解密 WSS | M8 |
| 网络条件：全局预设 | 已有 | 3G / 4G / DSL | |
| 网络条件：自定义预设、请求断连、按 host/规则生效 | 已有 | 自定义预设与规则作用域；失败率是请求断连，不是 TCP/IP 丢包；见使用指南限制 | M5 |
| No Caching | 已有 | | |
| DNS 映射（可按 App） | 已有 | 已升级为 Host 方案 | M2 |
| 上游代理：HTTP、SOCKS5 | 已有 | 只能全局一个 | |
| 上游代理：PAC、绕过名单、按 host 选择、认证 | 缺少 | | M7 |
| Repeat、Edit & repeat、批量重放 | 已有 | | |
| 高级重放：重复 N 次、并发、间隔 | 已有 | 复用 Replay 发送路径；单批 500 次、并发 8、重复 100、间隔 60 s；进度、成功／失败／取消、状态码分布、耗时；重放不进集合录制 | M5 |
| GraphQL：按 operation 名匹配规则、格式化 query | 已有 | 精确筛选／规则与预览共用解析；query 格式化保留语义，失败不改原文 | M5 |
| 规则和设置的导入导出（备份、分享） | 已有 | Settings → General：一个 JSON，含设置、名单、规则和 mock 文件、脚本、Host 方案、断点模板；不含密钥和变量；导入按部分替换 | M4 |

### 4.4 导入、导出、分享

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| HAR 导入导出、会话文件 | 已有 | | |
| 抓包流量 → Postman 集合 | 已有 | 通过"保存到集合"再导出 | |
| 抓包流量 → OpenAPI | 已有 | 向导完成页可同时生成独立草稿 | M3 |
| 导出 CSV | 已有 | 当前视图或选中；列与表格可见列一致；RFC 4180、公式注入防护；沿用导出脱敏 | M5 |
| 导出时脱敏选项 | 已有 | Postman、HAR、会话共用六类脱敏选项；CSV 使用其中适用的四类；默认全部启用，可明确原样导出，需确认范围 | M1 |
| 导入 Charles 会话（.chls）、Proxyman 日志 | 缺少 | | M7 |
| 分享到 GitHub Gist | 缺少 | | M9 |

### 4.5 设备和接入

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| 系统代理开关和崩溃恢复 | 已有 | | |
| CA 安装和信任检查 | 已有 | | |
| 局域网设备接入（iOS/Android 引导） | 已有 | | |
| 本地客户端配置示例 | 已有 | | |
| 一键打开已配好代理的终端（设置 `HTTP_PROXY`、`NODE_EXTRA_CA_CERTS`、`REQUESTS_CA_BUNDLE`、`SSL_CERT_FILE` 等） | 已有 | 独立窗口；替换型信任变量指向系统根证书 + app CA 的 0600 bundle；只影响该窗口 | M7 |
| 一键打开已配好代理的 Chrome/Firefox（独立配置） | 已有 | 临时 profile；Chrome `--proxy-server`／`--user-data-dir`，Firefox `user.js`；不关闭 TLS 校验 | M7 |
| Android 模拟器自动配置（adb 设代理、装证书） | 已有 | `adb reverse` + 可撤销的 global proxy；Undo 恢复原值并移除对应 reverse；CA 只复制到 Downloads，系统信任需手动或 root 镜像 | M7 |
| iOS 模拟器自动装证书（macOS，simctl） | 已有 | 仅已启动的模拟器，不配置真机 | M7 |
| Java keystore 导入证书 | 已有 | app 自有 PKCS#12 truststore；先从 JDK cacerts 复制系统根证书，再加 app CA；JVM 选项含代理 host／port；先审阅命令再确认 | M7 |
| Electron App 带代理参数启动 | 已有 | `--proxy-server`；不传 `--ignore-certificate-errors`；信任取决于系统／NSS 或应用自身 | M7 |

### 4.6 请求（Postman 相关）

| 功能 | 状态 | 说明 | 里程碑 |
| --- | --- | --- | --- |
| HTTP 请求编辑、tab、草稿恢复 | 已有 | | |
| Params、Headers（启用开关、批量编辑、敏感值遮罩） | 已有 | | |
| Body：raw、urlencoded、multipart | 已有 | | |
| Body：binary 文件 | 已有 | 只保存本地路径，每次发送原样读取，普通文件 ≤16 MiB；Send/Runner 共用，Postman 导入导出保留引用，cURL 与原生代码生成导出引用源文件 | M6 |
| Body：GraphQL（query + variables） | 已有 | 独立编辑与保存，变量展开、Send/Runner、Postman 导入导出、代码导出 | M6 |
| GraphQL schema 自省和补全 | 缺少 | 基础 Body 和 query 格式化已完成；operationName 编辑选择仍待做 | M6 |
| Auth：Inherit、Bearer、Basic、API key、OAuth 2.0、OAuth 1.0、Digest、AWS、JWT、Hawk、Akamai EdgeGrid | 已有 | OAuth 1.0：HMAC-SHA1/SHA256、PLAINTEXT、RSA-SHA1/256/512，header／query／body 位置、body hash、Postman oauth1 导入导出；Hawk：SHA-256/SHA-1、payload hash、ext／app／delegation；EdgeGrid：Base URL、签名头列表、POST 正文 hash | |
| Auth：NTLM | 缺少 | 需要同一连接完成 Type 1/2/3 质询；Send／Runner 每次发送新建连接（`capture.ReplayWithRedirects` 禁用 keep-alive，代理按下游连接固定上游），无法握手；Postman 导入警告并置为 None | M6 |
| 环境、全局变量、集合/文件夹变量、Vault | 已有 | | |
| 变量分"可提交的初始值"和"仅本地的当前值" | 缺少 | 对放进 Git 的集合很重要 | M6 |
| `{{变量}}` 自动补全、悬停预览、未定义高亮 | 缺少 | | M6 |
| 动态变量 | 部分 | 已有 32 个：补齐常用姓名、电话、国家代码、经纬度、IPv6/MAC、域名/URL、示例邮箱、版本、价格、颜色和星期/月；内置小型词库，尚非完整 Faker | M6 |
| `pm` 脚本 API、Console、测试 | 已有 | | |
| 脚本沙箱库（lodash、crypto-js、moment 等） | 部分 | 已内嵌 Lodash 4.18.1，支持 `require('lodash')`，按脚本隔离；无定时器，暂不支持 delay/debounce 等；crypto-js、moment 等仍缺少 | M6 |
| `have.jsonSchema` | 已有 | 响应和 `pm.expect(data)` 共用 Ajv 6.12.6，默认 Draft-07，支持选项和取反；不下载外部引用，不支持异步 schema | M6 |
| `pm.visualizer`（响应可视化） | 缺少 | | M9 |
| 可复用脚本库（Postman Packages） | 缺少 | | M9 |
| Checks（无代码断言） | 已有 | 导出 Postman 时转成 `pm.test`，结果与 Checks 一致；无效的 Checks 跳过并警告 | M1 |
| 集合、文件夹、设置、落盘为目录 | 已有 | | |
| 请求描述、集合文档视图 | 缺少 | Postman 导入时会丢 | M6 |
| 单个请求的设置：超时、跟随重定向、证书校验 | 缺少 | Postman 导入时会丢 | M6 |
| 客户端证书（按域名）、自定义 CA | 缺少 | | M6 |
| Examples | 已有 | | |
| Examples 作为 Mock 响应（本地 mock） | 已有 | 见 6.3；Examples 标签里的 Mock，集合菜单的 Mock from examples | M4 |
| 代码 Mock（脚本生成本地响应） | 首版已有 | Code Mock 规则运行 `onMock`，读取 `pm.request`、生成 `pm.response`；复用编辑器／Scenario／配置导出，失败返回本地错误而不穿透原站；不是 Postman Node.js／`pm.mock` 服务兼容层 | M9 |
| Runner：数据文件、迭代、setNextRequest | 已有 | | |
| Runner：性能测试（虚拟用户、时长、指标） | 已有首版 | 内置固定并发集合负载；复用 Runner、HdrHistogram 执行耗时；原生／CLI／MCP。无固定 RPS、爬坡或分布式压测 | M9 |
| 命令行运行集合（CI，JUnit/JSON 报告） | 已有 | 独立无 UI CLI；本地集合/Postman、环境/数据文件、超时/取消/执行预算、私有报告 | M9 |
| Cookies 管理 | 已有 | 按域名分组，值默认遮罩；可按域名或全部清除 | |
| Cookies 可选持久化 | 已有 | 默认关闭；持久 Cookie 落盘到 `~/.vscroxy/cookies.json`（0600），Session Cookie 永不落盘；不进集合、导出或配置导入导出 | M6 |
| 响应：Pretty、Raw、Hex、预览、测试结果、耗时 | 已有 | | |
| 响应保存到文件 | 已有 | Requests 保存按钮／Capture Body 菜单；原样保存已解压正文，流式读取完整落盘内容，拒绝不完整正文 | M6 |
| 全局搜索（集合、请求、环境） | 已有 | 并进命令面板；搜索集合、文件夹、请求名称／method／URL、环境名和变量名，不搜索变量值；请求索引随集合变化更新 | M6 |
| 代码生成：cURL、fetch、axios、Python、Go、HTTPie | 已有 | 补齐 Bash／CMD cURL、PowerShell 7+、浏览器／Node fetch；抓包／草稿共用复制入口，另有脱敏／原样 HAR 复制 | M9 |
| 代码生成：Swift、Kotlin、Java、C#、PHP、Ruby、Rust | 已有 | Swift URLSession、Kotlin OkHttp、Java 11+ java.net.http、C# HttpClient、PHP cURL、Ruby Net::HTTP、Rust reqwest；抓包／草稿共用 Generate code… 与 Copy as… 入口；Binary body 引用源文件，其他文本 body 内联；非 UTF-8 抓包 body 仍用 cURL | M9 |
| 导入：Postman v2.0/2.1、环境、OpenAPI 3.x、cURL、HAR | 已有 | | |
| 导入：Postman Collection v3 | 缺少 | Postman v12 的新格式 | M6 |
| 导入：Swagger 2、Insomnia、Bruno、Hoppscotch | 已有 | Swagger 2 转 OpenAPI 3 后复用集合导入；Insomnia v4 JSON／v5 YAML（集合与环境）；Bruno `.bru` 目录；Hoppscotch 集合／环境 JSON。文件夹、变量、auth、body 类型和描述尽量保留，无法表达的部分在导入报告中列出 | M7 |
| 导入：PowerShell / CMD 风格的 cURL | 已有 | Chrome Copy as cURL (cmd) 与 Copy as PowerShell (Invoke-WebRequest)；复用 cURL 导入入口和请求模型，未支持的参数明确报错 | M7 |
| WebSocket 客户端（文本） | 已有 | | |
| WebSocket 客户端：二进制消息、子协议 | 已有 | Hex／Base64 发送、原样二进制回显；WS／WSS 双侧握手协商和实际子协议显示 | M8 |
| SSE 客户端（持续接收事件） | 首版已有 | HTTP(S) 实时 GET、事件类型／ID／数据、搜索复制、显式可选重连与 Last-Event-ID、取消及有界时间线 | M8 |
| gRPC 客户端（导入 proto 或服务端反射） | 首版已有 | 请求文档、proto／descriptor set、v1／v1alpha 反射、四种调用、metadata／trailers／最终状态、取消和 deadline；HTTP Runner／脚本暂不支持 | M8 |
| Socket.IO 客户端 | 首版已有 | 请求文档、EIO4／WebSocket、namespace／auth、JSON／二进制附件、双向 ACK、有界实时消息、取消；无轮询／重连／重发 | M8 |
| MCP 客户端（调试 MCP server） | 缺少 | | M8 |
| MQTT 客户端 | 缺少 | 需求少，最后做 | M8 |

### 4.7 一体化（vscroxy 独有）

| 功能 | 状态 | 里程碑 |
| --- | --- | --- |
| 抓包原地编辑、保存成请求、多选保存到集合 | 已有 | |
| **"整理成集合"向导**（6.1） | 已有 | M3 |
| 保存时附带抓到的响应作为 Example | 已有 | M3 |
| 抓到的流量识别出对应的已保存请求 | 已有 | M3 |
| **录制到集合**（6.2） | 已有：原生 UI／CLI／MCP，Host／App 过滤、环境变量、重复接口 Example 更新、安全停止 | M9 |
| Example → Mock 规则，集合 → Scenario | 已有 | M4 |
| 登录 token 链路识别，建议生成提取脚本 | 缺少 | M9 |
| **Host 方案**（第 7 节） | 已有 | M2 |

### 4.8 自动化和生态

| 功能 | 状态 | 里程碑 |
| --- | --- | --- |
| 本地控制接口（供命令行和 MCP 共用） | 已有：显式启用、回环鉴权、主线程动作、异步任务／取消 | M9 |
| 命令行：开关工具、开关系统代理、导出流量、导入规则、运行集合 | 已有：查询、录制／规则开关、系统代理异步修改与回读、受控 HAR 导出、追加规则导入、运行集合 | M9 |
| MCP server：让 AI 工具读取流量、解释请求、创建规则、运行请求 | 已有：stdio、查询／请求执行、独立写入／敏感权限、受控 HAR 导出和追加规则创建 | M9 |
| 中文界面 | 缺少 | M9 之前插入 |
| 第三方主题（Tokyo Night、Dracula 等） | 缺少 | M9 |

## 5. 里程碑

每个里程碑单独可发布。UI 改版的步骤见 [ui-redesign.md](ui-redesign.md) 第 9 节。

### M0：UI 基础

先接入 CI，再做 UI 改版第 1–3 步：`internal/uikit` 设计系统和 Paper／Ink 主题、把现有界面迁到 uikit、一个工作区和两种布局。之后所有新功能都在新组件上做，避免做两遍。

### M1：抓包基础补齐，概念收敛

前置（见基础设施方向第 4 节）：动作层、通知层、带版本的存储层和迁移、日志文件。

- 录制开关（标题栏状态 pill 里的暂停按钮）。
- Allow list（Settings → 抓包）。
- 内存和磁盘上限、自动清理。
- 合并历史和留证（3.1）（已完成：History + Sessions；不迁移旧数据）。
- 合并诊断入口（已完成：侧栏 Live → Diagnostics 一页五步检查；失败行的 Explain、Client setup、设备接入、空状态都跳到这里）。
- 导出时的脱敏选项（已完成：Postman、HAR、会话）。
- Checks 导出为 `pm.test`。
- 旧版脚本 API 删除（已完成）。
- Settings 重新分区。
- UI 改版第 4 步（录制 pill、查询筛选、活动柱状图等标志性组件）。

### M2：Host 方案

第 7 节的全部内容，加上 Remote IP 列。

已完成：`internal/hosts`（解析、优先级、冲突、订阅），标题栏 Hosts 下拉框，Tools 窗口（只开 Host profiles），Remote IP 列和检查器的"Mapped by"。原型阶段不迁移：`dns.txt` 不再读取，需要的话把内容粘贴进一个方案。

### M3："整理成集合"

- "整理成集合"向导（6.1），保存时附带 Example。
- 抓到的流量识别出对应的已保存请求。
- 抓包 → OpenAPI 串进向导。

首版已实现：六步草稿向导，多选／域名／App 入口，数字和 UUID 路径推断及手动拆合，
Host／Path prefix／Flat 组织、拖动和排序，环境 Current value／Vault 凭据引用，
最近成功及各错误状态 Examples，可选独立 OpenAPI 草稿，以及 Matches 弹层的打开／追加示例。
Current value 仅本机明文存储且不导出；集合 Auth 只共享一致的 Authorization，
Cookie 和其他 API key 保留为变量 header，避免把多个认证机制硬塞进单一 Auth。
连续录制已在 M9 实现（见 6.2），Example → Mock 已在 M4 实现；token 链路识别仍待做。

### M4：规则引擎升级（Tools 窗口）

- Tools 管理窗口：规则、Scenario、脚本、Host 方案集中管理。（已完成：Scenarios、Scripts、Mocks 页已开放；规则页：规则从弹窗移进 Tools，测试请求时按作用顺序标号，显示命中数和 Scenario。）
- 规则匹配：通配符、正则、method、App、header。（已完成）
- Map Local：app 内编辑响应、目录映射。（已完成）
- Map Remote：端口、路径、Host header、WebSocket。（已完成，含原主机不可达时的 HTTPS 映射）
- Block：隐藏或拦截、断开连接、按 App。（已完成）
- Breakpoint 模板。（已完成）
- 规则脚本：`pm` 风格 API、编辑器、异步请求、共享状态、环境变量、内置库、代码片段；`script.js` 迁移。（已完成，`script.js` 按原型规则直接删除不迁移；含自定义 addon）
- Example → Mock 规则、集合 → Scenario。（已完成：URL 填入当前变量后转成正则，`:id` 和未知变量匹配一段，查询串不限；同一请求的其他 Example 用 `X-Mock-Response-Name` 选择；主机不可达时 Map Local 也用生成的证书）
- 规则和设置的导入导出。（已完成）

### M5：检查器和调试增强

- 第一阶段已完成：快速解码、Cookies 标签（抓包两侧和已发送响应）。
- 第二阶段已完成：JSONPath / jq 后台只读查询，保留大整数，支持取消和结果复制。
- 第三阶段已完成：multipart 分段、表单字段表、MessagePack、XML 查看（抓包两侧和已发送响应）。
- 第四阶段已完成：请求/响应 JSON 字段自定义列，保留大小写和整数精度，后台读取、字典序排序；Remote IP 显隐。
- 第五阶段已完成：GraphQL operation 列、精确 operation 筛选与规则／预览、保留语义的 query 格式化。GET／JSON POST／raw query／batch；只认实际选中且可验证的 operation，限制与边界见使用指南。
- 第六阶段已完成：耗时瀑布图，分两次提交请求级 trace／会话保留和原生 UI／HAR 导出。实际起始偏移、重叠阶段、复用、失败／未结束和亚毫秒精度；抓包和精确发送均可查看。不将请求前 MITM 建连、下游交付或估算限速冒充请求级测量。
- 第七阶段已完成：内容筛选 All（AND）／Any（OR），含后台完整正文、独立视图与保存筛选。空字段不成为 OR 真值；搜索／侧栏／协议／时间仍收窄，暂不支持嵌套组。
- 第八阶段已完成：搜索 Text／Wildcard／Regex；通配符整串匹配、Go/RE2 默认忽略大小写，表达式最大 4096 字节，错误可恢复；新到流量、视图／保存筛选与重置保持一致，不把模式内容解析成条件 token。
- 网络条件：自定义、请求断连、按 host／完整规则作用域。（已完成：全局按请求／响应正文方向限速、可取消延迟与请求断连；自定义预设安全持久化；规则按原始 URL／方法／App／Headers／GraphQL operation／Scenario 匹配，首个命中替换全局条件，Off 保存失败不改运行状态。不是 TCP/IP 丢包，不覆盖 WS／不透明 CONNECT；限制见使用指南）
- 第九阶段已完成：高级重放。重复 N 次、并发和间隔，单批 500 次、并发 8、重复 100、间隔 60 s；进度、成功／失败／取消、状态码分布、耗时汇总。复用 Replay 发送路径，结果照常进入表格并带 Replay of 链接，不进入集合录制。
- 第十阶段已完成：Copy as Markdown 表格／CSV／原始 HTTP，以及当前视图／选中的 CSV 导出。表格格式使用可见列；CSV 遵循 RFC 4180 并防公式注入；原始报文按 HTTP/1.1 文本渲染，二进制、缺失和超大正文有明确标记；CSV 导出沿用脱敏对话框。
- 抓包直接生成代码已确认满足：抓包就是请求文档，Request options 和命令面板的 Generate code… 直接可用，仅更新状态。

### M6：Postman 兼容补齐

- Body：binary、GraphQL 基础已完成；GraphQL schema 自省、补全和 operationName 选择待做。
- 请求描述和集合文档、单个请求的设置。
- 证书：请求侧的客户端证书和自定义 CA；抓包侧的上游 mTLS 和自定义根证书。
- 变量：初始值/当前值、自动补全和悬停、完整动态变量（常用项已扩至 32 个，仍未覆盖完整 Faker）。
- 脚本：沙箱库；`have.jsonSchema` 已完成（Ajv 6.12.6，无 UI 改动）。
- Postman Collection v3 导入导出。
- OAuth 1.0、Hawk、Akamai EdgeGrid 已完成，含变量展开、Vault、Send／Runner、Postman 导入导出和代码导出；NTLM 因需要同一连接完成质询握手，当前发送路径每次新建连接，未实现。
- Cookies 可选持久化、全局搜索已完成：Cookie 默认仅在内存（可选保留，0600 本地文件，Session Cookie 不落盘，按域名／全部清除，值默认遮罩）；命令面板可搜索集合、文件夹、请求、环境和变量名，不搜索变量值，请求索引随集合更新。响应保存到文件已完成（含二进制和完整落盘正文）。
- UI 改版第 6 步（VS Code 主题）在 M3 到 M6 期间穿插完成。

### M7：接入和网络

- 一键接入已完成（[使用指南](guide.md#one-click-client-setup)）：终端、Chrome、Firefox、Android 模拟器、iOS 模拟器（仅 macOS）、Java truststore、Electron。
  终端把替换型信任变量指向系统根证书 + app CA 的 0600 bundle，未解密 host 仍可验证；找不到系统 bundle 时只设 `NODE_EXTRA_CA_CERTS` 并警告。
  浏览器只改变临时 profile；Android 用 `adb reverse`，Undo 恢复原代理并移除对应 reverse；Java 先复制 JDK cacerts 再加 CA，JVM 选项含代理 host／port；Electron 不关闭证书校验。
  Chrome/Electron 在 Linux 走 NSS，不读取 app 的 CA 文件；Firefox 临时 profile 在 Linux/macOS 用 `certutil`，Windows 用 enterprise roots。
  临时 profile 在浏览器退出时删除，崩溃遗留的在下次启动清理；不写 hosts，不修改用户全局配置。
- SOCKS5 入口、反向代理。
- 上游代理：PAC、绕过、按 host、认证；系统代理绕过名单。
- 导入：Swagger 2、Insomnia、Bruno、Hoppscotch、PowerShell／CMD cURL 已完成（见[使用指南](guide.md#import-and-export)）；Charles 会话、Proxyman 日志待做。Insomnia v4/v5、Bruno 目录、Hoppscotch 集合与环境、Swagger 2 均转换为现有集合／请求／环境模型；脚本、测试和无法表达的请求设置会明确列入导入报告。

### M8：协议扩展

- gRPC 客户端首版已完成（[使用指南](guide.md#interactive-grpc-requests)）：独立引擎与请求文档两个提交阶段；proto／descriptor set 导入、v1／v1alpha 反射、四种调用方式、双向实时收发、metadata／trailers／结构化错误状态、deadline／取消，schema 随请求保存。TLS 校验；HTTPS 通过运行中的抓包代理，h2c 明确直连不抓包。暂不接 HTTP 脚本／Checks／Runner／History；无自动重连或重发。
- WebSocket 三阶段已完成：① 二进制与子协议（Hex／Base64 发送、WS／WSS 双侧协商）；② 实时会话与抓包消息的显式 Protobuf 解码（复用 schema，拒绝截断消息）；③ 双向消息断点（文本／Base64 编辑、原样放行、单条丢弃）与 `onMessage(message)` 脚本钩子。WS／解密 WSS，脚本先于消息断点；不拦截握手和控制帧。脚本／编辑上限 1 MiB；断开、代理关闭、断点关闭或超时释放暂停，详见[使用指南](guide.md#websocket-message-interception)。
- SSE 客户端首版已完成（[使用指南](guide.md#interactive-sse-sessions)）：独立接收引擎与原生会话面板两个提交阶段；HTTP(S) 经运行中的抓包代理、TLS 校验、UTF-8／多行／空事件／ID 重置、响应头与事件搜索复制、有界时间线。自动重连默认关闭；显式启用后支持 Last-Event-ID／retry，HTTP 204 停止；停止／关闭取消连接及重连等待。GET-only，暂不接请求脚本／Checks／Runner／History，不保存或恢复会话。
- Socket.IO 客户端首版已完成（[使用指南](guide.md#interactive-socketio-requests)）：协议引擎与请求文档两个提交阶段；EIO4／Socket.IO protocol 5、WebSocket-only、namespace／auth、JSON 与二进制附件、双向 ACK、搜索复制、有界时间线和取消。WS／解密 WSS 经抓包代理、TLS 校验、原作用域变量／Vault；官方 Socket.IO 4.8.4 服务端互通。无 polling／自动重连／重试／离线重放，不接 HTTP 脚本／Checks／Runner／History；配置保存，会话不恢复。
- MCP 客户端、MQTT 客户端。

### M9：自动化和生态

- 本地控制接口、CLI／MCP 共用动作闭环已完成：查询、录制／规则开关、系统代理修改与回读、受控 HAR 导出、追加规则导入创建和请求／集合运行。
- 命令行运行集合及 CI 报告、首版内置集合负载已完成（见 [自动化指南](automation.md)）：固定并发用户／时长、隔离状态、HdrHistogram 聚合执行指标；原生面板／CLI／MCP 共用 Runner。不含固定 RPS、爬坡或分布式压测。
- 录制到集合及首版 token 链路识别已完成：原生入口、CLI／MCP 共用动作、Host／App 过滤、Current 凭据引用和 Example 归并；JSON token → 同源 Bearer 的脱敏证据、显式审阅应用和失败关闭。Cookie／refresh／多跳链路暂不覆盖。
- DevTools 式常用复制格式已完成：Bash／CMD cURL、原生 PowerShell、浏览器／Node fetch 和受限 HAR（默认脱敏、明确选择敏感内容）。生成器不增加运行时依赖；cURL 导入选项仍独立待做。
- 首版代码 Mock 已完成（见[使用指南](guide.md#code-mocks)）：Code Mock 规则、`onMock` 动态本地响应、脚本编辑器与 Scenario；HTTP／已解密 HTTPS 不连接原站，失败关闭。Postman Node.js Mock／`pm.state` 等不在首版范围。
- 更多代码生成语言已完成（见[使用指南](guide.md#curl)）：Swift URLSession、Kotlin OkHttp、Java java.net.http、C# HttpClient、PHP cURL、Ruby Net::HTTP、Rust reqwest；method、query、重复／空 header、raw／urlencoded／multipart／GraphQL body 和已展开的 auth header 原样输出，Binary body 引用本地文件。生成器本身不增加运行时依赖；OkHttp／reqwest 由目标项目提供，非 UTF-8 的抓包 body 仍只支持 cURL。
- `pm.visualizer`、可复用脚本库。
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

首版已实现：Capture More／Collections／命令面板／Host 和 App 菜单入口，运行状态和停止 chip，
以及 CLI／MCP 的 `collection-recording.get/start/stop` 共用动作。选择已有环境；Host／App 精确匹配，
数字／UUID 路径推断，生成 Host／路径变量，凭据 header 使用仅本机 Current 引用（明文、不导出，非 Vault）。
重复接口保留手写请求和手动 Examples，仅更新录制专属的最近成功及各状态响应。
只保存完整 UTF-8 请求／响应（每侧最多 5 MiB）；静态资源、流、失败连接和自身 Send／Runner／Replay 排除。
有界后台转换；丢包、过载和保存冲突停止，Stop 丢弃未保存工作且不再写入，重启不自动恢复。
其他 headers／query／正文仍可能含秘密，分享前须审阅。具体合约见 [自动化指南](automation.md#record-live-traffic-into-a-collection)。

### 6.3 双向打通

- **集合 → 抓包**：任意 Example 一键"作为 Mock 响应"，生成一条 Map Local 规则；一个集合的全部 Examples 可以作为一个 Scenario 一起开关，这就是离线的 mock 服务。
- **抓包 → 已有请求**：流量匹配到已保存请求的 method + 路径模板时，检查器里显示"对应请求：Get user"，一键打开，或把这次响应存为新的 Example。
- **链路识别**（首版已完成）：从完整成功 JSON 响应中的 token 字段识别后续同源 Bearer 使用，只展示脱敏证据。选定已保存请求、环境和新变量后审阅，再显式 Apply；保留手写脚本，建立空 Current 和源请求早期清理绑定，生成提取及消费者守卫。录制停止后可直接审阅有界内存证据；CLI／MCP 同样支持 analyze/get/prepare/apply。详见 [自动化指南](automation.md#recognize-and-apply-token-links)。不推断 Cookie、refresh 或多跳依赖。

## 7. Host 方案（SwitchHosts 式）

### 现状

已有 DNS 映射：`~/.vscroxy/dns.txt`，一行一条 `name = address`，支持子域名、`*.` 通配一层、`@ Chrome` 按 App 生效，可以从列表右键"Map this host…"添加，命中的流量有 DNS 标记。它只影响走代理的流量，保留原 host、Host header 和 SNI，不需要管理员权限，设备也能用。

### 要做的

1. **多个命名方案**：比如"本地"、"测试"、"预发"，每个方案是一组映射。（`dns.txt` 不迁移，见 AGENTS.md。）
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
