package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/ZhiYi-R/moon-bridge/internal/service/api"
	"github.com/ZhiYi-R/moon-bridge/internal/session"
)

func (server *Server) ListSessions() []api.SessionInfo {
	if server.sessionManager == nil {
		return nil
	}
	return server.sessionManager.List()
}

func (server *Server) sessionForRequest(request *http.Request) *session.Session {
	if server.sessionManager == nil {
		return nil
	}
	key := sessionKeyFromRequest(request)
	if key == "" {
		return server.sessionManager.NewEphemeral()
	}
	return server.sessionManager.GetOrCreate(key, time.Now())
}

func sessionKeyFromRequest(request *http.Request) string {
	if value := strings.TrimSpace(request.Header.Get("Session_id")); value != "" {
		return "session:" + value
	}
	if value := strings.TrimSpace(request.Header.Get("X-Codex-Window-Id")); value != "" {
		return "codex-window:" + value
	}
	return ""
}
