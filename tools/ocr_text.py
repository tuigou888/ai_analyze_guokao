#!/usr/bin/env python3
"""题目图 → 文本 worker（表格/图表里的文字）。

与 ocr_formula.py 同一套协议（见 ocr_common.py），但用 PP-OCRv5 文本识别，
并且**按坐标重建表格行**。

为什么要重建行：题目图里最要紧的是资料分析的材料表格（"年份 × 指标"矩阵）。
把识别出的文本按顺序平铺成一行，表格的行列关系就丢了，模型读到的是
"年份 粮食 油料 棉花 2006 2893 201 6.5 …" 这种无法对齐的序列。
按 y 坐标聚类成行、行内按 x 排序、用 | 分隔，得到的是模型能读懂的表格。
"""

import argparse
import json
import sys
import time
import traceback

from ocr_common import load_done, log, predict_aligned, read_manifest


def _xy(item):
    """把 rec_boxes / rec_polys 的元素统一成 (x_min, y_min, x_max, y_max)。"""
    arr = list(item)
    if len(arr) == 4 and not hasattr(arr[0], "__len__"):
        return float(arr[0]), float(arr[1]), float(arr[2]), float(arr[3])
    xs = [float(p[0]) for p in arr]
    ys = [float(p[1]) for p in arr]
    return min(xs), min(ys), max(xs), max(ys)


def rebuild_rows(res: dict) -> list:
    """按 y 坐标把识别结果聚类成表格行。"""
    texts = res.get("rec_texts") or []
    boxes = res.get("rec_boxes")
    if boxes is None or len(boxes) != len(texts):
        boxes = res.get("rec_polys")
    if boxes is None or len(boxes) != len(texts):
        return [t for t in texts if t]

    items = []
    for t, b in zip(texts, boxes):
        if not t:
            continue
        try:
            x0, y0, x1, y1 = _xy(b)
        except Exception:
            continue
        items.append({"t": t, "x": x0, "yc": (y0 + y1) / 2, "h": max(y1 - y0, 1.0)})
    if not items:
        return []

    items.sort(key=lambda i: i["yc"])
    rows, cur = [], [items[0]]
    for it in items[1:]:
        ref_h = cur[0]["h"] or 10.0
        # 同一行的判定：y 中心差不超过行高的一半多一点。
        # 阈值太严会把同一行切成两行，太松会把相邻行粘在一起。
        if abs(it["yc"] - cur[0]["yc"]) <= ref_h * 0.6:
            cur.append(it)
        else:
            rows.append(cur)
            cur = [it]
    rows.append(cur)

    out = []
    for r in rows:
        r.sort(key=lambda i: i["x"])
        out.append(" | ".join(i["t"] for i in r))
    return out


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--batch", type=int, default=8)
    args = ap.parse_args()

    items = read_manifest(args.manifest)
    done = load_done(args.out)
    todo = [it for it in items if it["sha256"] not in done]
    log(f"[text-shard] manifest {len(items)} 条，已完成 {len(done)} 条，待识别 {len(todo)} 条")
    if not todo:
        return 0

    from paddleocr import PaddleOCR

    t0 = time.time()
    ocr = PaddleOCR(
        use_doc_orientation_classify=False,
        use_doc_unwarping=False,
        use_textline_orientation=False,
    )
    log(f"[text-shard] 模型加载完成，耗时 {time.time()-t0:.1f}s")

    out = open(args.out, "a", encoding="utf-8")
    ok = failed = 0
    t0 = time.time()

    for start in range(0, len(todo), args.batch):
        chunk = todo[start:start + args.batch]
        results = predict_aligned(ocr, [it["path"] for it in chunk])

        for it, res in zip(chunk, results):
            rec = {"sha256": it["sha256"], "model": "PP-OCRv5"}
            if isinstance(res, Exception):
                rec |= {"status": "error", "error": f"{type(res).__name__}: {res}"}
                failed += 1
            else:
                try:
                    data = res.json if hasattr(res, "json") else res
                    inner = data.get("res", data)
                    rec |= {"status": "ok", "tex": "\n".join(rebuild_rows(inner))}
                    ok += 1
                except Exception as exc:
                    rec |= {"status": "error", "error": f"解析结果失败: {exc}"}
                    failed += 1
            out.write(json.dumps(rec, ensure_ascii=False) + "\n")
            out.flush()

        n = min(start + args.batch, len(todo))
        dt = time.time() - t0
        log(f"[text-shard] {n}/{len(todo)}  ok={ok} err={failed}  {n/dt:.1f} 张/秒")

    out.close()
    log(f"[text-shard] 完成 ok={ok} err={failed}，耗时 {time.time()-t0:.1f}s")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        log("[text-shard] 被中断，已完成的识别结果已落盘")
        sys.exit(130)
    except Exception:
        traceback.print_exc()
        sys.exit(1)
