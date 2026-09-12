# -*- coding: utf-8 -*-
import io, re
f = r'frontend\src\i18n\locales\zh-CN.ts'
with io.open(f, encoding='utf-8') as fh:
    t = fh.read()
m = re.search(r"createChat: \{.*?title: '([^']*)'", t, re.S)
res = 'zh-CN title: ' + m.group(1)
for f2 in ['en-US', 'ko-KR', 'ru-RU']:
    with io.open(r'frontend\src\i18n\locales' + '\\' + f2 + '.ts', encoding='utf-8') as fh:
        t2 = fh.read()
    m2 = re.search(r"createChat: \{.*?title: '([^']*)'", t2, re.S)
    has = 'WeKnora' in m2.group(1)
    res += '\n' + f2 + ' title: ' + m2.group(1) + '  [containsWeKnora=' + str(has) + ']'
with io.open(r'scripts\greet_out.txt', 'w', encoding='utf-8') as fh:
    fh.write(res)
print('done')
