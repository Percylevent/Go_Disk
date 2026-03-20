package handler

import (
	"GoDisk/internal/config"
	"GoDisk/internal/middleware"
	"GoDisk/internal/model"
	"GoDisk/internal/pkg/response"
	"GoDisk/internal/service"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ShareHandler struct {
	db      *gorm.DB
	fileSvc *service.FileService
	embSvc  service.EmbeddingService
}

func NewShareHandler(db *gorm.DB, fileSvc *service.FileService, embSvc service.EmbeddingService) *ShareHandler {
	return &ShareHandler{
		db:      db,
		fileSvc: fileSvc,
		embSvc:  embSvc,
	}
}

// CreateShareRequest 创建分享请求
type CreateShareRequest struct {
	FileID         uint   `json:"file_id" binding:"required"`
	AccessPassword string `json:"access_password"`
	DownloadLimit  int    `json:"download_limit"` // -1表示无限
	ExpireDays     int    `json:"expire_days"`    // 0表示永不过期
}

// CreateShare 创建分享链接
func (h *ShareHandler) CreateShare(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	var req CreateShareRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// 检查文件是否存在且属于当前用户
	var file model.File
	if err := h.db.Where("id = ? AND user_id = ?", req.FileID, userID).First(&file).Error; err != nil {
		response.NotFound(c, "file not found")
		return
	}

	// 生成分享码
	shareCode, err := generateShareCode()
	if err != nil {
		response.InternalError(c, "failed to generate share code")
		return
	}

	// 处理密码
	var hashedPassword string
	if req.AccessPassword != "" {
		hashedBytes, err := bcrypt.GenerateFromPassword([]byte(req.AccessPassword), bcrypt.DefaultCost)
		if err != nil {
			response.InternalError(c, "failed to hash password")
			return
		}
		hashedPassword = string(hashedBytes)
	}

	// 处理过期时间
	var expireAt *time.Time
	if req.ExpireDays > 0 {
		expireTime := time.Now().AddDate(0, 0, req.ExpireDays)
		expireAt = &expireTime
	}

	// 创建分享记录
	share := &model.Share{
		UserID:         userID,
		FileID:         req.FileID,
		ShareCode:      shareCode,
		AccessPassword: hashedPassword,
		DownloadLimit:  req.DownloadLimit,
		DownloadCount:  0,
		ExpireAt:       expireAt,
	}

	if err := h.db.Create(share).Error; err != nil {
		response.InternalError(c, "failed to create share")
		return
	}

	// 预加载文件信息
	h.db.Preload("File").First(share, share.ID)

	response.Success(c, share)
}

// ListShares 获取我的分享列表
func (h *ShareHandler) ListShares(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	size := 20
	if sizeStr := c.Query("size"); sizeStr != "" {
		if s, err := strconv.Atoi(sizeStr); err == nil && s > 0 {
			size = s
		}
	}

	offset := (page - 1) * size

	var shares []*model.Share
	var total int64

	h.db.Model(&model.Share{}).Where("user_id = ?", userID).Count(&total)
	h.db.Where("user_id = ?", userID).
		Preload("File").
		Order("created_at DESC").
		Limit(size).
		Offset(offset).
		Find(&shares)

	response.Page(c, total, shares, page, size)
}

// DeleteShare 取消分享
func (h *ShareHandler) DeleteShare(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	shareIDStr := c.Param("id")
	shareID, err := strconv.ParseUint(shareIDStr, 10, 32)
	if err != nil {
		response.BadRequest(c, "invalid share id")
		return
	}

	// 检查分享是否存在且属于当前用户
	var share model.Share
	if err := h.db.Where("id = ? AND user_id = ?", shareID, userID).First(&share).Error; err != nil {
		response.NotFound(c, "share not found")
		return
	}

	if err := h.db.Delete(&share).Error; err != nil {
		response.InternalError(c, "failed to delete share")
		return
	}

	response.Success(c, nil)
}

// AccessShare 访问分享链接
func (h *ShareHandler) AccessShare(c *gin.Context) {
	shareCode := c.Param("code")

	var share model.Share
	if err := h.db.Where("share_code = ?", shareCode).Preload("File").First(&share).Error; err != nil {
		response.NotFound(c, "share not found")
		return
	}

	// 检查是否过期
	if share.IsExpired() {
		response.Error(c, response.CodeShareExpired, "share has expired")
		return
	}

	// 构建响应数据
	responseData := map[string]interface{}{
		"id":             share.ID,
		"user_id":        share.UserID,
		"file_id":        share.FileID,
		"share_code":     share.ShareCode,
		"download_limit": share.DownloadLimit,
		"download_count": share.DownloadCount,
		"expire_at":      share.ExpireAt,
		"created_at":     share.CreatedAt,
		"has_password":   share.AccessPassword != "",
		"file":           share.File,
	}

	response.Success(c, responseData)
}

// VerifyShare 验证分享密码，成功后返回短时效 download_token
func (h *ShareHandler) VerifyShare(c *gin.Context) {
	shareCode := c.Param("code")

	var req struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var share model.Share
	if err := h.db.Where("share_code = ?", shareCode).First(&share).Error; err != nil {
		response.NotFound(c, "share not found")
		return
	}

	// 检查是否设置了密码
	if share.AccessPassword == "" {
		response.Success(c, gin.H{"has_password": false})
		return
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(share.AccessPassword), []byte(req.Password)); err != nil {
		response.Error(c, response.CodeUnauthorized, "invalid password")
		return
	}

	// 密码验证成功，生成短时效 download_token（10分钟有效）
	token := generateDownloadToken(shareCode, config.Get().JWT.Secret)

	response.Success(c, gin.H{
		"has_password":   true,
		"verified":       true,
		"download_token": token,
	})
}

// DownloadShare 下载分享文件
func (h *ShareHandler) DownloadShare(c *gin.Context) {
	shareCode := c.Param("code")

	var share model.Share
	if err := h.db.Where("share_code = ?", shareCode).Preload("File").First(&share).Error; err != nil {
		response.NotFound(c, "share not found")
		return
	}

	// 检查是否可以下载
	if !share.CanDownload() {
		if share.IsExpired() {
			response.Error(c, response.CodeShareExpired, "share has expired")
		} else {
			response.Error(c, response.CodeShareInvalid, "download limit reached")
		}
		return
	}

	// 验证访问权限：有密码的分享需要验证 download_token
	if share.AccessPassword != "" {
		token := c.Query("token")
		if token == "" {
			response.Unauthorized(c, "download_token required, please verify password first")
			return
		}

		if !validateDownloadToken(token, shareCode, config.Get().JWT.Secret) {
			response.Unauthorized(c, "invalid or expired download token")
			return
		}
	}

	// 打开文件
	file, err := h.fileSvc.DownloadFileByPath(share.File.FilePath)
	if err != nil {
		response.InternalError(c, "failed to open file")
		return
	}
	defer file.Close()

	// 使用安全的 Content-Disposition + http.ServeContent 处理 Range 请求
	c.Header("Content-Disposition", shareContentDisposition(share.File.FileName))
	c.Header("Content-Type", share.File.MimeType)
	http.ServeContent(c.Writer, c.Request, share.File.FileName, share.CreatedAt, file)

	// 更新下载计数
	share.IncrementDownload()
	h.db.Save(&share)
}

// generateShareCode 生成分享码
func generateShareCode() (string, error) {
	bytes := make([]byte, 6) // 12位十六进制字符
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// generateDownloadToken 生成短时效下载令牌（HMAC签名，10分钟有效）
// 格式: base64(shareCode:expiry:hmac_signature)
func generateDownloadToken(shareCode string, secret string) string {
	expiry := time.Now().Add(10 * time.Minute).Unix()
	data := fmt.Sprintf("%s:%d", shareCode, expiry)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	sig := hex.EncodeToString(mac.Sum(nil))
	raw := fmt.Sprintf("%s:%d:%s", shareCode, expiry, sig)
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

// validateDownloadToken 验证下载令牌
func validateDownloadToken(token string, shareCode string, secret string) bool {
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 3)
	if len(parts) != 3 {
		return false
	}
	if parts[0] != shareCode {
		return false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return false
	}
	if time.Now().Unix() > expiry {
		return false
	}
	// 验证 HMAC 签名
	data := fmt.Sprintf("%s:%d", parts[0], expiry)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[2]), []byte(expectedSig))
}

// shareContentDisposition 生成安全的 Content-Disposition 头
func shareContentDisposition(fileName string) string {
	asciiName := make([]byte, 0, len(fileName))
	for i := 0; i < len(fileName); i++ {
		if fileName[i] >= 0x20 && fileName[i] < 0x7f && fileName[i] != '"' && fileName[i] != '\\' {
			asciiName = append(asciiName, fileName[i])
		} else {
			asciiName = append(asciiName, '_')
		}
	}
	encoded := url.PathEscape(fileName)
	return `attachment; filename="` + string(asciiName) + `"; filename*=UTF-8''` + encoded
}
