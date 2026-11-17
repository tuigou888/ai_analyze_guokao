package study

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// sanitizeText 收敛用户自由文本的字符集（安全审计 L-2）。
//
// 前端全部走 {{ }} 插值，当前不可利用；但输入侧不收敛意味着控制字符、零宽
// 字符与 <script> 字面量都能入库，一旦将来新增渲染点（v-html、导出 PDF、
// 日志高亮）就成了 XSS 面。这里在写入前剔除不可见字符，并拒绝尖括号与
// 反引号——正常昵称/简介用不到它们。
func sanitizeText(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			return "", false
		case r == '\t':
			b.WriteRune(' ')
		case r == '\n' || r == '\r':
			b.WriteRune('\n')
		case r == '<' || r == '>' || r == '`':
			return "", false
		case unicode.IsControl(r):
			return "", false
		case unicode.In(r, unicode.Cf):
			// Cf 覆盖零宽连接符、方向控制符等不可见格式字符。
			return "", false
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	return out, utf8.ValidString(out)
}
