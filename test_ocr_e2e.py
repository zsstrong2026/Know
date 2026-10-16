# -*- coding: utf-8 -*-
import os
# 全部路径经 ASCII junction 规避 paddle_inference 原生层中文路径问题
os.environ.setdefault('PADDLE_PDX_CACHE_HOME', r'D:\pdx_junction\.paddlex_cache')

from PIL import Image, ImageDraw, ImageFont

IMG = r'D:\pdx_junction\ocr_test_img.png'
OUT = r'D:\pdx_junction\ocr_test_result'

lines = [
    (u'深度学习 OCR 识别测试', 52),
    (u'PaddleOCR 3.7 + RTX 4060 GPU', 44),
    (u'部署验证成功 2026', 48),
]

img = Image.new('RGB', (1000, 420), (255, 255, 255))
d = ImageDraw.Draw(img)
y = 40
for text, size in lines:
    font = ImageFont.truetype(r'C:\Windows\Fonts\msyh.ttc', size)
    d.text((50, y), text, fill=(20, 20, 20), font=font)
    y += size + 45
img.save(IMG)
print('test image saved:', IMG)

import paddle
print('paddle device:', paddle.device.get_device())

from paddleocr import PaddleOCR
ocr = PaddleOCR(
    lang='ch',
    device='gpu',
    use_doc_orientation_classify=False,
    use_doc_unwarping=False,
    use_textline_orientation=False,
)
result = ocr.predict(IMG)

print('\n===== OCR RESULT =====')
for res in result:
    texts = res['rec_texts']
    scores = res['rec_scores']
    for t, s in zip(texts, scores):
        print(f'  [{s:.4f}] {t}')
    res.save_to_img(OUT)
print('annotated image saved to:', OUT)
print('===== DONE =====')
