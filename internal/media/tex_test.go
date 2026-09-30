package media

import "testing"

// 测试用例全部取自 502 张真实公式图的 OCR 输出（人工核对过形态），
// 不是编造的——这些分类边界是实测出来的。
func TestDisplayTeX(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		// 纯数学片段：必须包 $，否则页面会显示字面量 R_{2}
		{"分数", `\frac{9}{10}`, `$\frac{9}{10}$`},
		{"下标", `R_{2}`, `$R_{2}$`},
		{"上标", `N^{\prime}`, `$N^{\prime}$`},
		{"比号", `R1:R2=`, `$R1:R2=$`},
		{"算式无标记", `(3409+3444+3364)`, `$(3409+3444+3364)$`},
		{"减法无标记", `x-8`, `$x-8$`},
		{"纯数字范围", `4-6`, `$4-6$`},
		{"小数", `17.9`, `$17.9$`},
		{"比值", `2:3`, `$2:3$`},
		{"坐标系点", `(5,5)`, `$(5,5)$`},
		{"字母数字组合", `2a`, `$2a$`},

		// 中文文字片段：绝不能包 $，否则 KaTeX 用数学模式渲染汉字
		{"汉字词", `基期量`, `基期量`},
		{"中文加编号", `图 3`, `图 3`},
		{"中文比较式", `女研究生 > 男本科生`, `女研究生 > 男本科生`},
		{"中文单位", `4767 千桶 / 天`, `4767 千桶 / 天`},
		{"中文加号", `视频增值服务 + 其他`, `视频增值服务 + 其他`},
		{"中文年月", `2021年1-2月`, `2021年1-2月`},

		// 中文占位符 + LaTeX：是公式，要包
		{"中文占位符公式", `\frac{ 左上角数字 }{ 右下角数字 }=`, `$\frac{ 左上角数字 }{ 右下角数字 }=$`},

		// 模型已自带定界：原样保留，不重复包
		{"已定界句子", `2015 年本地网中继光缆净增线路长度 $=1157-995=162 万公里 $`,
			`2015 年本地网中继光缆净增线路长度 $=1157-995=162 万公里$`},
		{"已定界行内", `其他辅助服务产业亏损额 $=$`, `其他辅助服务产业亏损额 $=$`},

		// 边界
		{"空", ``, ``},
		{"只有空白", "  \n ", ``},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DisplayTeX(c.raw); got != c.want {
				t.Errorf("DisplayTeX(%q)\n  得到 %q\n  期望 %q", c.raw, got, c.want)
			}
		})
	}
}

func TestNormalizeTeXStripsTrailingSemicolon(t *testing.T) {
	cases := map[string]string{
		`\frac{9}{10};`: `\frac{9}{10}`,
		`\frac{9}{10}；`: `\frac{9}{10}`,
		`abc ;`:         `abc`,
		`a>b;；;`:        `a>b`,
		"多行\n输出":        `多行 输出`,
		"\t 前后空白  ":     `前后空白`,
		`正常; 分号在中间`:     `正常; 分号在中间`, // 中间的不能动
	}
	for in, want := range cases {
		if got := NormalizeTeX(in); got != want {
			t.Errorf("NormalizeTeX(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// 定界符内侧的空白必须去掉，否则 KaTeX auto-render 可能不识别为公式。
func TestTrimInsideDelimiters(t *testing.T) {
	cases := map[string]string{
		`净增 $=1157-995=162 万公里 $`: `净增 $=1157-995=162 万公里$`,
		`亏损额 $= $ 继续`:             `亏损额 $=$ 继续`,
		`$a$ 和 $b$`:               `$a$ 和 $b$`,
		`未成对 的 $`:                 `未成对 的 $`, // 单个 $ 不处理
	}
	for in, want := range cases {
		if got := NormalizeTeX(in); got != want {
			t.Errorf("NormalizeTeX(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestHasMathMarker(t *testing.T) {
	yes := []string{`\frac{a}{b}`, `a=b`, `x≥1`, `3×4`, `n^{2}`, `a_{1}`}
	no := []string{`基期量`, `图 3`, `女研究生 > 男本科生`, `4767 千桶 / 天`, `x-8`, `2:3`}
	for _, s := range yes {
		if !HasMathMarker(s) {
			t.Errorf("%q 应含数学标记", s)
		}
	}
	for _, s := range no {
		if HasMathMarker(s) {
			t.Errorf("%q 不应被判为含数学标记", s)
		}
	}
}
