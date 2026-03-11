package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 统一响应结构
type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// PageData 分页数据
type PageData struct {
	Total int64       `json:"total"`
	Items interface{} `json:"items"`
	Page  int         `json:"page"`
	Size  int         `json:"size"`
}

// Success 成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data:    data,
	})
}

// Error 错误响应
func Error(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, Response{
		Code:    code,
		Message: message,
	})
}

// ErrorWithStatus 带HTTP状态码的错误响应
func ErrorWithStatus(c *gin.Context, httpStatus int, code int, message string) {
	c.JSON(httpStatus, Response{
		Code:    code,
		Message: message,
	})
}

// Page 分页响应
func Page(c *gin.Context, total int64, items interface{}, page, size int) {
	c.JSON(http.StatusOK, Response{
		Code:    0,
		Message: "success",
		Data: PageData{
			Total: total,
			Items: items,
			Page:  page,
			Size:  size,
		},
	})
}

// 常用错误码
const (
	CodeSuccess          = 0
	CodeInvalidParams    = 40001
	CodeUnauthorized     = 40101
	CodeForbidden        = 40301
	CodeNotFound         = 40401
	CodeConflict         = 40901
	CodeInternalError    = 50001
	CodeStorageLimit     = 50201
	CodeFileNotFound     = 40402
	CodeUploadFailed     = 50002
	CodeChunkUploadError = 50003
	CodeShareExpired     = 41001
	CodeShareInvalid     = 40403
)

// 常用响应函数
func BadRequest(c *gin.Context, message string) {
	Error(c, CodeInvalidParams, message)
}

func Unauthorized(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusUnauthorized, CodeUnauthorized, message)
}

func Forbidden(c *gin.Context, message string) {
	ErrorWithStatus(c, http.StatusForbidden, CodeForbidden, message)
}

func NotFound(c *gin.Context, message string) {
	Error(c, CodeNotFound, message)
}

func InternalError(c *gin.Context, message string) {
	Error(c, CodeInternalError, message)
}

func Conflict(c *gin.Context, message string) {
	Error(c, CodeConflict, message)
}

func StorageLimitExceeded(c *gin.Context) {
	Error(c, CodeStorageLimit, "storage limit exceeded")
}

func FileNotFound(c *gin.Context) {
	Error(c, CodeFileNotFound, "file not found")
}

func UploadFailed(c *gin.Context, message string) {
	Error(c, CodeUploadFailed, message)
}
