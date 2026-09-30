package serve

import (
	"net/http"
	"strings"
)

// handleStatic 托管前端构建产物（web/dist）。
//
// 前端是 SPA：任何未匹配到静态文件的路径都回退到 index.html，让前端路由接管。
// 未注入 Assets 时给一个说明页——开发阶段访问根路径看到 404 会让人误判服务没起来。
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if s.Assets == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("gk 服务已启动。\n前端尚未构建：请在 web/ 下执行 npm install && npm run build，" +
			"产物会被内嵌进二进制。\n管理接口位于 /api/admin/*。\n"))
		return
	}

	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	f, err := s.Assets.Open(p)
	if err != nil {
		// SPA 回退
		s.serveIndex(w, r)
		return
	}
	defer f.Close()

	seeker, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	})
	if !ok {
		s.serveIndex(w, r)
		return
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		s.serveIndex(w, r)
		return
	}
	// 带内容哈希的资源名（Vite 默认）可以长缓存；index.html 必须每次校验。
	// 注意 p 已去掉开头的 "/"，所以这里判断的是 "assets/" 而不是 "/assets/"。
	if strings.HasPrefix(p, "assets/") || strings.Contains(p, ".immutable.") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, p, info.ModTime(), seeker)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	f, err := s.Assets.Open("index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	seeker, ok := f.(interface {
		Read([]byte) (int, error)
		Seek(int64, int) (int64, error)
	})
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, _ := f.Stat()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", info.ModTime(), seeker)
}
