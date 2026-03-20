package handler

import (
	"GoDisk/internal/middleware"
	"GoDisk/internal/model"
	"GoDisk/internal/pkg/response"
	"GoDisk/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminHandler 管理员功能
type AdminHandler struct {
	embSvc service.EmbeddingService
}

func NewAdminHandler(embSvc service.EmbeddingService) *AdminHandler {
	return &AdminHandler{
		embSvc: embSvc,
	}
}

// RegenerateAllEmbeddings 为所有文件重新生成embedding
// @Summary 重新生成所有文件的embedding
// @Description 为所有没有embedding的文件生成向量
// @Tags admin
// @Produce json
// @Security Bearer
// @Success 200 {object} response.Response
// @Router /api/admin/regenerate-embeddings [post]
func (h *AdminHandler) RegenerateAllEmbeddings(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// 查询所有文件
	var files []model.File
	if err := h.embSvc.GetDB().Where("user_id = ? AND is_directory = ?", userID, false).Find(&files).Error; err != nil {
		response.InternalError(c, err.Error())
		return
	}

	// 统计
	type Result struct {
		Success int `json:"success"`
		Skipped int `json:"skipped"`
		Failed  int `json:"failed"`
		Total   int `json:"total"`
	}
	result := Result{Total: len(files)}

	// 异步处理
	go func() {
		for _, file := range files {
			// 检查是否已有embedding
			if file.EmbeddingID != "" {
				result.Skipped++
				continue
			}

			// 生成embedding (放入异步队列)
			h.embSvc.AsyncEmbeddingTask(file.ID, false, userID, "")

			// 避免请求过快
			result.Success++
		}
	}()

	response.Success(c, gin.H{
		"message": "正在后台生成embedding，请稍候...",
		"total":   len(files),
	})
}

// GetEmbeddingStatus 获取embedding状态
// @Summary 获取embedding状态
// @Description 查看有多少文件已生成embedding
// @Tags admin
// @Produce json
// @Security Bearer
// @Success 200 {object} response.Response
// @Router /api/admin/embedding-status [get]
func (h *AdminHandler) GetEmbeddingStatus(c *gin.Context) {
	userID := middleware.MustGetUserID(c)

	// 统计文件总数
	var totalFiles int64
	h.embSvc.GetDB().Model(&model.File{}).Where("user_id = ? AND is_directory = ?", userID, false).Count(&totalFiles)

	// 统计已有embedding的文件数
	var embeddedFiles int64
	h.embSvc.GetDB().Model(&model.File{}).
		Where("user_id = ? AND is_directory = ? AND embedding_id != ?", userID, false, "").
		Count(&embeddedFiles)

	coveragePct := 0.0
	if totalFiles > 0 {
		coveragePct = float64(embeddedFiles) / float64(totalFiles) * 100
	}
	response.Success(c, gin.H{
		"total_files":      totalFiles,
		"embedded_files":   embeddedFiles,
		"not_embedded":     totalFiles - embeddedFiles,
		"coverage_percent": coveragePct,
	})
}
