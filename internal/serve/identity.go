package serve

import (
	"net/http"
	"strconv"
)

// This optional page precondition can only reject. The authenticated Cookie
// principal, never this header, continues to select every business query.
func expectedUserMatches(w http.ResponseWriter, r *http.Request, userID int64) bool {
	values, present := r.Header[http.CanonicalHeaderKey("X-GK-Expected-User")]
	if !present {
		return true
	}
	if len(values) != 1 || values[0] != strconv.FormatInt(userID, 10) {
		if len(values) != 1 {
			writeErr(w, 400, "无效的页面账户前提")
			return false
		}
		expected, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || expected <= 0 || values[0] != strconv.FormatInt(expected, 10) {
			writeErr(w, 400, "无效的页面账户前提")
			return false
		}
		writeJSON(w, 409, map[string]any{"code": "account_changed", "error": "登录账户已变化，请记录未保存输入后重新进入个人中心"})
		return false
	}
	return true
}
