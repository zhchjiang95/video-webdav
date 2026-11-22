package com.videoserver.webdav

import android.content.Context
import org.json.JSONArray
import org.json.JSONObject
import java.io.File

/**
 * 单个媒体目录配置。
 */
data class MediaDirItem(
    val name: String,
    val path: String,
    val types: List<String>
)

/**
 * 当前配置摘要（由 Go 核心 config-info 命令返回）。
 */
data class ConfigSummary(
    val version: String,
    val listen: String,
    val tls: Boolean,
    val username: String,
    val dirs: List<MediaDirItem>
) {
    /** 提取纯端口号（如 ":8443" -> 8443）。 */
    fun portNumber(): Int {
        val idx = listen.lastIndexOf(':')
        val raw = if (idx >= 0) listen.substring(idx + 1) else listen
        return raw.toIntOrNull() ?: 8443
    }

    fun scheme(): String = if (tls) "https" else "http"
}

/**
 * 封装对内置 Go 可执行文件（libvideoserver.so）的调用。
 * 通过 Go 侧单一真源管理 config.yaml，避免双端 YAML 解析不一致。
 */
object CoreBridge {

    /** 返回解压在 nativeLibraryDir 中的 Go 核心二进制路径。 */
    fun binaryFile(context: Context): File {
        return File(context.applicationInfo.nativeLibraryDir, "libvideoserver.so")
    }

    /** 返回应用私有目录中的 config.yaml 路径。 */
    fun configFile(context: Context): File {
        return File(context.filesDir, "config.yaml")
    }

    /** 若配置文件尚不存在，调用 Go 核心生成默认可用的 HTTP 配置。 */
    fun ensureDefaultConfig(context: Context): Result<Boolean> {
        val res = runCommand(context, listOf("init-config", "-c", configFile(context).absolutePath))
        return res.map { it.optBoolean("created", false) }
    }

    /** 读取当前配置摘要。 */
    fun loadConfigSummary(context: Context): Result<ConfigSummary> {
        val initRes = ensureDefaultConfig(context)
        if (initRes.isFailure) {
            return Result.failure(initRes.exceptionOrNull()!!)
        }
        val res = runCommand(context, listOf("config-info", "-c", configFile(context).absolutePath))
        return res.map { json ->
            val dirsJson = json.optJSONArray("dirs") ?: JSONArray()
            val dirs = mutableListOf<MediaDirItem>()
            for (i in 0 until dirsJson.length()) {
                val obj = dirsJson.optJSONObject(i) ?: continue
                val typesArr = obj.optJSONArray("types")
                val types = mutableListOf<String>()
                if (typesArr != null) {
                    for (j in 0 until typesArr.length()) {
                        val t = typesArr.optString(j)
                        if (t.isNotBlank()) types.add(t)
                    }
                }
                if (types.isEmpty()) types.add("video")
                dirs.add(
                    MediaDirItem(
                        name = obj.optString("name"),
                        path = obj.optString("path"),
                        types = types
                    )
                )
            }
            ConfigSummary(
                version = json.optString("version", "dev"),
                listen = json.optString("listen", ":8443"),
                tls = json.optBoolean("tls", false),
                username = json.optString("username", "admin"),
                dirs = dirs
            )
        }
    }

    /** 添加一个媒体目录并持久化到 config.yaml。 */
    fun addDir(context: Context, name: String, path: String, types: List<String>): Result<Unit> {
        val typesArg = types.ifEmpty { listOf("video") }.joinToString(",")
        return runCommand(
            context,
            listOf(
                "dir-add",
                "-c", configFile(context).absolutePath,
                "-name", name,
                "-path", path,
                "-types", typesArg
            )
        ).map { Unit }
    }

    /** 按名称移除一个媒体目录并持久化到 config.yaml。 */
    fun removeDir(context: Context, name: String): Result<Unit> {
        return runCommand(
            context,
            listOf(
                "dir-remove",
                "-c", configFile(context).absolutePath,
                "-name", name
            )
        ).map { Unit }
    }

    private fun runCommand(context: Context, args: List<String>): Result<JSONObject> {
        val bin = binaryFile(context)
        if (!bin.exists()) {
            return Result.failure(IllegalStateException("未找到内置核心组件：${bin.absolutePath}"))
        }
        return try {
            val cmd = mutableListOf(bin.absolutePath).apply { addAll(args) }
            val process = ProcessBuilder(cmd)
                .directory(context.filesDir)
                .redirectErrorStream(true)
                .start()
            val output = process.inputStream.bufferedReader().use { it.readText() }.trim()
            val exitCode = process.waitFor()

            val jsonLine = output.lineSequence().lastOrNull { it.trim().startsWith("{") }
            if (jsonLine != null) {
                val obj = JSONObject(jsonLine)
                if (obj.optBoolean("ok", exitCode == 0)) {
                    Result.success(obj)
                } else {
                    Result.failure(IllegalStateException(obj.optString("error", "操作失败")))
                }
            } else if (exitCode == 0) {
                Result.success(JSONObject())
            } else {
                Result.failure(IllegalStateException(output.ifBlank { "执行失败（退出码 $exitCode）" }))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
}
