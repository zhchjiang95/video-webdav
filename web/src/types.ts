// 系统状态信息
export interface ServerStatus {
  version: string
  uptime: number
  uptime_str: string
  listen: string
  tls: boolean
  go_version: string
  mem_alloc: string
  mem_sys: string
  num_goroutine: number
  dirs_count: number
  banned_count: number
}

// 被封禁客户端信息
export interface BannedIP {
  ip: string
  banned_until: string
  duration_sec: number
}

// 支持的目录文件类型
export type FileType = 'video' | 'audio' | 'image'

// 单个目录配置
export interface DirConfig {
  name: string
  path: string
  types: FileType[]
}

// 完整服务配置
export interface AppConfig {
  server: {
    listen: string
    tls: {
      enabled: boolean
      cert_file: string
      key_file: string
    }
  }
  auth: {
    username: string
    password?: string
  }
  security: {
    max_failures: number
    failure_window: string
    ban_duration: string
  }
  dirs: DirConfig[]
  filter: {
    video_exts: string[]
    subtitle_exts: string[]
    audio_exts: string[]
    lyric_exts: string[]
    image_exts: string[]
    extra_exts: string[]
    exclude_names: string[]
    hide_empty_dirs: boolean
    empty_dir_cache_ttl: string
    follow_symlinks: boolean
  }
  log: {
    file: string
    level: string
    access_log: boolean
  }
}

// 路径测试结果
export interface TestPathResult {
  exists: boolean
  is_dir: boolean
  match_count: number
  video_count?: number
  truncated?: boolean
  types?: FileType[]
  sample_files?: string[]
  error?: string
}

// 日志行数据
export interface LogItem {
  time: string
  level: 'info' | 'warn' | 'error' | 'debug'
  msg: string
  raw: string
}

