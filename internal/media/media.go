package media

// 图片类型，与 ingest 包写入 image.kind 的取值保持一致。
// 两边都用字面量，改动时必须同步（值来自源数据的目录名）。
const (
	KindFormula  = "公式图"
	KindQuestion = "题目图"
)

// imageDirName 数据集里的图片根目录名。
const imageDirName = "90-图片"

// OCR 引擎标识，写入 image.ocr_engine。
// 两类图的识别结果形态不同，回填时要区别对待：公式结果是 LaTeX（需判断要不要包 $），
// 题目图结果是按坐标重建的表格文本（直接插入）。
const (
	EngineFormula = "formula" // PP-FormulaNet → LaTeX
	EngineText    = "text"    // PP-OCRv5 → 表格文本
)

// ScriptFormula / ScriptText 是配套的 Python worker。
const (
	ScriptFormula = "tools/ocr_formula.py"
	ScriptText    = "tools/ocr_text.py"
)

// PlaceholderOpen / PlaceholderClose 与 ingest 包写入的占位符格式一致。
// 回填时靠它定位公式图的位置：⟦IMG:公式图/formula-xxxx.png⟧
const (
	PlaceholderOpen  = "⟦IMG:"
	PlaceholderClose = "⟧"
)
