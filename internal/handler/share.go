package handler

import (
	"GoDisk/internal/middleware"
	"GoDisk/internal/model"
	"GoDisk/internal/pkg/response"
	"GoDisk/internal/service"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strconv"
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
// @Summary 创建分享链接
// @Description 为文件创建分享链接
// @Tags shares
// @Accept json
// @Produce json
// @Security Bearer
// @Param request body CreateShareRequest true "分享信息"
// @Success 200 {object} response.Response{data=model.Share}
// @Router /api/shares/create [post]
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
// @Summary 获取分享列表
// @Description 获取当前用户的所有分享
// @Tags shares
// @Produce json
// @Security Bearer
// @Param page query int false "页码" default(1)
// @Param size query int false "每页数量" default(20)
// @Success 200 {object} response.Response{data=[]model.Share}
// @Router /api/shares [get]
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
// @Summary 取消分享
// @Description 删除分享链接
// @Tags shares
// @Produce json
// @Security Bearer
// @Param id path int true "分享ID"
// @Success 200 {object} response.Response
// @Router /api/shares/:id [delete]
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
// @Summary 访问分享链接
// @Description 通过分享码访问分享的文件信息
// @Tags shares
// @Produce json
// @Param code path string true "分享码"
// @Success 200 {object} response.Response{data=model.Share}
// @Router /s/:code [get]
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

// VerifyShare 验证分享密码
// @Summary 验证分享密码
// @Description 验证分享链接的访问密码
// @Tags shares
// @Accept json
// @Produce json
// @Param code path string true "分享码"
// @Param request body map[string]string true "密码"
// @Success 200 {object} response.Response
// @Router /s/:code/verify [post]
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

	response.Success(c, gin.H{"has_password": true, "verified": true})
}

// DownloadShare 下载分享文件
// @Summary 下载分享文件
// @Description 下载分享链接的文件
// @Tags shares
// @Produce application/octet-stream
// @Param code path string true "分享码"
// @Router /s/:code/download [get]
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

	// 检查密码
	if share.AccessPassword != "" {
		password := c.Query("password")
		if password == "" {
			response.Unauthorized(c, "password required")
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(share.AccessPassword), []byte(password)); err != nil {
			response.Unauthorized(c, "invalid password")
			return
		}
	}

	// 打开文件
	file, err := os.Open(share.File.FilePath)
	if err != nil {
		response.InternalError(c, "failed to open file")
		return
	}
	defer file.Close()

	// 设置响应头
	c.Header("Content-Disposition", "attachment; filename=\""+share.File.FileName+"\"")
	c.Header("Content-Type", share.File.MimeType)
	c.Header("Content-Length", strconv.FormatInt(share.File.FileSize, 10))

	// 复制文件内容
	io.Copy(c.Writer, file)

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
