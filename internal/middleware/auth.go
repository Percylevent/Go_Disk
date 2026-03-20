package middleware

import (
	"strings"

	"GoDisk/internal/config"
	"GoDisk/internal/pkg/jwt"
	"GoDisk/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// AuthMiddleware JWT认证中间件
func AuthMiddleware() gin.HandlerFunc {
	cfg := config.Get()
	return func(c *gin.Context) {
		var tokenString string

		// 优先从Authorization header获取token
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			// 解析Bearer token
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				tokenString = parts[1]
			}
		}

		// 如果header中没有，尝试从URL参数获取（用于文件下载等场景）
		if tokenString == "" {
			tokenString = c.Query("token")
		}

		// 如果都没有，返回未授权
		if tokenString == "" {
			response.Unauthorized(c, "missing authorization header")
			c.Abort()
			return
		}

		// 解析token
		claims, err := jwt.ParseToken(tokenString, cfg.JWT.Secret)
		if err != nil {
			response.Unauthorized(c, "invalid or expired token")
			c.Abort()
			return
		}

		// 将用户信息存储到context中
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)

		c.Next()
	}
}

// GetUserID 从context获取用户ID
func GetUserID(c *gin.Context) (uint, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0, false
	}

	// 尝试类型断言
	uid, ok := userID.(uint)
	if !ok {
		// 如果uint失败，尝试float64 (JSON数字默认是float64)
		if f, ok := userID.(float64); ok {
			return uint(f), true
		}
		return 0, false
	}
	return uid, true
}

// MustGetUserID 从context获取用户ID（必须存在）
func MustGetUserID(c *gin.Context) uint {
	userID, _ := GetUserID(c)
	return userID
}

// GetUsername 从context获取用户名
func GetUsername(c *gin.Context) (string, bool) {
	username, exists := c.Get("username")
	if !exists {
		return "", false
	}
	return username.(string), true
}
