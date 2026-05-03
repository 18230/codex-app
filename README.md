# Codex App

本项目提供一个自用 Codex 手机端入口：

- `desktop`：Go + Wails v3 桌面网关，监听本机端口，负责鉴权、连接 Codex app-server、转发实时输出和托盘常驻。
- `android`：Android 原生 Kotlin 客户端，通过 WebSocket 连接 `https://xxx.com`。

## 界面预览

<table>
  <tr>
    <td align="center" width="33%">
      <strong>会话侧边栏</strong><br />
      <sub>线程列表、新建会话和快速切换</sub>
    </td>
    <td align="center" width="33%">
      <strong>实时聊天</strong><br />
      <sub>用户消息右侧展示，Codex 回复左侧展示</sub>
    </td>
    <td align="center" width="33%">
      <strong>配置与检查</strong><br />
      <sub>连接测试、健康检查和工作目录检查</sub>
    </td>
  </tr>
  <tr>
    <td align="center">
      <img src="docs/images/screenshot-drawer.jpg" alt="Codex App 会话侧边栏" width="240" />
    </td>
    <td align="center">
      <img src="docs/images/screenshot-chat.jpg" alt="Codex App 实时聊天" width="240" />
    </td>
    <td align="center">
      <img src="docs/images/screenshot-config.jpg" alt="Codex App 配置页面" width="240" />
    </td>
  </tr>
</table>

## 功能特性

- 手机端连接本机 Codex Gateway，复用 Codex 线程能力。
- 支持新建会话、切换会话、历史同步和实时回复；手机端新建、桌面端绑定或对话完成后会自动刷新会话列表。
- 提供配置页、健康检查、目录检查和诊断日志。
- 桌面版 `CodexMobileGateway` 可在界面中配置工作目录、token、Codex 路径并通过会话列表绑定当前会话。
- 桌面版使用 Wails v3 原生 System Tray API，关闭窗口后仍可通过托盘菜单显示、启动、停止或退出网关。
- 桌面网关状态和健康检查不会回显完整连接地址或 token；手机端只能使用桌面网关配置的工作目录。
- 连接地址和 token 只保存在手机本地加密存储，不写入仓库。

## 下载

最新版本：`v0.2.4`。发布包在 GitHub Releases 中提供：

- `CodexMobileGateway-0.2.4-darwin-arm64.dmg`：macOS Apple Silicon 桌面网关。
- `CodexMobileGateway-0.2.4-windows-amd64.exe`：Windows x86_64 便携版桌面网关。
- `codex-app-0.2.4.apk`：Android 手机端安装包。

桌面网关第一次启动会自动生成 token。手机端只需要填写桌面网关界面展示的 WSS 连接地址。

## v0.2.4 更新

- 桌面网关新增按日期拆分的运行、错误和请求日志，便于排查 app-server、HTTP 和 WebSocket 状态。
- 桌面端新增日志页，可直接查看最近日志日期、切换日志类型并按最新时间优先显示最新 50 条记录。
- Codex app-server 标准输出和错误输出会写入日志，同时继续保留原有输出缓存。
- 请求发送失败、超时取消和 Codex RPC 返回错误时会记录脱敏后的摘要，避免泄露 token 和正文内容。
- 手机端锁屏、切后台或网络切换导致的 WebSocket 正常断连会归入运行日志，避免误判为错误。

## v0.2.3 更新

- 桌面网关新增 HTTP/WebSocket 服务 watchdog，监听异常时会自动重建本机 HTTP 服务。
- Codex app-server 断连或子进程异常退出时会立即触发 supervisor 重启，不再只依赖周期轮询发现。
- app-server 重启采用指数退避，连续失败时最多退避到 5 分钟，避免长期故障下高频拉起。
- supervisor 增加 generation 隔离和重启锁，防止旧实例迟到事件影响新实例，也避免同一故障重复触发多次重启。
- `/health` 和桌面诊断区新增 HTTP 重启次数、Codex app-server 重启次数、是否正在重启和下次重启时间，方便长时间运行观测。
- 网关停止时会统一取消 watchdog 和心跳协程，降低反复启停后的后台协程残留风险。

## v0.2.2 更新

- 网关会透传 Codex app-server 嵌套错误信息，避免手机端只显示泛化的“Codex 错误”。
- 手机端用量上限、登录态、目录和连接错误会显示更明确的中文提示。
- 手机端发送后显示“正在思考...”等待态，并根据规划、命令、计划更新和文件变更事件展示轻量阶段提示。
- “正在思考...”和阶段提示的点号会循环动画，完成、断线、错误或收到正文后自动清理。

## v0.2.1 更新

- 桌面端配置页改为“基础配置 / 会话列表”选项卡，诊断消息上移到标题区。
- 保存和启动网关前会检查工作目录和 Codex 可执行文件是否存在。
- 桌面端绑定会话后会同步推送到手机端，手机端会自动切换并刷新历史。
- 手机端会话列表统一命名为“会话列表”，选中态改为浅灰底色。
- 手机端发送新消息后增加轻量“Codex 正在思考”提示。
- 网关状态、健康检查和日志不再回显完整连接地址或 token。

## 桌面网关

桌面版位于 `desktop`，是当前唯一推荐的本地网关实现。首次启动会自动生成 token，`CODEX_THREAD_ID` 不需要手动填写，启动网关后在界面中刷新线程列表并选择要绑定的会话即可。

桌面版基于 Wails v3，使用原生统一 System Tray API：关闭主窗口只会隐藏应用，网关仍可常驻运行；托盘菜单提供显示窗口、启动网关、停止网关和退出。

旧的 Node.js 网关已经移除，后续统一使用 Go + Wails 桌面网关。

macOS Apple Silicon 本机打包：

```bash
cd desktop
./scripts/package-mac.sh
```

只发布桌面网关到现有 GitHub Release：

```bash
cd desktop
./scripts/release-gateway.sh v0.2.4
```

不传 tag 时会自动使用 GitHub latest release。脚本只构建并上传 `CodexMobileGateway-版本-darwin-arm64.dmg` 和 `CodexMobileGateway-版本-windows-amd64.exe`，不会构建或上传 Android APK。

产物位置：

```text
desktop/bin/CodexMobileGateway.dmg
desktop/bin/CodexMobileGateway.exe
android/app/build/outputs/apk/debug/codex-app.apk
```

Windows 便携版通过 GitHub Actions 构建，产物名为 `CodexMobileGateway.exe`。第一版未做代码签名，系统可能提示安全确认。

## 配置

桌面网关的配置都在应用界面中完成：

- `工作目录`：Codex 执行项目任务的目录，手机端会自动读取网关提供的目录。
- `Token`：访问网关的长随机 token，泄露后等同于允许远程控制本机 Codex。
- `Codex 可执行文件`：可留空后点击自动查找，也可填写本机 Codex 可执行文件路径。
- `公网连接地址`：Cloudflare Tunnel 或其他内网穿透暴露出的 HTTPS 地址。

手机端只需要填写桌面网关界面里显示的 WSS 连接地址：

```text
wss://xxx.com/ws?token=替换为你的长随机token
```

配置文件保存在系统用户配置目录：

- macOS：`~/Library/Application Support/CodexMobileGateway/config.json`
- Windows：`%APPDATA%\CodexMobileGateway\config.json`

配置文件包含 token，请不要提交到仓库或公开分享。

## 日志

桌面网关日志按日期和类型写入系统日志目录：

- macOS：`~/Library/Logs/CodexMobileGateway`
- Windows：`%LOCALAPPDATA%\CodexMobileGateway\Logs`

日志文件按类型拆分：

- `run-YYYY-MM-DD.log`：运行日志，包括网关启动停止、手机端连接断开和 Codex app-server 标准输出。
- `error-YYYY-MM-DD.log`：错误日志，包括鉴权失败、请求处理失败、Codex RPC 错误和 app-server 异常。
- `request-YYYY-MM-DD.log`：请求日志，包括 HTTP/WebSocket 摘要和网关到 Codex app-server 的 RPC 摘要。

桌面端“日志”选项卡会按最新时间优先展示指定日期、指定类型的最新 50 条日志。日志写入前会做 token 脱敏。

## 内网穿透

建议使用 Cloudflare Tunnel 把公网域名转发到本机网关 `127.0.0.1:8000`，这样手机端可以通过 HTTPS/WSS 连接本地 Codex Gateway。

Cloudflare Tunnel 的桌面端常驻、域名映射和开机启动配置，可以参考项目：[cloudflare-tunnel-desktop](https://github.com/18230/cloudflare-tunnel-desktop)。

## 本地监听

```text
http://127.0.0.1:8000
```

桌面网关默认监听 `127.0.0.1:8000`。如需公网访问，建议只通过 HTTPS/WSS 的内网穿透入口连接，不要直接暴露本机端口。

## 长连接心跳

- 服务端每 15 秒发送 WebSocket ping，45 秒没有收到 pong 或任何消息就关闭半开连接。
- Android 客户端每 25 秒发送业务心跳，并启用 OkHttp 底层 ping。
- Android 客户端断线后会自动指数退避重连，重连成功后自动恢复当前 thread 并刷新线程列表。

## Android 调试

```bash
cd android
./gradlew installDebug
```

如果需要指定设备：

```bash
adb devices
adb -s 设备序列号 install -r app/build/outputs/apk/debug/codex-app.apk
```
