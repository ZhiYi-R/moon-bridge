package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ZhiYi-R/moon-bridge/internal/config"
)

// ---- JSON response helpers ----

func respondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if data != nil {
		json.NewEncoder(w).Encode(data)
	}
}

func respondError(w http.ResponseWriter, status int, code, msg string) {
	respondJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": msg,
		},
	})
}

// ---- Pagination ----

type paginationParams struct {
	Offset int
	Limit  int
}

func parsePagination(r *http.Request) paginationParams {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return paginationParams{Offset: offset, Limit: limit}
}

type paginatedResponse struct {
	Data   any `json:"data"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// ---- Auth middleware ----

// AuthMiddleware returns an HTTP middleware that checks Bearer token auth
// and optionally checks store availability.
// The tokenProvider is called per-request to get the expected token
// (allowing it to change dynamically via runtime).
// The storeAvailable function, when non-nil, is called per-request to verify
// the store is available; if it returns false, a 503 is returned.
func AuthMiddleware(tokenProvider func() string, storeAvailable func() bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if storeAvailable != nil && !storeAvailable() {
				respondError(w, http.StatusServiceUnavailable, "store_unavailable", "配置存储不可用")
				return
			}
			token := tokenProvider()
			if token != "" {
				auth := r.Header.Get("Authorization")
				if !strings.HasPrefix(auth, "Bearer ") {
					respondError(w, http.StatusUnauthorized, "invalid_auth", "认证失败: 缺少 Authorization header")
					return
				}
				if strings.TrimSpace(auth[7:]) != token {
					respondError(w, http.StatusUnauthorized, "invalid_auth", "认证失败: 无效的认证令牌")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---- Secret masking ----

// maskAPIKey masks an API key showing only the first 4 and last 4 characters.
func maskAPIKey(key string) string {
	if len(key) <= 8 {
		if key == "" {
			return ""
		}
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// maskSessionKey partially masks a session key for the sessions endpoint.
func maskSessionKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****"
}

// maskFileConfigSecrets masks secret fields in a FileConfig before serialization.
func maskFileConfigSecrets(fc *config.FileConfig) {
	for key, def := range fc.Providers {
		def.APIKey = maskAPIKey(def.APIKey)
		ws := def.WebSearch
		ws.TavilyAPIKey = maskAPIKey(ws.TavilyAPIKey)
		ws.FirecrawlAPIKey = maskAPIKey(ws.FirecrawlAPIKey)
		def.WebSearch = ws
		fc.Providers[key] = def
	}

	fc.Server.AuthToken = maskAPIKey(fc.Server.AuthToken)

	fc.Proxy.Response.APIKey = maskAPIKey(fc.Proxy.Response.APIKey)
	fc.Proxy.Anthropic.APIKey = maskAPIKey(fc.Proxy.Anthropic.APIKey)

	fc.WebSearch.TavilyAPIKey = maskAPIKey(fc.WebSearch.TavilyAPIKey)
	fc.WebSearch.FirecrawlAPIKey = maskAPIKey(fc.WebSearch.FirecrawlAPIKey)
}

// sortStrings sorts a slice of strings in place (alias for sort.Strings).
func sortStrings(s []string) { sort.Strings(s) }
