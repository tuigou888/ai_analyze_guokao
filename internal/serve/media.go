package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Only the two image directories are exposed. Source notes and database files
// must never be served. Symlinks escaping the selected directory are rejected.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/media/")
	parts := strings.Split(p, "/")
	if len(parts) != 2 || (parts[0] != "题目图" && parts[0] != "公式图") || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.Contains(parts[1], "\\") {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" {
		http.NotFound(w, r)
		return
	}
	root, err := filepath.EvalSymlinks(filepath.Join(s.DataDir, "90-图片", parts[0]))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, parts[1]))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, target)
}
