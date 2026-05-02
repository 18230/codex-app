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
- 支持新建会话、切换会话、历史同步和实时回复；手机端新建或对话完成后会自动刷新会话列表。
- 提供配置页、健康检查、目录检查和诊断日志。
- 桌面版 `CodexMobileGateway` 可在界面中配置工作目录、token、Codex 路径并选择线程绑定。
- 桌面版使用 Wails v3 原生 System Tray API，关闭窗口后仍可通过托盘菜单显示、启动、停止或退出网关。
- 连接地址和 token 只保存在手机本地加密存储，不写入仓库。

## 下载

发布包在 GitHub Releases 中提供：

- `CodexMobileGateway.dmg`：macOS Apple Silicon 桌面网关。
- `CodexMobileGateway.exe`：Windows x86_64 便携版桌面网关。
- `app-debug.apk`：Android 手机端安装包。

桌面网关第一次启动会自动生成 token。手机端只需要填写桌面网关界面展示的 WSS 连接地址。

## 桌面网关

桌面版位于 `desktop`，是当前唯一推荐的本地网关实现。首次启动会自动生成 token，`CODEX_THREAD_ID` 不需要手动填写，启动网关后在界面中刷新线程列表并选择要绑定的会话即可。

桌面版基于 Wails v3，使用原生统一 System Tray API：关闭主窗口只会隐藏应用，网关仍可常驻运行；托盘菜单提供显示窗口、启动网关、停止网关和退出。

旧的 Node.js 网关已经移除，后续统一使用 Go + Wails 桌面网关。

macOS Apple Silicon 本机打包：

```bash
cd desktop
./scripts/package-mac.sh
```

产物位置：

```text
desktop/bin/CodexMobileGateway.dmg
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
adb -s 设备序列号 install -r app/build/outputs/apk/debug/app-debug.apk
```
