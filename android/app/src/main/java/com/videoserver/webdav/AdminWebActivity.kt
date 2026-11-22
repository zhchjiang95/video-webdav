package com.videoserver.webdav

import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.net.http.SslError
import android.os.Bundle
import android.view.View
import android.webkit.SslErrorHandler
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AppCompatActivity
import com.google.android.material.appbar.MaterialToolbar
import com.google.android.material.dialog.MaterialAlertDialogBuilder
import com.google.android.material.progressindicator.LinearProgressIndicator

/**
 * 内置 Web 管理后台页面，直接复用 Go 内嵌的 Vue 3 控制台（/admin/）。
 */
class AdminWebActivity : AppCompatActivity() {

    companion object {
        const val EXTRA_URL = "extra_admin_url"
    }

    private lateinit var webView: WebView
    private lateinit var progressBar: LinearProgressIndicator
    private var targetUrl: String = "http://127.0.0.1:8443/admin/"

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_admin_web)

        targetUrl = intent.getStringExtra(EXTRA_URL) ?: targetUrl

        val toolbar = findViewById<MaterialToolbar>(R.id.toolbarAdmin)
        progressBar = findViewById(R.id.progressWeb)
        webView = findViewById(R.id.webViewAdmin)

        toolbar.setNavigationIcon(R.drawable.ic_arrow_back)
        toolbar.setNavigationOnClickListener { finish() }
        toolbar.menu.add("刷新").setOnMenuItemClickListener {
            webView.reload()
            true
        }
        toolbar.menu.add("浏览器打开").setOnMenuItemClickListener {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(targetUrl)))
            true
        }

        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            useWideViewPort = true
            loadWithOverviewMode = true
        }

        // 启用 WebChromeClient 以支持前端 confirm()/alert() 弹窗与加载进度
        webView.webChromeClient = object : WebChromeClient() {
            override fun onProgressChanged(view: WebView?, newProgress: Int) {
                if (newProgress in 1..99) {
                    progressBar.visibility = View.VISIBLE
                    progressBar.progress = newProgress
                } else {
                    progressBar.visibility = View.GONE
                }
            }
        }

        webView.webViewClient = object : WebViewClient() {
            override fun onReceivedSslError(
                view: WebView?,
                handler: SslErrorHandler?,
                error: SslError?
            ) {
                val host = Uri.parse(error?.url ?: "").host
                if (host == "127.0.0.1" || host == "localhost") {
                    MaterialAlertDialogBuilder(this@AdminWebActivity)
                        .setTitle("本地 HTTPS 证书提示")
                        .setMessage("当前通过本机回环地址访问已启用域名证书的管理后台，证书域名与 127.0.0.1 不一致，是否继续访问？")
                        .setPositiveButton("继续访问") { _, _ -> handler?.proceed() }
                        .setNegativeButton("取消") { _, _ -> handler?.cancel() }
                        .show()
                } else {
                    handler?.cancel()
                }
            }
        }

        webView.loadUrl(targetUrl)
    }

    @Deprecated("Deprecated in Java")
    override fun onBackPressed() {
        if (webView.canGoBack()) {
            webView.goBack()
        } else {
            super.onBackPressed()
        }
    }
}
