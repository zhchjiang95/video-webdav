package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"video-server/web"
)

func TestHandlerNoRedirectLoop(t *testing.T) {
	handler := web.Handler()

	paths := []string{
		"/admin",
		"/admin/",
		"/admin/index.html",
		"/admin/dashboard",
	}

	for _, p := range paths {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("请求路径 %s 预期返回 200 OK，实际返回 %d (重定向目标: %s)", p, rec.Code, rec.Header().Get("Location"))
		}
		if ct := rec.Header().Get("Content-Type"); ct == "" {
			t.Fatalf("请求路径 %s 预期返回 Content-Type，实际为空", p)
		}
	}
}

