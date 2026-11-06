<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import {
  Folder,
  Server,
  Shield,
  FileText,
  Activity,
  LogOut,
  Plus,
  Trash2,
  Edit2,
  CheckCircle2,
  AlertCircle,
  Copy,
  RotateCw,
  Eye,
  EyeOff,
  ExternalLink,
  Power,
  RefreshCw,
  Search,
} from 'lucide-vue-next'
import { api, getToken, setToken } from './api'
import type { ServerStatus, BannedIP, AppConfig, TestPathResult } from './types'

// 状态管理
const token = ref(getToken())
const isAuthed = computed(() => !!token.value)
const activeTab = ref<'dashboard' | 'dirs' | 'settings' | 'logs'>('dashboard')

// 登录表单
const loginForm = reactive({ username: '', password: '' })
const loginLoading = ref(false)
const loginError = ref('')
const showLoginPass = ref(false)

// 数据状态
const status = ref<ServerStatus | null>(null)
const bannedIPs = ref<BannedIP[]>([])
const config = ref<AppConfig | null>(null)
const logs = ref<string[]>([])
const logFilter = ref('')
const autoRefreshLogs = ref(true)
let logTimer: number | null = null

// 操作状态
const saving = ref(false)
const restarting = ref(false)
const toast = reactive({ show: false, msg: '', type: 'success' as 'success' | 'error' | 'warn' })

// 客户端地址与 WebDAV 快捷连接参数
const currentHost = ref(typeof window !== 'undefined' ? window.location.hostname : 'localhost')
const currentPort = computed(() => {
  if (status.value?.listen) {
    return status.value.listen.replace(':', '')
  }
  return typeof window !== 'undefined' ? (window.location.port || (status.value?.tls ? '443' : '80')) : '8443'
})

const webdavUrl = computed(() => {
  const scheme = status.value?.tls ? 'https' : 'http'
  const user = config.value?.auth?.username || 'admin'
  const host = currentHost.value
  const port = status.value?.listen || ':8443'
  return `${scheme}://${user}@${host}${port}`
})

// 防护型安全计算属性，防止 null.length 导致页面崩溃
const safeBannedCount = computed(() => (bannedIPs.value || []).length)
const safeDirsCount = computed(() => (config.value?.dirs || []).length)

function showToast(msg: string, type: 'success' | 'error' | 'warn' = 'success') {
  toast.msg = msg
  toast.type = type
  toast.show = true
  setTimeout(() => {
    toast.show = false
  }, 3500)
}

// 目录编辑弹窗
const dirModal = reactive({
  show: false,
  isEdit: false,
  editIndex: -1,
  name: '',
  path: '',
  testResult: null as TestPathResult | null,
  testing: false,
})

// 登录逻辑
async function handleLogin() {
  if (!loginForm.username || !loginForm.password) {
    loginError.value = '请输入用户名和密码'
    return
  }
  loginLoading.value = true
  loginError.value = ''
  try {
    const res = await api.login(loginForm.username, loginForm.password)
    setToken(res.token)
    token.value = res.token
    showToast('登录成功！')
    loadAllData()
  } catch (err: any) {
    loginError.value = err.message || '登录失败'
  } finally {
    loginLoading.value = false
  }
}

async function handleLogout() {
  try {
    await api.logout()
  } catch {}
  setToken('')
  token.value = ''
  showToast('已退出登录')
}

// 数据加载
async function loadStatus() {
  try {
    status.value = await api.getStatus()
    const res = await api.getBannedIPs()
    bannedIPs.value = Array.isArray(res) ? res : []
  } catch (err: any) {
    console.error('加载状态失败', err)
  }
}

async function loadConfig() {
  try {
    const res = await api.getConfig()
    if (res && !Array.isArray(res.dirs)) {
      res.dirs = []
    }
    config.value = res
  } catch (err: any) {
    console.error('加载配置失败', err)
  }
}

async function loadLogs() {
  try {
    const res = await api.getLogs()
    logs.value = Array.isArray(res?.lines) ? res.lines : []
  } catch (err: any) {
    console.error('加载日志失败', err)
  }
}

async function loadAllData() {
  await Promise.all([loadStatus(), loadConfig(), loadLogs()])
}

// 目录管理
function openAddDirModal() {
  dirModal.isEdit = false
  dirModal.editIndex = -1
  dirModal.name = ''
  dirModal.path = ''
  dirModal.testResult = null
  dirModal.show = true
}

function openEditDirModal(index: number) {
  if (!config.value) return
  const item = config.value.dirs[index]
  dirModal.isEdit = true
  dirModal.editIndex = index
  dirModal.name = item.name
  dirModal.path = item.path
  dirModal.testResult = null
  dirModal.show = true
}

async function testCurrentPath() {
  if (!dirModal.path.trim()) {
    showToast('请输入目录绝对路径', 'warn')
    return
  }
  dirModal.testing = true
  dirModal.testResult = null
  try {
    const res = await api.testPath(dirModal.path.trim())
    dirModal.testResult = res
  } catch (err: any) {
    dirModal.testResult = { exists: false, is_dir: false, video_count: 0, error: err.message }
  } finally {
    dirModal.testing = false
  }
}

function saveDirModal() {
  if (!config.value) return
  const name = dirModal.name.trim()
  const path = dirModal.path.trim()
  if (!name) {
    showToast('目录显示名称不能为空', 'warn')
    return
  }
  if (['admin', 'api'].includes(name.toLowerCase())) {
    showToast('不能使用 admin 或 api 作为目录名', 'warn')
    return
  }
  if (!path) {
    showToast('本地绝对路径不能为空', 'warn')
    return
  }

  // 检查名称重复
  const exists = config.value.dirs.some((d, idx) => idx !== dirModal.editIndex && d.name.toLowerCase() === name.toLowerCase())
  if (exists) {
    showToast('已存在同名目录，请更换名称', 'warn')
    return
  }

  if (dirModal.isEdit) {
    config.value.dirs[dirModal.editIndex] = { name, path }
  } else {
    config.value.dirs.push({ name, path })
  }
  dirModal.show = false
  saveConfigData(true)
}

function removeDir(index: number) {
  if (!config.value) return
  const item = config.value.dirs[index]
  if (confirm(`确定要移除视频目录 "${item.name}" 吗？（不会删除磁盘文件）`)) {
    config.value.dirs.splice(index, 1)
    saveConfigData(true)
  }
}

// 解封 IP
async function unban(ip: string) {
  try {
    await api.unbanIP(ip)
    showToast(`已解封 IP: ${ip}`)
    bannedIPs.value = await api.getBannedIPs()
    loadStatus()
  } catch (err: any) {
    showToast(err.message, 'error')
  }
}

// 保存配置
async function saveConfigData(isDirChange = false) {
  if (!config.value) return
  saving.value = true
  try {
    const res = await api.saveConfig(config.value)
    showToast(res.message || '配置已成功保存！')
    await loadStatus()
    await loadConfig()
  } catch (err: any) {
    showToast(err.message || '保存配置失败', 'error')
  } finally {
    saving.value = false
  }
}

// 重启服务
async function handleRestart() {
  if (!confirm('确定要重启服务吗？正在播放的连接可能会短暂中断。')) return
  restarting.value = true
  try {
    const res = await api.restart()
    showToast(res.message || '正在重启服务...')
    setTimeout(() => {
      loadAllData()
      restarting.value = false
    }, 2500)
  } catch (err: any) {
    showToast(err.message || '重启指令下发失败', 'error')
    restarting.value = false
  }
}

// 复制文本
function copyText(text: string) {
  navigator.clipboard.writeText(text)
  showToast('已复制到剪贴板')
}

// 日志筛选
const filteredLogs = computed(() => {
  const list = logs.value || []
  if (!logFilter.value.trim()) return list
  const kw = logFilter.value.toLowerCase()
  return list.filter((l) => l.toLowerCase().includes(kw))
})

// 生命周期
onMounted(async () => {
  window.addEventListener('auth:expired', () => {
    token.value = ''
  })
  if (isAuthed.value) {
    try {
      const auth = await api.checkAuth()
      if (auth.authenticated) {
        loadAllData()
      } else {
        token.value = ''
      }
    } catch {
      token.value = ''
    }
  }

  // 日志定时器
  logTimer = window.setInterval(() => {
    if (isAuthed.value && activeTab.value === 'logs' && autoRefreshLogs.value) {
      loadLogs()
    }
  }, 3000)
})

onUnmounted(() => {
  if (logTimer) clearInterval(logTimer)
})
</script>

<template>
  <div class="min-h-screen bg-slate-50 text-slate-800 flex flex-col font-sans">
    <!-- Toast 通知 -->
    <transition
      enter-active-class="transform ease-out duration-300 transition"
      enter-from-class="translate-y-2 opacity-0 sm:translate-y-0 sm:translate-x-2"
      enter-to-class="translate-y-0 opacity-100 sm:translate-x-0"
      leave-active-class="transition ease-in duration-200"
      leave-from-class="opacity-100"
      leave-to-class="opacity-0"
    >
      <div
        v-if="toast.show"
        class="fixed top-5 right-5 z-50 flex items-center gap-2 px-4 py-3 rounded-xl shadow-xl text-white text-sm font-medium"
        :class="{
          'bg-emerald-600': toast.type === 'success',
          'bg-rose-600': toast.type === 'error',
          'bg-amber-600': toast.type === 'warn',
        }"
      >
        <CheckCircle2 v-if="toast.type === 'success'" class="w-4 h-4" />
        <AlertCircle v-else class="w-4 h-4" />
        <span>{{ toast.msg }}</span>
      </div>
    </transition>

    <!-- 未登录界面 -->
    <div v-if="!isAuthed" class="min-h-screen flex items-center justify-center p-4 bg-gradient-to-br from-slate-100 to-indigo-50/40">
      <div class="w-full max-w-md bg-white rounded-2xl shadow-xl border border-slate-200/80 p-8">
        <div class="flex flex-col items-center mb-6">
          <div class="w-14 h-14 bg-indigo-600 rounded-2xl flex items-center justify-center shadow-md shadow-indigo-200 mb-3 text-white">
            <Server class="w-7 h-7" />
          </div>
          <h1 class="text-2xl font-bold text-slate-900">video-webdav</h1>
          <p class="text-sm text-slate-500 mt-1">管理后台登录</p>
        </div>

        <form @submit.prevent="handleLogin" class="space-y-4">
          <div v-if="loginError" class="p-3 bg-rose-50 border border-rose-200 text-rose-700 rounded-xl text-xs flex items-center gap-2">
            <AlertCircle class="w-4 h-4 flex-shrink-0" />
            <span>{{ loginError }}</span>
          </div>

          <div>
            <label class="block text-xs font-semibold text-slate-600 uppercase mb-1.5">用户名</label>
            <input
              v-model="loginForm.username"
              type="text"
              autocomplete="username"
              placeholder="请输入管理员用户名"
              class="w-full px-3.5 py-2.5 rounded-xl border border-slate-300 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600 text-sm transition"
            />
          </div>

          <div>
            <label class="block text-xs font-semibold text-slate-600 uppercase mb-1.5">密码</label>
            <div class="relative">
              <input
                v-model="loginForm.password"
                :type="showLoginPass ? 'text' : 'password'"
                autocomplete="current-password"
                placeholder="请输入密码"
                class="w-full px-3.5 py-2.5 rounded-xl border border-slate-300 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600 text-sm pr-10 transition"
              />
              <button
                type="button"
                @click="showLoginPass = !showLoginPass"
                class="absolute right-3 top-2.5 text-slate-400 hover:text-slate-600"
              >
                <EyeOff v-if="showLoginPass" class="w-4 h-4" />
                <Eye v-else class="w-4 h-4" />
              </button>
            </div>
          </div>

          <button
            type="submit"
            :disabled="loginLoading"
            class="w-full py-2.5 px-4 bg-indigo-600 hover:bg-indigo-700 text-white font-medium rounded-xl shadow-md shadow-indigo-200 transition disabled:opacity-50 text-sm flex items-center justify-center gap-2 mt-2"
          >
            <RotateCw v-if="loginLoading" class="w-4 h-4 animate-spin" />
            <span>{{ loginLoading ? '登录中...' : '进入控制台' }}</span>
          </button>
        </form>
      </div>
    </div>

    <!-- 已登录控制台界面 -->
    <div v-else class="flex-1 flex flex-col">
      <!-- 顶部导航栏 -->
      <header class="bg-white border-b border-slate-200 sticky top-0 z-30">
        <div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between">
          <div class="flex items-center gap-3">
            <div class="w-9 h-9 bg-indigo-600 rounded-xl flex items-center justify-center text-white shadow-sm shadow-indigo-200">
              <Server class="w-5 h-5" />
            </div>
            <div>
              <div class="flex items-center gap-2">
                <span class="font-bold text-slate-900 tracking-tight">video-webdav</span>
                <span class="text-[11px] bg-slate-100 text-slate-600 px-1.5 py-0.5 rounded font-mono font-medium">
                  {{ status?.version || 'v1.0' }}
                </span>
              </div>
              <div class="flex items-center gap-1.5 text-xs text-slate-500">
                <span class="w-2 h-2 rounded-full bg-emerald-500"></span>
                <span>{{ status?.listen || ':8443' }} ({{ status?.tls ? 'HTTPS' : 'HTTP' }})</span>
              </div>
            </div>
          </div>

          <!-- 导航标签 -->
          <nav class="flex items-center gap-1 bg-slate-100 p-1 rounded-xl">
            <button
              @click="activeTab = 'dashboard'"
              class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-medium transition"
              :class="activeTab === 'dashboard' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
            >
              <Activity class="w-3.5 h-3.5" />
              <span>仪表盘</span>
            </button>
            <button
              @click="activeTab = 'dirs'"
              class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-medium transition"
              :class="activeTab === 'dirs' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
            >
              <Folder class="w-3.5 h-3.5" />
              <span>视频目录</span>
            </button>
            <button
              @click="activeTab = 'settings'"
              class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-medium transition"
              :class="activeTab === 'settings' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
            >
              <Server class="w-3.5 h-3.5" />
              <span>服务配置</span>
            </button>
            <button
              @click="activeTab = 'logs'"
              class="flex items-center gap-1.5 px-3.5 py-1.5 rounded-lg text-xs font-medium transition"
              :class="activeTab === 'logs' ? 'bg-white text-indigo-600 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
            >
              <FileText class="w-3.5 h-3.5" />
              <span>运行日志</span>
            </button>
          </nav>

          <!-- 右侧操作栏 -->
          <div class="flex items-center gap-2">
            <button
              @click="handleRestart"
              :disabled="restarting"
              title="重启后台服务"
              class="p-2 text-slate-500 hover:text-amber-600 hover:bg-amber-50 rounded-lg transition"
            >
              <Power class="w-4 h-4" :class="{ 'animate-pulse text-amber-600': restarting }" />
            </button>
            <button
              @click="handleLogout"
              title="退出登录"
              class="p-2 text-slate-500 hover:text-rose-600 hover:bg-rose-50 rounded-lg transition"
            >
              <LogOut class="w-4 h-4" />
            </button>
          </div>
        </div>
      </header>

      <!-- 主体内容 -->
      <main class="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-6">
        <!-- 1. 仪表盘 Tab -->
        <div v-if="activeTab === 'dashboard'" class="space-y-6">
          <!-- 统计概览卡片 -->
          <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <div class="bg-white p-5 rounded-2xl border border-slate-200/80 shadow-sm flex items-center justify-between">
              <div>
                <p class="text-xs font-medium text-slate-500">服务运行时间</p>
                <p class="text-xl font-bold text-slate-900 mt-1">{{ status?.uptime_str || '0秒' }}</p>
              </div>
              <div class="w-10 h-10 rounded-xl bg-indigo-50 text-indigo-600 flex items-center justify-center">
                <Activity class="w-5 h-5" />
              </div>
            </div>

            <div class="bg-white p-5 rounded-2xl border border-slate-200/80 shadow-sm flex items-center justify-between">
              <div>
                <p class="text-xs font-medium text-slate-500">已配置视频目录</p>
                <p class="text-xl font-bold text-slate-900 mt-1">{{ safeDirsCount }} 个</p>
              </div>
              <div class="w-10 h-10 rounded-xl bg-blue-50 text-blue-600 flex items-center justify-center">
                <Folder class="w-5 h-5" />
              </div>
            </div>

            <div class="bg-white p-5 rounded-2xl border border-slate-200/80 shadow-sm flex items-center justify-between">
              <div>
                <p class="text-xs font-medium text-slate-500">内存占用</p>
                <p class="text-xl font-bold text-slate-900 mt-1">{{ status?.mem_alloc || '0 MB' }}</p>
              </div>
              <div class="w-10 h-10 rounded-xl bg-emerald-50 text-emerald-600 flex items-center justify-center">
                <Server class="w-5 h-5" />
              </div>
            </div>

            <div class="bg-white p-5 rounded-2xl border border-slate-200/80 shadow-sm flex items-center justify-between">
              <div>
                <p class="text-xs font-medium text-slate-500">登录防爆破封禁</p>
                <p class="text-xl font-bold text-slate-900 mt-1" :class="safeBannedCount > 0 ? 'text-rose-600' : 'text-slate-900'">
                  {{ safeBannedCount }} 个 IP
                </p>
              </div>
              <div class="w-10 h-10 rounded-xl bg-purple-50 text-purple-600 flex items-center justify-center">
                <Shield class="w-5 h-5" />
              </div>
            </div>
          </div>

          <!-- VidHub / Infuse 接入参数卡片 -->
          <div class="bg-gradient-to-r from-indigo-900 via-indigo-800 to-slate-900 rounded-2xl p-6 text-white shadow-lg">
            <div class="flex flex-col md:flex-row md:items-center justify-between gap-4 pb-4 border-b border-indigo-700/60">
              <div>
                <span class="inline-flex items-center gap-1 text-xs font-semibold uppercase tracking-wider text-indigo-300 bg-indigo-500/20 px-2 py-0.5 rounded">
                  播放器接入指南
                </span>
                <h2 class="text-xl font-bold mt-1">在 VidHub / Infuse 中添加 WebDAV 源</h2>
                <p class="text-indigo-200 text-xs mt-0.5">
                  在手机、电视或电脑播放器中添加 WebDAV，填入以下参数即可开始刮削：
                </p>
              </div>
              <button
                @click="copyText(webdavUrl)"
                class="inline-flex items-center gap-2 px-3.5 py-2 bg-white/10 hover:bg-white/20 text-white rounded-xl text-xs font-medium backdrop-blur transition"
              >
                <Copy class="w-3.5 h-3.5" />
                <span>复制完整连接地址</span>
              </button>
            </div>

            <div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-4 pt-4 text-xs">
              <div class="bg-white/5 rounded-xl p-3 border border-white/10">
                <span class="text-indigo-300 block mb-1">服务类型</span>
                <span class="font-mono font-semibold text-sm">WebDAV</span>
              </div>
              <div class="bg-white/5 rounded-xl p-3 border border-white/10">
                <span class="text-indigo-300 block mb-1">传输协议</span>
                <span class="font-mono font-semibold text-sm">{{ status?.tls ? 'HTTPS' : 'HTTP' }}</span>
              </div>
              <div class="bg-white/5 rounded-xl p-3 border border-white/10 col-span-2 sm:col-span-1">
                <span class="text-indigo-300 block mb-1">服务器地址</span>
                <span class="font-mono font-semibold text-sm truncate block" title="公网IP或域名">
                  {{ currentHost }}
                </span>
              </div>
              <div class="bg-white/5 rounded-xl p-3 border border-white/10">
                <span class="text-indigo-300 block mb-1">端口</span>
                <span class="font-mono font-semibold text-sm">{{ currentPort }}</span>
              </div>
              <div class="bg-white/5 rounded-xl p-3 border border-white/10">
                <span class="text-indigo-300 block mb-1">路径</span>
                <span class="font-mono font-semibold text-sm">/</span>
              </div>
              <div class="bg-white/5 rounded-xl p-3 border border-white/10">
                <span class="text-indigo-300 block mb-1">用户名</span>
                <span class="font-mono font-semibold text-sm">{{ config?.auth?.username || 'admin' }}</span>
              </div>
            </div>
          </div>

          <!-- 被封禁 IP 列表 -->
          <div class="bg-white rounded-2xl border border-slate-200/80 shadow-sm p-5">
            <div class="flex items-center justify-between mb-4">
              <div class="flex items-center gap-2">
                <Shield class="w-5 h-5 text-indigo-600" />
                <h3 class="font-bold text-slate-900 text-sm">安全防护：当前被封禁的 IP</h3>
              </div>
              <button
                @click="loadStatus"
                class="text-xs text-indigo-600 hover:text-indigo-800 flex items-center gap-1"
              >
                <RefreshCw class="w-3.5 h-3.5" />
                <span>刷新</span>
              </button>
            </div>

            <div v-if="safeBannedCount === 0" class="text-center py-6 text-xs text-slate-400">
              当前没有被封禁的 IP，系统运行正常。
            </div>
            <div v-else class="divide-y divide-slate-100">
              <div
                v-for="item in (bannedIPs || [])"
                :key="item.ip"
                class="py-3 flex items-center justify-between text-xs"
              >
                <div class="flex items-center gap-3">
                  <span class="w-2 h-2 rounded-full bg-rose-500"></span>
                  <span class="font-mono font-medium text-slate-900">{{ item.ip }}</span>
                  <span class="text-slate-400">预计封禁至 {{ new Date(item.banned_until).toLocaleTimeString() }}</span>
                </div>
                <button
                  @click="unban(item.ip)"
                  class="px-2.5 py-1 text-xs bg-slate-100 hover:bg-slate-200 text-slate-700 rounded-lg transition"
                >
                  立即解封
                </button>
              </div>
            </div>
          </div>
        </div>

        <!-- 2. 视频目录 Tab -->
        <div v-else-if="activeTab === 'dirs'" class="space-y-4">
          <div class="flex items-center justify-between">
            <div>
              <h2 class="text-base font-bold text-slate-900">视频目录管理</h2>
              <p class="text-xs text-slate-500 mt-0.5">
                每个目录在 WebDAV 根目录下显示为一个独立的文件夹，修改后点击保存立即生效。
              </p>
            </div>
            <button
              @click="openAddDirModal"
              class="inline-flex items-center gap-1.5 px-3.5 py-2 bg-indigo-600 hover:bg-indigo-700 text-white rounded-xl text-xs font-medium shadow-sm transition"
            >
              <Plus class="w-4 h-4" />
              <span>添加视频目录</span>
            </button>
          </div>

          <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div
              v-for="(dir, idx) in config?.dirs || []"
              :key="idx"
              class="bg-white rounded-2xl border border-slate-200/80 p-5 shadow-sm hover:border-indigo-300 transition group flex flex-col justify-between"
            >
              <div>
                <div class="flex items-center justify-between">
                  <div class="flex items-center gap-2">
                    <div class="w-8 h-8 rounded-lg bg-indigo-50 text-indigo-600 flex items-center justify-center font-bold text-sm">
                      /
                    </div>
                    <span class="font-bold text-slate-900 text-base">{{ dir.name }}</span>
                  </div>
                  <div class="flex items-center gap-1 opacity-80 group-hover:opacity-100 transition">
                    <button
                      @click="openEditDirModal(idx)"
                      class="p-1.5 text-slate-400 hover:text-indigo-600 hover:bg-indigo-50 rounded-lg transition"
                      title="编辑"
                    >
                      <Edit2 class="w-4 h-4" />
                    </button>
                    <button
                      @click="removeDir(idx)"
                      class="p-1.5 text-slate-400 hover:text-rose-600 hover:bg-rose-50 rounded-lg transition"
                      title="删除"
                    >
                      <Trash2 class="w-4 h-4" />
                    </button>
                  </div>
                </div>
                <div class="mt-3">
                  <span class="text-[11px] text-slate-400 font-semibold block uppercase">本地真实磁盘路径</span>
                  <div class="mt-1 font-mono text-xs bg-slate-50 border border-slate-200/60 px-3 py-2 rounded-xl text-slate-700 break-all">
                    {{ dir.path }}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 3. 服务配置 Tab -->
        <div v-else-if="activeTab === 'settings'" class="space-y-6">
          <div class="flex items-center justify-between pb-2 border-b border-slate-200">
            <div>
              <h2 class="text-base font-bold text-slate-900">服务与安全配置</h2>
              <p class="text-xs text-slate-500 mt-0.5">修改完后请点击右侧保存按钮更新配置。</p>
            </div>
            <button
              @click="saveConfigData()"
              :disabled="saving"
              class="inline-flex items-center gap-1.5 px-4 py-2 bg-indigo-600 hover:bg-indigo-700 text-white rounded-xl text-xs font-medium shadow-sm transition disabled:opacity-50"
            >
              <RotateCw v-if="saving" class="w-3.5 h-3.5 animate-spin" />
              <span>{{ saving ? '保存中...' : '保存配置' }}</span>
            </button>
          </div>

          <div v-if="config" class="grid grid-cols-1 md:grid-cols-2 gap-6">
            <!-- 基础网络设置 -->
            <div class="bg-white rounded-2xl border border-slate-200/80 p-5 shadow-sm space-y-4">
              <h3 class="font-bold text-slate-900 text-sm flex items-center gap-2">
                <Server class="w-4 h-4 text-indigo-600" />
                <span>网络与服务监听</span>
              </h3>

              <div>
                <label class="block text-xs font-medium text-slate-600 mb-1">监听地址与端口</label>
                <input
                  v-model="config.server.listen"
                  type="text"
                  placeholder=":8443"
                  class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
                />
                <span class="text-[11px] text-slate-400 mt-1 block">路由器端口转发时指向此端口</span>
              </div>

              <div class="pt-2 border-t border-slate-100">
                <div class="flex items-center justify-between mb-3">
                  <div>
                    <label class="text-xs font-medium text-slate-700 block">启用 HTTPS (TLS)</label>
                    <span class="text-[11px] text-slate-400">暴露到公网时强烈建议开启</span>
                  </div>
                  <input
                    v-model="config.server.tls.enabled"
                    type="checkbox"
                    class="w-4 h-4 text-indigo-600 rounded border-slate-300 focus:ring-indigo-500"
                  />
                </div>

                <div v-if="config.server.tls.enabled" class="space-y-3 pt-2">
                  <div>
                    <label class="block text-xs font-medium text-slate-600 mb-1">证书文件路径 (cert_file)</label>
                    <input
                      v-model="config.server.tls.cert_file"
                      type="text"
                      placeholder="certs\video.example.com_bundle.crt"
                      class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
                    />
                  </div>
                  <div>
                    <label class="block text-xs font-medium text-slate-600 mb-1">私钥文件路径 (key_file)</label>
                    <input
                      v-model="config.server.tls.key_file"
                      type="text"
                      placeholder="certs\video.example.com.key"
                      class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
                    />
                  </div>
                </div>
              </div>
            </div>

            <!-- 登录账号与安全策略 -->
            <div class="bg-white rounded-2xl border border-slate-200/80 p-5 shadow-sm space-y-4">
              <h3 class="font-bold text-slate-900 text-sm flex items-center gap-2">
                <Shield class="w-4 h-4 text-indigo-600" />
                <span>认证与安全防护</span>
              </h3>

              <div>
                <label class="block text-xs font-medium text-slate-600 mb-1">登录用户名</label>
                <input
                  v-model="config.auth.username"
                  type="text"
                  class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
                />
              </div>

              <div>
                <label class="block text-xs font-medium text-slate-600 mb-1">登录密码 (留空表示保持原密码不变)</label>
                <input
                  v-model="config.auth.password"
                  type="password"
                  placeholder="留空表示不修改密码"
                  class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
                />
              </div>

              <div class="grid grid-cols-2 gap-3 pt-2 border-t border-slate-100">
                <div>
                  <label class="block text-xs font-medium text-slate-600 mb-1">最大失败次数</label>
                  <input
                    v-model.number="config.security.max_failures"
                    type="number"
                    class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono"
                  />
                </div>
                <div>
                  <label class="block text-xs font-medium text-slate-600 mb-1">封禁时长</label>
                  <input
                    v-model="config.security.ban_duration"
                    type="text"
                    placeholder="30m"
                    class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono"
                  />
                </div>
              </div>
            </div>

            <!-- 文件过滤规则 -->
            <div class="bg-white rounded-2xl border border-slate-200/80 p-5 shadow-sm space-y-4">
              <h3 class="font-bold text-slate-900 text-sm flex items-center gap-2">
                <Folder class="w-4 h-4 text-indigo-600" />
                <span>文件过滤与展示规则</span>
              </h3>

              <div class="flex items-center justify-between">
                <div>
                  <label class="text-xs font-medium text-slate-700 block">自动隐藏空目录</label>
                  <span class="text-[11px] text-slate-400">不包含任何视频文件的目录将对客户端不可见</span>
                </div>
                <input
                  v-model="config.filter.hide_empty_dirs"
                  type="checkbox"
                  class="w-4 h-4 text-indigo-600 rounded border-slate-300 focus:ring-indigo-500"
                />
              </div>

              <div>
                <label class="block text-xs font-medium text-slate-600 mb-1">视频文件扩展名 (以逗号分隔)</label>
                <input
                  :value="(config.filter.video_exts || []).join(', ')"
                  @input="(e: any) => config!.filter.video_exts = e.target.value.split(',').map((s: string) => s.trim()).filter(Boolean)"
                  type="text"
                  class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono"
                />
              </div>

              <div>
                <label class="block text-xs font-medium text-slate-600 mb-1">字幕文件扩展名 (以逗号分隔)</label>
                <input
                  :value="(config.filter.subtitle_exts || []).join(', ')"
                  @input="(e: any) => config!.filter.subtitle_exts = e.target.value.split(',').map((s: string) => s.trim()).filter(Boolean)"
                  type="text"
                  class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono"
                />
              </div>
            </div>

            <!-- 日志配置 -->
            <div class="bg-white rounded-2xl border border-slate-200/80 p-5 shadow-sm space-y-4">
              <h3 class="font-bold text-slate-900 text-sm flex items-center gap-2">
                <FileText class="w-4 h-4 text-indigo-600" />
                <span>系统日志配置</span>
              </h3>

              <div>
                <label class="block text-xs font-medium text-slate-600 mb-1">日志文件路径</label>
                <input
                  v-model="config.log.file"
                  type="text"
                  placeholder="logs\video-server.log"
                  class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs font-mono"
                />
              </div>

              <div class="grid grid-cols-2 gap-3">
                <div>
                  <label class="block text-xs font-medium text-slate-600 mb-1">日志级别</label>
                  <select
                    v-model="config.log.level"
                    class="w-full px-3 py-2 rounded-xl border border-slate-300 text-xs bg-white"
                  >
                    <option value="debug">debug (调试)</option>
                    <option value="info">info (正常)</option>
                    <option value="warn">warn (警告)</option>
                    <option value="error">error (错误)</option>
                  </select>
                </div>
                <div class="flex items-center justify-between pt-4">
                  <label class="text-xs font-medium text-slate-700">记录请求日志</label>
                  <input
                    v-model="config.log.access_log"
                    type="checkbox"
                    class="w-4 h-4 text-indigo-600 rounded border-slate-300 focus:ring-indigo-500"
                  />
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 4. 运行日志 Tab -->
        <div v-else-if="activeTab === 'logs'" class="space-y-4">
          <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <div>
              <h2 class="text-base font-bold text-slate-900">运行日志查看</h2>
              <p class="text-xs text-slate-500 mt-0.5">实时读取最近产生的系统与访问日志。</p>
            </div>
            <div class="flex items-center gap-3">
              <div class="relative">
                <Search class="w-3.5 h-3.5 absolute left-3 top-2.5 text-slate-400" />
                <input
                  v-model="logFilter"
                  type="text"
                  placeholder="按关键词过滤..."
                  class="pl-9 pr-3 py-1.5 bg-white border border-slate-200 rounded-xl text-xs focus:outline-none focus:ring-2 focus:ring-indigo-500/20"
                />
              </div>
              <label class="flex items-center gap-1.5 text-xs text-slate-600 cursor-pointer">
                <input
                  v-model="autoRefreshLogs"
                  type="checkbox"
                  class="w-3.5 h-3.5 text-indigo-600 rounded border-slate-300"
                />
                <span>自动刷新</span>
              </label>
              <button
                @click="loadLogs"
                class="px-3 py-1.5 bg-white border border-slate-200 hover:bg-slate-50 text-slate-700 rounded-xl text-xs font-medium transition flex items-center gap-1"
              >
                <RefreshCw class="w-3.5 h-3.5" />
                <span>刷新</span>
              </button>
            </div>
          </div>

          <div class="bg-slate-900 rounded-2xl p-4 font-mono text-xs text-slate-300 overflow-x-auto shadow-inner max-h-[600px] overflow-y-auto leading-relaxed">
            <div v-if="(filteredLogs || []).length === 0" class="text-slate-500 text-center py-10">
              暂无匹配的日志记录
            </div>
            <div v-else class="space-y-1">
              <div v-for="(line, idx) in (filteredLogs || [])" :key="idx" class="whitespace-pre-wrap hover:bg-slate-800/60 px-2 py-0.5 rounded">
                <span :class="{
                  'text-rose-400': line.includes('level=ERROR') || line.includes('失败'),
                  'text-amber-400': line.includes('level=WARN'),
                  'text-emerald-400': line.includes('level=INFO'),
                  'text-slate-400': line.includes('level=DEBUG'),
                }">
                  {{ line }}
                </span>
              </div>
            </div>
          </div>
        </div>
      </main>
    </div>

    <!-- 添加 / 编辑视频目录 Modal 弹窗 -->
    <div
      v-if="dirModal.show"
      class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-900/40 backdrop-blur-sm"
    >
      <div class="bg-white rounded-2xl shadow-2xl max-w-lg w-full p-6 border border-slate-200">
        <h3 class="text-base font-bold text-slate-900 mb-1">
          {{ dirModal.isEdit ? '编辑视频目录' : '添加新的视频目录' }}
        </h3>
        <p class="text-xs text-slate-500 mb-4">
          支持配置本地硬盘路径（如 D:\Movies）或局域网共享路径。
        </p>

        <div class="space-y-4">
          <div>
            <label class="block text-xs font-medium text-slate-700 mb-1">
              显示名称 <span class="text-slate-400">(在播放器 WebDAV 根目录下看到的文件夹名)</span>
            </label>
            <input
              v-model="dirModal.name"
              type="text"
              placeholder="例如: 电影、动漫、美剧"
              class="w-full px-3 py-2 rounded-xl border border-slate-300 text-sm focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
            />
          </div>

          <div>
            <div class="flex items-center justify-between mb-1">
              <label class="text-xs font-medium text-slate-700">本地绝对路径</label>
              <button
                type="button"
                @click="testCurrentPath"
                :disabled="dirModal.testing"
                class="text-xs text-indigo-600 hover:text-indigo-800 font-medium flex items-center gap-1"
              >
                <RotateCw v-if="dirModal.testing" class="w-3.5 h-3.5 animate-spin" />
                <span>{{ dirModal.testing ? '检测中...' : '测试此路径' }}</span>
              </button>
            </div>
            <input
              v-model="dirModal.path"
              type="text"
              placeholder="例如: D:\Movies 或 E:\TV"
              class="w-full px-3 py-2 rounded-xl border border-slate-300 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-600"
            />
          </div>

          <!-- 路径检测反馈 -->
          <div v-if="dirModal.testResult" class="p-3 rounded-xl text-xs" :class="dirModal.testResult.exists ? 'bg-emerald-50 border border-emerald-200 text-emerald-800' : 'bg-rose-50 border border-rose-200 text-rose-800'">
            <div v-if="dirModal.testResult.exists" class="flex items-center gap-2">
              <CheckCircle2 class="w-4 h-4 text-emerald-600 flex-shrink-0" />
              <span>检测成功！该目录存在，且扫描到 <strong>{{ dirModal.testResult.video_count }}</strong> 个视频文件。</span>
            </div>
            <div v-else class="flex items-center gap-2">
              <AlertCircle class="w-4 h-4 text-rose-600 flex-shrink-0" />
              <span>路径不可访问: {{ dirModal.testResult.error || '路径不存在或不是目录' }}</span>
            </div>
          </div>
        </div>

        <div class="flex items-center justify-end gap-3 mt-6 pt-4 border-t border-slate-100">
          <button
            type="button"
            @click="dirModal.show = false"
            class="px-4 py-2 text-xs font-medium text-slate-600 hover:text-slate-800 hover:bg-slate-100 rounded-xl transition"
          >
            取消
          </button>
          <button
            type="button"
            @click="saveDirModal"
            class="px-4 py-2 text-xs font-medium bg-indigo-600 hover:bg-indigo-700 text-white rounded-xl shadow-sm transition"
          >
            确定
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

