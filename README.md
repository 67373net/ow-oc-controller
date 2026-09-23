# OpenClash 简易面板

---

## 功能
  - **代理一键开关**
  - **机场管理**：自动读取机场，添加、删除机场。
  - **连接日志**：显示链接日志，可切换是否自动刷新。

---

## 快速部署 (Docker Compose)

### 1. 复制配置文件

```bash
cp .env.example .env
```

### 2. 编辑 `.env` 环境变量

```ini
# OpenWrt 主路由局域网 IP
OPENWRT_HOST=192.168.1.1

# OpenClash 外部控制端口与密钥 (OpenClash 设置 -> 外部控制)
OPENCLASH_API_PORT=9090
OPENCLASH_API_SECRET=your_secret_here

# OpenWrt SSH 凭证 (用于彻底启停服务与切换机场配置文件)
OPENWRT_SSH_PORT=22
OPENWRT_SSH_USER=root
OPENWRT_SSH_PASS=your_openwrt_password

# 控制器 Web 访问端口
PORT=8080
```

> **提示**：如果未配置 OpenWrt SSH 密码，系统将自动进入「核心代理模式」：支持节点切换、测速、以及通过切换直连模式 (Direct) 实现秒级关闭代理。

### 3. OpenWrt 端预先检查（跨机器通信必备）

由于 Docker 运行在局域网独立电脑/NAS 上，需确保 OpenWrt 允许局域网设备访问控制接口：
1. **OpenClash 外部控制**：在 OpenWrt 网页中进入 **服务 -> OpenClash -> 全局设置 -> 外部控制**：
   - 确认绑定地址包含局域网（通常默认 `0.0.0.0` 或勾选「允许局域网连接」）。
   - 端口（默认 `9090`）和密钥（Secret）与 `.env` 中保持一致。
2. **SSH 远程管理（若需服务启停及切机场）**：
   - OpenWrt 默认允许局域网（LAN）通过 22 端口访问 SSH。只需确保 `.env` 中的 `OPENWRT_SSH_PASS` 填写了正确的 root 密码即可。

### 4. 一键启动

```bash
docker compose up -d --build
```

启动完成后，直接在浏览器中访问：`http://<电脑局域网IP>:8080`。

---

## 目录结构

```
.
├── .env.example           # 环境变量配置模版
├── docker-compose.yml     # Docker Compose 编排文件
├── Dockerfile             # 多阶段极速构建 Dockerfile (带构建缓存)
├── go.mod / go.sum        # Go 依赖配置
├── main.go                # 入口主程序 (内嵌静态前端)
├── internal/
│   ├── config/            # 环境变量加载与校验
│   ├── clash/             # Clash / Mihomo RESTful 核心控制客户端
│   ├── openwrt/           # OpenWrt SSH 驱动与服务/机场切换模块
│   └── api/               # REST API 路由与控制器
├── web/                   # 前端资源 (内嵌到二进制中)
│   ├── index.html         # 全屏无 Emoji 简洁骨架
│   ├── app.css            # 系统主题感知、全宽响应式样式
│   └── app.js             # 业务逻辑、无感测速、非激活休眠优化
└── CONVERSATION_RECORD.md # 对话记录、敏感信息脱敏与 Token 消耗审计
```
