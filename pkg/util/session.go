package util

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// SetSession sets the session
func SetSession(c *gin.Context, list map[string]interface{}) {
	s := sessions.Default(c)
	for key, value := range list {
		s.Set(key, value)
	}

	err := s.Save()
	if err != nil {
		Log().Warning("Failed to set session value: %s", err)
	}
}

// GetSession gets the session
func GetSession(c *gin.Context, key any) interface{} {
	s := sessions.Default(c)
	return s.Get(key)
}

// DeleteSession deletes the session
func DeleteSession(c *gin.Context, key any) {
	s := sessions.Default(c)
	s.Delete(key)
	s.Save()
}

// ClearSession clears the session
func ClearSession(c *gin.Context) {
	s := sessions.Default(c)
	s.Clear()
	s.Save()
}
