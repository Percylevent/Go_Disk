# GoDisk 代码改进记录

> 基于代码审查发现的问题，逐项修复并记录。每项包含：问题场景、错误发生原理、修正方案及其原理、局限性与替代方案。

---

## 1. 分享下载密码通过 URL 明文传输

**涉及文件**：`internal/handler/share.go`

### 问题场景

GoDisk 的分享链接支持设置访问密码。当用户在前端输入密码后下载文件时，原代码的下载接口是这样验证密码的：

```go
// GET /s/:code/download?password=mypassword
password := c.Query("password")
bcrypt.CompareHashAndPassword([]byte(share.AccessPassword), []byte(password))
```

看起来功能正常 —— 密码通过 bcrypt 校验，安全吗？

### 错误发生原理

密码出现在 URL 的 Query String 中，会在多个环节被**明文记录**：

1. **浏览器历史记录**：用户在同一台电脑上打开浏览器历史就能看到 `?password=xxx`
2. **Web 服务器 Access Log**：Nginx/Apache 默认记录完整 URL，如：
   ```
   GET /s/a1b2c3/download?password=secret123 200 1048576 "-" "Chrome/120"
   ```
   运维人员、日志分析系统、SIEM 平台都能看到密码
3. **Referer 头泄露**：如果下载页面中加载了外部资源（如统计脚本），浏览器会将完整 URL（含密码）通过 `Referer` 头发送给第三方
4. **代理 / CDN 日志**：企业内网代理、Cloudflare 等 CDN 会记录完整请求 URL
5. **浏览器插件**：URL 栏中的内容可能被浏览器插件捕获

核心问题是：**HTTP 规范中 URL 从来不是传递敏感信息的安全通道**。POST Body 中的内容不会出现在上述任何日志中，这就是为什么登录表单都用 POST 而不是 GET。

### 修正方案及其原理

采用**短时效下载令牌（download_token）**机制，将流程分为两步：

**第一步：密码验证（POST，密码在 Body 中）**
```
POST /s/:code/verify
Body: {"password": "secret123"}

Response: {"download_token": "base64(shareCode:expiry:hmac_sig)"}
```

**第二步：下载（GET，只传 token）**
```
GET /s/:code/download?token=xxx
```

Token 的生成和验证使用 HMAC-SHA256 签名：

```go
func generateDownloadToken(shareCode string, secret string) string {
    expiry := time.Now().Add(10 * time.Minute).Unix()
    data := fmt.Sprintf("%s:%d", shareCode, expiry)
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(data))
    sig := hex.EncodeToString(mac.Sum(nil))
    return base64.URLEncoding.EncodeToString(
        []byte(fmt.Sprintf("%s:%d:%s", shareCode, expiry, sig)))
}
```

**为什么这样做安全？**

- 密码只在 POST Body 中传输一次，不会出现在任何 URL 日志中
- Token 即使被日志记录，10 分钟后自动过期，攻击窗口极小
- Token 通过 HMAC 签名绑定了 shareCode，不能被用于其他分享链接
- Token 是无状态的（不需要服务端存储），纯靠签名验证

### 局限性与替代方案

**局限性**：
- Token 在有效期内如果泄露（比如被人看到 URL），仍然可以被用于下载。但 10 分钟的窗口比永久有效的密码小得多
- 如果服务器的 JWT Secret 泄露，攻击者可以伪造任意 token

**替代方案**：
- **服务端存储型 token**：用 `sync.Map` 或 Redis 存储一次性 token，下载后立即失效。更安全但增加了状态管理复杂度
- **Cookie 方案**：验证密码后设置一个 HttpOnly + Secure 的 Session Cookie，下载请求自动携带。Cookie 不会出现在 URL 中，且可以设置 SameSite 属性防 CSRF。但这需要前后端都做相应改造
- **Signed URL 方案**（类似 AWS S3 预签名 URL）：生成一个包含签名的完整下载 URL，到期自动失效。本质上和当前方案类似，但业界认可度更高

---

## 2. Content-Disposition Header 注入

**涉及文件**：`internal/handler/file.go`、`internal/handler/share.go`

### 问题场景

用户下载文件时，服务端通过 `Content-Disposition` 头告诉浏览器文件名：

```go
c.Header("Content-Disposition", "attachment; filename=\"" + fileRecord.FileName + "\"")
```

正常情况下，文件名是 `report.pdf`，响应头变成：
```
Content-Disposition: attachment; filename="report.pdf"
```

浏览器弹出"另存为"对话框，默认文件名是 `report.pdf`。

### 错误发生原理

HTTP 头部以 `\r\n`（回车换行）分隔。如果用户在上传时构造了一个恶意文件名：

```
evil.txt"\r\nContent-Type: text/html\r\n\r\n<script>alert('XSS')</script>
```

拼接后的响应变成：
```
Content-Disposition: attachment; filename="evil.txt"
Content-Type: text/html

<script>alert('XSS')</script>"
```

攻击者通过文件名中的换行符**注入了额外的 HTTP 头部**，甚至**覆盖了 Content-Type**。浏览器可能将响应体当作 HTML 解析并执行 JavaScript（XSS 攻击）。

即使不考虑攻击，文件名中包含双引号 `"` 也会破坏头部格式：
```
Content-Disposition: attachment; filename="my "special" file.txt"
                                              ↑ 头部在此截断
```

另外，中文文件名直接放入 `filename=` 在 IE 等老浏览器下会显示乱码，因为 HTTP 头部只保证支持 ASCII。

### 修正方案及其原理

新增 `safeContentDisposition` 函数，遵循 RFC 6266 / RFC 5987：

```go
func safeContentDisposition(fileName string) string {
    // 1. 生成 ASCII 安全版本：非 ASCII 和危险字符替换为下划线
    asciiName := ... // "my_special_file.txt"

    // 2. 生成 UTF-8 URL 编码版本
    encoded := url.PathEscape(fileName) // "my%20%22special%22%20file.txt"

    // 3. 双版本输出
    return `attachment; filename="` + string(asciiName) +
           `"; filename*=UTF-8''` + encoded
}
```

输出：
```
Content-Disposition: attachment; filename="my_special_file.txt"; filename*=UTF-8''my%20%22special%22%20file.txt
```

**为什么安全？**
- `filename=` 中所有危险字符都被替换为下划线，无法注入
- `filename*=` 中使用 `url.PathEscape`，换行符变成 `%0D%0A`、双引号变成 `%22`，都不再是 HTTP 语法字符
- 浏览器优先使用 `filename*=`（RFC 5987），正确显示中文等 Unicode 字符；不支持的老浏览器使用 `filename=` 的 ASCII fallback

### 局限性与替代方案

**局限性**：
- `url.PathEscape` 不会转义所有字符（比如 `!`、`*` 等），但这些字符在 HTTP 头部中不构成威胁
- 极少数非常老的浏览器可能不支持 `filename*=`，此时用户看到的是下划线替代版的文件名

**替代方案**：
- Go 标准库的 `mime.FormatMediaType` 可以自动处理参数编码，但它的输出格式不总是与所有浏览器兼容
- 另一种彻底的方案是**不在 Content-Disposition 中放文件名**，而是将文件名编码到 URL 路径中（如 `/download/报告.pdf`），让浏览器从 URL 自动推断文件名

---

## 3. Range 请求解析过于简陋

**涉及文件**：`internal/handler/file.go`

### 问题场景

HTTP Range 请求让客户端只获取文件的一部分字节，常用于：

1. **断点续传**：下载中断后从上次的位置继续，而不是从头开始
2. **视频拖动播放**：用户在播放器中拖动进度条，浏览器发送 Range 请求获取对应位置的数据
3. **多线程下载**：下载工具将文件分段并行下载

### 错误发生原理

原代码手动解析 Range 头：

```go
httpRange := c.GetHeader("Range")
ranges := strings.TrimPrefix(httpRange, "bytes=")
rangeParts := strings.Split(ranges, "-")
start, _ := strconv.ParseInt(rangeParts[0], 10, 64)  // 错误被忽略！
end := fileRecord.FileSize - 1
if rangeParts[1] != "" {
    end, _ = strconv.ParseInt(rangeParts[1], 10, 64)  // 错误被忽略！
}
file.Seek(start, io.SeekStart)
io.CopyN(c.Writer, file, end-start+1)
```

**问题一：解析错误被吞**。如果 Range 值是 `bytes=abc-def`，`strconv.ParseInt` 返回错误和 0 值，但错误被 `_` 忽略了。于是 `start=0, end=0`，服务端返回文件第一个字节。客户端以为拿到了请求的数据段，实际上拿到了错误的内容，但不会收到任何错误提示。

**问题二：无边界校验**。
- `Range: bytes=-100`（合法：表示最后100字节），`Split("-")` 得到 `["", "100"]`，`start` 解析失败为 0 → 返回从头开始的 100 字节，语义错误
- `Range: bytes=999999999-`，如果文件只有 1000 字节，`Seek` 到一个不存在的位置，`io.CopyN` 读不到数据，返回空响应
- `Range: bytes=500-200`（start > end），`io.CopyN(c.Writer, file, 200-500+1)` 即 `CopyN(file, -299)`，行为未定义

**问题三：缺少缓存协商**。没有 ETag 和 Last-Modified 支持。如果文件内容发生了变化（比如用户上传了同名新文件），客户端续传时拿到的是新文件的中间段 + 旧文件的开头段，拼在一起变成损坏的文件，且**没有任何机制能检测到这个问题**。

**问题四：不支持多段 Range**。RFC 7233 允许 `Range: bytes=0-99,200-299` 请求多段数据，原代码完全不处理。

### 修正方案及其原理

使用 Go 标准库 `http.ServeContent` 替代全部手写逻辑：

```go
http.ServeContent(c.Writer, c.Request, fileRecord.FileName, fileRecord.UpdatedAt, file)
```

`http.ServeContent` 一行代码涵盖了：

1. **Range 解析**：完整实现 RFC 7233，支持单段和多段 Range，自动校验边界
2. **206 Partial Content**：Range 请求返回正确的 206 状态码和 `Content-Range` 头
3. **416 Range Not Satisfiable**：无效的 Range 返回 416 错误而不是静默返回错误数据
4. **ETag 生成**：基于文件修改时间和大小自动生成 ETag
5. **条件请求**：处理 `If-Modified-Since`、`If-None-Match`、`If-Range` 等头部，支持 304 Not Modified
6. **MIME 类型检测**：根据文件名扩展自动设置 Content-Type

参数中的 `fileRecord.UpdatedAt` 是文件的修改时间，用于 ETag 生成和 Last-Modified 响应。`file` 是 `*os.File`，实现了 `io.ReadSeeker` 接口，`ServeContent` 需要它来支持 Seek 定位。

### 局限性与替代方案

**局限性**：
- `http.ServeContent` 的 ETag 是基于修改时间生成的（不是基于文件内容哈希），如果文件内容变了但修改时间没变（理论上不太可能），ETag 不会改变。对于 GoDisk 来说，文件基于内容哈希存储，同一路径的内容不会变化，所以这不是实际问题
- `ServeContent` 自动设置 Content-Type，可能覆盖我们在前面手动设置的值。但我们仍然先设置了 Content-Disposition，`ServeContent` 不会覆盖已存在的 Content-Disposition

**替代方案**：
- `http.ServeFile(w, r, path)`：更简单，直接传文件路径。但它会处理目录列表（安全风险），且我们需要先做权限校验再返回文件，所以不适合
- 使用 Nginx 的 `X-Accel-Redirect` 或 Apache 的 `X-Sendfile`：Go 服务只做权限校验，返回一个特殊头部，由反向代理直接发送文件。性能最好，但需要特定的部署架构

---

## 4. JWT Secret 硬编码在配置文件中

**涉及文件**：`internal/config/config.go`、`config.yaml`

### 问题场景

JWT（JSON Web Token）用于用户认证。服务端用一个 Secret 对 token 签名，客户端每次请求携带 token，服务端验证签名。Secret 的安全性等同于用户数据库的安全性 —— 谁拿到 Secret，谁就能伪造任何用户的 token。

当前 Secret 写在 `config.yaml` 中：
```yaml
jwt:
  secret: your-secret-key-change-in-production
```

### 错误发生原理

**场景一：默认值上生产**

开发者复制代码后忘记修改默认的 `your-secret-key-change-in-production`。攻击者只要知道这个项目是 GoDisk，就能用默认 Secret 伪造管理员 token：

```go
// 攻击者伪造 token
token, _ := jwt.GenerateToken(1, "admin", "your-secret-key-change-in-production", 168)
// 用这个 token 调用任何 API，系统会认为是用户 ID=1
```

**场景二：Secret 随代码提交**

`config.yaml` 被提交到 Git 仓库。即使是私有仓库，以下情况都会泄露 Secret：
- 仓库被意外设为 public
- 团队成员离职但仍有仓库历史
- CI/CD 系统的日志中可能打印配置内容

**场景三：多环境 Secret 相同**

开发、测试、生产环境共用同一个 `config.yaml`。在开发环境拿到的 token 可以直接用于生产环境。

### 修正方案及其原理

```go
// 环境变量优先级高于配置文件
if envSecret := os.Getenv("JWT_SECRET"); envSecret != "" {
    cfg.JWT.Secret = envSecret
}

// 安全校验
if len(cfg.JWT.Secret) < 32 {
    fmt.Println("[WARNING] JWT secret is shorter than 32 characters")
}
if cfg.JWT.Secret == "your-secret-key-change-in-production" {
    fmt.Println("[WARNING] Using default JWT secret, please change for production")
}
```

**为什么用环境变量？**
- 环境变量不会被提交到版本控制系统
- 不同环境（dev/staging/prod）天然有不同的环境变量，确保 Secret 隔离
- 容器化部署（Docker/K8s）中，环境变量是注入密钥的标准方式（K8s Secrets → 环境变量）
- 12-Factor App 方法论明确推荐将配置存储在环境变量中

**为什么校验长度 ≥ 32？**

HS256 的签名安全性取决于 Secret 长度。HMAC-SHA256 的密钥应至少 256 位（32 字节）才能达到与算法本身相同的安全强度。短密钥可以被暴力破解。

### 局限性与替代方案

**局限性**：
- 环境变量在进程内是明文的，`/proc/<pid>/environ` 可以读取。但这至少比写在文件里要好
- 只是警告，不是强制。如果用户忽略警告仍然使用弱 Secret，系统仍然会启动

**替代方案**：
- **强制启动检查**：检测到默认 Secret 时 `log.Fatal` 拒绝启动，而不是仅警告。更安全但对开发环境不友好
- **密钥管理服务（KMS）**：从 HashiCorp Vault、AWS KMS 等服务动态获取 Secret。适合大型生产环境
- **启动时随机生成**：如果没设置 Secret，每次启动自动生成随机 Secret。缺点是重启后所有已发的 token 失效（用户需要重新登录）
- **RSA 非对称签名**：使用 RS256 替代 HS256，私钥签发 token，公钥验证。私钥只存在签发服务上，验证服务只需公钥。适合微服务架构

---

## 5. 上传/删除文件缺少数据库事务

**涉及文件**：`internal/service/file.go`

### 问题场景

文件上传和删除涉及多个数据库操作：创建/删除文件记录、更新用户存储配额。这些操作必须**全部成功或全部失败**（原子性），否则数据会不一致。

### 错误发生原理

**上传场景**：

```go
// 步骤1: 创建文件记录
s.db.Create(fileRecord)     // ← 假设成功

// 步骤2: 更新用户配额
user.AddStorage(savedSize)  // 内存中修改
s.db.Save(&user)            // ← 假设此时 SQLite 文件锁冲突，失败了
```

结果：文件记录存在（占用 100MB），但用户的 `storage_used` 没有增加。用户以为自己还有 10GB 空间，继续上传，实际磁盘已满。更严重的是，这种不一致**永远无法自动恢复**，因为没有对账机制。

**删除场景**（递归删除文件夹）：

```go
for _, child := range children {
    s.storageSvc.DeleteFile(child.FilePath)  // 删除物理文件
    s.db.First(&user, child.UserID)          // 查询用户
    user.RemoveStorage(child.FileSize)       // 减少配额
    s.db.Save(&user)                         // 保存
    s.db.Delete(child)                       // 删除记录
}
```

假设文件夹有 100 个文件，删到第 50 个时程序崩溃（OOM、panic 等）：
- 前 50 个文件的物理文件已删除、DB 记录已删除
- 后 50 个文件完好
- 文件夹记录本身还在（因为 `s.db.Delete(&file)` 在循环之后）
- 用户看到一个"半空"的文件夹，且配额计算只减了一半

另一个问题是**性能**：100 个文件产生 100 次 `db.First(&user)` + 100 次 `db.Save(&user)`，共 200 次数据库操作。每次都查询同一个用户并整行更新。

### 修正方案及其原理

**UploadFile**：所有 DB 操作包在一个事务中：

```go
s.db.Transaction(func(tx *gorm.DB) error {
    // 去重检查（在事务内，防止并发上传相同文件时重复创建）
    if err := tx.Where("user_id=? AND file_hash=? AND parent_id=?", ...).First(&existingFile).Error; err == nil {
        return nil // 已存在
    }
    // 创建记录
    tx.Create(fileRecord)
    // 原子更新配额（SQL 层面的原子操作，不是先读后写）
    tx.Model(&User{}).Where("id=?", userID).
        Update("storage_used", gorm.Expr("storage_used + ?", savedSize))
    return nil
})
```

关键改进：配额更新使用 `gorm.Expr("storage_used + ?", savedSize)` 而不是 `user.AddStorage(); db.Save(&user)`。前者生成的 SQL 是 `UPDATE users SET storage_used = storage_used + 100 WHERE id = 1`，这是一个**原子的 SQL 操作**，不需要先读取当前值。后者需要先 `SELECT` 再 `UPDATE`，两步之间可能有并发冲突。

**DeleteFile**：先收集信息，再在事务中批量处理：

```go
// 1. 递归收集所有子文件路径和总大小
filePaths, totalSize, err := s.collectAndDeleteFolder(tx, &file)

// 2. 在事务内一次性更新配额
tx.Model(&User{}).Where("id=?", userID).
    Update("storage_used", gorm.Expr("CASE WHEN storage_used >= ? THEN storage_used - ? ELSE 0 END", totalSize, totalSize))

// 3. 事务成功后删除物理文件
for _, fp := range filePaths {
    s.storageSvc.DeleteFile(fp)
}
```

**为什么物理文件在事务外删除？** 因为事务可能回滚。如果在事务内删除了物理文件，但事务回滚了（DB 记录还在），就会出现"DB 记录指向不存在的文件"的情况。所以正确的顺序是：先提交事务确保 DB 状态一致，再删除物理文件。反过来即使物理文件删除失败，DB 记录还在，可以重试。

### 局限性与替代方案

**局限性**：
- SQLite 的事务是串行化的（同一时间只有一个写事务），高并发场景下会成为瓶颈。但对于 GoDisk 的单机使用场景来说足够了
- 物理文件删除在事务外执行，如果程序在事务提交后、物理删除前崩溃，会留下孤儿物理文件。可以通过定期的"垃圾回收"任务（扫描 uploads 目录，对比 DB 记录）来清理
- 配额使用 `CASE WHEN` 防止变为负数，但这掩盖了潜在的配额计算错误。正式项目中应该有对账机制

**替代方案**：
- **Outbox 模式**：将"待删除物理文件"写入一张 `pending_deletions` 表，由后台任务异步执行物理删除。事务保证 DB 一致性，物理删除可重试。这是分布式系统的标准做法
- **WAL（Write-Ahead Logging）**：操作前先写日志，崩溃后从日志恢复。复杂度高，适合数据库引擎内部实现

---

## 6. StorageService.DeleteFile 的引用计数竞态

**涉及文件**：`internal/service/storage.go`

### 问题场景

GoDisk 的文件去重机制：多个用户上传相同内容的文件，磁盘上只存**一份**物理文件（以 SHA256 命名），但数据库中有**多条** File 记录指向同一个 `file_path`。

```
数据库 files 表:
┌────┬─────────┬─────────────────────┐
│ ID │ user_id │ file_path           │
├────┼─────────┼─────────────────────┤
│ 1  │ 用户A   │ uploads/abc123...   │  ← 同一个物理文件
│ 2  │ 用户B   │ uploads/abc123...   │  ← 同一个物理文件
└────┴─────────┴─────────────────────┘

磁盘:
uploads/abc123...  （只有一份）
```

删除时需要**引用计数**：只有当最后一条指向该物理文件的记录也被删了，才能安全地删除磁盘文件。

### 错误发生原理

原代码：

```go
func DeleteFile(filePath string) error {
    // 步骤1: 查还有多少条记录引用这个文件
    s.db.Model(&File{}).Where("file_path = ?", filePath).Count(&count)

    // ← 这里有时间间隙！

    // 步骤2: 没人引用了就删
    if count == 0 {
        os.Remove(filePath)
    }
}
```

而调用方（`FileService.DeleteFile`）是**先删 DB 记录，再调这个函数**。

**竞态场景：用户A 和用户B 同时删除**

```
时间线          用户A的请求                     用户B的请求
─────────────────────────────────────────────────────────
 T1     DELETE FROM files WHERE id=1
        （DB 记录 1 已删除）

 T2                                      DELETE FROM files WHERE id=2
                                         （DB 记录 2 已删除）

 T3     StorageService.DeleteFile:
        Count("file_path=abc123") → 0
        （记录 2 已经在 T2 被删了）

 T4                                      StorageService.DeleteFile:
                                         Count("file_path=abc123") → 0
                                         （记录 1 已经在 T1 被删了）

 T5     os.Remove("uploads/abc123") ✓

 T6                                      os.Remove("uploads/abc123")
                                         （文件已不存在，但 IsNotExist 保护不报错）
```

这种情况下两个请求**都**看到 count=0，都尝试删除。虽然第二次 Remove 不会报错（有 `IsNotExist` 保护），但逻辑上**不受控**。

**更危险的场景：删除 + 上传并发**

```
时间线          用户A删除文件                     用户C上传相同文件
─────────────────────────────────────────────────────────
 T1     DELETE FROM files WHERE id=1
 T2     DELETE FROM files WHERE id=2

 T3     StorageService.DeleteFile:
        Count → 0, 准备删除物理文件

 T4                                      Upload: SaveFile 查 DB 没有
                                         相同 hash → 写入 uploads/abc123

 T5     os.Remove("uploads/abc123")      ← 把用户C刚上传的文件删了！
```

**用户 C 的文件凭空消失**，DB 记录指向一个不存在的物理文件，且没有任何错误提示。下载时才会发现 "failed to open file"。

### 修正方案及其原理

```go
func DeleteFile(filePath string) error {
    if filePath == "" { return nil }

    var shouldDelete bool

    // 在事务中检查引用计数
    s.db.Transaction(func(tx *gorm.DB) error {
        var count int64
        tx.Model(&File{}).Where("file_path = ?", filePath).Count(&count)
        shouldDelete = (count == 0)
        return nil
    })

    if shouldDelete {
        os.Remove(filePath)
    }
}
```

**为什么事务能解决？**

SQLite 的事务模型是**串行化**的 —— 同一时刻只有一个写事务能持有锁。但更关键的是，这要配合**调用方的事务**一起工作。在修正后的 `FileService.DeleteFile` 中，DB 记录删除和配额更新本身就包在一个事务里：

```go
// FileService.DeleteFile 中
s.db.Transaction(func(tx *gorm.DB) error {
    tx.Delete(&file)         // 删除 DB 记录
    tx.Model(&User{}).Update(...) // 更新配额
    return nil
})
// 事务提交后，记录已确定被删除，再检查引用计数
s.storageSvc.DeleteFile(file.FilePath)
```

整体流程变成：**先在事务中确定性地删除 DB 记录 → 事务提交 → 再在事务中检查物理文件引用 → 安全删除**。SQLite 的写事务串行化保证了不会有两个请求同时处于"已删 DB 记录但还没检查引用"的中间状态。

同时添加了 `filePath == ""` 的空值保护，防止对文件夹记录（没有物理文件路径）调用 Remove。

### 局限性与替代方案

**局限性**：
- 此方案依赖 SQLite 的单写者串行化模型。如果将来换成 MySQL/PostgreSQL（真正并发写入），两个事务可以同时读到 count=0，竞态重现
- 事务中只做了 Count 查询（读操作），在 MySQL 默认的 REPEATABLE READ 隔离级别下不能阻止并发写入

**替代方案**：
- **SELECT ... FOR UPDATE**（MySQL/PostgreSQL）：在事务中对相关行加排他锁，确保其他事务必须等待。但 SQLite 不支持行级锁
- **延迟删除**：不直接删除物理文件，而是标记为"待清理"，由单线程后台任务统一扫描并删除。彻底消除并发问题，因为只有一个线程做删除决策。这是 Seafile 等成熟产品的做法
- **引用计数字段**：在物理文件上维护一个原子引用计数器（数据库字段），用 `UPDATE SET ref_count = ref_count - 1 WHERE ref_count > 0 RETURNING ref_count`，当返回 0 时删除。这需要额外的表来跟踪物理文件

---

## 7. deleteFolderRecursive 每个子文件单独查 User 更新配额

**涉及文件**：`internal/service/file.go`

### 问题场景

用户删除一个包含 1000 个文件的文件夹。

### 错误发生原理

原代码对每个子文件做：
```go
for _, child := range children {
    s.db.First(&user, child.UserID)    // SELECT * FROM users WHERE id=1
    user.RemoveStorage(child.FileSize) // 内存中计算
    s.db.Save(&user)                   // UPDATE users SET ... WHERE id=1
}
```

1000 个文件 → 1000 次 SELECT + 1000 次 UPDATE = **2000 次数据库操作**。每次查出来的都是同一个用户、每次更新的都是同一行。而且 `user.RemoveStorage` 在内存中修改 `StorageUsed` 后 `Save`，两个并发请求可能读到同一个旧值，导致**最后写入者获胜**（lost update）。

例如：
- storage_used = 1000MB
- 请求 A 读到 1000MB，减去文件 A 的 10MB，写入 990MB
- 请求 B 也读到 1000MB（还没看到 A 的写入），减去文件 B 的 20MB，写入 980MB
- 最终 storage_used = 980MB，但实际应该是 970MB（丢失了 A 的扣减）

### 修正方案及其原理

此问题已合并到 **#5** 中一起修复。新的 `collectAndDeleteFolder` 方法：

1. 递归遍历文件树，收集所有文件路径和文件大小
2. 计算总大小 `totalSize`
3. 在事务中一次性更新配额：
   ```go
   tx.Model(&User{}).Where("id=?", userID).
       Update("storage_used", gorm.Expr("CASE WHEN storage_used >= ? THEN storage_used - ? ELSE 0 END", totalSize, totalSize))
   ```

从 O(2N) 次数据库操作降为 O(N) 次 DELETE + 1 次 UPDATE。`gorm.Expr` 确保更新是 SQL 原子操作，不存在 lost update 问题。

### 局限性与替代方案

**局限性**：
- 递归遍历仍然是 O(N) 次查询（每层一次 `WHERE parent_id=?`）。对于极深的目录结构（100 层嵌套），会产生 100 次查询
- 收集阶段将所有文件路径存在内存中。百万级文件的文件夹可能占用大量内存

**替代方案**：
- **CTE 递归查询**（PostgreSQL/MySQL 8.0+）：一条 SQL 查询整棵子树。SQLite 也支持 WITH RECURSIVE，可以进一步优化
- **物化路径（Materialized Path）**：文件表增加 `path` 字段存储完整路径（如 `/1/5/12/`），删除时用 `LIKE '/1/5/%'` 一次性查出所有子孙。查询简单高效，但需要在移动文件时维护所有子孙的路径

---

## 8. UploadChunk 将整个分片读入内存

**涉及文件**：`internal/handler/file.go`、`internal/service/chunk.go`、`internal/service/storage.go`

### 问题场景

用户通过分片上传接口上传大文件，每个分片默认 5MB。Handler 层负责接收 HTTP multipart 请求中的分片数据并传给 Service 层处理。

### 错误发生原理

原代码在 Handler 层将整个分片一次性读入内存：

```go
// handler 层
chunkData, err := io.ReadAll(file)    // 一次性读取 5MB 到 []byte
h.chunkSvc.UploadChunk(userID, uploadID, chunkIndex, chunkData)

// service 层
func UploadChunk(..., chunkData []byte) error { ... }

// storage 层
func SaveChunk(..., data []byte) { os.WriteFile(path, data, 0644) }
```

内存计算：
- 1 个并发上传：5MB
- 10 个并发上传：50MB
- 100 个并发上传：500MB
- 如果客户端使用自定义分片大小（config 中允许），比如 50MB × 100 并发 = **5GB**

Go 的 `io.ReadAll` 在读取过程中还会因为 `append` 扩容产生额外的内存分配和 GC 压力。在内存紧张时，GC 频繁触发导致所有请求延迟飙升（STW），进一步恶化。

最终可能导致 OOM（Out of Memory），整个进程被操作系统杀死。

### 修正方案及其原理

全链路改为**流式传输**（Streaming），数据从网络直接流到磁盘，不在内存中暂存：

```go
// handler: 直接将 multipart.File（实现了 io.Reader）传下去
file, _ := chunkFile.Open()
h.chunkSvc.UploadChunk(userID, uploadID, chunkIndex, file)

// service: 参数类型改为 io.Reader
func UploadChunk(..., chunkData io.Reader) error {
    s.storageSvc.SaveChunk(uploadID, chunkIndex, chunkData)
}

// storage: 用 io.Copy 流式写入
func SaveChunk(..., data io.Reader) (string, error) {
    dst, _ := os.Create(chunkPath)
    io.Copy(dst, data)  // 内部使用 32KB 缓冲区
}
```

**为什么流式传输省内存？**

`io.Copy` 的内部实现（来自 Go 源码）：
```go
func copyBuffer(dst Writer, src Reader, buf []byte) (written int64, err error) {
    if buf == nil {
        buf = make([]byte, 32*1024) // 固定 32KB 缓冲区
    }
    for {
        nr, er := src.Read(buf)      // 读 32KB
        nw, ew := dst.Write(buf[:nr]) // 写 32KB
        // ... 循环直到 EOF
    }
}
```

无论分片多大，内存占用始终是 **32KB**。100 个并发上传 = 100 × 32KB = **3.2MB**，相比原来的 500MB 降低了 99.4%。

### 局限性与替代方案

**局限性**：
- 流式传输意味着数据只能读一次。原代码在读取后还计算了分片哈希（虽然是死代码），如果未来需要分片级哈希验证，需要用 `io.TeeReader` 边读边哈希
- Gin 框架的 `c.FormFile` 对于小文件（< 32MB）已经在内存中了（因为 `multipart.Reader` 的默认行为），对于大文件才会存到临时文件。真正要优化大分片的内存，需要设置 `r.MaxMultipartMemory`

**替代方案**：
- **TUS 协议**：专门为可恢复上传设计的开放协议，用 PATCH 方法流式传输数据，天然支持流式处理
- **直接读 Request Body**：不用 multipart，客户端直接把分片数据作为 Request Body 发送（`Content-Type: application/octet-stream`），Handler 直接用 `c.Request.Body`（就是 `io.Reader`）。省去 multipart 解析开销

---

## 9. uploadID 生成不安全且易冲突

**涉及文件**：`internal/service/chunk.go`

### 问题场景

用户 Alice（ID=42）先后上传两个同名但内容不同的文件 `report.pdf`。

### 错误发生原理

原代码生成 uploadID 的方式：
```go
func generateUploadID(userID uint, fileName string) string {
    return fmt.Sprintf("%d_%s_%d", userID, fileName, len(fileName))
}
```

对于 Alice 的两次上传：
- 第一次：`generateUploadID(42, "report.pdf")` → `"42_report.pdf_10"`
- 第二次：`generateUploadID(42, "report.pdf")` → `"42_report.pdf_10"`

**完全相同的 ID！** 第二次上传的分片会覆盖第一次的分片文件。虽然 `InitUpload` 中有基于 `file_hash` 的幂等检查可以避免部分情况，但如果两次上传的 hash 不同（内容不同），就会创建两条 `FileChunk` 记录共用同一组分片文件路径，导致数据混乱。

**安全问题**：

1. **路径穿越**：文件名 `../../etc/passwd` 会让分片路径变成：
   ```
   uploads/chunks/42_../../etc/passwd_16_chunk_0
   → uploads/etc/passwd_16_chunk_0  (路径穿越)
   ```

2. **可预测性**：攻击者知道目标用户 ID（通常从 1 开始递增）和文件名，就能推测 uploadID，伪造上传进度或覆盖他人的分片。

### 修正方案及其原理

```go
func generateUploadID(userID uint, fileName string) string {
    return uuid.New().String()  // 例如 "550e8400-e29b-41d4-a716-446655440000"
}
```

UUID v4 的特性：
- **唯一性**：基于 122 位随机数，碰撞概率约 2^-61（生成 10 亿个 UUID 碰撞概率为 50 亿分之一）
- **不可预测**：使用 `crypto/rand`（密码学安全随机数），攻击者无法推测
- **无特殊字符**：只包含十六进制字符和 `-`，不存在路径穿越风险

保留函数签名 `(userID uint, fileName string)` 不变，避免修改所有调用方。参数不使用但不影响功能。

### 局限性与替代方案

**局限性**：
- UUID v4 理论上有碰撞可能（虽然概率极低）。对于分片上传这种短生命周期场景，完全可以忽略
- `google/uuid` 包已是项目的间接依赖，此改动将其提升为直接依赖

**替代方案**：
- **UUID v7**（时间排序的 UUID）：包含时间戳前缀，方便按创建时间排序和调试。Go 1.22+ 的 `google/uuid` 支持 `uuid.NewV7()`
- **crypto/rand + hex**：`crypto/rand.Read(16 bytes)` → hex 编码。与 UUID v4 本质相同，但格式更紧凑（没有 `-` 分隔符）
- **NanoID**：更短的随机 ID（默认 21 字符 vs UUID 的 36 字符），URL 友好。但需要额外依赖

---

## 10. Embedding Worker 无优雅退出

**涉及文件**：`internal/service/embedding.go`

### 问题场景

系统运行时，2 个 embedding worker goroutine 不断从队列中取任务，调用 Qwen API 生成向量。管理员按 Ctrl+C 停止服务。

### 错误发生原理

原代码中 worker 的生命周期没有任何控制机制：

```go
func (s *embeddingServiceImpl) startWorkerPool() {
    for i := 0; i < 2; i++ {
        go func(workerID int) {
            for task := range s.taskQueue {  // 永远阻塞等待
                s.CreateFileEmbedding(task.fileID)
                time.Sleep(500 * time.Millisecond)
            }
        }(i)
    }
}
```

**问题一：Goroutine 泄漏**

`s.taskQueue` 这个 channel 永远不会被关闭（没有 `close(s.taskQueue)` 的调用），所以 `for task := range s.taskQueue` 永远不会返回。这两个 goroutine 在进程生命周期内永远存在。

虽然进程退出时所有 goroutine 都会被回收，但在以下情况下是真正的问题：
- 如果 `EmbeddingService` 被重新创建（比如热重载配置），旧的 worker 不会停止
- 单元测试中每个测试创建一个 Service，测试结束后 goroutine 累积

**问题二：任务丢失**

队列中可能有 100 个待处理任务。进程被强制终止时，这些任务直接丢失。虽然 embedding 可以通过 `/api/admin/regenerate-embeddings` 重建，但对用户来说"刚上传的文件搜不到"是一个可感知的问题。

**问题三：API 请求中断**

Worker 正在调用 Qwen API（HTTP 请求进行中），进程被杀，TCP 连接被强制关闭。虽然 Qwen API 不会因此产生副作用（HTTP 是幂等的），但这是一个不干净的退出。

### 修正方案及其原理

添加 `context.Context` + `sync.WaitGroup` 实现协作式关闭：

```go
type embeddingServiceImpl struct {
    ...
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

func (s *embeddingServiceImpl) startWorkerPool() {
    for i := 0; i < 2; i++ {
        s.wg.Add(1)
        go func(workerID int) {
            defer s.wg.Done()
            for {
                select {
                case <-s.ctx.Done():        // 收到关闭信号
                    return
                case task := <-s.taskQueue: // 正常取任务
                    s.CreateFileEmbedding(task.fileID)
                    time.Sleep(500 * time.Millisecond)
                }
            }
        }(i)
    }
}

func (s *embeddingServiceImpl) Shutdown() {
    s.cancel()   // 通知所有 worker 停止
    s.wg.Wait()  // 等待当前正在处理的任务完成
}
```

**工作原理**：

`select` 语句同时监听两个 channel：
- `s.ctx.Done()`：当 `cancel()` 被调用时变为可读
- `s.taskQueue`：有新任务时变为可读

正常运行时，`ctx.Done()` 不可读，worker 一直从 `taskQueue` 取任务处理。

关闭时，`main.go` 调用 `embSvc.Shutdown()`：
1. `cancel()` 让 `ctx.Done()` 变为可读
2. 正在处理任务的 worker 会**完成当前任务**（因为 `select` 只在循环顶部检查）
3. 回到 `select`，此时 `ctx.Done()` 可读，`return` 退出
4. `defer s.wg.Done()` 将 WaitGroup 计数减 1
5. 所有 worker 退出后，`wg.Wait()` 返回，`Shutdown()` 完成

### 局限性与替代方案

**局限性**：
- 如果 `CreateFileEmbedding` 内部的 HTTP 请求耗时很长（Qwen API 超时设置为 60 秒），`Shutdown` 最多要等 60 秒才能返回。可以将 `s.ctx` 传入 HTTP 请求，实现取消：`http.NewRequestWithContext(s.ctx, ...)`
- 关闭后队列中未处理的任务会丢失。如果需要持久化，应将任务写入数据库

**替代方案**：
- **errgroup**：`golang.org/x/sync/errgroup` 提供了带 context 的 goroutine 组管理，自动处理错误传播
- **Worker Pool 库**：如 `github.com/gammazero/workerpool`，提供 `StopWait()` 方法和更完善的队列管理
- **消息队列**：将 embedding 任务发送到 Redis Stream 或 RabbitMQ，由独立的 worker 进程消费。即使 Web 服务重启，任务也不会丢失

---

## 11. 后台清理任务是空壳

**涉及文件**：`cmd/server/main.go`

### 问题场景

系统运行一段时间后：
- 用户创建了很多设了过期时间的分享链接，过期后 DB 中的分享记录一直存在
- 用户开始了分片上传但网络断开，再也没有完成。分片文件永久残留在 `uploads/chunks/` 目录

### 错误发生原理

两个清理函数只有 TODO 注释和一行日志：

```go
func cleanupExpiredShares(db interface{}) {
    // TODO: 实现清理过期分享的逻辑
    log.Println("Running cleanup task: expired shares")
}
func cleanupIncompleteUploads(cfg *config.Config, db interface{}) {
    // TODO: 实现清理未完成上传的逻辑
    log.Println("Running cleanup task: incomplete uploads")
}
```

后果：
- 过期分享虽然不能下载（有 `IsExpired()` 检查），但记录永远不被清理，`shares` 表无限增长
- 每个分片 5MB，一个 1GB 文件的上传中断后留下 200 个 5MB 分片文件，占 1GB 磁盘空间。长期累积会耗尽磁盘
- 函数参数类型是 `interface{}`，丢失了类型安全

### 修正方案及其原理

**cleanupExpiredShares**：

```go
func cleanupExpiredShares(db *gorm.DB) {
    result := db.Where("expire_at IS NOT NULL AND expire_at < ?", time.Now()).Delete(&model.Share{})
    if result.RowsAffected > 0 {
        log.Printf("[Cleanup] Cleaned up %d expired shares", result.RowsAffected)
    }
}
```

GORM 的 `Delete` 使用软删除（`deleted_at` 字段），所以这不是真正的物理删除。过期分享被标记为已删除后：
- `ListShares` 查询自动过滤 `deleted_at IS NULL`
- 数据仍可通过 `Unscoped()` 恢复（如果需要审计）

**cleanupIncompleteUploads**：

```go
func cleanupIncompleteUploads(cfg *config.Config, db *gorm.DB, storageSvc *service.StorageService) {
    cutoff := time.Now().Add(-24 * time.Hour)
    var chunks []model.FileChunk
    db.Where("status IN ? AND updated_at < ?", []string{"pending", "uploading"}, cutoff).Find(&chunks)
    for _, chunk := range chunks {
        storageSvc.CleanChunks(chunk.UploadID)        // 删除磁盘上的分片文件
        db.Model(&chunk).Update("status", "failed")   // 标记为失败
    }
}
```

24 小时的阈值是因为：正常的分片上传即使网络慢也应该在数小时内完成。超过 24 小时未完成的上传几乎可以确定是被放弃了。标记为 `failed` 而不是删除记录，保留了审计能力。

同时，清理任务改为监听 `context.Context`，可以在 Graceful Shutdown 时干净退出：

```go
func startCleanupTasks(ctx context.Context, ...) {
    ticker := time.NewTicker(24 * time.Hour)
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C:
            cleanupExpiredShares(db)
            cleanupIncompleteUploads(cfg, db, storageSvc)
        }
    }
}
```

### 局限性与替代方案

**局限性**：
- 清理周期是 24 小时，意味着过期分享最多可以在过期后继续存在 24 小时。实际上由于 `IsExpired()` 检查，过期分享已经不能使用，只是 DB 记录没被清理
- 清理未完成上传时，如果用户确实在 24 小时内慢速上传（如通过极慢的网络），会被误判为失败

**替代方案**：
- **更短的清理周期**：改为每小时清理。但对于 SQLite 来说频繁扫描可能影响性能
- **事件驱动**：在分享过期时间点设置一个定时器，精确触发清理。复杂度高但更精确
- **数据库 TTL**：Redis 原生支持 key 过期。如果分享信息存在 Redis 中，可以自动过期

---

## 12. 缺少 Graceful Shutdown

**涉及文件**：`cmd/server/main.go`

### 问题场景

系统正在处理请求（比如用户正在上传一个 500MB 的文件，已经上传到 80%），管理员按 Ctrl+C 或部署系统发送 SIGTERM 停止服务。

### 错误发生原理

原代码直接使用 Gin 的 `r.Run()`：

```go
if err := r.Run(addr); err != nil {
    log.Fatalf("Failed to start server: %v", err)
}
```

`r.Run()` 内部就是 `http.ListenAndServe`，它在收到 SIGINT/SIGTERM 后**立即终止进程**。

后果：
1. 正在上传的文件：分片写到一半，磁盘上留下不完整的文件
2. 正在执行的数据库事务：SQLite 使用 WAL 模式可以自动回滚，但可能丢失最后的写入
3. 正在下载的文件：客户端收到不完整的数据，可能导致下载的文件损坏
4. Embedding worker：正在调用 Qwen API 的请求被强制中断
5. 后台清理任务：被强制终止，可能停在"已查询但还没更新状态"的中间状态

### 修正方案及其原理

使用 Go 的标准优雅关闭模式：

```go
// 1. 创建可控的 HTTP Server
srv := &http.Server{Addr: addr, Handler: r}

// 2. 在 goroutine 中启动
go srv.ListenAndServe()

// 3. 主 goroutine 等待中断信号
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit

// 4. 按序关闭各组件
cleanupCancel()                        // 停止后台清理任务
embSvc.Shutdown()                      // 等待 embedding worker 完成当前任务
ctx, _ := context.WithTimeout(..., 30*time.Second)
srv.Shutdown(ctx)                      // 等待 HTTP 请求完成
```

**`srv.Shutdown(ctx)` 做了什么？**

1. **关闭监听端口**：不再接受新连接
2. **等待已有连接完成**：所有 in-flight 的请求会继续处理直到完成
3. **超时强制关闭**：如果 30 秒内还有请求没完成，强制关闭连接
4. **空闲连接立即关闭**：keep-alive 但没有 in-flight 请求的连接立即关闭

**关闭顺序的设计考量**：
- 先停清理任务（避免在关闭过程中还在修改数据库）
- 再停 embedding worker（等待当前 API 调用完成）
- 最后停 HTTP 服务器（等待用户请求完成）

这样保证了**由内到外、先后台再前台**的关闭顺序。

### 局限性与替代方案

**局限性**：
- 30 秒超时是硬编码的。如果用户正在上传一个需要超过 30 秒才能完成的分片，还是会被中断。但分片上传本身是可恢复的，下次启动后可以续传
- WebSocket 连接（如果有的话）不会被 `Shutdown` 自动关闭，需要额外处理

**替代方案**：
- **Pre-stop Hook**：在 Kubernetes 中配置 `preStop` hook，先将 Pod 从 Service 中摘除（不再接收新流量），再发送 SIGTERM。这样有一段时间可以排空现有请求
- **双阶段关闭**：第一个 SIGTERM 启动优雅关闭，第二个 SIGTERM（或 SIGQUIT）立即强制退出。给运维人员"不想等了"的选择
- **健康检查联动**：收到关闭信号后，先让 `/health` 返回 503，让负载均衡器停止分配新请求，等一段时间后再关闭

---

## 13. MoveFile 未防止循环引用

**涉及文件**：`internal/service/file.go`

### 问题场景

用户有如下文件夹结构：
```
/项目A
  /代码
    /后端
      server.go
```

用户尝试将 `/项目A` 移到 `/项目A/代码/后端` 下面。

### 错误发生原理

原代码只检查了一种情况：

```go
if targetParentID == file.ID {
    return errors.New("cannot move to itself")
}
```

这防止了"把文件夹移到自身下面"（A → A），但没有防止"把文件夹移到自己的子孙下面"（A → A/B/C）。

如果这个移动操作成功，数据库中的 `parent_id` 关系会变成：

```
项目A.parent_id = 后端.ID
代码.parent_id = 项目A.ID
后端.parent_id = 代码.ID
```

形成循环：项目A → 后端 → 代码 → 项目A → 后端 → ...

**后果**：
1. `ListFiles` 正常工作（只查当前层级的子文件），用户看不出问题
2. `DownloadFolder`（ZIP 打包下载）使用递归遍历，会进入**无限递归**直到栈溢出（goroutine 栈默认最大 1GB）
3. `DeleteFile` 的递归删除同样会无限循环
4. `buildFilePath`（构建文件完整路径用于 embedding）会无限循环

Go 的 goroutine 栈溢出会导致 `fatal error: stack overflow`，**直接终止整个进程**（不是 panic，recover 也捕获不了）。一个用户的一次操作就能让整个服务崩溃。

### 修正方案及其原理

从 `targetParentID` 沿 `parent_id` 链向上遍历，检查是否会回到当前文件：

```go
if file.IsDirectory && targetParentID != 0 {
    currentID := targetParentID
    for currentID != 0 {
        if currentID == file.ID {
            return errors.New("cannot move folder into its own subfolder")
        }
        var ancestor model.File
        if err := s.db.Select("parent_id").First(&ancestor, currentID).Error; err != nil {
            break
        }
        currentID = ancestor.ParentID
    }
}
```

**工作原理**：

以上面的例子为例，移动 `/项目A`（ID=1）到 `/项目A/代码/后端`（ID=3）：

```
检查: currentID = 3（后端）→ 后端.ParentID = 2（代码）
检查: currentID = 2（代码）→ 代码.ParentID = 1（项目A）
检查: currentID = 1 == file.ID(1) → 发现循环！拒绝操作
```

遍历到根目录（`parent_id = 0`）就停止。最多遍历**目录深度**次（通常不超过 10-20 层），不会有性能问题。

只对文件夹做检查（`file.IsDirectory`），因为普通文件不会成为其他文件的 parent，不可能形成循环。

### 局限性与替代方案

**局限性**：
- 每次移动需要 O(D) 次数据库查询（D = 目标文件夹深度）。对于极深的嵌套可能较慢
- 如果数据库中已经存在循环引用（之前的 bug 导致的脏数据），这个遍历会陷入死循环。可以加一个最大深度限制（如 100）作为安全网

**替代方案**：
- **最大深度限制**：在遍历中增加计数器，超过 100 次遍历直接拒绝。既防了循环引用，也防了用户创建过深的目录结构
- **物化路径（Materialized Path）**：如果文件表有 `path` 字段，判断循环只需一次字符串比较：`target.path.startsWith(file.path)`。O(1) 时间复杂度
- **嵌套集模型（Nested Set）**：使用左值/右值表示树结构，判断祖先关系只需比较数值大小。查询快但维护复杂

---

## 14. 分片上传的哈希验证是死代码

**涉及文件**：`internal/service/chunk.go`

### 问题场景

分片上传的每个分片到达服务端时，需要确保数据完整性 —— 传输过程中没有被篡改或损坏。

### 错误发生原理

原代码计算了分片哈希但完全没有使用：

```go
if chunkRecord.FileHash != "" {
    calculatedHash := hash.CalculateBytesSHA256(chunkData)
    _ = calculatedHash   // 计算了但赋给了空标识符
    _ = chunkPath        // 同上
}
```

这段代码有两个层面的问题：

**层面一：死代码**。`_ = calculatedHash` 明确丢弃了计算结果。没有任何 `if calculatedHash != expectedHash` 的比较。这 5MB 数据被白白计算了一次 SHA256，浪费 CPU。

**层面二：即使验证也是错误的**。`chunkRecord.FileHash` 存储的是**整个文件**的 SHA256，不是**单个分片**的 SHA256。拿整个文件的哈希去验证一个 5MB 分片，永远不会匹配（除非文件恰好只有一个分片）。

### 修正方案及其原理

直接移除这段死代码。完整文件的 SHA256 验证已经在 `CompleteUpload` 中正确实现：

```go
// CompleteUpload 中的验证逻辑
if chunkRecord.FileHash != "" {
    calculatedHash, err := hash.CalculateFileSHA256(filePath)  // 计算合并后的完整文件哈希
    if calculatedHash != chunkRecord.FileHash {
        os.Remove(filePath)                                      // 哈希不匹配，删除并报错
        return nil, errors.New("file hash mismatch")
    }
}
```

这个验证是正确的 —— 在所有分片合并成完整文件之后，计算整体 SHA256 并与客户端提供的预期哈希比对。

### 局限性与替代方案

**局限性**：
- 当前方案只在全部分片上传完成并合并后才能发现数据损坏。如果第 3 个分片损坏了，需要等到第 200 个分片都上传完、合并完才能发现，然后整个上传作废
- 服务端无法知道具体是哪个分片损坏了，客户端必须重新上传所有分片

**替代方案**：
- **分片级哈希验证**：客户端在 `InitUpload` 时提供每个分片的 MD5/CRC32 列表（或在上传每个分片时 Header 中传 `Content-MD5`）。服务端收到分片后立即验证，失败的分片立即要求客户端重传。这是 S3 Multipart Upload 的做法
- **分片级 CRC32**：比 SHA256 快得多（10x+），适合传输完整性校验（不需要密码学强度）。HTTP 标准也定义了 `Content-CRC32` 头部

---

## 修改文件总览

| 文件 | 修改内容 |
|------|---------|
| `internal/config/config.go` | JWT Secret 环境变量覆盖 + 安全校验 |
| `internal/handler/file.go` | Content-Disposition 安全编码、http.ServeContent 替换手动 Range、分片上传流式传输 |
| `internal/handler/share.go` | download_token 机制替代 URL 明文密码、Content-Disposition 安全编码、http.ServeContent |
| `internal/service/file.go` | UploadFile/DeleteFile 事务化、deleteFolderRecursive 批量优化、MoveFile 循环引用检查、新增 DownloadFileByPath |
| `internal/service/storage.go` | SaveChunk 流式写入、DeleteFile 事务化引用计数 |
| `internal/service/chunk.go` | UUID uploadID、UploadChunk 流式接口、移除分片哈希死代码 |
| `internal/service/embedding.go` | Worker 优雅退出（context + WaitGroup + Shutdown） |
| `cmd/server/main.go` | Graceful Shutdown、清理任务实现、context 传递 |
