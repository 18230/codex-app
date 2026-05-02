package work.codex.mobile

import android.content.Context
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.view.inputmethod.InputMethodManager
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Menu
import androidx.compose.material.icons.filled.Send
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.DrawerValue
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalDrawerSheet
import androidx.compose.material3.ModalNavigationDrawer
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.lightColorScheme
import androidx.compose.material3.rememberDrawerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKeys
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONArray
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.launch

private const val APP_HEARTBEAT_INTERVAL_MS = 25_000L
private const val APP_HEARTBEAT_TIMEOUT_MS = 75_000L
private const val RECONNECT_BASE_DELAY_MS = 1_000L
private const val RECONNECT_MAX_DELAY_MS = 15_000L
private const val CONNECTION_URL_KEY = "connection_url_v2"

/**
 * Android 入口 Activity，负责加载安全存储并挂载 Compose 应用。
 */
class MainActivity : ComponentActivity() {
    /**
     * 初始化界面，避免在 Composable 中直接持有 Activity。
     */
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val store = SecureConnectionStore(applicationContext)
        setContent {
            CodexMobileApp(store)
        }
    }
}

/**
 * 连接与输出状态中的消息类型。
 */
private enum class LineKind {
    System,
    User,
    Assistant,
    Command,
    Plan,
    Error,
}

/**
 * 主界面的顶层页面。
 */
private enum class AppScreen {
    Chat,
    Config,
    Diagnostics,
}

/**
 * 表示实时输出区域里的一条可渲染记录。
 */
private data class OutputLine(
    val id: String = UUID.randomUUID().toString(),
    val kind: LineKind,
    val text: String,
    val streamKey: String? = null,
)

/**
 * 表示 Codex 历史线程列表中的一条摘要。
 */
private data class ThreadSummary(
    val id: String,
    val preview: String,
    val cwd: String,
    val updatedAt: Long,
)

/**
 * 诊断面板中的一条本地事件记录。
 */
private data class DiagnosticEntry(
    val id: String = UUID.randomUUID().toString(),
    val level: String,
    val message: String,
    val time: String = nowTimeLabel(),
)

/**
 * 网关健康检查的聚合结果。
 */
private data class HealthReport(
    val gateway: String = "unknown",
    val appServer: String = "unknown",
    val cwd: String = "",
    val threadId: String = "",
    val activeTurnId: String = "",
    val checkedAt: String = "",
)

/**
 * 使用 Android Keystore 包装的 SharedPreferences 保存连接地址。
 */
private class SecureConnectionStore(context: Context) {
    private val preferences = EncryptedSharedPreferences.create(
        "codex_mobile",
        MasterKeys.getOrCreate(MasterKeys.AES256_GCM_SPEC),
        context,
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
    )

    /**
     * 保存原始连接地址，token 不进入普通明文配置。
     */
    fun saveConnectionUrl(url: String) {
        preferences.edit().putString(CONNECTION_URL_KEY, url).apply()
    }

    /**
     * 读取上次成功使用的连接地址。
     */
    fun readConnectionUrl(): String? = preferences.getString(CONNECTION_URL_KEY, null)

    /**
     * 清空已保存的连接地址，用于 token 轮换或切换电脑。
     */
    fun clearConnectionUrl() {
        preferences.edit().remove(CONNECTION_URL_KEY).apply()
    }
}

/**
 * 封装 OkHttp WebSocket，统一把回调切回主线程。
 */
private class GatewayClient(
    private val url: String,
    private val onOpen: (GatewayClient) -> Unit,
    private val onEvent: (JSONObject) -> Unit,
    private val onClosed: (String) -> Unit,
    private val onReconnecting: (String, Long) -> Unit,
    private val onError: (String) -> Unit,
) {
    private val mainHandler = Handler(Looper.getMainLooper())
    private val httpClient = OkHttpClient.Builder()
        .pingInterval(15, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.SECONDS)
        .build()
    private var webSocket: WebSocket? = null
    private var closedByUser = false
    private var reconnectAttempt = 0
    private var lastMessageAt = 0L
    private var reconnectRunnable: Runnable? = null
    private val heartbeatRunnable = object : Runnable {
        /**
         * 定期发送业务心跳，补充 OkHttp 底层 ping 对 UI 状态不可见的问题。
         */
        override fun run() {
            sendHeartbeat()
            mainHandler.postDelayed(this, APP_HEARTBEAT_INTERVAL_MS)
        }
    }

    /**
     * 建立 WebSocket 连接。
     */
    fun connect() {
        closedByUser = false
        reconnectAttempt = 0
        openSocket()
    }

    /**
     * 打开一次 WebSocket 连接，失败后交给重连调度处理。
     */
    private fun openSocket() {
        reconnectRunnable?.let { mainHandler.removeCallbacks(it) }
        reconnectRunnable = null
        val request = Request.Builder().url(url).build()
        webSocket = httpClient.newWebSocket(
            request,
            object : WebSocketListener() {
                /**
                 * 连接成功后通知 UI 恢复当前 Codex 线程。
                 */
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    mainHandler.post {
                        reconnectAttempt = 0
                        lastMessageAt = SystemClock.elapsedRealtime()
                        startHeartbeat()
                        onOpen(this@GatewayClient)
                    }
                }

                /**
                 * 收到网关事件后转成 JSON 对象。
                 */
                override fun onMessage(webSocket: WebSocket, text: String) {
                    mainHandler.post {
                        runCatching { JSONObject(text) }
                            .onSuccess { event ->
                                lastMessageAt = SystemClock.elapsedRealtime()
                                if (event.optString("type") != "pong") onEvent(event)
                            }
                            .onFailure { onError("无法解析服务端消息") }
                    }
                }

                /**
                 * 连接关闭时同步 UI 状态。
                 */
                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    mainHandler.post {
                        stopHeartbeat()
                        this@GatewayClient.webSocket = null
                        val message = reason.ifBlank { "连接已关闭" }
                        if (closedByUser) onClosed(message) else scheduleReconnect(message)
                    }
                }

                /**
                 * 连接失败时给出可见错误。
                 */
                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    mainHandler.post {
                        stopHeartbeat()
                        this@GatewayClient.webSocket = null
                        val message = t.message ?: "连接失败"
                        if (closedByUser) onClosed(message) else scheduleReconnect(message)
                    }
                }
            },
        )
    }

    /**
     * 发送手机端业务消息。
     */
    fun send(payload: JSONObject) {
        payload.put("id", UUID.randomUUID().toString())
        val sent = webSocket?.send(payload.toString()) ?: false
        if (!sent) onError("消息发送失败，等待连接恢复")
    }

    /**
     * 主动断开连接。
     */
    fun close() {
        closedByUser = true
        stopHeartbeat()
        reconnectRunnable?.let { mainHandler.removeCallbacks(it) }
        reconnectRunnable = null
        webSocket?.close(1000, "client closed")
        webSocket = null
        httpClient.dispatcher.executorService.shutdown()
    }

    /**
     * 启动应用层心跳定时器，连接成功后才会运行。
     */
    private fun startHeartbeat() {
        stopHeartbeat()
        mainHandler.postDelayed(heartbeatRunnable, APP_HEARTBEAT_INTERVAL_MS)
    }

    /**
     * 停止应用层心跳，避免旧连接重连后仍在发送。
     */
    private fun stopHeartbeat() {
        mainHandler.removeCallbacks(heartbeatRunnable)
    }

    /**
     * 发送 ping 并在长时间没有收到服务端消息时触发重连。
     */
    private fun sendHeartbeat() {
        val socket = webSocket ?: return
        val now = SystemClock.elapsedRealtime()
        if (lastMessageAt > 0 && now - lastMessageAt > APP_HEARTBEAT_TIMEOUT_MS) {
            socket.cancel()
            webSocket = null
            scheduleReconnect("心跳超时")
            return
        }

        socket.send(JSONObject().put("type", "ping").put("id", "heartbeat-${UUID.randomUUID()}").toString())
    }

    /**
     * 按指数退避重连，避免网络恢复前持续打满请求。
     */
    private fun scheduleReconnect(reason: String) {
        if (closedByUser || reconnectRunnable != null) return
        reconnectAttempt += 1
        val multiplier = 1L shl (reconnectAttempt - 1).coerceAtMost(4)
        val delay = (RECONNECT_BASE_DELAY_MS * multiplier).coerceAtMost(RECONNECT_MAX_DELAY_MS)
        onReconnecting(reason, delay)
        val task = Runnable {
            reconnectRunnable = null
            if (!closedByUser) openSocket()
        }
        reconnectRunnable = task
        mainHandler.postDelayed(task, delay)
    }
}

/**
 * 渲染 Codex 手机端主界面。
 */
@Composable
private fun CodexMobileApp(store: SecureConnectionStore) {
    val savedUrl = remember { mutableStateOf(store.readConnectionUrl()) }
    var connectionInput by remember { mutableStateOf(maskConnectionUrl(savedUrl.value.orEmpty())) }
    var gatewayWorkspace by remember { mutableStateOf("") }
    var status by remember { mutableStateOf("未连接") }
    var threadId by remember { mutableStateOf("") }
    var threadTitle by remember { mutableStateOf("无标题会话") }
    var draft by remember { mutableStateOf("") }
    var activeTurnId by remember { mutableStateOf<String?>(null) }
    var pendingThreadSwitchId by remember { mutableStateOf<String?>(null) }
    var client by remember { mutableStateOf<GatewayClient?>(null) }
    var autoConnectDone by remember { mutableStateOf(false) }
    var healthReport by remember { mutableStateOf(HealthReport()) }
    var currentScreen by remember { mutableStateOf(AppScreen.Chat) }
    val drawerState = rememberDrawerState(initialValue = DrawerValue.Closed)
    val scope = rememberCoroutineScope()
    val keyboardController = LocalSoftwareKeyboardController.current
    val focusManager = LocalFocusManager.current
    val mainHandler = remember { Handler(Looper.getMainLooper()) }
    val outputLines = remember { mutableStateListOf<OutputLine>() }
    val threadSummaries = remember { mutableStateListOf<ThreadSummary>() }
    val diagnostics = remember { mutableStateListOf<DiagnosticEntry>() }
    var scrollNonce by remember { mutableStateOf(0) }

    /**
     * 记录最近诊断事件，限制数量避免长时间运行后占用过多内存。
     */
    fun addDiagnostic(level: String, message: String) {
        if (message.isBlank()) return
        diagnostics.add(0, DiagnosticEntry(level = level, message = message))
        while (diagnostics.size > 60) diagnostics.removeAt(diagnostics.lastIndex)
    }

    /**
     * 更新顶部状态，并按需写入诊断日志。
     */
    fun setAppStatus(nextStatus: String, log: Boolean = false) {
        status = nextStatus
        if (log) addDiagnostic("状态", nextStatus)
    }

    /**
     * 请求输出列表滚到底部，覆盖历史切换和流式追加两类场景。
     */
    fun requestOutputScroll() {
        scrollNonce += 1
    }

    /**
     * 追加一条输出并显式触发底部滚动。
     */
    fun appendOutput(kind: LineKind, text: String, streamKey: String? = null) {
        appendLine(outputLines, kind, text, streamKey)
        requestOutputScroll()
    }

    /**
     * 展示服务端错误，并为空错误提供稳定兜底，避免界面出现空白错误行。
     */
    fun appendGatewayError(rawMessage: String) {
        val message = classifyGatewayError(rawMessage)
        setAppStatus(errorStatusLabel(message), log = true)
        addDiagnostic("错误", message)
        appendOutput(LineKind.Error, message)
    }

    /**
     * 请求网关返回当前健康状态。
     */
    fun requestHealthCheck() {
        val activeClient = client
        if (activeClient == null) {
            addDiagnostic("健康检查", "尚未连接网关")
            setAppStatus("未连接", log = true)
            return
        }
        addDiagnostic("健康检查", "已发送健康检查请求")
        activeClient.send(JSONObject().put("type", "health.check"))
    }

    /**
     * 清空本地保存的连接地址，下次打开仍需要手动输入。
     */
    fun clearSavedConnection() {
        store.clearConnectionUrl()
        savedUrl.value = null
        connectionInput = ""
        addDiagnostic("配置", "已清空手机端连接地址")
        setAppStatus("连接已重置", log = true)
    }

    /**
     * 请求刷新手机端会话列表；部分标题更新有落盘延迟，允许延迟补刷。
     */
    fun requestThreadList(delayMs: Long = 0L) {
        val task = Runnable {
            client?.send(JSONObject().put("type", "thread.list"))
        }
        if (delayMs <= 0L) {
            task.run()
        } else {
            mainHandler.postDelayed(task, delayMs)
        }
    }

    /**
     * 处理网关事件并更新界面状态。
     */
    fun handleEvent(event: JSONObject) {
        when (event.optString("type")) {
            "ready" -> {
                threadId = event.optString("threadId")
                gatewayWorkspace = event.optString("cwd").ifBlank { gatewayWorkspace }
                setAppStatus("已连接", log = true)
            }
            "thread" -> {
                val thread = event.optJSONObject("thread")
                val nextThreadId = thread?.optString("id").orEmpty()
                val nextCwd = thread?.optString("cwd").orEmpty()
                val nextTitle = thread?.let { firstNonBlankJsonString(it, "name", "title", "preview") }.orEmpty()
                if (nextThreadId.isNotBlank()) threadId = nextThreadId
                if (nextCwd.isNotBlank()) gatewayWorkspace = nextCwd
                if (nextThreadId.isNotBlank()) {
                    threadTitle = nextTitle.ifBlank { "无标题会话" }
                    if (pendingThreadSwitchId == nextThreadId) {
                        pendingThreadSwitchId = null
                        setAppStatus("已切换", log = true)
                        addDiagnostic("会话", "已切换到：${threadTitle.ifBlank { nextThreadId.take(8) }}")
                    } else if (status == "正在新建会话") {
                        setAppStatus("已新建", log = true)
                        addDiagnostic("会话", "已新建会话：${threadTitle.ifBlank { nextThreadId.take(8) }}")
                    }
                    requestThreadList()
                    requestThreadList(1200L)
                }
            }
            "threads" -> {
                threadSummaries.clear()
                threadSummaries.addAll(parseThreadSummaries(event.optJSONArray("threads")))
                threadSummaries.firstOrNull { it.id == threadId }?.preview
                    ?.takeIf { it.isNotBlank() }
                    ?.let { threadTitle = it }
            }
            "history" -> {
                val nextThreadId = event.optString("threadId")
                if (nextThreadId.isNotBlank()) threadId = nextThreadId
                val resolvedThreadId = nextThreadId.ifBlank { threadId }
                threadSummaries.firstOrNull { it.id == resolvedThreadId }?.preview
                    ?.takeIf { it.isNotBlank() }
                    ?.let { threadTitle = it }
                outputLines.clear()
                outputLines.addAll(parseHistoryLines(event.optJSONArray("lines")))
                if (pendingThreadSwitchId == resolvedThreadId) {
                    pendingThreadSwitchId = null
                    setAppStatus("已切换", log = true)
                    addDiagnostic("会话", "已同步 ${outputLines.size} 条历史记录")
                }
                requestOutputScroll()
            }
            "turn.started" -> {
                activeTurnId = event.optString("turnId")
                setAppStatus("正在执行", log = true)
            }
            "turn.completed" -> {
                activeTurnId = null
                setAppStatus("执行完成", log = true)
                requestThreadList()
                requestThreadList(1200L)
            }
            "delta" -> appendOutput(
                LineKind.Assistant,
                event.optString("text"),
                event.optString("itemId"),
            )
            "command.delta" -> setAppStatus("正在执行命令")
            "plan.delta" -> setAppStatus("正在规划")
            "plan.updated" -> setAppStatus("计划已更新")
            "file.patch" -> setAppStatus("文件变更已更新")
            "status" -> setAppStatus("Codex 状态更新")
            "threads.changed" -> {
                requestThreadList()
                requestThreadList(1200L)
            }
            "health" -> {
                healthReport = parseHealthReport(event.optJSONObject("report"))
                if (healthReport.cwd.isNotBlank()) gatewayWorkspace = healthReport.cwd
                val nextStatus = if (healthReport.appServer == "connected") "健康正常" else "Codex 未连接"
                setAppStatus(nextStatus, log = true)
                addDiagnostic("健康检查", healthReportSummary(healthReport))
            }
            "workspace" -> {
                if (event.optBoolean("ok", false)) {
                    val checkedCwd = event.optString("cwd").ifBlank { gatewayWorkspace }
                    gatewayWorkspace = checkedCwd
                    setAppStatus("目录可用", log = true)
                    addDiagnostic("目录检查", "目录可用：$checkedCwd")
                } else {
                    val error = event.optString("error").ifBlank { "目录不可用" }
                    setAppStatus("目录错误", log = true)
                    addDiagnostic("目录检查", error)
                    appendOutput(LineKind.Error, "目录检查失败：$error")
                }
            }
            "response" -> {
                if (!event.optBoolean("ok", true)) {
                    appendGatewayError(event.optString("error"))
                }
            }
            "error" -> appendGatewayError(event.optString("message"))
        }
    }

    /**
     * 建立连接并在成功后恢复当前线程。
     */
    fun connect() {
        val rawUrl = runCatching { resolveConnectionUrl(connectionInput, savedUrl.value) }
            .getOrElse {
                appendOutput(LineKind.Error, it.message ?: "连接地址不合法")
                return
            }
        val wsUrl = runCatching { parseGatewayWsUrl(rawUrl) }
            .getOrElse {
                appendOutput(LineKind.Error, it.message ?: "连接地址不合法")
                return
            }

        client?.close()
        setAppStatus("连接中", log = true)

        val gateway = GatewayClient(
            url = wsUrl,
            onOpen = { opened ->
                store.saveConnectionUrl(rawUrl)
                savedUrl.value = rawUrl
                connectionInput = maskConnectionUrl(rawUrl)
                setAppStatus("已连接", log = true)
                opened.send(JSONObject().put("type", "thread.read"))
                opened.send(JSONObject().put("type", "thread.list"))
                opened.send(JSONObject().put("type", "health.check"))
            },
            onEvent = ::handleEvent,
            onClosed = { reason ->
                setAppStatus(reason, log = true)
            },
            onReconnecting = { reason, delayMs ->
                setAppStatus("重连中：${delayMs / 1000} 秒后重试", log = true)
                addDiagnostic("连接", "连接异常：$reason")
            },
            onError = { message ->
                if (status != "重连中") setAppStatus("连接异常", log = true)
                addDiagnostic("连接", message)
                appendOutput(LineKind.Error, message)
            },
        )
        client = gateway
        gateway.connect()
    }

    LaunchedEffect(savedUrl.value) {
        if (!autoConnectDone && !savedUrl.value.isNullOrBlank()) {
            autoConnectDone = true
            connect()
        }
    }

    MaterialTheme(
        colorScheme = lightColorScheme(
            primary = Color(0xFF111111),
            secondary = Color(0xFF111111),
            tertiary = Color(0xFF111111),
            surface = Color.White,
            background = Color.White,
        ),
    ) {
        ModalNavigationDrawer(
            drawerState = drawerState,
            drawerContent = {
                ModalDrawerSheet(
                    modifier = Modifier.fillMaxWidth(0.88f),
                    drawerContainerColor = Color.White,
                ) {
                    Column(
                        modifier = Modifier
                            .fillMaxSize()
                            .statusBarsPadding()
                            .navigationBarsPadding()
                            .padding(16.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        Text(
                            text = "Codex",
                            style = MaterialTheme.typography.titleLarge,
                            fontWeight = FontWeight.SemiBold,
                        )
                        DrawerNavPanel(
                            currentScreen = currentScreen,
                            onOpenChat = {
                                currentScreen = AppScreen.Chat
                                scope.launch { drawerState.close() }
                            },
                            onOpenConfig = {
                                currentScreen = AppScreen.Config
                                scope.launch { drawerState.close() }
                            },
                            onOpenDiagnostics = {
                                currentScreen = AppScreen.Diagnostics
                                scope.launch { drawerState.close() }
                            },
                        )
                        ThreadListPanel(
                            threads = threadSummaries,
                            currentThreadId = threadId,
                            onCreate = {
                currentScreen = AppScreen.Chat
                client?.send(JSONObject().put("type", "thread.start"))
                setAppStatus("正在新建会话", log = true)
                scope.launch { drawerState.close() }
            },
                            onRefresh = {
                                client?.send(JSONObject().put("type", "thread.list"))
                            },
                            onSwitch = { summary ->
                                currentScreen = AppScreen.Chat
                                if (summary.cwd.isNotBlank()) gatewayWorkspace = summary.cwd
                                threadTitle = summary.preview.ifBlank { "无标题会话" }
                                pendingThreadSwitchId = summary.id
                                setAppStatus("正在切换", log = true)
                                addDiagnostic("会话", "请求切换到：${threadTitle.ifBlank { summary.id.take(8) }}")
                                client?.send(
                                    JSONObject()
                                        .put("type", "thread.resume")
                                        .put("threadId", summary.id),
                                )
                                scope.launch { drawerState.close() }
                            },
                        )
                    }
                }
            },
        ) {
            Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                Column(
                    modifier = Modifier
                        .fillMaxSize()
                        .statusBarsPadding()
                        .padding(horizontal = 16.dp, vertical = 12.dp),
                    verticalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        IconButton(
                            onClick = { scope.launch { drawerState.open() } },
                            modifier = Modifier.size(36.dp),
                        ) {
                            Icon(
                                Icons.Filled.Menu,
                                contentDescription = "线程",
                                modifier = Modifier.size(20.dp),
                            )
                        }
                        Header(
                            title = threadTitle,
                            status = status,
                            threadId = threadId,
                            modifier = Modifier.weight(1f),
                        )
                    }
                    when (currentScreen) {
                        AppScreen.Chat -> {
                            OutputList(
                                lines = outputLines,
                                scrollSignal = scrollNonce,
                                hasConnection = !savedUrl.value.isNullOrBlank(),
                                status = status,
                                onCopy = { copyText ->
                                    // 复制动作由气泡内部执行，这里保留扩展点。
                                    addDiagnostic("消息", "已复制 ${copyText.length} 个字符")
                                },
                                onQuote = { text ->
                                    draft = if (draft.isBlank()) text else "$draft\n$text"
                                    addDiagnostic("消息", "已引用消息到输入框")
                                },
                                onResend = { text ->
                                    draft = text
                                    addDiagnostic("消息", "已放回输入框，可再次发送")
                                },
                                modifier = Modifier.weight(1f),
                            )
                            InputBar(
                                draft = draft,
                                activeTurnId = activeTurnId,
                                onDraftChange = { draft = it },
                                onInterrupt = {
                                    activeTurnId?.let {
                                        client?.send(JSONObject().put("type", "turn.interrupt").put("turnId", it))
                                    }
                                },
                                onSend = {
                                    val text = draft.trim()
                                    if (text.isEmpty()) return@InputBar
                                    keyboardController?.hide()
                                    focusManager.clearFocus()
                                    appendOutput(LineKind.User, text)
                                    val payload = if (activeTurnId == null) {
                                        JSONObject()
                                            .put("type", "turn.start")
                                            .put("text", text)
                                    } else {
                                        JSONObject()
                                            .put("type", "turn.steer")
                                            .put("expectedTurnId", activeTurnId)
                                            .put("text", text)
                                    }
                                    client?.send(payload)
                                    draft = ""
                                },
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .navigationBarsPadding()
                                    .imePadding(),
                            )
                        }

                        AppScreen.Config -> ConfigScreen(
                            connectionInput = connectionInput,
                            onConnectionChange = { connectionInput = it },
                            gatewayWorkspace = gatewayWorkspace,
                            status = status,
                            savedUrl = savedUrl.value,
                            healthReport = healthReport,
                            onConnect = ::connect,
                            onDisconnect = {
                                client?.close()
                                client = null
                                setAppStatus("已断开", log = true)
                            },
                            onClearConnection = ::clearSavedConnection,
                            onHealthCheck = ::requestHealthCheck,
                            modifier = Modifier.weight(1f),
                        )

                        AppScreen.Diagnostics -> DiagnosticsScreen(
                            diagnostics = diagnostics,
                            onClear = {
                                diagnostics.clear()
                                addDiagnostic("诊断", "诊断日志已清空")
                            },
                            modifier = Modifier.weight(1f),
                        )
                    }
                }
            }
        }
    }
}

/**
 * 顶部展示当前会话标题、连接状态和当前线程。
 */
@Composable
private fun Header(title: String, status: String, threadId: String, modifier: Modifier = Modifier) {
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(
            text = title.ifBlank { "无标题会话" },
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = if (threadId.isBlank()) status else "$status · ${threadId.take(8)}",
            style = MaterialTheme.typography.labelMedium,
            color = Color(0xFF4E5A54),
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * 侧边栏顶部导航入口，配置和诊断页从这里进入。
 */
@Composable
private fun DrawerNavPanel(
    currentScreen: AppScreen,
    onOpenChat: () -> Unit,
    onOpenConfig: () -> Unit,
    onOpenDiagnostics: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        DrawerNavRow("聊天", currentScreen == AppScreen.Chat, onOpenChat)
        DrawerNavRow("配置", currentScreen == AppScreen.Config, onOpenConfig)
        DrawerNavRow("诊断", currentScreen == AppScreen.Diagnostics, onOpenDiagnostics)
    }
}

/**
 * 渲染侧边栏的单个导航行。
 */
@Composable
private fun DrawerNavRow(text: String, selected: Boolean, onClick: () -> Unit) {
    Text(
        text = text,
        modifier = Modifier
            .fillMaxWidth()
            .background(if (selected) Color(0xFFF1F1F1) else Color.White, RoundedCornerShape(8.dp))
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 9.dp),
        style = MaterialTheme.typography.bodyLarge,
        fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
        color = Color(0xFF1F2622),
    )
}

/**
 * 独立配置页，集中处理连接和健康检查。
 */
@Composable
private fun ConfigScreen(
    connectionInput: String,
    onConnectionChange: (String) -> Unit,
    gatewayWorkspace: String,
    status: String,
    savedUrl: String?,
    healthReport: HealthReport,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onClearConnection: () -> Unit,
    onHealthCheck: () -> Unit,
    modifier: Modifier = Modifier,
) {
    LazyColumn(
        modifier = modifier.fillMaxWidth(),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        contentPadding = PaddingValues(bottom = 20.dp),
    ) {
        item {
            Text("配置", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        }
        item {
            ConnectionPanel(
                connectionInput = connectionInput,
                onConnectionChange = onConnectionChange,
                gatewayWorkspace = gatewayWorkspace,
                status = status,
                hasSavedConnection = !savedUrl.isNullOrBlank(),
                onConnect = onConnect,
                onDisconnect = onDisconnect,
                onClearConnection = onClearConnection,
                onHealthCheck = onHealthCheck,
            )
        }
        item {
            HealthPanel(healthReport = healthReport)
        }
    }
}

/**
 * 渲染连接地址和网关提供的工作目录。
 */
@Composable
private fun ConnectionPanel(
    connectionInput: String,
    onConnectionChange: (String) -> Unit,
    gatewayWorkspace: String,
    status: String,
    hasSavedConnection: Boolean,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onClearConnection: () -> Unit,
    onHealthCheck: () -> Unit,
) {
    Card(
        shape = RoundedCornerShape(8.dp),
        colors = CardDefaults.cardColors(containerColor = Color.White),
    ) {
        Column(
            modifier = Modifier.padding(10.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Text("连接", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            CompactTextField(
                value = connectionInput,
                onValueChange = onConnectionChange,
                modifier = keyboardOnFocusModifier(
                    Modifier
                        .fillMaxWidth()
                        .heightIn(min = 42.dp),
                ),
                placeholder = "连接地址",
                singleLine = true,
            )
            Text(
                text = if (hasSavedConnection) "已保存连接地址 · $status" else "未保存连接地址 · $status",
                style = MaterialTheme.typography.bodySmall,
                color = Color(0xFF5A665F),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                CompactActionButton(text = "连接测试", onClick = onConnect)
                CompactActionButton(text = "断开", onClick = onDisconnect)
                CompactActionButton(text = "清空", onClick = onClearConnection)
            }
            Text("工作目录", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            Text(
                text = gatewayWorkspace.ifBlank { "连接网关后自动获取" },
                modifier = Modifier.fillMaxWidth(),
                style = MaterialTheme.typography.bodyMedium,
                color = Color(0xFF1F2622),
            )
            Text(
                text = "工作目录由桌面网关统一提供，手机端无需单独填写。",
                style = MaterialTheme.typography.bodySmall,
                color = Color(0xFF5A665F),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                CompactActionButton(text = "健康检查", onClick = onHealthCheck)
            }
        }
    }
}

/**
 * 展示最近一次健康检查结果。
 */
@Composable
private fun HealthPanel(healthReport: HealthReport) {
    Card(shape = RoundedCornerShape(8.dp), colors = CardDefaults.cardColors(containerColor = Color.White)) {
        Column(
            modifier = Modifier.padding(10.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            Text("健康状态", style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
            HealthLine("网关", healthReport.gateway)
            HealthLine("Codex", healthReport.appServer)
            HealthLine("工作目录", healthReport.cwd.ifBlank { "未检查" })
            HealthLine("线程", healthReport.threadId.take(8).ifBlank { "未绑定" })
            HealthLine("检查时间", healthReport.checkedAt.ifBlank { "未检查" })
        }
    }
}

/**
 * 健康检查结果中的一行键值。
 */
@Composable
private fun HealthLine(label: String, value: String) {
    Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Text(label, modifier = Modifier.width(72.dp), style = MaterialTheme.typography.bodySmall, color = Color(0xFF5A665F))
        Text(value, modifier = Modifier.weight(1f), style = MaterialTheme.typography.bodySmall, color = Color(0xFF1F2622))
    }
}

/**
 * 独立诊断页，展示最近关键事件，便于真机排障。
 */
@Composable
private fun DiagnosticsScreen(
    diagnostics: List<DiagnosticEntry>,
    onClear: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("诊断日志", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            CompactActionButton(text = "清空", onClick = onClear)
        }
        if (diagnostics.isEmpty()) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("暂无诊断事件", color = Color(0xFF5A665F), style = MaterialTheme.typography.bodyMedium)
            }
        } else {
            LazyColumn(
                modifier = Modifier.fillMaxWidth(),
                verticalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                items(diagnostics, key = { it.id }) { entry ->
                    DiagnosticRow(entry)
                }
            }
        }
    }
}

/**
 * 渲染单条诊断事件。
 */
@Composable
private fun DiagnosticRow(entry: DiagnosticEntry) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(Color.White, RoundedCornerShape(8.dp))
            .border(1.dp, Color(0xFFE5E5E5), RoundedCornerShape(8.dp))
            .padding(10.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(entry.level, style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.SemiBold)
            Text(entry.time, style = MaterialTheme.typography.bodySmall, color = Color(0xFF5A665F))
        }
        Text(entry.message, style = MaterialTheme.typography.bodySmall, color = Color(0xFF1F2622))
    }
}

/**
 * 渲染线程列表，并提供一键切换当前 Codex thread。
 */
@Composable
private fun ThreadListPanel(
    threads: List<ThreadSummary>,
    currentThreadId: String,
    onCreate: () -> Unit,
    onRefresh: () -> Unit,
    onSwitch: (ThreadSummary) -> Unit,
) {
    Column(
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "最近",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                CompactActionButton(text = "新建", icon = Icons.Filled.Add, onClick = onCreate)
                CompactActionButton(text = "刷新", onClick = onRefresh)
            }
        }
        if (threads.isEmpty()) {
            Text(
                text = "暂无线程列表",
                color = Color(0xFF5A665F),
                style = MaterialTheme.typography.bodyMedium,
            )
        } else {
            LazyColumn(
                modifier = Modifier
                    .fillMaxWidth()
                    .heightIn(max = 620.dp),
                verticalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                items(threads, key = { it.id }) { thread ->
                    ThreadRow(
                        thread = thread,
                        selected = thread.id == currentThreadId,
                        onSwitch = { onSwitch(thread) },
                    )
                }
            }
        }
    }
}

/**
 * 渲染单个线程摘要行。
 */
@Composable
private fun ThreadRow(
    thread: ThreadSummary,
    selected: Boolean,
    onSwitch: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onSwitch)
            .background(Color.White, RoundedCornerShape(8.dp))
            .border(
                if (selected) 1.dp else 0.dp,
                if (selected) Color(0xFF111111) else Color.Transparent,
                RoundedCornerShape(8.dp),
            )
            .padding(horizontal = 10.dp, vertical = 9.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(
            text = thread.preview.ifBlank { "无标题会话" },
            modifier = Modifier.weight(1f),
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * 渲染白底细边框的小操作按钮，保持侧边栏风格干净。
 */
@Composable
private fun CompactActionButton(
    text: String,
    onClick: () -> Unit,
    icon: ImageVector? = null,
) {
    Row(
        modifier = Modifier
            .background(Color.White, RoundedCornerShape(16.dp))
            .border(1.dp, Color(0xFFD6D6D6), RoundedCornerShape(16.dp))
            .clickable(onClick = onClick)
            .padding(horizontal = 10.dp, vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        if (icon != null) {
            Icon(icon, contentDescription = text, modifier = Modifier.size(14.dp), tint = Color(0xFF1F2622))
        }
        Text(text, style = MaterialTheme.typography.bodySmall, color = Color(0xFF1F2622))
    }
}

/**
 * 渲染紧凑文本输入框，避开 Material OutlinedTextField 的固定最小高度。
 */
@Composable
private fun CompactTextField(
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    modifier: Modifier = Modifier,
    singleLine: Boolean = false,
    minLines: Int = 1,
    maxLines: Int = 1,
) {
    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        modifier = modifier,
        singleLine = singleLine,
        minLines = minLines,
        maxLines = maxLines,
        textStyle = MaterialTheme.typography.bodyMedium.copy(color = Color(0xFF1F2622)),
        decorationBox = { innerTextField ->
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .background(Color.White, RoundedCornerShape(6.dp))
                    .border(1.dp, Color(0xFF7E7A82), RoundedCornerShape(6.dp))
                    .padding(horizontal = 12.dp, vertical = 8.dp),
                contentAlignment = Alignment.CenterStart,
            ) {
                if (value.isEmpty()) {
                    Text(
                        text = placeholder,
                        style = MaterialTheme.typography.bodyMedium,
                        color = Color(0xFF5F6661),
                    )
                }
                innerTextField()
            }
        },
    )
}

/**
 * 渲染实时输出列表。
 */
@Composable
private fun OutputList(
    lines: List<OutputLine>,
    scrollSignal: Int,
    hasConnection: Boolean,
    status: String,
    onCopy: (String) -> Unit,
    onQuote: (String) -> Unit,
    onResend: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val listState = rememberLazyListState()
    val lastLine = lines.lastOrNull()

    LaunchedEffect(scrollSignal, lines.size, lastLine?.text) {
        if (lines.isNotEmpty()) {
            withFrameNanos { }
            listState.scrollToItem(lines.size)
        }
    }

    if (lines.isEmpty()) {
        EmptyChatState(hasConnection = hasConnection, status = status, modifier = modifier)
    } else {
        LazyColumn(
            modifier = modifier
                .fillMaxWidth()
                .background(Color.White)
                .padding(vertical = 8.dp),
            state = listState,
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            items(lines, key = { it.id }) { line ->
                OutputBubble(
                    line = line,
                    onCopy = onCopy,
                    onQuote = onQuote,
                    onResend = onResend,
                )
            }
            item(key = "bottom-anchor") {
                Spacer(Modifier.height(1.dp))
            }
        }
    }
}

/**
 * 聊天页空状态，提示先完成配置或直接开始对话。
 */
@Composable
private fun EmptyChatState(hasConnection: Boolean, status: String, modifier: Modifier = Modifier) {
    Box(
        modifier = modifier.fillMaxWidth(),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(8.dp),
            modifier = Modifier.padding(horizontal = 20.dp),
        ) {
            Text(
                text = if (hasConnection) "开始新的对话" else "请先打开配置页填写连接地址",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                textAlign = TextAlign.Center,
            )
            Text(
                text = status,
                style = MaterialTheme.typography.bodySmall,
                color = Color(0xFF5A665F),
                textAlign = TextAlign.Center,
            )
        }
    }
}

/**
 * 根据消息来源渲染单条输出。
 */
@Composable
private fun OutputBubble(
    line: OutputLine,
    onCopy: (String) -> Unit,
    onQuote: (String) -> Unit,
    onResend: (String) -> Unit,
) {
    val isUser = line.kind == LineKind.User
    val family = if (line.kind == LineKind.Command) FontFamily.Monospace else FontFamily.Default
    val context = LocalContext.current
    val clipboardManager = LocalClipboardManager.current
    var menuExpanded by remember { mutableStateOf(false) }

	    Row(
	        modifier = Modifier.fillMaxWidth(),
	        horizontalArrangement = if (isUser) Arrangement.End else Arrangement.Start,
	    ) {
	        val bubbleBackground = if (isUser) Color(0xFFF1F1F1) else Color.White
            Box {
                Text(
                    text = line.text,
                    modifier = Modifier
                        .widthIn(max = 320.dp)
                        .background(bubbleBackground, RoundedCornerShape(8.dp))
                        .pointerInput(line.text) {
                            detectTapGestures(
                                onLongPress = {
                                    menuExpanded = true
                                },
                            )
                        }
                        .padding(10.dp),
                    color = Color(0xFF1F2622),
                    fontFamily = family,
                    textAlign = if (isUser) TextAlign.End else TextAlign.Start,
                    style = MaterialTheme.typography.bodyMedium,
                )
                DropdownMenu(expanded = menuExpanded, onDismissRequest = { menuExpanded = false }) {
                    DropdownMenuItem(
                        text = { Text("复制") },
                        onClick = {
                            menuExpanded = false
                            copyMessageToClipboard(context, clipboardManager, line.text)
                            onCopy(line.text)
                        },
                    )
                    DropdownMenuItem(
                        text = { Text("引用到输入框") },
                        onClick = {
                            menuExpanded = false
                            onQuote(line.text)
                        },
                    )
                    if (isUser) {
                        DropdownMenuItem(
                            text = { Text("重新发送") },
                            onClick = {
                                menuExpanded = false
                                onResend(line.text)
                            },
                        )
                    }
                }
            }
    }
}

/**
 * 长按消息时复制完整文本，并用系统 Toast 给出轻量反馈。
 */
private fun copyMessageToClipboard(
    context: Context,
    clipboardManager: androidx.compose.ui.platform.ClipboardManager,
    text: String,
) {
    clipboardManager.setText(AnnotatedString(text))
    Toast.makeText(context, "已复制", Toast.LENGTH_SHORT).show()
}

/**
 * 渲染底部输入栏，并在活跃 turn 时切换为 steer 语义。
 */
@Composable
private fun InputBar(
    draft: String,
    activeTurnId: String?,
    onDraftChange: (String) -> Unit,
    onInterrupt: () -> Unit,
    onSend: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Row(
        modifier = modifier,
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        CompactTextField(
            value = draft,
            onValueChange = onDraftChange,
            modifier = keyboardOnFocusModifier(
                Modifier
                    .weight(1f)
                    .heightIn(min = 44.dp),
            ),
            placeholder = if (activeTurnId == null) "消息" else "追加",
            minLines = 1,
            maxLines = 3,
        )
        if (activeTurnId != null) {
            IconButton(onClick = onInterrupt, modifier = Modifier.size(42.dp)) {
                Icon(Icons.Filled.Stop, contentDescription = "停止", modifier = Modifier.size(22.dp))
            }
        }
        SendActionButton(enabled = draft.isNotBlank(), onClick = onSend)
    }
}

/**
 * 渲染白底发送按钮，禁用时仅降低图标对比度。
 */
@Composable
private fun SendActionButton(enabled: Boolean, onClick: () -> Unit) {
    Box(
        modifier = Modifier
            .size(width = 52.dp, height = 44.dp)
            .background(Color.White, RoundedCornerShape(22.dp))
            .border(1.dp, Color(0xFFD6D6D6), RoundedCornerShape(22.dp))
            .clickable(enabled = enabled, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            Icons.Filled.Send,
            contentDescription = "发送",
            modifier = Modifier.size(18.dp),
            tint = if (enabled) Color(0xFF111111) else Color(0xFFB8B8B8),
        )
    }
}

/**
 * 输入框获得焦点时主动唤起软键盘，兼容真机连接 USB/硬件键盘时系统不自动弹出 IME 的情况。
 */
@Composable
private fun keyboardOnFocusModifier(base: Modifier, onFocusedChanged: (Boolean) -> Unit = {}): Modifier {
    val context = LocalContext.current
    val view = LocalView.current
    val inputMethodManager = remember(context) {
        context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager
    }
    return base.onFocusChanged { focusState ->
        onFocusedChanged(focusState.isFocused)
        if (focusState.isFocused) {
            view.post {
                inputMethodManager.showSoftInput(view, InputMethodManager.SHOW_FORCED)
            }
        }
    }
}

/**
 * 将同一个流式 item 的 delta 合并，避免 UI 被碎片消息刷屏。
 */
private fun appendLine(
    lines: MutableList<OutputLine>,
    kind: LineKind,
    text: String,
    streamKey: String? = null,
) {
    if (text.isEmpty()) return
    val last = lines.lastOrNull()
    if (streamKey != null && last?.kind == kind && last.streamKey == streamKey) {
        lines[lines.lastIndex] = last.copy(text = last.text + text)
    } else {
        lines.add(OutputLine(kind = kind, text = text, streamKey = streamKey))
    }
}

/**
 * 把用户粘贴的 HTTPS 地址转换为网关 WebSocket 地址。
 */
private fun parseGatewayWsUrl(rawUrl: String): String {
    val uri = Uri.parse(rawUrl.trim())
    val token = uri.getQueryParameter("token")
        ?: throw IllegalArgumentException("连接地址缺少 token")
    val scheme = when (uri.scheme) {
        "https" -> "wss"
        "http" -> "ws"
        "wss", "ws" -> uri.scheme
        else -> throw IllegalArgumentException("连接地址协议不支持")
    }
    return Uri.Builder()
        .scheme(scheme)
        .encodedAuthority(uri.encodedAuthority)
        .encodedPath("/ws")
        .appendQueryParameter("token", token)
        .build()
        .toString()
}

/**
 * 连接框显示时隐藏 token，避免完整 token 长期暴露在屏幕上。
 */
private fun maskConnectionUrl(rawUrl: String): String {
    if (rawUrl.isBlank()) return ""
    return runCatching {
        val uri = Uri.parse(rawUrl)
        val token = uri.getQueryParameter("token") ?: return rawUrl
        rawUrl.replace("token=$token", "token=••••••")
    }.getOrDefault(rawUrl)
}

/**
 * 从当前输入和安全存储中解析真正要连接的原始地址。
 */
private fun resolveConnectionUrl(input: String, savedUrl: String?): String {
    val trimmed = input.trim()
    if ("token=" in trimmed && "••••" !in trimmed) return trimmed
    if (!savedUrl.isNullOrBlank()) return savedUrl
    throw IllegalArgumentException("请粘贴连接地址")
}

/**
 * 将网关返回的线程 JSON 数组转换为界面摘要。
 */
private fun parseThreadSummaries(array: JSONArray?): List<ThreadSummary> {
    if (array == null) return emptyList()
    return buildList {
        for (index in 0 until array.length()) {
            val item = array.optJSONObject(index) ?: continue
            val id = item.optString("id")
            if (id.isBlank()) continue
            val preview = firstNonBlankJsonString(item, "name", "title", "preview")
            add(
                ThreadSummary(
                    id = id,
                    preview = preview,
                    cwd = jsonStringOrBlank(item, "cwd"),
                    updatedAt = item.optLong("updatedAt", 0L),
                ),
            )
        }
    }.sortedByDescending { it.updatedAt }
}

/**
 * 将服务端归一化后的历史消息转换成输出列表记录。
 */
private fun parseHistoryLines(array: JSONArray?): List<OutputLine> {
    if (array == null) return emptyList()
    return buildList {
        for (index in 0 until array.length()) {
            val item = array.optJSONObject(index) ?: continue
            val text = jsonStringOrBlank(item, "text")
            if (text.isBlank()) continue
            add(
                OutputLine(
                    kind = parseLineKind(jsonStringOrBlank(item, "kind")),
                    text = text,
                    streamKey = jsonStringOrBlank(item, "itemId").ifBlank { null },
                ),
            )
        }
    }
}

/**
 * 解析服务端健康检查结果。
 */
private fun parseHealthReport(item: JSONObject?): HealthReport {
    if (item == null) return HealthReport(checkedAt = nowTimeLabel())
    return HealthReport(
        gateway = jsonStringOrBlank(item, "gateway").ifBlank { if (item.optBoolean("ok", false)) "ok" else "unknown" },
        appServer = jsonStringOrBlank(item, "appServer"),
        cwd = jsonStringOrBlank(item, "cwd"),
        threadId = jsonStringOrBlank(item, "threadId"),
        activeTurnId = jsonStringOrBlank(item, "activeTurnId"),
        checkedAt = nowTimeLabel(),
    )
}

/**
 * 将健康检查结果压缩成适合诊断日志的一行。
 */
private fun healthReportSummary(report: HealthReport): String {
    return "网关=${report.gateway.ifBlank { "unknown" }}，Codex=${report.appServer.ifBlank { "unknown" }}，目录=${report.cwd.ifBlank { "未返回" }}"
}

/**
 * 把底层错误归类成用户能直接处理的提示。
 */
private fun classifyGatewayError(rawMessage: String): String {
    val message = rawMessage.trim().ifBlank { "Codex 错误：未返回具体错误，请查看电脑端网关日志。" }
    val lower = message.lowercase(Locale.US)
    return when {
        "401" in lower || "unauthorized" in lower || "token_revoked" in lower ->
            "Codex 登录态失效：电脑端 Codex 认证失败，请在电脑端重新登录后重启网关。"
        "token" in lower && ("缺少" in message || "invalid" in lower || "unauthorized" in lower) ->
            "连接 token 无效：请确认配置页中的连接地址是最新生成的地址。"
        "工作" in message || "目录" in message || "cwd" in lower || "path" in lower ->
            "工作目录错误：请在桌面网关确认目录存在，然后在手机端重新连接。原始信息：$message"
        "already" in lower && "turn" in lower || "已有 turn" in message ->
            "当前已有任务正在执行：请等待完成或点击停止后再发送。"
        "failed to connect" in lower || "连接" in message || "timeout" in lower ->
            "连接错误：请检查 Cloudflare tunnel、本机网关和网络状态。原始信息：$message"
        else -> message
    }
}

/**
 * 根据错误内容给顶部状态栏一个短标签。
 */
private fun errorStatusLabel(message: String): String {
    return when {
        "登录态" in message -> "登录态失效"
        "token" in message -> "Token 错误"
        "目录" in message -> "目录错误"
        "连接" in message -> "连接错误"
        "任务" in message -> "任务执行中"
        else -> "Codex 错误"
    }
}

/**
 * 生成本地诊断时间标签。
 */
private fun nowTimeLabel(): String {
    return SimpleDateFormat("HH:mm:ss", Locale.getDefault()).format(Date())
}

/**
 * 把网关历史消息类型映射为界面渲染类型。
 */
private fun parseLineKind(rawKind: String): LineKind {
    return when (rawKind) {
        "user" -> LineKind.User
        "assistant" -> LineKind.Assistant
        "command" -> LineKind.Command
        "plan" -> LineKind.Plan
        "error" -> LineKind.Error
        else -> LineKind.System
    }
}

/**
 * 安全读取 JSON 字符串，避免服务端 null 被 org.json 转成界面里的 "null"。
 */
private fun jsonStringOrBlank(item: JSONObject, key: String): String {
    if (!item.has(key) || item.isNull(key)) return ""
    val value = item.optString(key).trim()
    return if (value.equals("null", ignoreCase = true)) "" else value
}

/**
 * 按优先级读取第一个非空 JSON 字段，用于兼容不同版本 thread 摘要字段。
 */
private fun firstNonBlankJsonString(item: JSONObject, vararg keys: String): String {
    for (key in keys) {
        val value = jsonStringOrBlank(item, key)
        if (value.isNotBlank()) return value
    }
    return ""
}
