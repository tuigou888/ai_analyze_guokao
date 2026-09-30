"""OCR worker 的公共部分：协议、断点续跑、结果配对。

两个 worker（公式识别、文本识别）共用同一套协议，抽在这里避免两处各写一遍：

* 输入是一个 JSONL manifest，每行 {"sha256","url","path"}
* 输出是一个 JSONL，逐条 flush，按 sha256 可续跑
* 日志一律走 stderr，结果只写文件，避免两类信息混流
"""

import hashlib
import json
import os
import sys
import tempfile

# PaddleOCR 支持的输入后缀。不在这张表里的格式（实测数据里有 .gif 与 .jfif）
# 会被 predict **静默跳过**——不报错、不返回结果，这是最危险的失败形态。
SUPPORTED_EXTS = {
    ".bmp", ".dib", ".jpeg", ".jpg", ".png", ".webp",
    ".pbm", ".pgm", ".ppm", ".pnm", ".sr", ".ras",
    ".tiff", ".tif", ".pdf",
}


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def read_manifest(path: str) -> list:
    """读 manifest 并按 sha256 去重（同内容不同文件名只识别一次）。"""
    items, seen = [], set()
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                log(f"[warn] manifest 行无法解析，跳过: {line[:80]}")
                continue
            h = rec.get("sha256")
            if not h or h in seen:
                continue
            seen.add(h)
            items.append(rec)
    return items


def load_done(path: str) -> set:
    """已完成的 sha256 集合，用于断点续跑。末行可能被截断，忽略即可。"""
    done = set()
    if not os.path.exists(path):
        return done
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            if rec.get("sha256"):
                done.add(rec["sha256"])
    return done


def _to_png(path: str) -> str:
    """把不支持的格式转成临时 PNG。文件名按原路径哈希，重复调用可复用。"""
    from PIL import Image

    key = hashlib.sha256(path.encode()).hexdigest()[:16]
    tmp = os.path.join(tempfile.gettempdir(), f"gk_ocr_{key}.png")
    if os.path.exists(tmp):
        return tmp
    with Image.open(path) as im:
        im.convert("RGB").save(tmp)
    return tmp


def _one(model, path: str, batch):
    """识别单张，返回结果对象或异常对象。"""
    try:
        results = (list(model.predict([path], batch_size=1)) if batch is not None
                   else list(model.predict([path])))
    except Exception as exc:
        return exc
    if results:
        return results[0]

    ext = os.path.splitext(path)[1].lower()
    if ext in SUPPORTED_EXTS:
        return RuntimeError("predict 未返回结果（文件可能已损坏）")
    # 数据集中存在 .gif / .jfif，PaddleOCR 不支持这些后缀，会静默跳过。
    # 图片本身是好的，转成 PNG 再试一次。
    try:
        png = _to_png(path)
    except Exception as exc:
        return RuntimeError(f"格式 {ext} 不受支持且转 PNG 失败: {exc}")
    try:
        results = (list(model.predict([png], batch_size=1)) if batch is not None
                   else list(model.predict([png])))
    except Exception as exc:
        return exc
    return results[0] if results else RuntimeError(f"格式 {ext} 转 PNG 后仍未返回结果")


def predict_aligned(model, paths: list, batch=None) -> list:
    """返回与 paths **等长**的结果列表。

    为什么不能写成 `zip(chunk, list(model.predict(paths)))`：predict 会静默跳过
    不支持的格式，返回列表因此比输入短。一旦短了，zip 会让后续每一项与"下一张图"
    的文本配对——sha256 与文本错位，错误数据被静默写库，比直接报错危险得多。

    因此：长度不匹配就逐张重跑（拿到正确配对），仍然拿不到的补一个异常对象，
    由调用方记成 error 而不是丢掉。
    """
    try:
        results = (list(model.predict(paths, batch_size=batch)) if batch is not None
                   else list(model.predict(paths)))
        if len(results) == len(paths):
            return results
        log(f"[warn] predict 返回 {len(results)} 条，输入 {len(paths)} 张"
            f"——有文件被静默跳过，改为逐张重跑")
    except Exception as exc:
        log(f"[warn] 批量识别失败（{len(paths)} 张），改为逐张: {exc}")

    return [_one(model, p, batch) for p in paths]
