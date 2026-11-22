package com.videoserver.webdav

import android.net.Uri
import android.os.Environment
import android.provider.DocumentsContract
import java.io.File

/**
 * 将系统文件夹选择器（ACTION_OPEN_DOCUMENT_TREE）返回的 SAF Tree URI
 * 解析为本地真实绝对路径，供 Go 文件系统直接读取。
 */
object PathResolver {

    /**
     * 解析 SAF 文件夹树 URI，支持：
     *  - 主内置存储（primary:...）
     *  - Documents 快捷目录（home:...）
     *  - 外置 TF 卡 / USB OTG 存储卷（<UUID>:...）
     */
    fun resolveTreeUri(treeUri: Uri): String? {
        if (treeUri.authority != "com.android.externalstorage.documents") {
            return null
        }
        val docId = try {
            DocumentsContract.getTreeDocumentId(treeUri)
        } catch (_: Exception) {
            return null
        } ?: return null

        val parts = docId.split(":", limit = 2)
        val volumeId = parts[0]
        val relPath = parts.getOrElse(1) { "" }.trimStart('/')

        val baseDir = when {
            volumeId.equals("primary", ignoreCase = true) -> {
                Environment.getExternalStorageDirectory().absolutePath
            }
            volumeId.equals("home", ignoreCase = true) -> {
                File(Environment.getExternalStorageDirectory(), Environment.DIRECTORY_DOCUMENTS).absolutePath
            }
            else -> {
                // 外置 SD 卡或 U 盘通常挂载在 /storage/<volumeId>
                "/storage/$volumeId"
            }
        }

        val fullPath = if (relPath.isEmpty()) baseDir else "$baseDir/$relPath"
        val dir = File(fullPath)
        return if (dir.exists() && dir.isDirectory) dir.absolutePath else fullPath
    }

    /** 根据文件夹名称智能推荐默认扫描的文件类型。 */
    fun guessDefaultTypes(dirPath: String): List<String> {
        val lower = File(dirPath).name.lowercase()
        return when {
            lower.contains("music") || lower.contains("audio") || lower.contains("song") ||
                lower.contains("音乐") || lower.contains("音频") || lower.contains("歌曲") ||
                lower.contains("podcasts") || lower.contains("audiobook") -> listOf("audio")

            lower.contains("dcim") || lower.contains("picture") || lower.contains("photo") ||
                lower.contains("image") || lower.contains("camera") || lower.contains("screenshot") ||
                lower.contains("相册") || lower.contains("图片") || lower.contains("照片") -> listOf("image")

            else -> listOf("video")
        }
    }
}
