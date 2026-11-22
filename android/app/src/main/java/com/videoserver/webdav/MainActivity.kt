package com.videoserver.webdav

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.DialogInterface
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.PowerManager
import android.provider.Settings
import android.view.LayoutInflater
import android.view.View
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.result.contract.ActivityResultContracts
import androidx.appcompat.app.AppCompatActivity
import androidx.core.content.ContextCompat
import androidx.lifecycle.lifecycleScope
import com.google.android.material.button.MaterialButton
import com.google.android.material.card.MaterialCardView
import com.google.android.material.chip.Chip
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.textfield.TextInputEditText
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File

/**
 * 安卓主界面：提供一键启停、局域网地址复制、内置管理后台入口，
 * 以及基于系统文件夹选择器的媒体目录管理（支持视频/音频/图片多选与热重载）。
 */
class MainActivity : AppCompatActivity() {

    private lateinit var viewStatusDot: View
    private lateinit var tvStatusTitle: TextView
    private lateinit var tvStatusDetail: TextView
    private lateinit var btnToggleServer: MaterialButton
    private lateinit var layoutRunningInfo: LinearLayout
    private lateinit var tvWebdavUrl: TextView
    private lateinit var btnCopyUrl: MaterialButton
    private lateinit var btnOpenAdmin: MaterialButton

    private lateinit var cardStorageWarn: MaterialCardView
    private lateinit var btnGrantStorage: MaterialButton
    private lateinit var cardBatteryTip: MaterialCardView
    private lateinit var btnIgnoreBattery: MaterialButton

    private lateinit var btnPickFolder: MaterialButton
    private lateinit var tvDirEmptyHint: TextView
    private lateinit var layoutDirList: LinearLayout
    private lateinit var tvFooterTip: TextView
    private lateinit var tvLinkGithub: TextView
    private lateinit var tvLinkWebsite: TextView
    private lateinit var tvLinkWechat: TextView

    private var currentSummary: ConfigSummary? = null

    // 系统文件夹选择器（ACTION_OPEN_DOCUMENT_TREE）
    private val folderPickerLauncher = registerForActivityResult(
        ActivityResultContracts.OpenDocumentTree()
    ) { treeUri ->
        if (treeUri != null) {
            onFolderPicked(treeUri)
        }
    }

    // Android 8~10 存储读取权限回调
    private val legacyStorageLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        refreshPermissionCards()
        if (!granted) {
            toast("未授予存储权限，可能无法扫描本地媒体文件")
        }
    }

    // Android 13+ 通知权限回调（用于显示常驻前台服务通知）
    private val notificationPermLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { _ ->
        WebDavService.start(this)
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        bindViews()
        setupListeners()
        observeServiceState()
        refreshConfigSummary()
    }

    override fun onResume() {
        super.onResume()
        refreshPermissionCards()
        // 从 Web 管理后台返回后自动刷新目录列表与端口信息
        refreshConfigSummary()
    }

    private fun bindViews() {
        viewStatusDot = findViewById(R.id.viewStatusDot)
        tvStatusTitle = findViewById(R.id.tvStatusTitle)
        tvStatusDetail = findViewById(R.id.tvStatusDetail)
        btnToggleServer = findViewById(R.id.btnToggleServer)
        layoutRunningInfo = findViewById(R.id.layoutRunningInfo)
        tvWebdavUrl = findViewById(R.id.tvWebdavUrl)
        btnCopyUrl = findViewById(R.id.btnCopyUrl)
        btnOpenAdmin = findViewById(R.id.btnOpenAdmin)

        cardStorageWarn = findViewById(R.id.cardStorageWarn)
        btnGrantStorage = findViewById(R.id.btnGrantStorage)
        cardBatteryTip = findViewById(R.id.cardBatteryTip)
        btnIgnoreBattery = findViewById(R.id.btnIgnoreBattery)

        btnPickFolder = findViewById(R.id.btnPickFolder)
        tvDirEmptyHint = findViewById(R.id.tvDirEmptyHint)
        layoutDirList = findViewById(R.id.layoutDirList)
        tvFooterTip = findViewById(R.id.tvFooterTip)
        tvLinkGithub = findViewById(R.id.tvLinkGithub)
        tvLinkWebsite = findViewById(R.id.tvLinkWebsite)
        tvLinkWechat = findViewById(R.id.tvLinkWechat)
    }

    private fun setupListeners() {
        btnToggleServer.setOnClickListener {
            if (WebDavService.state.value.running) {
                WebDavService.stop(this)
            } else {
                startServerWithPermissionCheck()
            }
        }

        btnCopyUrl.setOnClickListener {
            val url = tvWebdavUrl.text.toString().trim()
            if (url.isNotEmpty()) {
                val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
                cm.setPrimaryClip(ClipData.newPlainText("WebDAV URL", url))
                toast("已复制到剪贴板")
            }
        }

        btnOpenAdmin.setOnClickListener {
            val state = WebDavService.state.value
            val port = state.portNumber()
            val scheme = state.scheme()
            val adminUrl = "$scheme://127.0.0.1:$port/admin/"
            startActivity(
                Intent(this, AdminWebActivity::class.java)
                    .putExtra(AdminWebActivity.EXTRA_URL, adminUrl)
            )
        }

        btnGrantStorage.setOnClickListener { requestStoragePermission() }
        btnIgnoreBattery.setOnClickListener { requestIgnoreBatteryOptimization() }

        tvLinkGithub.setOnClickListener {
            openExternalUrl("https://github.com/zhchjiang95/video-webdav")
        }
        tvLinkWebsite.setOnClickListener {
            openExternalUrl("https://fiume.cn")
        }
        tvLinkWechat.setOnClickListener {
            val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            cm.setPrimaryClip(ClipData.newPlainText("公众号", "Slothful"))
            toast("已复制公众号名称：Slothful")
        }

        btnPickFolder.setOnClickListener {
            if (!hasStoragePermission()) {
                MaterialAlertDialogBuilder(this)
                    .setTitle("请先授予文件访问权限")
                    .setMessage("授予“所有文件访问权限”后，服务才能正常扫描您选择的文件夹中的视频、字幕、音频和图片。")
                    .setPositiveButton("去授权") { _, _ -> requestStoragePermission() }
                    .setNegativeButton("仍然选择") { _, _ -> launchFolderPicker() }
                    .show()
            } else {
                launchFolderPicker()
            }
        }
    }

    private fun launchFolderPicker() {
        try {
            folderPickerLauncher.launch(null)
        } catch (e: Exception) {
            toast("无法打开系统文件夹选择器：${e.message}")
        }
    }

    private fun observeServiceState() {
        lifecycleScope.launch {
            WebDavService.state.collect { state ->
                renderServiceState(state)
            }
        }
    }

    private fun renderServiceState(state: ServiceState) {
        val dot = viewStatusDot.background.mutate() as? GradientDrawable
        if (state.running) {
            dot?.setColor(ContextCompat.getColor(this, R.color.status_running))
            tvStatusTitle.text = getString(R.string.status_running)
            tvStatusDetail.text = "监听 ${state.listen} (${state.scheme().uppercase()})"
            btnToggleServer.text = getString(R.string.btn_stop)
            btnToggleServer.setBackgroundColor(ContextCompat.getColor(this, R.color.danger))

            val ip = NetworkUtils.getLanIpv4Address(this) ?: "127.0.0.1"
            val url = "${state.scheme()}://$ip:${state.portNumber()}/"
            tvWebdavUrl.text = url
            layoutRunningInfo.visibility = View.VISIBLE
        } else {
            dot?.setColor(ContextCompat.getColor(this, R.color.status_stopped))
            tvStatusTitle.text = getString(R.string.status_stopped)
            val listenText = currentSummary?.listen ?: state.listen
            val schemeText = (currentSummary?.scheme() ?: state.scheme()).uppercase()
            tvStatusDetail.text = if (!state.lastError.isNullOrBlank()) {
                "上次错误：${state.lastError}"
            } else {
                "就绪 · 监听 $listenText ($schemeText)"
            }
            btnToggleServer.text = getString(R.string.btn_start)
            btnToggleServer.setBackgroundColor(ContextCompat.getColor(this, R.color.brand_primary))
            layoutRunningInfo.visibility = View.GONE
        }
    }

    private fun refreshConfigSummary() {
        lifecycleScope.launch {
            val res = withContext(Dispatchers.IO) {
                CoreBridge.loadConfigSummary(this@MainActivity)
            }
            res.onSuccess { summary ->
                currentSummary = summary
                renderDirList(summary.dirs)
                tvFooterTip.text = "当前登录账号：${summary.username}（初始默认密码 admin123456）\n可在 Web 管理后台修改密码、端口与文件扩展名规则"
                renderServiceState(WebDavService.state.value)
            }.onFailure { err ->
                tvStatusDetail.text = "配置读取失败：${err.message}"
            }
        }
    }

    private fun renderDirList(dirs: List<MediaDirItem>) {
        layoutDirList.removeAllViews()
        if (dirs.isEmpty()) {
            tvDirEmptyHint.visibility = View.VISIBLE
            return
        }
        tvDirEmptyHint.visibility = View.GONE

        val inflater = LayoutInflater.from(this)
        for (item in dirs) {
            val row = inflater.inflate(R.layout.item_media_dir, layoutDirList, false)
            row.findViewById<TextView>(R.id.tvDirName).text = "/${item.name}"
            row.findViewById<TextView>(R.id.tvDirPath).text = item.path

            val typeContainer = row.findViewById<LinearLayout>(R.id.layoutDirTypes)
            for (type in item.types) {
                val badge = TextView(this).apply {
                    text = when (type) {
                        "video" -> "视频"
                        "audio" -> "音频"
                        "image" -> "图片"
                        else -> type
                    }
                    textSize = 11f
                    setPadding(20, 6, 20, 6)
                    val bg = ContextCompat.getDrawable(this@MainActivity, R.drawable.bg_type_chip)?.mutate() as? GradientDrawable
                    when (type) {
                        "audio" -> {
                            bg?.setColor(ContextCompat.getColor(this@MainActivity, R.color.chip_audio_bg))
                            setTextColor(ContextCompat.getColor(this@MainActivity, R.color.chip_audio_text))
                        }
                        "image" -> {
                            bg?.setColor(ContextCompat.getColor(this@MainActivity, R.color.chip_image_bg))
                            setTextColor(ContextCompat.getColor(this@MainActivity, R.color.chip_image_text))
                        }
                        else -> {
                            bg?.setColor(ContextCompat.getColor(this@MainActivity, R.color.chip_video_bg))
                            setTextColor(ContextCompat.getColor(this@MainActivity, R.color.chip_video_text))
                        }
                    }
                    background = bg
                }
                val lp = LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.WRAP_CONTENT,
                    LinearLayout.LayoutParams.WRAP_CONTENT
                ).apply { marginEnd = 12 }
                typeContainer.addView(badge, lp)
            }

            row.findViewById<TextView>(R.id.btnDeleteDir).setOnClickListener {
                confirmRemoveDir(item)
            }
            layoutDirList.addView(row)
        }
    }

    private fun onFolderPicked(treeUri: Uri) {
        val resolvedPath = PathResolver.resolveTreeUri(treeUri)
        if (resolvedPath.isNullOrBlank()) {
            MaterialAlertDialogBuilder(this)
                .setTitle("暂不支持该存储提供方")
                .setMessage("请选择本机内部存储或外置存储卡中的真实文件夹（例如“内部存储 -> Movies / Music / DCIM”）。")
                .setPositiveButton("知道了", null)
                .show()
            return
        }

        val suggestedName = File(resolvedPath).name.ifBlank { "媒体目录" }
        val guessedTypes = PathResolver.guessDefaultTypes(resolvedPath)

        val dialogView = LayoutInflater.from(this).inflate(R.layout.dialog_add_dir, null)
        val etName = dialogView.findViewById<TextInputEditText>(R.id.etDirName)
        val tvPath = dialogView.findViewById<TextView>(R.id.tvResolvedPath)
        val chipVideo = dialogView.findViewById<Chip>(R.id.chipTypeVideo)
        val chipAudio = dialogView.findViewById<Chip>(R.id.chipTypeAudio)
        val chipImage = dialogView.findViewById<Chip>(R.id.chipTypeImage)

        etName.setText(suggestedName)
        tvPath.text = resolvedPath
        chipVideo.isChecked = guessedTypes.contains("video")
        chipAudio.isChecked = guessedTypes.contains("audio")
        chipImage.isChecked = guessedTypes.contains("image")

        val dialog = MaterialAlertDialogBuilder(this)
            .setTitle("添加媒体目录")
            .setView(dialogView)
            .setNegativeButton("取消", null)
            .setPositiveButton("添加", null)
            .create()

        dialog.setOnShowListener {
            dialog.getButton(DialogInterface.BUTTON_POSITIVE).setOnClickListener {
                val name = etName.text?.toString()?.trim().orEmpty()
                if (name.isEmpty()) {
                    toast("显示名称不能为空")
                    return@setOnClickListener
                }
                val selectedTypes = buildList {
                    if (chipVideo.isChecked) add("video")
                    if (chipAudio.isChecked) add("audio")
                    if (chipImage.isChecked) add("image")
                }
                if (selectedTypes.isEmpty()) {
                    toast("请至少选择一种文件类型")
                    return@setOnClickListener
                }

                lifecycleScope.launch {
                    val res = withContext(Dispatchers.IO) {
                        CoreBridge.addDir(this@MainActivity, name, resolvedPath, selectedTypes)
                    }
                    res.onSuccess {
                        dialog.dismiss()
                        // 服务正在运行时触发零中断热重载
                        WebDavService.reload(this@MainActivity)
                        refreshConfigSummary()
                        toast(if (WebDavService.state.value.running) "已添加并立即生效" else "目录已添加")
                    }.onFailure { err ->
                        toast("添加失败：${err.message}")
                    }
                }
            }
        }
        dialog.show()
    }

    private fun confirmRemoveDir(item: MediaDirItem) {
        MaterialAlertDialogBuilder(this)
            .setTitle("移除媒体目录")
            .setMessage("确定要从 WebDAV 中移除“/${item.name}”吗？（不会删除手机中的任何文件）")
            .setNegativeButton("取消", null)
            .setPositiveButton("移除") { _, _ ->
                lifecycleScope.launch {
                    val res = withContext(Dispatchers.IO) {
                        CoreBridge.removeDir(this@MainActivity, item.name)
                    }
                    res.onSuccess {
                        WebDavService.reload(this@MainActivity)
                        refreshConfigSummary()
                        toast("已移除目录")
                    }.onFailure { err ->
                        toast("移除失败：${err.message}")
                    }
                }
            }
            .show()
    }

    private fun startServerWithPermissionCheck() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            val granted = ContextCompat.checkSelfPermission(
                this,
                Manifest.permission.POST_NOTIFICATIONS
            ) == PackageManager.PERMISSION_GRANTED
            if (!granted) {
                notificationPermLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
                return
            }
        }
        WebDavService.start(this)
    }

    private fun hasStoragePermission(): Boolean {
        return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            Environment.isExternalStorageManager()
        } else {
            ContextCompat.checkSelfPermission(
                this,
                Manifest.permission.READ_EXTERNAL_STORAGE
            ) == PackageManager.PERMISSION_GRANTED
        }
    }

    private fun requestStoragePermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            try {
                val intent = Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION).apply {
                    data = Uri.parse("package:$packageName")
                }
                startActivity(intent)
            } catch (_: Exception) {
                startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
            }
        } else {
            legacyStorageLauncher.launch(Manifest.permission.READ_EXTERNAL_STORAGE)
        }
    }

    private fun isIgnoringBatteryOptimizations(): Boolean {
        val pm = getSystemService(Context.POWER_SERVICE) as? PowerManager ?: return true
        return pm.isIgnoringBatteryOptimizations(packageName)
    }

    private fun requestIgnoreBatteryOptimization() {
        try {
            val intent = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                data = Uri.parse("package:$packageName")
            }
            startActivity(intent)
        } catch (_: Exception) {
            try {
                startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
            } catch (_: Exception) {
                toast("请在系统设置中手动将本应用设为“电池无限制”")
            }
        }
    }

    private fun refreshPermissionCards() {
        cardStorageWarn.visibility = if (hasStoragePermission()) View.GONE else View.VISIBLE
        cardBatteryTip.visibility = if (isIgnoringBatteryOptimizations()) View.GONE else View.VISIBLE
    }

    private fun openExternalUrl(url: String) {
        try {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
        } catch (_: Exception) {
            toast("无法打开链接：$url")
        }
    }

    private fun toast(msg: String) {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
    }
}
