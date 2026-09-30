package ingest

import (
	"path"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"ai_analyze_guokao/internal/model"
)

// 图片占位符。写成 ⟦IMG:类型/文件名⟧ 而不是直接丢掉 <img>，
// 因为图片位置本身是语义信息（"下图中"、"根据上表"），蒸馏时模型必须知道此处有图；
// 且占位符可逆，L3 把公式图 OCR 成 LaTeX 后可以直接回填。
const (
	ImgOpen  = "⟦IMG:"
	ImgClose = "⟧"
)

// HTMLToText 把一段 HTML 片段转成纯文本，图片就地转为占位符。
func HTMLToText(frag string) (string, []model.ImageRef) {
	frag = strings.TrimSpace(frag)
	if frag == "" {
		return "", nil
	}

	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(frag), ctx)
	if err != nil {
		// 片段无法解析时退化为粗暴去标签，保证不丢内容。
		return collapseLines(StripTags(frag)), nil
	}

	var (
		sb   strings.Builder
		imgs []model.ImageRef
	)
	for _, n := range nodes {
		walk(n, &sb, &imgs, "")
	}
	return collapseLines(sb.String()), imgs
}

// walk 深度优先遍历，把节点内容写入 sb。inWhere 标记当前所处的语义位置
// （stem/material/explanation），用于给图片引用打上来源标签。
func walk(n *html.Node, sb *strings.Builder, imgs *[]model.ImageRef, inWhere string) {
	switch n.Type {
	case html.TextNode:
		sb.WriteString(n.Data)

	case html.ElementNode:
		switch n.DataAtom {
		case atom.Img:
			ref := imageRef(attr(n, "src"))
			if ref.Name != "" {
				ref.In = inWhere
				*imgs = append(*imgs, ref)
				sb.WriteString(ImgOpen + ref.URL + ImgClose)
			}
			return
		case atom.Br:
			sb.WriteString("\n")
			return
		case atom.P, atom.Div, atom.Li, atom.Tr:
			// 块级元素前后断行，避免相邻段落文字粘连。
			sb.WriteString("\n")
			defer sb.WriteString("\n")
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, sb, imgs, inWhere)
	}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// imageRef 把 src 归一化成 (类型, 文件名, 相对路径)。
// 输入形如 `../90-图片/题目图/183ef2629055a97.png`。
func imageRef(src string) model.ImageRef {
	src = strings.TrimSpace(src)
	if src == "" {
		return model.ImageRef{}
	}
	clean := path.Clean(strings.ReplaceAll(src, "\\", "/"))
	dir, name := path.Split(clean)
	kind := path.Base(strings.TrimSuffix(dir, "/"))
	if kind == "." || kind == "/" || kind == "" {
		kind = "未知"
	}
	return model.ImageRef{Kind: kind, Name: name, URL: kind + "/" + name}
}

// StripTags 粗暴去标签，仅作为 HTML 解析失败时的兜底。
func StripTags(s string) string {
	var sb strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				sb.WriteRune(r)
			}
		}
	}
	return html.UnescapeString(sb.String())
}

// collapseLines 去掉每行首尾空白、把连续空行压成一个换行。
func collapseLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := true // 抑制开头的空行
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			if !blank {
				out = append(out, "")
				blank = true
			}
			continue
		}
		out = append(out, l)
		blank = false
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}
