# GoDisk 云存储系统 - 完整面试指南

> 本文档将把您当作 Go 语言小白，详细讲解整个项目的技术细节、架构设计和实现原理。

---

## 目录

1. [项目简介](#项目简介)
2. [技术栈](#技术栈)
3. [Go 语言基础讲解](#go-语言基础讲解)
4. [项目架构设计](#项目架构设计)
5. [核心模块详解](#核心模块详解)
6. [数据库设计](#数据库设计)
7. [API 接口设计](#api-接口设计)
8. [面试题与解答](#面试题与解答)

---

## 项目简介

**GoDisk** 是一个功能完善的云存储后端系统，类似于百度网盘、Google Drive 等云存储服务。项目采用 Go 语言 + Gin 框架开发，具备以下核心功能：

### 核心功能

| 功能模块 | 描述 |
|---------|------|
| 用户认证 | 注册、登录、JWT Token 认证、密码修改 |
| 文件管理 | 文件上传/下载、文件夹管理、文件重命名/移动/删除 |
| 分片上传 | 大文件分片上传、断点续传、分片合并 |
| 文件去重 | 基于 SHA256 哈希值的文件去重机制 |
| 语义搜索 | 基于 Qwen 多模态 Embedding API 的智能搜索 |
| 分享链接 | 生成分享链接、密码保护、过期时间、下载次数限制 |
| 存储配额 | 用户存储空间管理和限制 |
| Web 界面 | 单页应用前端，支持文件管理操作 |

---

## 技术栈

### 后端框架

```
Gin Web Framework (HTTP 路由 + 中间件)
    ↓
GORM (ORM 数据库操作)
    ↓
SQLite (数据库 - 使用纯 Go 驱动)
```

### 核心依赖

| 依赖包 | 用途 | 说明 |
|-------|------|------|
| `github.com/gin-gonic/gin` | Web 框架 | 轻量级 HTTP 框架 |
| `gorm.io/gorm` | ORM | 数据库操作抽象层 |
| `github.com/glebarez/sqlite` | SQLite 驱动 | 纯 Go 实现，无需 CGO |
| `github.com/golang-jwt/jwt` | JWT 认证 | Token 生成和验证 |
| `golang.org/x/crypto/bcrypt` | 密码加密 | bcrypt 哈希算法 |
| `github.com/spf13/viper` | 配置管理 | YAML 配置文件加载 |
| `github.com/philippgille/chromem-go` | 向量数据库 | 本地向量存储，用于语义搜索 |
| `github.com/ledongthuc/pdf` | PDF 解析 | 提取 PDF 文本内容 |

---

## Go 语言基础讲解

### 1. 什么是 Go 语言？

Go 是 Google 开发的一种静态强类型、编译型、并发型编程语言。

**特点**：
- **静态类型**：变量类型在编译时确定
- **编译型**：代码编译成机器码后执行
- **垃圾回收**：自动管理内存
- **原生并发**：通过 goroutine 和 channel 实现并发

### 2. 基础语法讲解

#### 变量声明

```go
// 方式一：完整声明
var username string = "admin"

// 方式二：类型推导
var email = "admin@example.com"

// 方式三：短变量声明（函数内常用）
fileSize := 1024

// 常量
const MaxFileSize = 1073741824  // 1GB
```

#### 函数定义

```go
// 基本函数
func Add(a, b int) int {
    return a + b
}

// 多返回值（Go 语言特色）
func Divide(a, b int) (int, error) {
    if b == 0 {
        return 0, errors.New("division by zero")
    }
    return a / b, nil
}

// 命名返回值
func Calculate() (result int, err error) {
    result = 100
    return  // 自动返回 result, err
}
```

#### 结构体（Struct）

```go
// 定义结构体
type User struct {
    ID           uint      `gorm:"primaryKey" json:"id"`
    Username     string    `gorm:"uniqueIndex" json:"username"`
    PasswordHash string    `json:"-"`  // "-" 表示不序列化到 JSON
    CreatedAt    time.Time `json:"created_at"`
}

// 方法（接收者函数）
func (u *User) CanUpload(fileSize int64) bool {
    return u.StorageUsed+fileSize <= u.StorageLimit
}
```

**Struct Tag 解释**：
- `` `gorm:"primaryKey"` ``：告诉 GORM 这是主键
- `` `json:"id"` ``：JSON 序列化时的字段名
- `` `json:"-"` ``：JSON 序列化时忽略此字段

#### 接口（Interface）

```go
// 定义接口
type EmbeddingService interface {
    CreateFileEmbedding(fileID uint) error
    SearchByEmbedding(userID uint, query string, limit int) (*SearchResult, error)
}

// 实现接口（无需显式声明）
type embeddingServiceImpl struct {
    db  *gorm.DB
    cfg *config.Config
}

// embeddingServiceImpl 自动实现了 EmbeddingService 接口
```

#### 错误处理

```go
// Go 推荐显式处理错误
file, err := os.Open("test.txt")
if err != nil {
    return fmt.Errorf("failed to open file: %w", err)
}
defer file.Close()  // 延迟执行，函数返回时关闭文件

// 自定义错误
var (
    ErrUserNotFound = errors.New("user not found")
    ErrInvalidAuth  = errors.New("invalid authentication")
)
```

#### 并发（Goroutine + Channel）

```go
// 启动 goroutine（并发执行）
go h.embSvc.CreateFileEmbedding(fileRecord.ID)

// 带缓冲的 channel
taskQueue := make(chan embeddingTask, 500)

// 生产者
taskQueue <- embeddingTask{fileID: 123}

// 消费者
go func() {
    for task := range taskQueue {
        // 处理任务
        s.CreateFileEmbedding(task.fileID)
    }
}()
```

#### 指针

```go
// 值类型：复制数据
func modifyValue(u User) {
    u.Username = "modified"  // 不会影响原始值
}

// 指针类型：传递引用
func modifyPointer(u *User) {
    u.Username = "modified"  // 会修改原始值
}

// 使用 new 分配内存，返回指针
userPtr := new(User)
// 等价于
userPtr := &User{}
```

---

## 项目架构设计

### 整体架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                         客户端 (Browser)                        │
│                    webpage/index.html (SPA)                     │
└──────────────────────────────┬──────────────────────────────────┘
                               │ HTTP/JSON
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│                      Handler 层 (HTTP 处理)                     │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────────┐  │
│  │AuthHandler│FileHandler│ShareHandler│PageHandler│AdminHandler│  │
│  └──────────┴──────────┴──────────┴──────────┴──────────────┘  │
└──────────────────────────────┬──────────────────────────────────┘
                               │ 调用
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│                     Middleware (中间件)                         │
│  ┌──────────────┬──────────────┬──────────────────────────────┐│
│  │AuthMiddleware│CORS Middleware│Logger Middleware            ││
│  │JWT 认证       │跨域处理        │请求日志                     ││
│  └──────────────┴──────────────┴──────────────────────────────┘│
└──────────────────────────────┬──────────────────────────────────┘
                               │ 调用
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│                      Service 层 (业务逻辑)                       │
│  ┌──────────┬──────────┬──────────┬──────────┬──────────────┐  │
│  │AuthService│FileService│ChunkService│StorageService│Embedding│  │
│  └──────────┴──────────┴──────────┴──────────┴──────────────┘  │
└──────────────────────────────┬──────────────────────────────────┘
                               │ 操作
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│                      Model 层 (数据模型)                         │
│  ┌──────────┬──────────┬──────────┬──────────────────────────┐ │
│  │   User   │   File   │   Share  │   FileChunk              │ │
│  └──────────┴──────────┴──────────┴──────────────────────────┘ │
└──────────────────────────────┬──────────────────────────────────┘
                               │ 持久化
                               ↓
┌─────────────────────────────────────────────────────────────────┐
│                       数据存储层                                 │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────┐  │
│  │  SQLite Database │  │  File System     │  │Chromem DB    │  │
│  │  godisk.db       │  │  ./uploads/      │  │Vector Store  │  │
│  └──────────────────┘  └──────────────────┘  └──────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### 分层架构详解

#### 1. Handler 层（HTTP 处理层）

**职责**：
- 接收 HTTP 请求
- 解析请求参数（JSON、Form、URL 参数）
- 参数校验
- 调用 Service 层处理业务
- 统一响应格式

**示例代码分析**（`internal/handler/file.go:40-75`）：

```go
func (h *FileHandler) UploadFile(c *gin.Context) {
    // 1. 从中间件获取用户 ID
    userID := middleware.MustGetUserID(c)

    // 2. 解析表单参数
    fileHeader, err := c.FormFile("file")
    if err != nil {
        response.BadRequest(c, "file is required")
        return
    }

    // 3. 调用 Service 层处理业务
    fileRecord, err := h.fileSvc.UploadFile(userID, fileHeader.Filename, ...)
    if err != nil {
        response.UploadFailed(c, err.Error())
        return
    }

    // 4. 异步生成语义向量（不阻塞响应）
    go h.embSvc.CreateFileEmbedding(fileRecord.ID)

    // 5. 返回统一格式响应
    response.Success(c, fileRecord)
}
```

#### 2. Middleware 层（中间件层）

**职责**：
- 全局请求处理
- 身份认证
- 跨域处理
- 日志记录

**JWT 认证流程**（`internal/middleware/auth.go:13-54`）：

```go
func AuthMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // 1. 提取 Token（支持 Header 和 URL 参数）
        authHeader := c.GetHeader("Authorization")
        if authHeader != "" {
            parts := strings.SplitN(authHeader, " ", 2)
            if len(parts) == 2 && parts[0] == "Bearer" {
                tokenString = parts[1]
            }
        }

        // 2. 验证 Token
        claims, err := jwt.ParseToken(tokenString, cfg.JWT.Secret)
        if err != nil {
            response.Unauthorized(c, "invalid or expired token")
            c.Abort()  // 终止请求处理
            return
        }

        // 3. 将用户信息存入 Context，供后续 Handler 使用
        c.Set("user_id", claims.UserID)
        c.Set("username", claims.Username)

        c.Next()  // 继续处理请求
    }
}
```

#### 3. Service 层（业务逻辑层）

**职责**：
- 核心业务逻辑
- 数据校验
- 事务处理
- 调用 Storage/Model 层

**文件上传服务示例**（`internal/service/file.go:50-120`）：

```go
func (s *FileService) UploadFile(...) (*model.File, error) {
    // 1. 检查用户存储空间
    var user model.User
    if err := s.db.First(&user, userID).Error; err != nil {
        return nil, fmt.Errorf("failed to get user: %w", err)
    }

    if !user.CanUpload(fileSize) {
        return nil, errors.New("storage limit exceeded")
    }

    // 2. 计算文件哈希（用于去重）
    fileHash, err := hash.CalculateReaderSHA256(file)

    // 3. 保存文件（带去重逻辑）
    filePath, savedSize, err := s.storageSvc.SaveFile(file, fileHash)

    // 4. 创建数据库记录
    fileRecord := &model.File{
        UserID:   userID,
        FileName: fileName,
        FilePath: filePath,
        FileHash: fileHash,
        FileSize: savedSize,
    }

    if err := s.db.Create(fileRecord).Error; err != nil {
        // 创建失败，回滚：删除已保存的文件
        s.storageSvc.DeleteFile(filePath)
        return nil, fmt.Errorf("failed to create file record: %w", err)
    }

    // 5. 更新用户存储使用量
    user.AddStorage(savedSize)
    s.db.Save(&user)

    return fileRecord, nil
}
```

#### 4. Model 层（数据模型层）

**职责**：
- 数据库表结构定义
- 数据库连接管理
- 数据库迁移

---

## 核心模块详解

### 模块一：用户认证系统

#### JWT 认证流程图

```
┌─────────┐                  ┌─────────┐                  ┌─────────┐
│  Client │                  │ Server  │                  │ Database│
└────┬────┘                  └────┬────┘                  └────┬────┘
     │                            │                            │
     │ POST /api/auth/login       │                            │
     │ {username, password}       │                            │
     ├───────────────────────────>│                            │
     │                            │ SELECT * FROM users        │
     │                            │ WHERE username = ?         │
     │                            ├───────────────────────────>│
     │                            │ user record               │
     │                            │<───────────────────────────┤
     │                            │                            │
     │                            │ bcrypt.CompareHashPassword │
     │                            │ verify password            │
     │                            │                            │
     │  {token, username, email}  │                            │
     │<───────────────────────────┤                            │
     │                            │                            │
     │  Store token in localStorage                             │
     │                            │                            │
     │                            │                            │
     │ GET /api/files/list        │                            │
     │ Authorization: Bearer <token>                            │
     ├───────────────────────────>│                            │
     │                            │ Parse and verify token     │
     │                            │ Extract user_id            │
     │                            │                            │
     │  files list                │                            │
     │<───────────────────────────┤                            │
```

#### JWT 实现（`internal/pkg/jwt/jwt.go`）

```go
// Claims 定义 JWT 载荷
type Claims struct {
    UserID   uint   `json:"user_id"`
    Username string `json:"username"`
    jwt.RegisteredClaims  // 内置标准字段（过期时间、签发时间等）
}

// 生成 Token
func GenerateToken(userID uint, username string, secret string, expireHours int) (string, error) {
    claims := Claims{
        UserID:   userID,
        Username: username,
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireHours) * time.Hour)),
            IssuedAt:  jwt.NewNumericDate(time.Now()),
            NotBefore: jwt.NewNumericDate(time.Now()),
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString([]byte(secret))
}

// 解析 Token
func ParseToken(tokenString string, secret string) (*Claims, error) {
    token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
        return []byte(secret), nil
    })

    if claims, ok := token.Claims.(*Claims); ok && token.Valid {
        return claims, nil
    }
    return nil, errors.New("invalid token")
}
```

**关键点**：
- JWT 由三部分组成：Header.Payload.Signature
- 使用 HS256 算法签名
- Token 包含用户 ID 和用户名
- 设置过期时间（默认 7 天）

---

### 模块二：文件去重系统

#### 去重原理

```
┌─────────────────────────────────────────────────────────────────┐
│                     文件去重流程                                 │
└─────────────────────────────────────────────────────────────────┘

用户A 上传 file.pdf (SHA256: abc123...)
    │
    ├─> 保存到 ./uploads/abc123...
    ├─> 创建数据库记录 (user_id=A, hash=abc123...)
    └─> storage_used += 1MB

用户B 上传相同文件 file.pdf (SHA256: abc123...)
    │
    ├─> 计算哈希：abc123...
    ├─> 检查数据库：hash=abc123... 是否存在？
    │       └─> 是！返回已有文件路径
    ├─> 创建数据库记录 (user_id=B, hash=abc123..., path=同一文件)
    └─> storage_used += 1MB (只计费，不重复存储)

结果：两个用户共享同一个物理文件，节省存储空间
```

#### 代码实现（`internal/service/storage.go:25-67`）

```go
func (s *StorageService) SaveFile(file io.Reader, fileHash string) (string, int64, error) {
    // 1. 检查是否已存在相同哈希的文件
    var existingFile model.File
    if err := s.db.Where("file_hash = ?", fileHash).First(&existingFile).Error; err == nil {
        // 文件已存在，直接返回现有文件路径（去重核心）
        return existingFile.FilePath, existingFile.FileSize, nil
    }

    // 2. 生成存储路径（使用哈希作为文件名）
    filePath := filepath.Join(s.cfg.Storage.UploadPath, fileHash)

    // 3. 同时写入文件和计算哈希（确保一致性）
    dst, err := os.Create(filePath)
    hasher := sha256.New()
    writer := io.MultiWriter(dst, hasher)

    size, err := io.Copy(writer, file)

    // 4. 验证哈希（防止传输错误）
    calculatedHash := hex.EncodeToString(hasher.Sum(nil))
    if calculatedHash != fileHash {
        os.Remove(filePath)  // 哈希不匹配，清理文件
        return "", 0, fmt.Errorf("file hash mismatch")
    }

    return filePath, size, nil
}
```

---

### 模块三：分片上传系统

#### 分片上传流程

```
┌─────────────────────────────────────────────────────────────────┐
│                    分片上传完整流程                              │
└─────────────────────────────────────────────────────────────────┘

阶段 1: 初始化上传
Client                        Server
  │                             │
  │ POST /upload/chunk/init     │
  │ {file_name, file_size,      │
  │  file_hash, chunk_size}     │
  ├────────────────────────────>│
  │                             │ 1. 检查存储空间
  │                             │ 2. 计算分片数量
  │                             │ 3. 生成 upload_id
  │                             │ 4. 创建 file_chunks 记录
  │                             │
  │<────────────────────────────┤
  │ {upload_id, total_chunks,   │
  │  chunk_size, uploaded_chunks}│
  │                             │

阶段 2: 上传分片（可并发）
Client                        Server
  │                             │
  │ POST /upload/chunk          │
  │ {upload_id, chunk_index,    │
  │  chunk_file}                │
  ├────────────────────────────>│
  │                             │ 1. 保存分片到 ./chunks/
  │                             │ 2. 更新 uploaded_chunks JSON
  │                             │
  │<────────────────────────────┤
  │ success                     │
  │                             │
  │ [重复上传其他分片...]        │

阶段 3: 完成上传
Client                        Server
  │                             │
  │ POST /upload/chunk/complete │
  │ {upload_id, parent_id}      │
  ├────────────────────────────>│
  │                             │ 1. 验证所有分片已上传
  │                             │ 2. 合并分片 → ./uploads/<hash>
  │                             │ 3. 验证最终文件哈希
  │                             │ 4. 创建 File 记录
  │                             │ 5. 清理临时分片文件
  │                             │
  │<────────────────────────────┤
  │ {file_record}               │
```

#### 分片数据结构（`internal/model/file.go:31-92`）

```go
type FileChunk struct {
    ID             uint      `gorm:"primaryKey"`
    UserID         uint      `gorm:"not null;index"`
    UploadID       string    `gorm:"size:100;not null;index"`
    FileName       string    `gorm:"size:255;not null"`
    FileHash       string    `gorm:"size:64"`
    TotalSize      int64     `gorm:"default:0"`
    ChunkSize      int       `gorm:"default:0"`
    UploadedChunks string    `gorm:"type:text"`  // JSON 数组: [0,2,3,5]
    Status         string    `gorm:"size:20;default:pending"`
}

// 获取已上传的分片列表
func (fc *FileChunk) GetUploadedChunks() ([]int, error) {
    var chunks []int
    if err := json.Unmarshal([]byte(fc.UploadedChunks), &chunks); err != nil {
        return nil, err
    }
    return chunks, nil
}

// 添加已上传的分片
func (fc *FileChunk) AddUploadedChunk(chunkIndex int) error {
    chunks, err := fc.GetUploadedChunks()
    if err != nil {
        return err
    }

    // 检查是否已存在（幂等性）
    for _, c := range chunks {
        if c == chunkIndex {
            return nil
        }
    }

    chunks = append(chunks, chunkIndex)
    return fc.SetUploadedChunks(chunks)
}
```

---

### 模块四：语义搜索系统

#### 架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                    语义搜索架构                                  │
└─────────────────────────────────────────────────────────────────┘

文件上传时：
┌────────────┐     ┌────────────┐     ┌────────────┐
│   文件     │────>│ 内容解析   │────>│ Qwen API   │
│ file.pdf   │     │ PDF/文本   │     │ Embedding  │
└────────────┘     └────────────┘     └────────────┘
                                              │
                                              ↓
┌────────────┐     ┌────────────┐     ┌────────────┐
│  Chromem   │<────│ 向量存储   │<────│ 1024维向量 │
│  Vector DB │     │            │     │            │
└────────────┘     └────────────┘     └────────────┘

搜索时：
┌────────────┐     ┌────────────┐     ┌────────────┐
│ 用户查询   │────>│ Qwen API   │────>│ 查询向量   │
│ "项目文档" │     │ Embedding  │     │            │
└────────────┘     └────────────┘     └────────────┘
                                              │
                                              ↓
┌────────────┐     ┌────────────┐     ┌────────────┐
│ 返回结果   │<────│ 余弦相似度 │<────│ 向量检索   │
│            │     │ 排序       │     │            │
└────────────┘     └────────────┘     └────────────┘
```

#### 多模态内容解析（`internal/service/embedding.go:259-324`）

```go
func (s *embeddingServiceImpl) ParseFileContent(filePath, fileName string) ([]QwenContent, string, error) {
    mtype, err := mimetype.DetectFile(filePath)

    // 1. 图片文件
    if strings.HasPrefix(mimeStr, "image/") {
        imgBytes, _ := os.ReadFile(filePath)
        imgBase64 := base64.StdEncoding.EncodeToString(imgBytes)
        dataURI := fmt.Sprintf("data:%s;base64,%s", mimeStr, imgBase64)

        // 多模态：图片 + 文本融合向量
        return []QwenContent{
            {
                "image": dataURI,
                "text":  "图片文件名: " + fileName,
            },
        }, "图片", nil
    }

    // 2. PDF 文件
    if ext == ".pdf" {
        text, err := extractPDFText(filePath)
        return []QwenContent{{"text": text}}, "PDF文档", nil
    }

    // 3. 代码/文本文件
    if isCodeFile(ext) {
        data, _ := os.ReadFile(filePath)
        content := fmt.Sprintf("以下是%s代码文件内容：\n%s", ext, string(data))
        return []QwenContent{{"text": content}}, "代码", nil
    }

    // 4. 其他二进制文件
    return []QwenContent{{"text": "文件名称: " + fileName}}, "其他", nil
}
```

#### 异步 Worker Pool 设计（`internal/service/embedding.go:88-121`）

```go
// 启动固定数量的 worker，限制并发度为 2
func (s *embeddingServiceImpl) startWorkerPool() {
    concurrency := 2
    for i := 0; i < concurrency; i++ {
        go func(workerID int) {
            for task := range s.taskQueue {
                if task.isDir {
                    s.CreateFolderEmbedding(task.fileID, task.userID, task.folderSummary)
                } else {
                    s.CreateFileEmbedding(task.fileID)
                }
                // 防止 API 限流
                time.Sleep(500 * time.Millisecond)
            }
        }(i)
    }
}

// 异步提交任务
func (s *embeddingServiceImpl) AsyncEmbeddingTask(...) {
    select {
    case s.taskQueue <- embeddingTask{...}:
        // 成功加入队列
    default:
        // 队列满了，使用 goroutine 强行执行
        go func() {
            if isDir {
                s.CreateFolderEmbedding(...)
            } else {
                s.CreateFileEmbedding(...)
            }
        }()
    }
}
```

---

### 模块五：分享链接系统

#### 分享链接安全设计

```
┌─────────────────────────────────────────────────────────────────┐
│                    分享链接安全机制                              │
└─────────────────────────────────────────────────────────────────┘

创建分享：
┌────────────┐
│ File ID    │
│ Password   │──> bcrypt.Hash() ──> 存储哈希到数据库
│ ExpireDays │
│ Limit      │
└────────────┘
        │
        ↓
┌─────────────────────────────────────┐
│ 生成 12 位随机 Share Code           │
│ crypto/rand.Read() + hex.Encode()  │
│ 例：a3f5c8d9e2b1                     │
└─────────────────────────────────────┘
        │
        ↓
分享 URL: https://godisk.com/s/a3f5c8d9e2b1

访问控制检查：
┌─────────────────────────────────────────────────────────────┐
│ 1. 检查过期时间                                               │
│    if expire_at != nil && now.After(expire_at) → 拒绝       │
│                                                              │
│ 2. 检查下载次数                                               │
│    if download_limit != -1 && count >= limit → 拒绝         │
│                                                              │
│ 3. 验证访问密码                                               │
│    bcrypt.CompareHashAndPassword(hash, password)            │
│                                                              │
│ 4. 下载后增加计数                                             │
│    download_count++                                          │
└─────────────────────────────────────────────────────────────┘
```

#### 分享码生成（`internal/handler/share.go:336-343`）

```go
func generateShareCode() (string, error) {
    bytes := make([]byte, 6)  // 6 字节 = 12 位十六进制字符
    if _, err := rand.Read(bytes); err != nil {
        return "", err
    }
    return hex.EncodeToString(bytes), nil
}
```

**安全性**：
- 使用 `crypto/rand`（密码学安全的随机数）
- 6 字节 → 16^12 = 2.8×10^14 种组合
- 暴力破解几乎不可能

---

## 数据库设计

### ER 图

```
┌─────────────────────────────────────────────────────────────────┐
│                          users                                  │
├─────────────────────────────────────────────────────────────────┤
│ PK │ id           │ uint                                       │
│    │ username     │ varchar(50)  UNIQUE                        │
│    │ email        │ varchar(100) UNIQUE                        │
│    │ password_hash│ varchar(255)                                │
│    │ storage_used │ bigint      DEFAULT 0                      │
│    │ storage_limit│ bigint      DEFAULT 10737418240 (10GB)     │
│    │ created_at   │ timestamp                                   │
│    │ updated_at   │ timestamp                                   │
└─────────────────────────────────────────────────────────────────┘
                              │ 1:N
                              ↓
┌─────────────────────────────────────────────────────────────────┐
│                          files                                  │
├─────────────────────────────────────────────────────────────────┤
│ PK │ id           │ uint                                       │
│ FK │ user_id      │ uint                                       │
│ FK │ parent_id    │ uint       DEFAULT 0 (0=根目录)            │
│    │ file_name    │ varchar(255)                                │
│    │ file_path    │ varchar(500)                                │
│    │ file_hash    │ varchar(64)  INDEX (用于去重)               │
│    │ file_size    │ bigint                                     │
│    │ mime_type    │ varchar(100)                                │
│    │ is_directory │ boolean     DEFAULT false                  │
│    │ embedding_id │ varchar(100)                                │
│    │ created_at   │ timestamp                                  │
└─────────────────────────────────────────────────────────────────┘
                              │ 1:N
                              ↓ (自引用)
                    ┌─────────────────┐
                    │ parent_id 指向  │
                    │ 另一个 File ID  │
                    └─────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                       file_chunks                               │
├─────────────────────────────────────────────────────────────────┤
│ PK │ id             │ uint                                     │
│ FK │ user_id        │ uint                                     │
│    │ upload_id      │ varchar(100) INDEX                       │
│    │ file_name      │ varchar(255)                             │
│    │ file_hash      │ varchar(64)                              │
│    │ total_size     │ bigint                                   │
│    │ chunk_size     │ int                                      │
│    │ uploaded_chunks│ text       JSON数组 [0,1,3,5]           │
│    │ status         │ varchar(20)  pending/uploading/...      │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                         shares                                  │
├─────────────────────────────────────────────────────────────────┤
│ PK │ id             │ uint                                     │
│ FK │ user_id        │ uint                                     │
│ FK │ file_id        │ uint                                     │
│    │ share_code     │ varchar(50)  UNIQUE INDEX                │
│    │ access_password│ varchar(255) (bcrypt 哈希)               │
│    │ download_limit │ int         DEFAULT -1 (无限)            │
│    │ download_count │ int         DEFAULT 0                    │
│    │ expire_at      │ timestamp  (NULL=永不过期)               │
└─────────────────────────────────────────────────────────────────┘
```

### 表关系说明

| 关系 | 说明 | 外键 |
|------|------|------|
| users → files | 一对多 | files.user_id |
| files → files | 自引用（父子关系） | files.parent_id |
| users → shares | 一对多 | shares.user_id |
| files → shares | 一对多 | shares.file_id |
| users → file_chunks | 一对多 | file_chunks.user_id |

---

## API 接口设计

### 公开接口（无需认证）

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/auth/register | 用户注册 |
| POST | /api/auth/login | 用户登录 |
| GET | /health | 健康检查 |
| GET | /s/:code | 访问分享页面 |
| GET | /s/:code/download | 下载分享文件 |

### 认证接口（需要 Bearer Token）

#### 用户管理

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | /api/auth/me | 获取当前用户信息 |
| PUT | /api/auth/profile | 更新用户信息 |
| POST | /api/auth/change-password | 修改密码 |

#### 文件管理

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/files/upload | 单文件上传 |
| POST | /api/files/upload/chunk/init | 初始化分片上传 |
| POST | /api/files/upload/chunk | 上传分片 |
| POST | /api/files/upload/chunk/complete | 完成分片上传 |
| GET | /api/files/list | 列出文件 |
| GET | /api/files/download/:id | 下载文件 |
| DELETE | /api/files/:id | 删除文件 |
| POST | /api/files/folder | 创建文件夹 |
| PUT | /api/files/move | 移动文件 |
| PUT | /api/files/rename | 重命名文件 |
| GET | /api/files/search | 搜索文件 |
| POST | /api/files/:id/build_index | 手动构建语义索引 |

#### 分享管理

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/shares/create | 创建分享 |
| GET | /api/shares | 获取分享列表 |
| DELETE | /api/shares/:id | 取消分享 |

#### 管理员

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/admin/regenerate-embeddings | 重建所有索引 |
| GET | /api/admin/embedding-status | 索引状态 |

---

## 面试题与解答

### 一、基础理论题

#### Q1: Go 语言与 Java/Python 的区别是什么？

| 特性 | Go | Java | Python |
|------|-----|------|--------|
| 类型系统 | 静态类型 | 静态类型 | 动态类型 |
| 编译方式 | 编译型 | 编译型（JVM） | 解释型 |
| 并发模型 | goroutine + channel | 线程 + 锁 | 线程 + 锁 |
| 执行速度 | 快 | 中等 | 慢 |
| 学习曲线 | 中等 | 陡峭 | 平缓 |
| 典型应用 | 微服务、云原生 | 企业应用 | 数据科学、脚本 |

#### Q2: 什么是 goroutine？与线程有什么区别？

**goroutine** 是 Go 语言的轻量级线程。

**区别**：
- **内存占用**：goroutine 约 2KB，线程约 1MB
- **调度方式**：goroutine 由 Go runtime 调度（M:N 调度），线程由 OS 调度
- **创建成本**：goroutine 创建极快，线程创建较慢
- **切换成本**：goroutine 切换只需保存 3 个寄存器，线程需要保存完整上下文

**项目中的应用**（`internal/handler/file.go:72`）：
```go
// 异步生成 embedding，不阻塞响应
go h.embSvc.CreateFileEmbedding(fileRecord.ID)
```

#### Q3: channel 是什么？如何使用？

**channel** 是 goroutine 之间通信的管道，遵循 "CSP"（通信顺序进程）模型。

**基本操作**：
```go
// 创建 channel
ch := make(chan int)       // 无缓冲 channel
ch := make(chan int, 10)   // 带缓冲 channel

// 发送数据
ch <- 42

// 接收数据
value := <-ch

// 关闭 channel
close(ch)

// 遍历 channel
for value := range ch {
    fmt.Println(value)
}
```

**项目中的应用**（`internal/service/embedding.go:79`）：
```go
type embeddingServiceImpl struct {
    taskQueue  chan embeddingTask  // 任务队列
}

// 生产者
func (s *embeddingServiceImpl) AsyncEmbeddingTask(...) {
    s.taskQueue <- embeddingTask{...}
}

// 消费者（worker）
go func() {
    for task := range s.taskQueue {
        s.CreateFileEmbedding(task.fileID)
    }
}()
```

#### Q4: defer 的作用是什么？

**defer** 用于延迟函数执行，直到所在函数返回时执行。

**特点**：
- 多个 defer 按 LIFO 顺序执行
- 常用于资源清理（关闭文件、解锁互斥锁）

**项目中的应用**（`internal/service/file.go:62-64`）：
```go
file, err := fileHeader.Open()
if err != nil {
    return err
}
defer file.Close()  // 函数返回时自动关闭文件
```

---

### 二、项目架构题

#### Q5: 为什么要采用分层架构？各层的职责是什么？

**分层的好处**：
1. **职责分离**：每层专注自己的职责
2. **易于测试**：可以独立测试每一层
3. **易于维护**：修改一层不影响其他层
4. **代码复用**：Service 层可被多个 Handler 调用

**各层职责**：

| 层 | 职责 | 不应该做 |
|----|------|----------|
| Handler | HTTP 请求处理、参数解析、响应格式化 | 业务逻辑、数据库操作 |
| Service | 业务逻辑、事务处理、调用 Model/Storage | HTTP 处理、数据持久化 |
| Model | 数据库表定义、CRUD 基础操作 | 业务逻辑、HTTP 处理 |

#### Q6: 什么是依赖注入？项目中如何使用？

**依赖注入（DI）** 是一种设计模式，通过构造函数或 setter 方法将依赖传递给对象。

**好处**：
- 降低耦合度
- 便于单元测试（可注入 mock 对象）
- 便于理解依赖关系

**项目中的应用**（`cmd/server/main.go:32-37`）：
```go
// 1. 创建服务实例，注入依赖
storageSvc := service.NewStorageService(model.DB, cfg)
fileSvc := service.NewFileService(model.DB, cfg, storageSvc)
chunkSvc := service.NewChunkService(model.DB, cfg, storageSvc, fileSvc)

// 2. 创建 Handler，注入服务
fileHandler := handler.NewFileHandler(fileSvc, chunkSvc, embSvc)
```

**构造函数示例**（`internal/service/file.go:24-30`）：
```go
func NewFileService(db *gorm.DB, cfg *config.Config, storageSvc *StorageService) *FileService {
    return &FileService{
        db:         db,
        cfg:        cfg,
        storageSvc: storageSvc,  // 依赖注入
    }
}
```

---

### 三、技术实现题

#### Q7: JWT 认证是如何实现的？

**完整流程**：

```
1. 用户登录 → 验证密码 → 生成 Token
2. 客户端存储 Token（localStorage/cookie）
3. 后续请求携带 Token（Authorization: Bearer <token>）
4. 服务器中间件验证 Token → 提取用户信息
5. Handler 从 Context 获取用户 ID
```

**Token 生成**（`internal/pkg/jwt/jwt.go:16-30`）：
```go
func GenerateToken(userID uint, username string, secret string, expireHours int) (string, error) {
    claims := Claims{
        UserID:   userID,
        Username: username,
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireHours) * time.Hour)),
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString([]byte(secret))
}
```

**Token 验证**（`internal/middleware/auth.go:40-46`）：
```go
claims, err := jwt.ParseToken(tokenString, cfg.JWT.Secret)
if err != nil {
    response.Unauthorized(c, "invalid or expired token")
    c.Abort()
    return
}

// 存储到 Context
c.Set("user_id", claims.UserID)
c.Set("username", claims.Username)
```

#### Q8: 文件去重是如何实现的？

**核心思想**：相同内容的文件具有相同的 SHA256 哈希值。

**实现步骤**：

1. **计算文件哈希**
```go
func CalculateReaderSHA256(reader io.Reader) (string, error) {
    hash := sha256.New()
    if _, err := io.Copy(hash, reader); err != nil {
        return "", err
    }
    return hex.EncodeToString(hash.Sum(nil)), nil
}
```

2. **检查是否已存在**
```go
var existingFile model.File
if err := s.db.Where("file_hash = ?", fileHash).First(&existingFile).Error; err == nil {
    // 文件已存在，返回现有文件路径
    return existingFile.FilePath, existingFile.FileSize, nil
}
```

3. **新文件存储**
```go
filePath := filepath.Join(s.cfg.Storage.UploadPath, fileHash)
// 使用哈希作为文件名，确保相同文件存储在同一位置
```

#### Q9: 分片上传如何保证数据完整性？

**三重保障**：

1. **分片哈希验证**（可选）
```go
if chunkRecord.FileHash != "" {
    calculatedHash := hash.CalculateBytesSHA256(chunkData)
    // 验证分片哈希
}
```

2. **完整文件哈希验证**
```go
calculatedHash, err := hash.CalculateFileSHA256(filePath)
if calculatedHash != chunkRecord.FileHash {
    os.Remove(filePath)
    s.storageSvc.CleanChunks(uploadID)
    return nil, errors.New("file hash mismatch")
}
```

3. **分片计数验证**
```go
totalChunks := (chunkRecord.TotalSize + int64(chunkRecord.ChunkSize) - 1) / int64(chunkRecord.ChunkSize)
if len(uploadedChunks) != int(totalChunks) {
    return nil, errors.New("not all chunks uploaded")
}
```

#### Q10: 语义搜索是如何实现的？

**核心流程**：

```
1. 文件上传 → 解析内容（文本/PDF/图片）
2. 调用 Qwen API → 生成 1024 维向量
3. 存储到 Chromem 向量数据库
4. 搜索时：查询文本 → 向量 → 余弦相似度 → 返回最相似文件
```

**关键代码**（`internal/service/embedding.go:195-257`）：
```go
func (s *embeddingServiceImpl) GenerateMultimodalEmbedding(contents []QwenContent) ([]float32, error) {
    // 1. 构造 API 请求
    reqBody := QwenEmbeddingRequest{
        Model: s.cfg.Qwen.Model,
    }
    reqBody.Input.Contents = contents
    reqBody.Parameters.Dimension = 1024
    reqBody.Parameters.OutputType = "dense"

    // 2. 发送 HTTP 请求
    resp, err := http.DefaultClient.Do(req)

    // 3. 解析响应
    var qwenResp QwenEmbeddingResponse
    json.Unmarshal(body, &qwenResp)

    // 4. 转换为 float32（Chromem 要求）
    float64Vec := qwenResp.Output.Embeddings[0].Embedding
    float32Vec := make([]float32, len(float64Vec))
    for i, v := range float64Vec {
        float32Vec[i] = float32(v)
    }

    return float32Vec, nil
}
```

---

### 四、数据库题

#### Q11: GORM 的软删除是如何实现的？

**软删除**：记录不会真正删除，而是设置 `deleted_at` 字段。

**定义模型**（`internal/model/user.go:18`）：
```go
type User struct {
    // ...
    DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}
```

**GORM 自动处理**：
- `db.Delete(&user)` → 执行 `UPDATE users SET deleted_at = NOW() WHERE id = ?`
- `db.Find(&users)` → 自动添加 `WHERE deleted_at IS NULL`
- `db.Unscoped().Find(&users)` → 查询所有记录（包括已删除）

#### Q12: 如何防止 SQL 注入？

**GORM 自动防护**：
```go
// 安全：GORM 自动参数化查询
db.Where("username = ?", userInput).First(&user)

// 危险：直接拼接 SQL（不要这样做！）
db.Raw("SELECT * FROM users WHERE username = '" + userInput + "'")
```

**参数绑定**：
```go
// 使用 ? 占位符
db.Where("file_name LIKE ?", "%"+keyword+"%").Find(&files)

// 使用命名参数
db.Where("user_id = @uid AND parent_id = @pid",
    sql.Named("uid", userID),
    sql.Named("pid", parentID)).Find(&files)
```

---

### 五、系统设计题

#### Q13: 如何设计一个支持断点续传的文件上传？

**核心思路**：记录已上传的分片，支持重新上传失败的分片。

**数据结构**：
```go
type FileChunk struct {
    UploadID       string
    UploadedChunks string  // JSON: [0,1,3,5]
}
```

**断点续传流程**：

```
1. 客户端初始化上传 → 获取 upload_id 和已上传分片列表
2. 客户端检查本地分片 → 找出未上传的分片
3. 重新上传未完成的分片
4. 服务器检查分片是否已上传（幂等性）
5. 完成上传
```

**幂等性保证**（`internal/service/chunk.go:117-128`）：
```go
// 检查分片是否已上传
uploadedChunks, err := chunkRecord.GetUploadedChunks()
for _, c := range uploadedChunks {
    if c == chunkIndex {
        return nil  // 已上传，直接返回
    }
}

// 添加新分片
chunkRecord.AddUploadedChunk(chunkIndex)
```

#### Q14: 如何处理大文件的内存占用？

**流式处理**：

```go
// 1. 流式读取（不一次性加载到内存）
file, _ := os.Open("large_file.bin")
defer file.Close()

// 2. 分块处理
buffer := make([]byte, 32*1024)  // 32KB 缓冲区
for {
    n, err := file.Read(buffer)
    if n > 0 {
        hasher.Write(buffer[:n])
    }
    if err == io.EOF {
        break
    }
}

// 3. io.MultiWriter 同时写入多个目标
dst, _ := os.Create("output.bin")
hasher := sha256.New()
writer := io.MultiWriter(dst, hasher)
io.Copy(writer, srcFile)
```

#### Q15: 如何实现文件夹的递归删除？

**递归算法**（`internal/service/file.go:256-285`）：

```go
func (s *FileService) deleteFolderRecursive(folder *model.File) error {
    // 1. 查找所有子文件
    var children []*model.File
    s.db.Where("parent_id = ?", folder.ID).Find(&children)

    // 2. 递归删除子文件
    for _, child := range children {
        if child.IsDirectory {
            // 递归删除子文件夹
            s.deleteFolderRecursive(child)
        } else {
            // 删除物理文件
            s.storageSvc.DeleteFile(child.FilePath)
            // 更新存储配额
            user.RemoveStorage(child.FileSize)
            s.db.Save(&user)
        }
        // 删除数据库记录
        s.db.Delete(child)
    }

    return nil
}
```

---

### 六、并发安全题

#### Q16: map 并发访问会 panic，如何解决？

**问题代码**：
```go
var m = make(map[string]int)
go func() { m["key"] = 1 }()     // 写
go func() { _ = m["key"] }()     // 读
// panic: concurrent map read and map write
```

**解决方案**：

**方案一：使用 sync.Mutex**
```go
var (
    m     = make(map[string]int)
    mu    sync.Mutex
)

func write(key string, value int) {
    mu.Lock()
    m[key] = value
    mu.Unlock()
}

func read(key string) int {
    mu.Lock()
    defer mu.Unlock()
    return m[key]
}
```

**方案二：使用 sync.Map**（适合读多写少）
```go
var m sync.Map

m.Store("key", 123)    // 写
value, _ := m.Load("key")  // 读
```

#### Q17: 如何实现一个 worker pool？

**标准实现**（项目中的应用）：

```go
// 1. 定义任务结构
type Task struct {
    FileID uint
    UserID uint
}

// 2. 创建 worker pool
func StartWorkerPool(numWorkers int, taskQueue chan Task) {
    for i := 0; i < numWorkers; i++ {
        go func(workerID int) {
            for task := range taskQueue {
                // 处理任务
                processTask(task)
            }
        }(i)
    }
}

// 3. 使用
taskQueue := make(chan Task, 100)
StartWorkerPool(5, taskQueue)

// 提交任务
taskQueue <- Task{FileID: 1, UserID: 1}
```

---

### 七、性能优化题

#### Q18: 如何优化大量文件的查询性能？

**优化措施**：

1. **添加索引**
```go
type File struct {
    UserID   uint `gorm:"index"`
    ParentID uint `gorm:"index"`
    FileHash string `gorm:"size:64;index"`
}
```

2. **使用分页**
```go
offset := (page - 1) * size
db.Limit(size).Offset(offset).Find(&files)
```

3. **只查询需要的字段**
```go
db.Select("id", "file_name", "file_size").Find(&files)
```

4. **使用 Preload 预加载关联数据**
```go
db.Preload("File").Find(&shares)
```

#### Q19: 如何减少数据库查询次数？

**策略**：

1. **批量查询**
```go
// N+1 问题
for _, share := range shares {
    db.First(&share.File, share.FileID)  // N 次查询
}

// 优化：一次查询
var fileIDs []uint
for _, share := range shares {
    fileIDs = append(fileIDs, share.FileID)
}
var files []File
db.Where("id IN ?", fileIDs).Find(&files)
```

2. **使用 Join**
```go
db.Joins("File").Find(&shares)
```

3. **使用缓存**
```go
// 使用 go-cache 或 Redis
cache.Set("user_"+userID, user, 5*time.Minute)
```

---

### 八、安全题

#### Q20: 如何存储用户密码？

**安全存储流程**：

```go
// 1. 使用 bcrypt 加密（自动加盐）
hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)

// 2. 存储哈希到数据库
user.PasswordHash = string(hashedPassword)

// 3. 验证密码
err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(inputPassword))
```

**为什么不用 MD5/SHA256**？
- MD5/SHA256 是"快速哈希"，容易被彩虹表攻击
- bcrypt 是"慢速哈希"，专门设计用于密码存储
- bcrypt 自动加盐，防止相同密码产生相同哈希

#### Q21: 如何防止 CSRF 攻击？

**项目中的措施**：

1. **使用 JWT 认证**（不依赖 Cookie）
```go
Authorization: Bearer <token>
```

2. **CORS 限制**
```go
c.Writer.Header().Set("Access-Control-Allow-Origin", "https://yourdomain.com")
```

3. **SameSite Cookie 属性**
```go
http.SetCookie(w, &http.Cookie{
    Name:     "session",
    SameSite: http.SameSiteStrictMode,
})
```

---

### 九、代码阅读题

#### Q22: 解释以下代码的含义

```go
db.Where("user_id = ? AND parent_id = ?", userID, parentID).
   Order("is_directory DESC, created_at DESC").
   Limit(size).
   Offset(offset).
   Find(&files)
```

**逐行解释**：

| 部分 | 含义 |
|------|------|
| `Where("user_id = ? AND parent_id = ?", userID, parentID)` | 查询指定用户、指定父文件夹的文件 |
| `Order("is_directory DESC, created_at DESC")` | 先按是否文件夹降序（文件夹在前），再按创建时间降序 |
| `Limit(size)` | 每页 size 条记录 |
| `Offset(offset)` | 跳过前 offset 条记录 |
| `Find(&files)` | 执行查询，将结果存入 files 变量 |

**生成的 SQL**：
```sql
SELECT * FROM files
WHERE user_id = ? AND parent_id = ?
ORDER BY is_directory DESC, created_at DESC
LIMIT ? OFFSET ?
```

---

### 十、实战题

#### Q23: 如何实现一个文件预览功能？

**实现方案**：

```go
// Handler 层
func (h *FileHandler) PreviewFile(c *gin.Context) {
    fileID := c.Param("id")

    // 获取文件信息
    var file model.File
    db.First(&file, fileID)

    // 根据文件类型处理
    switch {
    case strings.HasPrefix(file.MimeType, "image/"):
        // 图片：直接返回
        c.File(file.FilePath)

    case file.MimeType == "application/pdf":
        // PDF：返回文件或使用在线预览
        c.File(file.FilePath)

    case strings.HasPrefix(file.MimeType, "text/"):
        // 文本文件：读取内容返回
        content, _ := os.ReadFile(file.FilePath)
        c.String(200, string(content))

    case strings.HasPrefix(file.MimeType, "video/"):
        // 视频：支持 Range 请求（流式传输）
        serveVideo(c, file)

    default:
        c.JSON(200, gin.H{"error": "preview not supported"})
    }
}

// 视频流式传输
func serveVideo(c *gin.Context, file model.File) {
    f, _ := os.Open(file.FilePath)
    defer f.Close()

    fileInfo, _ := f.Stat()
    c.Header("Content-Type", file.MimeType)
    c.Header("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))
    c.Header("Accept-Ranges", "bytes")

    // 处理 Range 请求
    rangeHeader := c.GetHeader("Range")
    if rangeHeader != "" {
        // 解析并返回部分内容
        // ...
    }

    io.Copy(c.Writer, f)
}
```

#### Q24: 如何实现文件版本控制？

**数据表设计**：

```go
type FileVersion struct {
    ID         uint   `gorm:"primaryKey"`
    FileID     uint   `gorm:"index"`
    Version    int    `gorm:"default:1"`
    FilePath   string
    FileHash   string
    FileSize   int64
    CreatedAt  time.Time
    CreatedBy  uint
    Comment    string  // 版本备注
}
```

**实现逻辑**：

```go
// 保存新版本
func (s *FileService) SaveVersion(fileID uint, reader io.Reader, comment string) error {
    // 1. 获取当前文件
    var file model.File
    s.db.First(&file, fileID)

    // 2. 保存为新文件（去重逻辑）
    newHash, _ := hash.CalculateReaderSHA256(reader)
    newPath := s.storageSvc.SaveFile(reader, newHash)

    // 3. 创建版本记录
    version := &FileVersion{
        FileID:    fileID,
        Version:   s.getNextVersion(fileID),
        FilePath:  newPath,
        FileHash:  newHash,
        CreatedBy: file.UserID,
        Comment:   comment,
    }
    s.db.Create(version)

    // 4. 更新当前文件指针
    s.db.Model(&file).Updates(map[string]interface{}{
        "file_path": newPath,
        "file_hash": newHash,
    })

    return nil
}

// 恢复到指定版本
func (s *FileService) RestoreVersion(fileID, versionID uint) error {
    var version FileVersion
    s.db.First(&version, versionID)

    var file model.File
    s.db.First(&file, fileID)

    // 更新为旧版本的文件
    s.db.Model(&file).Updates(map[string]interface{}{
        "file_path": version.FilePath,
        "file_hash": version.FileHash,
    })

    return nil
}
```

---

## 总结

### 项目技术亮点

1. **Clean Architecture**：清晰的分层架构
2. **文件去重**：基于 SHA256 的智能去重
3. **分片上传**：支持大文件和断点续传
4. **语义搜索**：多模态 Embedding + 向量数据库
5. **异步处理**：Worker Pool 控制并发
6. **安全性**：JWT + bcrypt + 密码学随机数

### Go 语言核心知识点

| 知识点 | 项目中的应用 |
|--------|-------------|
| goroutine | 异步生成 embedding |
| channel | Worker Pool 任务队列 |
| defer | 文件关闭、资源清理 |
| interface | EmbeddingService 接口定义 |
| struct tag | GORM 标签、JSON 标签 |
| error handling | 多返回值错误处理 |
| pointer | 传递引用、避免值拷贝 |

### 面试准备建议

1. **熟悉项目整体架构**，能画出架构图
2. **理解每个模块的实现细节**
3. **能说出使用的技术栈及其原因**
4. **了解 Go 语言的特色特性**
5. **准备几个可以深入讲解的功能点**

祝面试顺利！
