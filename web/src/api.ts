import type { ServerStatus, BannedIP, AppConfig, TestPathResult } from './types'

const TOKEN_KEY = 'video_webdav_token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) || ''
}

export function setToken(token: string) {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token)
  } else {
    localStorage.removeItem(TOKEN_KEY)
  }
}

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers || {})
  headers.set('Accept', 'application/json')
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  if (options.body && typeof options.body === 'string' && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const res = await fetch(url, { ...options, headers })
  if (res.status === 401) {
    setToken('')
    window.dispatchEvent(new CustomEvent('auth:expired'))
    throw new Error('未登录或登录已过期，请重新登录')
  }
  if (!res.ok) {
    let errMsg = `请求失败: HTTP ${res.status}`
    try {
      const errData = await res.json()
      if (errData && errData.error) {
        errMsg = errData.error
      }
    } catch {
      // 忽略非 JSON 响应
    }
    throw new Error(errMsg)
  }
  return res.json() as Promise<T>
}

export const api = {
  // 认证接口
  login: (username: string, password: string) =>
    request<{ ok: boolean; token: string; username: string }>('/api/admin/login', {
      method: 'POST',
      body: JSON.stringify({ username, password }),
    }),

  logout: () =>
    request<{ ok: boolean }>('/api/admin/logout', { method: 'POST' }),

  checkAuth: () =>
    request<{ authenticated: boolean; username?: string }>('/api/admin/auth/check'),

  // 状态与信息
  getStatus: () =>
    request<ServerStatus>('/api/admin/status'),

  // 被封禁 IP 管理
  getBannedIPs: () =>
    request<BannedIP[]>('/api/admin/banned-ips'),

  unbanIP: (ip: string) =>
    request<{ ok: boolean }>('/api/admin/unban', {
      method: 'POST',
      body: JSON.stringify({ ip }),
    }),

  // 配置获取与保存
  getConfig: () =>
    request<AppConfig>('/api/admin/config'),

  saveConfig: (cfg: AppConfig) =>
    request<{ ok: boolean; restart_required: boolean; message: string }>('/api/admin/config', {
      method: 'POST',
      body: JSON.stringify(cfg),
    }),

  // 路径测试
  testPath: (path: string) =>
    request<TestPathResult>('/api/admin/test-path', {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),

  // 获取运行日志
  getLogs: () =>
    request<{ lines: string[] }>('/api/admin/logs'),

  // 重启服务
  restart: () =>
    request<{ ok: boolean; message: string }>('/api/admin/restart', { method: 'POST' }),
}

