# -*- coding: utf-8 -*-
import base64, json, urllib.request

with open(r'D:\pdx_junction\ocr_test_img.png', 'rb') as f:
    b64 = base64.b64encode(f.read()).decode('ascii')

payload = {
    'file': b64,
    'fileType': 1,
    'useDocOrientationClassify': False,
    'useDocUnwarping': False,
    'useTextlineOrientation': False,
    'visualize': False,
}
req = urllib.request.Request(
    'http://127.0.0.1:8081/ocr',
    data=json.dumps(payload).encode('utf-8'),
    headers={'Content-Type': 'application/json'},
    method='POST',
)
with urllib.request.urlopen(req, timeout=120) as resp:
    body = json.load(resp)

print('HTTP OK, keys:', list(body.keys()))
res = body.get('result', body)
ocr = res.get('ocrResults') or res.get('ocrResult') or res
print(json.dumps(body, ensure_ascii=False)[:600])

# 提取文本
def walk(o):
    if isinstance(o, dict):
        for k in ('rec_texts', 'recTexts', 'texts'):
            if k in o and isinstance(o[k], list):
                return o[k], o.get('rec_scores') or o.get('recScores') or []
        for v in o.values():
            r = walk(v)
            if r: return r
    elif isinstance(o, list):
        for v in o:
            r = walk(v)
            if r: return r
    return None

found = walk(body)
if found:
    texts, scores = found
    print('\n===== OCR RESULT =====')
    for i, t in enumerate(texts):
        s = scores[i] if i < len(scores) else None
        print(f'  [{s if s is None else round(float(s),4)}] {t}')
