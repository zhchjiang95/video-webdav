package com.videoserver.webdav

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.io.BufferedWriter
import java.io.OutputStreamWriter
import java.util.TimeZone

/**
 * WebDAV 后台运行状态快照。
 */
data class ServiceState(
    val running: Boolean = false,
    val listen: String = ":8443",
    val tls: Boolean = false,
    val lastError: String? = null
) {
    fun portNumber(): Int {
        val idx = listen.lastIndexOf(':')
        val raw = if (idx >= 0) listen.substring(idx + 1) else listen
        return raw.toIntOrNull() ?: 8443
    }

    fun scheme(): String = if (tls) "https" else "http"
}

/**
 * 安卓前台服务：托管 Go 核心进程，持有 WakeLock / WifiLock 保证息屏串流不卡顿，
 * 并通过 stdin 控制管道实现目录增删热重载与优雅停机。
 */
class WebDavService : Service() {

    companion object {
        const val ACTION_START = "com.videoserver.webdav.action.START"
        const val ACTION_STOP = "com.videoserver.webdav.action.STOP"
        const val ACTION_RELOAD = "com.videoserver.webdav.action.RELOAD"

        private const val CHANNEL_ID = "webdav_server_channel"
        private const val NOTIFICATION_ID = 1001

        private val _state = MutableStateFlow(ServiceState())
        val state: StateFlow<ServiceState> = _state.asStateFlow()

        fun start(context: Context) {
            val intent = Intent(context, WebDavService::class.java).setAction(ACTION_START)
            ContextCompat.startForegroundService(context, intent)
        }

        fun stop(context: Context) {
            val intent = Intent(context, WebDavService::class.java).setAction(ACTION_STOP)
            context.startService(intent)
        }

        fun reload(context: Context) {
            if (!_state.value.running) return
            val intent = Intent(context, WebDavService::class.java).setAction(ACTION_RELOAD)
            context.startService(intent)
        }
    }

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private var serverJob: Job? = null
    @Volatile
    private var process: Process? = null
    @Volatile
    private var stdinWriter: BufferedWriter? = null
    @Volatile
    private var userStopped = false

    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                userStopped = true
                stopGoProcess()
                releaseLocks()
                _state.value = _state.value.copy(running = false, lastError = null)
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
                return START_NOT_STICKY
            }

            ACTION_RELOAD -> {
                scope.launch { sendStdinCommand("reload") }
                return START_STICKY
            }

            ACTION_START, null -> {
                userStopped = false
                createNotificationChannel()
                startInForeground(buildNotification("正在启动 WebDAV 服务..."))
                if (serverJob?.isActive != true) {
                    acquireLocks()
                    serverJob = scope.launch { runServerLoop() }
                }
                return START_STICKY
            }
        }
        return START_NOT_STICKY
    }

    private suspend fun runServerLoop() {
        while (!userStopped) {
            val summaryResult = CoreBridge.loadConfigSummary(this)
            if (summaryResult.isFailure) {
                val errMsg = summaryResult.exceptionOrNull()?.message ?: "初始化配置失败"
                _state.value = _state.value.copy(running = false, lastError = errMsg)
                break
            }
            val summary = summaryResult.getOrThrow()
            val bin = CoreBridge.binaryFile(this)
            val cfg = CoreBridge.configFile(this)

            val pb = ProcessBuilder(
                bin.absolutePath,
                "serve",
                "-c", cfg.absolutePath,
                "-stdin-ctl"
            ).apply {
                directory(filesDir)
                redirectErrorStream(true)
                environment()["TZ"] = TimeZone.getDefault().id
            }

            val proc = try {
                pb.start()
            } catch (e: Exception) {
                _state.value = ServiceState(
                    running = false,
                    listen = summary.listen,
                    tls = summary.tls,
                    lastError = "启动核心进程失败: ${e.message}"
                )
                break
            }

            process = proc
            stdinWriter = BufferedWriter(OutputStreamWriter(proc.outputStream, Charsets.UTF_8))

            _state.value = ServiceState(
                running = true,
                listen = summary.listen,
                tls = summary.tls,
                lastError = null
            )

            val ip = NetworkUtils.getLanIpv4Address(this)
            val urlHint = if (ip != null) {
                "${summary.scheme()}://$ip:${summary.portNumber()}"
            } else {
                "监听 ${summary.listen} (${summary.scheme().uppercase()})"
            }
            updateNotification("运行中 · $urlHint")

            // 持续消费输出日志防止管道阻塞，并保留最后一行以便异常排查
            var lastLogLine = ""
            try {
                proc.inputStream.bufferedReader().use { reader ->
                    while (true) {
                        val line = reader.readLine() ?: break
                        if (line.isNotBlank()) {
                            lastLogLine = line
                        }
                    }
                }
            } catch (_: Exception) {
                // 进程终止时流关闭，忽略
            }

            val exitCode = try {
                proc.waitFor()
            } catch (_: InterruptedException) {
                -1
            } finally {
                closeStdinQuietly()
                process = null
            }

            if (userStopped) {
                break
            }

            // 当用户在 Web 管理后台点击“重启服务”时，Go 进程会以 0 退出，此时自动拉起新配置
            if (exitCode == 0) {
                updateNotification("正在应用新配置并重启...")
                delay(600)
                continue
            }

            _state.value = ServiceState(
                running = false,
                listen = summary.listen,
                tls = summary.tls,
                lastError = lastLogLine.ifBlank { "服务异常退出（退出码 $exitCode）" }
            )
            break
        }

        if (!userStopped && !_state.value.running) {
            releaseLocks()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
    }

    private fun sendStdinCommand(cmd: String) {
        try {
            val w = stdinWriter ?: return
            synchronized(w) {
                w.write(cmd)
                w.newLine()
                w.flush()
            }
        } catch (_: Exception) {
            // 管道已关闭时忽略
        }
    }

    private fun stopGoProcess() {
        val proc = process ?: return
        scope.launch {
            sendStdinCommand("stop")
            closeStdinQuietly()
            delay(800)
            if (proc.isAlive) {
                proc.destroy()
            }
        }
    }

    private fun closeStdinQuietly() {
        try {
            stdinWriter?.close()
        } catch (_: Exception) {
        } finally {
            stdinWriter = null
        }
    }

    private fun acquireLocks() {
        try {
            val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
            if (wakeLock == null) {
                wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "VideoServer::WebDavWakeLock").apply {
                    setReferenceCounted(false)
                    acquire()
                }
            }
        } catch (_: Exception) {
        }
        try {
            val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            if (wifiLock == null) {
                val mode = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    WifiManager.WIFI_MODE_FULL_LOW_LATENCY
                } else {
                    @Suppress("DEPRECATION")
                    WifiManager.WIFI_MODE_FULL_HIGH_PERF
                }
                wifiLock = wm.createWifiLock(mode, "VideoServer::WebDavWifiLock").apply {
                    setReferenceCounted(false)
                    acquire()
                }
            }
        } catch (_: Exception) {
        }
    }

    private fun releaseLocks() {
        try {
            if (wakeLock?.isHeld == true) wakeLock?.release()
        } catch (_: Exception) {
        } finally {
            wakeLock = null
        }
        try {
            if (wifiLock?.isHeld == true) wifiLock?.release()
        } catch (_: Exception) {
        } finally {
            wifiLock = null
        }
    }

    private fun createNotificationChannel() {
        val manager = getSystemService(NotificationManager::class.java) ?: return
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notification_channel_name),
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = getString(R.string.notification_channel_desc)
            setShowBadge(false)
        }
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(contentText: String): Notification {
        val openIntent = Intent(this, MainActivity::class.java)
        val openPending = PendingIntent.getActivity(
            this,
            0,
            openIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val stopIntent = Intent(this, WebDavService::class.java).setAction(ACTION_STOP)
        val stopPending = PendingIntent.getService(
            this,
            1,
            stopIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.ic_server_status)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(contentText)
            .setOngoing(true)
            .setContentIntent(openPending)
            .addAction(0, getString(R.string.btn_stop), stopPending)
            .build()
    }

    private fun startInForeground(notification: Notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(
                NOTIFICATION_ID,
                notification,
                ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE
            )
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
    }

    private fun updateNotification(contentText: String) {
        val manager = getSystemService(NotificationManager::class.java) ?: return
        manager.notify(NOTIFICATION_ID, buildNotification(contentText))
    }

    override fun onDestroy() {
        userStopped = true
        process?.destroy()
        closeStdinQuietly()
        releaseLocks()
        _state.value = _state.value.copy(running = false)
        scope.cancel()
        super.onDestroy()
    }
}
