# Codex App

本项目提供一个自用 Codex 手机端入口：

- `server`：监听 `127.0.0.1:8000` 的本地网关，负责鉴权、连接 Codex app-server、转发实时输出。
- `android`：Android 原生 Kotlin 客户端，通过 WebSocket 连接 `https://xxx.com`。

## 配置文件

复制模板并改成本机配置：

```bash
cp server/.env.example ~/.codex-mobile-gateway.env
```

最小配置：

```env
CODEX_MOBILE_TOKEN=替换为至少16位随机token
CODEX_MOBILE_DEFAULT_CWD=/path/to/your/project
CODEX_BINARY=codex
```

`CODEX_THREAD_ID` 可选。不配置时，网关启动会自动创建一个默认 Codex 线程；手机端发起新会话时会创建并绑定新线程。

手机端连接示例：

```text
https://xxx.com?token=替换为你的长随机token
```

## 本地启动

```bash
cd server
npm install
npm run build
CODEX_MOBILE_TOKEN='替换为你的长随机token' npm run start
```

网关默认监听：

```text
http://127.0.0.1:8000
```

## macOS 常驻

```bash
cd server
npm install
npm run build
bin/install-launchd.sh
```

运行配置保存在：

```text
~/.codex-mobile-gateway.env
```

日志位置：

```text
~/Library/Logs/CodexMobileGateway/gateway.log
~/Library/Logs/CodexMobileGateway/gateway.err.log
```

卸载：

```bash
cd server
bin/uninstall-launchd.sh
```

## Windows 常驻

先准备配置文件：

```powershell
Copy-Item server\.env.example $HOME\.codex-mobile-gateway.env
notepad $HOME\.codex-mobile-gateway.env
```

安装计划任务并启动：

```powershell
cd server
powershell -ExecutionPolicy Bypass -File .\bin\install-windows-task.ps1
```

卸载：

```powershell
powershell -ExecutionPolicy Bypass -File .\bin\uninstall-windows-task.ps1
```

Windows 上 `CODEX_BINARY` 需要配置为本机 Codex 可执行文件路径，或者保证 `codex` 已在 `PATH` 中。

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
