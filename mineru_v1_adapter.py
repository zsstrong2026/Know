"""MinerU V1(/file_parse) -> V2(作业式 API) 协议适配层。

WeKnora 的 MinerUReader 依赖 MinerU 2.x 旧版接口:
  - 连通性探测: GET  {endpoint}/docs      (本服务由 FastAPI 自动提供)
  - 同步解析:    POST {endpoint}/file_parse (multipart, 文件字段名 files)
  - 返回: {"results": {"<文件名去扩展名>": {"md_content": str, "images": {路径: base64}}}}

MinerU 4.0.11 已移除该接口, 只提供 V2 作业式 API(上传 -> 建任务 -> 轮询 -> 下载产物)。
本服务监听 8002, 把 V1 请求实时翻译为对 127.0.0.1:8001 的 V2 调用。

V1 表单字段映射:
  - parse_method(auto/txt/ocr) -> V2 ocr_mode(枚举一致, 直接透传)
  - start_page_id/end_page_id(0 基) -> V2 page_range(1 基)
  - backend/formula_enable/table_enable/lang_list/server_url:
    V2 无作业级对应参数, 由 V2 服务启动参数(--tier standard --vlm-server-url ...)统一承担
"""

import asyncio
import base64
import io
import os
import zipfile
from pathlib import Path

import httpx
import uvicorn
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse

V2_BASE = os.environ.get("MINERU_V2_BASE", "http://127.0.0.1:8001").rstrip("/")
JOB_TIMEOUT = float(os.environ.get("MINERU_V1_JOB_TIMEOUT", "3600"))  # 单任务轮询最长等待(秒)
POLL_INTERVAL = 3.0
IMG_EXTS = (".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp")

app = FastAPI(title="MinerU V1-compatible adapter", docs_url="/docs")


def _page_range(start: str, end: str) -> str | None:
    """V1 的 0 基闭区间页码 -> V2 的 1 基 page_range; 全页返回 None 表示不限制"""
    try:
        s, e = int(start), int(end)
    except (TypeError, ValueError):
        return None
    if s <= 0 and e >= 99999:
        return None
    return f"{max(s, 0) + 1}-{e + 1}"


async def _download(client: httpx.AsyncClient, file_id: str) -> bytes:
    r = await client.get(f"{V2_BASE}/v1/files/{file_id}/content")
    r.raise_for_status()
    return r.content


async def _upload(client: httpx.AsyncClient, name: str, data: bytes) -> str:
    """V2 三步上传: 创建 upload -> PUT 字节 -> complete, 返回可用于建任务的 file_id"""
    r = await client.post(f"{V2_BASE}/v1/uploads", json={
        "filename": name, "bytes": len(data), "mime_type": "application/pdf",
    })
    r.raise_for_status()
    uid = r.json()["id"]
    r = await client.put(f"{V2_BASE}/v1/uploads/{uid}/content", content=data,
                         headers={"Content-Type": "application/octet-stream"})
    r.raise_for_status()
    r = await client.post(f"{V2_BASE}/v1/uploads/{uid}/complete", json={})
    r.raise_for_status()
    return r.json()["file"]["id"]


def _extract_zip(zb: bytes) -> tuple[str, dict]:
    """从 V2 zip 产物中提取 markdown 文本与图片映射。

    图片键保持 zip 内相对路径(去掉公共顶层目录), 与 V1 images 语义一致,
    使 markdown 里的 images/xxx.jpg 相对引用可被 WeKnora 对应替换。
    """
    md, images = "", {}
    with zipfile.ZipFile(io.BytesIO(zb)) as zf:
        names = [n for n in zf.namelist() if not n.endswith("/")]
        tops = {n.split("/")[0] for n in names if "/" in n}
        prefix = ""
        if len(tops) == 1:
            t = next(iter(tops))
            if all(n.startswith(t + "/") for n in names):
                prefix = t + "/"
        for n in names:
            key = n[len(prefix):] if prefix else n
            low = key.lower()
            if low.endswith(".md") and not md:
                md = zf.read(n).decode("utf-8")
            elif low.endswith(IMG_EXTS):
                images[key] = base64.b64encode(zf.read(n)).decode("ascii")
    return md, images


async def _parse_one(client: httpx.AsyncClient, name: str, data: bytes,
                     ocr_mode: str, page_range: str | None) -> dict:
    """单个文件走完整 V2 流程, 返回 V1 风格的 {md_content, images}"""
    file_id = await _upload(client, name, data)
    entry = {"source": {"type": "file_id", "file_id": file_id}}
    if page_range:
        entry["page_range"] = page_range
    r = await client.post(f"{V2_BASE}/v1/parse/jobs", json={
        "files": [entry], "ocr_mode": ocr_mode,
        "output_formats": ["markdown", "zip"],
    })
    r.raise_for_status()
    job_id = r.json()["job_id"]

    # 轮询任务直至终态; CPU 上 VLM 约 100s/页, 默认 1 小时上限
    deadline = asyncio.get_running_loop().time() + JOB_TIMEOUT
    while True:
        await asyncio.sleep(POLL_INTERVAL)
        r = await client.get(f"{V2_BASE}/v1/parse/jobs/{job_id}")
        r.raise_for_status()
        job = r.json()
        if job["status"] in ("completed", "partial", "failed", "canceled"):
            break
        if asyncio.get_running_loop().time() > deadline:
            raise TimeoutError(f"V2 任务 {job_id} 超过 {JOB_TIMEOUT:.0f}s 未完成")

    fr = job["files"][0]
    if fr["status"] != "completed":
        raise RuntimeError(f"V2 解析失败: {fr.get('error') or job['status']}")

    outs = fr.get("output_files") or {}
    md_text, images = "", {}
    if outs.get("zip"):
        md_text, images = _extract_zip(await _download(client, outs["zip"]["file_id"]))
    if not md_text and outs.get("markdown"):  # zip 缺失时退化为纯 markdown(无独立图片映射)
        md_text = (await _download(client, outs["markdown"]["file_id"])).decode("utf-8")
    return {"md_content": md_text, "images": images}


@app.post("/file_parse")
async def file_parse(files: list[UploadFile] = File(...),
                     parse_method: str = Form("auto"),
                     start_page_id: str = Form("0"),
                     end_page_id: str = Form("99999")):
    ocr_mode = parse_method if parse_method in ("auto", "txt", "ocr") else "auto"
    pr = _page_range(start_page_id, end_page_id)
    results = {}
    async with httpx.AsyncClient(timeout=httpx.Timeout(600.0, connect=10.0)) as client:
        for f in files:  # WeKnora 每次只发一个文件; 顺序处理避免压垮并发为 1 的 V2 服务
            name = Path(f.filename or "document.pdf").name
            try:
                results[Path(name).stem] = await _parse_one(
                    client, name, await f.read(), ocr_mode, pr)
            except Exception as e:
                raise HTTPException(status_code=502, detail=f"{name}: {e}") from e
    return JSONResponse({
        "backend": "vlm-http-client",
        "version_name": "mineru-4-v2-adapter",
        "results": results,
    })


if __name__ == "__main__":
    import argparse

    p = argparse.ArgumentParser(description="MinerU V1 -> V2 adapter")
    p.add_argument("--host", default="0.0.0.0")  # 需对 Docker 容器可达, 默认监听全网卡
    p.add_argument("--port", type=int, default=8002)
    args = p.parse_args()
    uvicorn.run(app, host=args.host, port=args.port, log_level="info")
