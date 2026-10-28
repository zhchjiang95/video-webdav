# video-server

专为 VidHub / Infuse 等播放器打造的轻量只读 WebDAV 视频服务，支持多目录映射、自动过滤与 Windows 服务常驻。

- 用 Go 编写，编译后就是一个 `video-server.exe`，可以注册成 **Windows 服务**开机自启
- 可配置**多个视频目录**，每个目录在根目录下显示为一个独立的文件夹
- **只暴露视频和字幕文件**，其它文件对客户端完全不可见；不含视频的目录会自动隐藏
- **只读**：拒绝上传、删除、重命名等所有写操作
- 固定账号的 **Basic 认证**，同一 IP 多次登录失败会**自动封禁**
- 支持 **HTTPS**（腾讯云免费证书），替换证书文件后**自动热加载**，无需重启
- 支持 Range 请求，可以拖动进度条

## 目录结构

```
├── main.go                    # 入口：命令行和 Windows 服务管理
├── internal/
│   ├── config/                # 配置加载与校验
│   ├── vfs/                   # 只读虚拟文件系统（多目录映射、过滤、路径安全校验）
│   ├── auth/                  # Basic 认证 + 失败封禁
│   ├── tlscert/               # 证书加载与热更新
│   └── server/                # HTTP 服务组装、只读方法过滤、访问日志
├── config.example.yaml        # 配置示例
└── build.sh                   # 交叉编译 Windows 版本
```

## 编译

需要 Go 1.22 及以上版本（Mac 上可以用 `brew install go` 安装）。

```bash
go mod tidy        # 首次编译前下载依赖
./build.sh         # 生成 dist/video-server.exe
```

如果 Windows 电脑是 ARM 架构，改用 `GOARCH=arm64 ./build.sh`。

## 部署到 Windows

### 1. 放置文件

比如放在 `D:\video-server\`：

```
D:\video-server\
├── video-server.exe
├── config.yaml                     # 由 config.example.yaml 复制后修改
└── certs\
    ├── video.example.com_bundle.crt
    └── video.example.com.key
```

### 2. 配置模式选择

#### 模式一：纯公网 IP + HTTP 访问（当前最简方案）

如果路由器不支持绑定自己的域名，可以直接使用公网 IP 和 HTTP 访问：
1. 在 `config.yaml` 中将 `server.tls.enabled` 设为 `false`（默认即为 `false`）。
2. 在客户端（VidHub）直接填写你的公网 IP 地址，协议选 **HTTP**。
3. **特别注意**：HTTP 下密码为明文传输，请务必设置复杂的强密码。服务自带登录失败封禁机制（防爆破），但仍建议避开常用端口。

> **公网 IP 变动问题**：家宽重新拨号后 IP 会变。如果不希望每次手动查 IP，请参考下文的“如何搭配自有域名”。

#### 模式二：域名 + 腾讯云免费 HTTPS 证书

如果你想用域名并开启 HTTPS：
1. 在 `config.yaml` 中将 `server.tls.enabled` 设为 `true`。
2. 在腾讯云 SSL 证书控制台**下载证书**，服务器类型选 **Nginx**。
3. 解压后把 `xxx_bundle.crt` 和 `xxx.key` 放到 `certs\` 目录，并在 `config.yaml` 里配置证书文件路径。
4. **服务支持证书热重载**：免费证书到期前在腾讯云重新申请并覆盖文件，1 分钟内自动生效，无需重启。

---

### 💡 路由器不支持腾讯云 DDNS 怎么用自有域名？

如果路由器内置的 DDNS 只有花生壳、公云、No-IP 等，有两种非常成熟的方案让你依然能用上自己的域名和证书：

- **方案 1（强烈推荐，最稳定）：在 Windows 上运行开源工具 `ddns-go`**
  - 下载单文件版 `ddns-go.exe`（GitHub 开源），在 Windows 电脑上点击安装为服务。
  - 打开自带的 Web 界面，服务商直接选择 **腾讯云 (DNSPod)**，填入你的 SecretId/SecretKey。
  - 它会每隔几分钟自动获取公网 IP 并更新你的域名解析，完全脱离路由器的限制。
- **方案 2：利用路由器的免费 DDNS 做 CNAME 别名**
  - 在路由器上开启花生壳或公云（3322.org），免费申请一个二级域名（如 `xxx.vicp.net`）。
  - 登录腾讯云域名解析控制台，给你的域名添加一条 **CNAME 记录**（例如 `video.yourdomain.com` -> `xxx.vicp.net`）。
  - 这样访问你的域名时，就会自动解析到花生壳上报的公网 IP。

---

### 3. 校验配置

```powershell
cd D:\video-server
.\video-server.exe check
```

### 4. 先在前台试运行

```powershell
.\video-server.exe
```

看到“服务已启动”后，先在局域网内用播放器连接测试，按 `Ctrl+C` 退出。

### 5. 注册为 Windows 服务（以管理员身份打开 PowerShell）

```powershell
.\video-server.exe install
.\video-server.exe start
```

其它命令：`stop`、`restart`、`uninstall`。服务名为 `VideoServer`，也可以在“服务”管理器（services.msc）里管理。

### 6. 放行防火墙（管理员 PowerShell）

```powershell
netsh advfirewall firewall add rule name="VideoServer" dir=in action=allow protocol=TCP localport=8443
```

### 7. 路由器端口转发

把外网端口（比如 `8443` 或 `9000`）转发到这台 Windows 电脑的 `局域网IP:8443`。建议在路由器里给这台电脑**固定局域网 IP**（DHCP 静态分配）。

> 国内家宽通常封了 80、443、8080 等常用端口，请在路由器和配置中选用 8443、9000 等高位端口。

## 在 VidHub 中添加

| 项目 | 纯 IP + HTTP 模式 | 域名 + HTTPS 模式 |
|---|---|---|
| 类型 | WebDAV | WebDAV |
| 协议 | **HTTP** | **HTTPS** |
| 地址 | 你的公网 IPv4 地址（如 `123.45.67.89`） | 你的域名（如 `video.example.com`） |
| 端口 | 路由器转发的外网端口（如 `8443`） | 路由器转发的外网端口（如 `8443`） |
| 路径 | `/` | `/` |
| 用户名 / 密码 | `config.yaml` 中的 `auth` | `config.yaml` 中的 `auth` |

添加后会看到配置的各个目录（如“电影”“剧集”），在 VidHub 里把对应目录加入媒体库即可刮削。

## 日常管理与修改配置后重启

### 1. 修改配置文件后重启服务

修改 `config.yaml`（例如修改了密码、新增了目录、调整了端口等）后：

**强烈建议先校验配置文件语法：**
```powershell
.\video-server.exe check
```

**根据运行方式进行重启：**
- **如果是作为 Windows 服务后台运行**（推荐方式）：
  以管理员身份打开 PowerShell / CMD，进入程序目录执行：
  ```powershell
  .\video-server.exe restart
  ```
  *或者直接在系统的“服务”管理器（`services.msc`）中找到 **Video Server (WebDAV)** 右键点击“重新启动”，或者运行 `Restart-Service VideoServer`。*

- **如果是前台命令行直接运行**：
  直接在原来的窗口按键盘快捷键 `Ctrl + C` 退出，然后重新输入 `.\video-server.exe` 启动即可。

> **小贴士**：如果**仅更新了 HTTPS 证书文件**（.crt / .key 覆盖），**无需重启服务**，服务内置热检测，1 分钟内会自动平滑生效。

## 注意事项

### 服务账号与网络驱动器

Windows 服务默认以 **LocalSystem** 账号运行，**看不到当前用户映射的网络驱动器盘符**（比如映射成 `Z:` 的 NAS 共享）。如果视频放在 NAS 上，有两种办法：

- 在 services.msc 里把 `VideoServer` 的“登录身份”改为你自己的 Windows 账号；
- 或者在 `dirs.path` 里直接写 UNC 路径（如 `'\\NAS\Movies'`），并确保服务账号有权限访问。

### 符号链接 / 目录联接 / OneDrive

默认 `follow_symlinks: false`：符号链接、目录联接（junction）以及 OneDrive“按需下载”的占位文件都**不会显示**，这是为了防止通过链接访问到配置目录之外的文件。如果确实需要，可以开启，但要自行确认链接的目标都是安全的。

### 安全相关

- 密码至少 8 位，**公网环境请务必使用随机强密码**。
- 同一 IP 在 10 分钟内登录失败 5 次会被封禁 30 分钟，可在 `security` 中调整。被封禁期间该 IP 的请求会收到 `429`。
- 只信任 TCP 连接的来源 IP，不读取 `X-Forwarded-For`。如果以后在前面加了反向代理，需要调整 [auth.go](internal/auth/auth.go) 里的 `ClientIP`。
- 只允许 `OPTIONS / GET / HEAD / PROPFIND`。`PROPFIND` 的 `Depth: infinity` 会被降为 `1`，防止一次请求就遍历整个目录树。
- 路径校验会拦截 `..`、反斜杠、盘符/NTFS 备用数据流（`:`）、末尾带点或空格等 Windows 特殊写法。

### 外网播放卡顿

外网播放的速度受限于**家宽上行带宽**（通常是 30～100Mbps）。4K 原盘 Remux 的码率可能超过这个数，播放会卡顿，这种情况属于带宽限制，不是服务的问题。

### 新增视频后目录没出现

`hide_empty_dirs` 的判断结果会缓存 `empty_dir_cache_ttl`（默认 2 分钟）。新建的文件夹在缓存过期后才会出现；已经显示的目录里新增的文件则会立即可见。

## 日志与排查

- 日志文件默认在程序目录下的 `logs\video-server.log`，会自动轮转（单个文件 20MB，保留 5 个，最多 30 天）。
- 如果以服务方式启动失败（比如配置文件有误），错误会写进 **Windows 事件查看器 → Windows 日志 → 应用程序**。
- 想看更详细的信息，把 `log.level` 设为 `debug`。
