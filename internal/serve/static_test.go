package serve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// W1 回归：静态资源路径缺失必须 404，避免 index.html 被按 .js/.png 路径缓存；
// 页面路由（无扩展名）仍然回退 index.html，由前端路由接管。
func TestStaticMissingAssetReturns404(t *testing.T) {
	s := &Server{Assets: fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<html>index</html>")},
		"assets/index-1.js":  &fstest.MapFile{Data: []byte("console.log(1)")},
		"assets/index-1.css": &fstest.MapFile{Data: []byte("body{}")},
	}}
	cases := []struct {
		path string
		code int
		body string
	}{
		{"/assets/index-1.js", 200, "console.log(1)"},
		{"/assets/no-such.js", 404, ""},
		{"/favicon.ico", 404, ""},
		{"/robots.txt", 404, ""},
		{"/", 200, "<html>index</html>"},
		{"/questions", 200, "<html>index</html>"},
		{"/admin/login", 200, "<html>index</html>"},
		{"/concepts/12", 200, "<html>index</html>"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		s.handleStatic(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		if w.Code != c.code {
			t.Errorf("GET %s -> %d, want %d", c.path, w.Code, c.code)
			continue
		}
		if c.body != "" && !strings.Contains(w.Body.String(), c.body) {
			t.Errorf("GET %s 响应缺少 %q", c.path, c.body)
		}
	}
}

// W3 回归：HEAD 应路由到 GET 处理器返回 200，而不是 405。
func TestHeadRequestsHandled(t *testing.T) {
	s, _ := fixture(t)
	ts := httptest.NewServer(s.Routes())
	t.Cleanup(ts.Close)
	for _, path := range []string{"/healthz", "/login"} {
		resp, err := http.Head(ts.URL + path)
		if err != nil {
			t.Fatalf("HEAD %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("HEAD %s -> %d, want 200", path, resp.StatusCode)
		}
	}
}
