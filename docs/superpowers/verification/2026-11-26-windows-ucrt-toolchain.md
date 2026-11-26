# Windows 本机构建环境（DuckDB 链接）

> 适用范围：在 **Windows** 上直接 `go build ./...` / `go test` 本仓库的包（不经容器）。
> 记录的是一次真实排查的结论与可复现步骤，供新会话/新人直接照做，不必重新踩一遍。

## 症状

在 Windows 上编含 `internal/container`（间接拉入 `github.com/duckdb/duckdb-go/v2`）的包时链接失败：

```
libduckdb_static.a(ub_duckdb_common_http.cpp.obj): undefined reference to
  `std::basic_streambuf<char, std::char_traits<char> >::seekpos(std::fpos<_Mbstatet>, std::_Ios_Openmode)'
libduckdb_static.a(re2.cc.obj): undefined reference to `__emutls_v._ZSt11__once_call'
libduckdb_static.a(re2.cc.obj): undefined reference to `__emutls_v._ZSt15__once_callable'
libduckdb_static.a(platform.cpp.obj): undefined reference to `__stdio_common_vsnprintf_s'
libduckdb_static.a(platform.cpp.obj): undefined reference to `__stdio_common_vswprintf'
```

影响面：`go build ./...` 与 `go test ./internal/application/service/...`（后者经由
`internal/container` 间接依赖 duckdb）全部无法执行——**不是测试失败，是测试二进制链接不出来**。

## 根因：C 运行时（CRT）与 C++ 标准库 ABI 不匹配

`github.com/duckdb/duckdb-go-bindings` 随包发布的预编译静态库是
**UCRT + 大 `mbstate_t`** 编出来的，而默认的 w64devkit 是 **MSVCRT** 工具链。

预编译库的编译者签名（`strings` 读 obj 的 `.comment` 段）：

```
GCC: (x86_64-posix-seh-rev0, Built by MinGW-Builds project) 14.2.0
```

三个报错各自对应一处 ABI 分歧：

| 报错 | 原因 |
| --- | --- |
| `fpos<_Mbstatet>` | `wchar.h` 里 `#if defined(_UCRT) \|\| defined(__LARGE_MBSTATE_T)` 时 `mbstate_t` 是 `struct _Mbstatet`，否则是 `int`。`std::fpos<mbstate_t>` 的模板实参随之不同，本地 `libstdc++.a` 只有 `fpos<int>` 版本 |
| `__emutls_v._ZSt11__once_call` | `__emutls_v.` 前缀是 GCC 的 **emulated TLS** 命名。本地 `libstdc++.a` 提供的是原生 TLS 的 `_ZSt11__once_call`，名字对不上 |
| `__stdio_common_vswprintf` | UCRT 私有 API。`libmsvcrt.a` 里不存在（0 命中），只有 `libucrt.a` 有（22 命中），而 MSVCRT 工具链默认不链它 |

对照：同一版本库的 `linux-amd64` 那份 **0 个** `__emutls` / `_Mbstatet` / `__stdio_common` 命中，
`__once_call` 是标准 `_ZSt11__once_call`——纯 GCC 原生 ABI，所以容器构建与 CI 一路绿灯。

## 解决：换 UCRT 工具链（一次性，约 10 分钟）

选 **与预编译库同版本、同线程/异常模型** 的包，避免引入新的 `GLIBCXX_*` 符号版本问题：

```
源：https://github.com/niXman/mingw-builds-binaries/releases
包：x86_64-14.2.0-release-posix-seh-ucrt-rt_v12-rev0.7z   （83.8 MB）
```

安装（解压即用，不写注册表、不改系统 PATH）：

```bash
cd /d/toolchain
curl -L -o mingw-ucrt.7z \
  "https://github.com/niXman/mingw-builds-binaries/releases/download/14.2.0-rt_v12-rev0/x86_64-14.2.0-release-posix-seh-ucrt-rt_v12-rev0.7z"
"/c/Program Files/7-Zip/7z.exe" x mingw-ucrt.7z -o./mingw-ucrt -y
# 产物在 D:/toolchain/mingw-ucrt/mingw64/
```

使用（只在当前 shell 生效，不污染既有 w64devkit）：

```bash
export CC=/d/toolchain/mingw-ucrt/mingw64/bin/gcc.exe
export CXX=/d/toolchain/mingw-ucrt/mingw64/bin/g++.exe
export PATH=/d/toolchain/mingw-ucrt/mingw64/bin:$PATH

go build ./internal/...                 # 应通过
go test ./internal/application/service/...   # 现在能跑
```

## 装完怎么确认真的修好了

```bash
# 1) 编译者签名要与预编译库一致
gcc --version | head -1
# 期望：gcc.exe (x86_64-posix-seh-rev0, Built by MinGW-Builds project) 14.2.0

# 2) 必须定义 _UCRT（MSVCRT 工具链这里是空的）
echo | gcc -dM -E - | grep -E "_UCRT|__LARGE_MBSTATE_T"
# 期望：至少出现 _UCRT

# 3) fpos 模板实参必须是 _Mbstatet
printf '#include <fstream>\nvoid f(){std::basic_streambuf<char,std::char_traits<char>>*s=nullptr;(void)s->pubseekpos(std::streampos(0),std::ios_base::in);}\n' > /tmp/a.cpp
g++ -c /tmp/a.cpp -o /tmp/a.o && nm /tmp/a.o | grep -oE "4fposI[a-zA-Z0-9_]+E" | head -2
# 期望：4fposI9_MbstatetE      （MSVCRT 工具链会得到 4fposIiE）

# 4) 真实链接
go build -o /tmp/probe.exe ./internal/handler/
```

## 注意

- **Go build cache 按 `CC`/`CXX` 路径分桶**：切回 w64devkit 会重新编译整个 cgo 依赖树（DuckDB 静态库 290MB，约 3 分钟）。
  建议在 UCRT 环境里连续工作，不要在两种工具链之间反复切换。
- **容器构建不受影响**：`scripts/build-anydoc-image.sh` 走 `golang:1.26-bookworm` 容器内的 Linux 工具链，
  与 Windows 本机工具链无关。
- **不要用 `-lucrt` 之类的「补链接」绕过**：`fpos<_Mbstatet>` 与 emutls 两处是 C++ 模板/符号层面
  的不匹配，补 C 运行时的库解决不了，只会把错误推到更晚的地方。
- 本文件只描述 **本机开发环境**。CI 与生产走 Linux 容器，不需要这套工具链。
