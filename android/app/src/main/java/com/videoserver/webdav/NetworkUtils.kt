package com.videoserver.webdav

import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import java.net.Inet4Address
import java.net.NetworkInterface

/**
 * 获取手机/平板当前的局域网 IPv4 地址（兼容 Android 11+ SELinux 限制）。
 */
object NetworkUtils {

    fun getLanIpv4Address(context: Context): String? {
        // 优先使用系统 ConnectivityManager（Wi-Fi / 以太网），兼容 Android 11+
        try {
            val cm = context.getSystemService(Context.CONNECTIVITY_SERVICE) as? ConnectivityManager
            if (cm != null) {
                val active = cm.activeNetwork
                val candidates = buildList {
                    if (active != null) add(active)
                    addAll(cm.allNetworks.filter { it != active })
                }
                for (network in candidates) {
                    val caps = cm.getNetworkCapabilities(network) ?: continue
                    val isLan = caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) ||
                        caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET)
                    if (!isLan) continue
                    val props = cm.getLinkProperties(network) ?: continue
                    for (linkAddr in props.linkAddresses) {
                        val addr = linkAddr.address
                        if (addr is Inet4Address && !addr.isLoopbackAddress) {
                            return addr.hostAddress
                        }
                    }
                }
            }
        } catch (_: Exception) {
            // 忽略异常并降级到 NetworkInterface
        }

        // 兜底：遍历网卡列表（适用于热点或部分平板）
        return try {
            val interfaces = NetworkInterface.getNetworkInterfaces()
            while (interfaces != null && interfaces.hasMoreElements()) {
                val nif = interfaces.nextElement()
                if (!nif.isUp || nif.isLoopback) continue
                val addrs = nif.inetAddresses
                while (addrs.hasMoreElements()) {
                    val addr = addrs.nextElement()
                    if (addr is Inet4Address && !addr.isLoopbackAddress) {
                        return addr.hostAddress
                    }
                }
            }
            null
        } catch (_: Exception) {
            null
        }
    }
}
