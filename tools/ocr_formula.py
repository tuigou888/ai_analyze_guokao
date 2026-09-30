#!/usr/bin/env python3
"""公式图 → LaTeX 识别 worker。

由 Go 侧 `gk media ocr-formula` 以子进程方式调用，一次处理一个 shard。
协议与容错细节见 ocr_common.py。
"""

import argparse
import json
import sys
import time
import traceback

from ocr_common import load_done, log, predict_aligned, read_manifest


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--manifest", required=True, help="输入 JSONL，每行 {sha256, path}")
    ap.add_argument("--out", required=True, help="输出 JSONL（追加写）")
    ap.add_argument("--batch", type=int, default=16)
    ap.add_argument("--model", default=None, help="模型名，默认 PP-FormulaNet_plus-M")
    args = ap.parse_args()

    items = read_manifest(args.manifest)
    done = load_done(args.out)
    todo = [it for it in items if it["sha256"] not in done]
    log(f"[shard] manifest {len(items)} 条，已完成 {len(done)} 条，待识别 {len(todo)} 条")
    if not todo:
        return 0

    from paddleocr import FormulaRecognition

    t0 = time.time()
    model = FormulaRecognition(**({"model_name": args.model} if args.model else {}))
    log(f"[shard] 模型加载完成，耗时 {time.time()-t0:.1f}s")

    out = open(args.out, "a", encoding="utf-8")
    ok = failed = 0
    t0 = time.time()

    for start in range(0, len(todo), args.batch):
        chunk = todo[start:start + args.batch]
        results = predict_aligned(model, [it["path"] for it in chunk], batch=args.batch)

        for it, res in zip(chunk, results):
            rec = {"sha256": it["sha256"], "model": getattr(model, "_model_name", args.model or "")}
            if isinstance(res, Exception):
                rec |= {"status": "error", "error": f"{type(res).__name__}: {res}"}
                failed += 1
            else:
                try:
                    data = res.json if hasattr(res, "json") else res
                    tex = ((data.get("res") or {}).get("rec_formula") or "").strip()
                    # 空结果也算成功：空白小图上模型确实输出空串，
                    # 记成失败会让这些图每次运行都重试一遍。
                    rec |= {"status": "ok", "tex": tex}
                    ok += 1
                except Exception as exc:
                    rec |= {"status": "error", "error": f"解析结果失败: {exc}"}
                    failed += 1
            # 逐条 flush：这是"中途失败不丢已完成结果"的关键。
            out.write(json.dumps(rec, ensure_ascii=False) + "\n")
            out.flush()

        n = min(start + args.batch, len(todo))
        dt = time.time() - t0
        log(f"[shard] {n}/{len(todo)}  ok={ok} err={failed}  {n/dt:.1f} 张/秒")

    out.close()
    log(f"[shard] 完成 ok={ok} err={failed}，耗时 {time.time()-t0:.1f}s")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except KeyboardInterrupt:
        log("[shard] 被中断，已完成的识别结果已落盘，可重跑续接")
        sys.exit(130)
    except Exception:
        traceback.print_exc()
        sys.exit(1)
