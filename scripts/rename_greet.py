# -*- coding: utf-8 -*-
import io, re
files = {
  r'frontend\src\i18n\locales\en-US.ts': ('WeKnora', 'Tianlu'),
  r'frontend\src\i18n\locales\ko-KR.ts': ('WeKnora', 'Tianlu'),
  r'frontend\src\i18n\locales\ru-RU.ts': ('WeKnora', 'Tianlu'),
}
for f, (a, b) in files.items():
    with io.open(f, encoding='utf-8') as fh:
        txt = fh.read()
    # Replace brand only within the createChat.title line, keep other phrasing
    def repl(m):
        line = m.group(0)
        return line.replace('WeKnora', b)
    txt2, n = re.subn(r"(createChat: \{.*?title: '[^']*')", repl, txt, flags=re.S)
    with io.open(f, 'w', encoding='utf-8', newline='') as fh:
        fh.write(txt2)
    print(f, 'replaced blocks:', n)
