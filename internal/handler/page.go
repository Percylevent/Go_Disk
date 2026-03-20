package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// PageHandler 处理页面渲染
type PageHandler struct{}

// NewPageHandler 创建页面处理器
func NewPageHandler() *PageHandler {
	return &PageHandler{}
}

// SharePage 分享页面
func (h *PageHandler) SharePage(c *gin.Context) {
	shareCode := c.Param("code")
	c.HTML(http.StatusOK, "share.html", gin.H{
		"shareCode": shareCode,
	})
}

// IndexPage 主页
func (h *PageHandler) IndexPage(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", nil)
}
