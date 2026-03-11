# GoDisk

GoDisk 是一个基于 Go + Gin 框架的云存储后端系统，提供文件上传/下载、分块断点续传、文件去重、语义搜索和分享链接等功能。

## 功能特性

- 文件上传/下载，支持大文件分块断点续传
- 基于 SHA256 的文件去重
- 语义搜索（基于 Qwen Embeddings API）
- 分享链接，支持密码保护和过期设置
- 用户存储配额管理
- SQLite 数据库存储

## 环境要求

- Go 1.21+
- 无需 CGO（使用纯 Go SQLite 驱动）

## 安装

1. 克隆项目
```bash
git clone https://github.com/yourusername/godisk.git
cd godisk
```

2. 创建配置文件
```bash
cp config.yaml.example config.yaml
```

3. 编辑 `config.yaml`，填写必要配置：
```yaml
jwt:
  secret: YOUR_JWT_SECRET_HERE  # 修改为强随机字符串

qwen:
  api_key: YOUR_QWEN_API_KEY_HERE  # 通义千问 API Key（可选）
```

## 运行

```bash
# 开发环境运行
go run ./cmd/server/main.go

# 编译后运行
go build -o GoDisk.exe ./cmd/server/main.go
.\GoDisk.exe    # Windows
```

服务启动后访问 `http://localhost:8080`

## 使用说明

### Web 界面

服务启动后，通过浏览器访问：

```
http://localhost:8080
```

1. **注册/登录**：首次使用请注册账号
2. **文件管理**：上传、下载、删除文件，支持拖拽上传
3. **文件夹**：创建文件夹管理文件
4. **搜索**：支持文件名搜索和语义搜索
5. **分享**：生成分享链接，支持密码保护和过期设置

### 分享文件

点击文件后的「分享」按钮生成链接，分享格式：

```
http://localhost:8080/s/{share_code}
```

接收方通过浏览器打开链接即可下载。

## 配置说明

| 配置项 | 说明 |
|--------|------|
| `server.port` | 服务端口，默认 8080 |
| `storage.max_file_size` | 单文件最大大小，默认 1GB |
| `storage.default_storage_limit` | 用户默认存储配额，默认 10GB |
| `jwt.expire_hours` | Token 有效期，默认 168 小时（7天） |

## 项目结构

```
GoDisk/
├── cmd/server/      # 程序入口
├── internal/
│   ├── handler/     # HTTP 处理层
│   ├── service/     # 业务逻辑层
│   ├── model/       # 数据模型层
│   ├── middleware/  # 中间件
│   └── config/      # 配置管理
├── uploads/         # 用户上传文件（自动创建）
├── data/            # 数据库和向量数据（自动创建）
└── webpage/         # 分享页面静态文件
```

## License

MIT License
