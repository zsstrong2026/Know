from PIL import Image, ImageDraw, ImageFont
from reportlab.pdfgen import canvas as rl_canvas
from reportlab.lib.pagesizes import A4

W, H = 1240, 1754
img = Image.new("RGB", (W, H), "white")
d = ImageDraw.Draw(img)
font = ImageFont.truetype("C:/Windows/Fonts/msyh.ttc", 44)
big = ImageFont.truetype("C:/Windows/Fonts/msyh.ttc", 64)

d.text((80, 70), "季度销售汇总表", font=big, fill="black")
d.text((80, 180), "Quarterly sales summary 2026", font=font, fill="black")

rows = [("地区", "销量", "增长率"), ("华东", "1,280", "+12.5%"), ("华北", "964", "+8.1%"), ("华南", "1,533", "+17.3%")]
x0, y0, cw, rh = 80, 280, 340, 96
for i, row in enumerate(rows):
    for j, cell in enumerate(row):
        x, y = x0 + j * cw, y0 + i * rh
        d.rectangle([x, y, x + cw, y + rh], outline="black", width=3)
        d.text((x + 30, y + 22), cell, font=font, fill="black")

d.text((80, 720), "合计: 3,777 件, 平均增长率 +12.6%", font=font, fill="black")
img.save("vlm_table_page.png")

c = rl_canvas.Canvas("vlm_test.pdf", pagesize=A4)
c.drawImage("vlm_table_page.png", 0, 0, width=A4[0], height=A4[1])
c.showPage()
c.save()
print("OK")
