# jar2native

将任何可执行的 JAR 或 WAR 打包成独立原生二进制文件 — 运行时无需安装 Java。

[English README](README.md) · [下载 Release](https://github.com/15525730080/jar2native/releases)

jar2native 把 Java JAR/WAR 连同内嵌 JRE 一起打包成单一可执行文件。产物是一个普通二进制文件，拷到目标平台直接运行，零外部依赖。

## 适用场景

- **以原生二进制形式分发 Java 应用** — 只发一个可执行文件，用户不需要装 Java。
- **简化部署** — 一个文件，复制后即可运行。不用 `java -jar`，不用配 CLASSPATH，不用装 JRE。
- **跨平台构建** — 一条命令打包 Linux、macOS 或 Windows。
- **容器内嵌** — 比 JDK 镜像更小；直接把二进制丢进去就行。

## 工作原理

1. **检查** — 解析 `MANIFEST.MF`，检测 Spring Boot，校验 `Main-Class`。
2. **解析 JDK** — 从 `--jdk`、`JAVA_HOME` 或各平台标准路径自动搜索。
3. **构建运行时** — 默认用 `jlink` 构建全量 JRE（JDK 9+），或拷贝旧版 JRE（Java 8）。加 `-analyze` 可通过 `jdeps` 裁剪模块。
4. **组装 payload** — 确定性打包 `payload.zip`（应用 + JRE + `manifest.json`），固定时间戳 + 内容哈希。
5. **盖印启动器** — 使用预编译的目标平台通用 runner，从二进制尾部读取配置；直接追加 payload 和 JSON，无需编译 Go。
6. **回退路径** — 目标平台没有内嵌 runner 时，使用旧的 Go 工程生成和 `go build`。

## 使用方法

### 直接使用 Release 可执行文件

从 [Releases 页面](https://github.com/15525730080/jar2native/releases) 下载对应平台的可执行文件，然后直接执行打包。

Linux/macOS：

```bash
chmod +x jar2native

# 将 JAR 打包成 ./myapp
./jar2native -jar app.jar -o myapp

# 将 WAR 打包成 ./myapp
./jar2native -jar app.war -o myapp

# 运行生成的自包含应用
./myapp
```
 5. **盖印启动器** — 使用预编译的目标平台通用 runner，从二进制尾部读取配置；直接追加 payload 和 JSON，无需编译 Go。
Windows 请使用 PowerShell。Windows 产物使用 `.exe` 后缀：

```powershell
# 将 JAR 打包成 .\myapp.exe
.\jar2native.exe -jar app.jar -o myapp

# 运行生成的自包含应用
.\myapp.exe
```

输入的 JAR/WAR 必须是可执行包，并包含 `Main-Class`。打包机器需要兼容的 JDK 和 Go；生成的应用运行时不需要 Java 或 JRE。

### 从源码构建

```bash
# 构建工具和内嵌平台 runner（需要 Go 1.21+）
make build

# 打包 JAR — 产出 ./myapp
./jar2native -jar app.jar -o myapp

# 打包 WAR（必须是可执行 WAR，即带 Main-Class）
./jar2native -jar app.war -o myapp

# 带 JVM 参数
./jar2native -jar app.jar -o myapp --jvm-args "-Xmx2g -Dfile.encoding=UTF-8"

# 运行产物 — 就这么简单
./myapp
```

Windows 构建和运行方式：

 输入的 JAR/WAR 必须是可执行包，并包含 `Main-Class`。打包机器需要兼容的 JDK；Release 构建已内嵌通用 runner，因此打包应用不需要 Go。生成的应用运行时不需要 Java 或 JRE。
go build -o jar2native.exe .
.\jar2native.exe -jar app.jar -o myapp
.\myapp.exe
```

`--platform` 只控制生成的 Go runner 的目标平台。内嵌 JRE 是通过 `--jdk` 或 `JAVA_HOME` 指定的 JDK 调用 `jlink` 构建的，因此必须准备目标平台对应的 JDK。当前实现不会自动下载或切换 JDK；如需可靠地打包其他平台，请在目标平台的 JDK 和构建环境中运行 jar2native。

## 环境要求

- **构建 jar2native：** Go 1.21+（运行 `make build`，会生成内嵌 runner）
- **打包应用：** JDK 9+（jlink）或 JDK 8（旧版拷贝），不需要 Go
- **运行产物：** 无依赖。产物二进制完全自包含，不需要 Java、JRE 或额外 DLL。

## 平台支持

| 平台 | JDK 来源 | 运行时镜像 | 状态 |
|------|---------|-----------|------|
| macOS (amd64/arm64) | Homebrew、`.jdks`、`/Library/Java/...` | `jlink` 或旧版拷贝 | ✅ |
| Linux (amd64/arm64) | `/usr/lib/jvm`、`/usr/java`、`.jdks` | `jlink` 或旧版拷贝 | ✅ |
| Windows (amd64) | `Program Files\Java`、`.jdks` | `jlink` 或旧版拷贝 | ✅ |

`amd64` 指 x86-64，也就是 64 位 x86，Intel 和 AMD 的 64 位 CPU 都支持。当前文档和测试覆盖的是 `windows/amd64`；32 位 x86（`windows/386`）暂不支持，因为它还需要兼容的 32 位 JDK/JRE。

工具按各平台标准路径自动探测 JDK。可用 `--jdk` 或 `JAVA_HOME` 覆盖。

## 命令行选项

```
  -jar            JAR 或 WAR 路径（必填）
 `--platform` 选择内嵌 runner 和 JRE 的目标平台。内嵌 JRE 是通过 `--jdk` 或 `JAVA_HOME` 指定的 JDK 调用 `jlink` 构建的，因此必须准备目标平台对应的 JDK。当前实现不会自动下载或切换 JDK。没有内嵌 runner 的目标平台会回退到 Go 编译路径。
      --jdk        JDK 路径（默认 JAVA_HOME 或自动探测）
      --platform   目标平台（默认本机，如 linux/amd64）
      --jre-mode   JRE 构建模式：auto, jlink, copy（默认 auto）
      -analyze      运行 jdeps 裁剪模块（默认全量 JRE）
      --modules    额外模块，逗号分隔（与 -analyze 配合使用）
      --jvm-args   传给 runner 的 JVM 参数（如 "-Xmx2g -Dfile.encoding=UTF-8"）
      --verbose    详细输出
```

## 关键设计

- **默认全量 JRE** — `jdeps` 对真实 WAR 经常失败（混淆类、新字节码 tag）。默认全量 JRE，jdeps 通过 `-analyze` 显式开启。
- **确定性打包** — 固定时间戳（1980-01-01）、统一权限（0755/0644）、deflate 压缩。相同输入永远产出字节一致的产物。
- **PID 缓存锁** — 进程被杀后的残留锁自动回收。原子解压到临时目录再 rename。
- **单一真源** — `runner/shared.go` 同时负责 zip-slip 防护、缓存管理和 manifest 校验。它被工具正常编译，也原样写入每个生成的 runner 项目，消除构建端与运行端的逻辑漂移。

## 项目结构

```
jar2native/
├── main.go                 # CLI 入口 + 配置 + 平台 + 日志 + 打包编排
├── go.mod
├── Makefile
├── jdk/jdk.go              # JDK 探测/校验/版本解析/jlink compress 参数
├── runtime/builder.go      # jlink 全量构建 + 模块裁剪 + Java8 JRE 拷贝
├── payload/payload.go     # artifact 识别 + manifest + 确定性 payload.zip + zip-slip 安全提取
├── analyzer/analyzer.go   # jdeps 模块依赖分析（opt-in）
├── runner/runner.go       # 旧 runner 模板生成 + go build 回退路径
├── runner/stamp.go        # 确定性的 payload/config 尾部格式
├── runner/generic/main.go # 运行时读取配置的预编译 runner
├── runner/bin/            # make runners 生成并嵌入 jar2native 的 runner
├── runner/shared.go       # zip-slip / 缓存 / manifest 共享逻辑
└── tests/e2e/run.sh       # 端到端测试（3 行 shell 脚本）
```

## 测试

```bash
# 先构建工具
make build

# 运行 e2e — 下载 Jenkins WAR → 打包 → 运行验证
bash tests/e2e/run.sh
```

## 许可证

MIT
