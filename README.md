# jar2native 🚀

Package a Java JAR/WAR into a **single self-contained executable** with an embedded Java runtime — no Java installation needed on the target machine.
将 Java JAR/WAR 打包为单文件自包含可执行程序,内置 Java 运行时,目标机器无需安装 Java。

---

## 默认行为 / Default behavior

> **By default, jar2native packages a full Java runtime.**

```bash
jar2native app.jar
```

默认不执行 `jdeps`、不做任何模块依赖分析,直接打包当前 JDK 的完整运行时,以最大化运行兼容性。开箱即用,优先保证「能跑」,而不是「最小」。

By default no `jdeps` and no module analysis is performed — the full runtime of the detected JDK is bundled for maximum compatibility. Correctness and compatibility come first; size does not.

---

## `--deps`:体积优先,自担风险

```bash
jar2native app.jar --deps
```

> **Use `--deps` to enable dependency analysis and create a smaller runtime.**

`--deps` enables Java module dependency analysis using `jdeps`, then builds a minimal runtime with `jlink`. This can significantly reduce the final binary size, but applications using:

- reflection
- dynamic class loading
- `ServiceLoader`
- JNI
- framework-generated classes
- unusual class loaders

may require additional modules that `jdeps` cannot detect. Use `--extra-module <name>` to add missing modules explicitly.

**If `jdeps` analysis fails, the build fails** — jar2native never silently falls back to a guessed module set, because a "successfully built" binary that crashes at runtime is worse than a clear error. You can either fix the dependency problem or rerun without `--deps` to use the full JRE.

默认 = 稳,`--deps` = 小。

---

## 平台限制 / Platform policy

> jar2native currently packages a **platform-specific** Java runtime.
> Cross-platform JRE packaging is **not supported**.

The embedded JRE comes from the build machine, so the executable platform, the embedded JRE platform and the build machine platform must all be identical:

```text
Windows amd64 → build on Windows amd64 → run on Windows amd64
macOS arm64   → build on macOS arm64   → run on macOS arm64
Linux amd64   → build on Linux amd64   → run on Linux amd64
```

If you request `--os/--arch` different from the current machine, jar2native refuses with a clear error instead of producing a binary that embeds a foreign JRE. **Build on the platform where you intend to run the generated executable.** Go's cross-compilation is kept as a low-level capability, but it is rejected whenever the embedded JRE would not match the target.

---

## 使用方法 / Usage

```bash
# Default: full JRE, current platform, maximum compatibility
jar2native myapp.jar

# Opt-in dependency analysis (smaller runtime, compatibility trade-off)
jar2native myapp.jar --deps

# Add modules that jdeps could not detect (Spring apps often need these)
jar2native myapp.jar --deps --extra-module java.sql --extra-module java.naming

# Bake JVM arguments into the executable
jar2native myapp.jar --jvm-arg "-Xmx2g" --jvm-arg "-Dfoo=bar"

# Use a specific local JDK (validated strictly)
jar2native myapp.jar --jdk-path /usr/lib/jvm/java-17-openjdk

# Skip the post-build smoke test (not recommended)
jar2native myapp.jar --skip-smoke-test
```

### Prerequisites

- **A full JDK on the build machine** (Java 8+). jar2native **only** uses the JDK you point to via `--jdk-path` or one detected locally on the machine — it never downloads or installs a JDK itself. Detected JDKs are strictly validated (`bin/java`, `bin/jlink`, `jdeps` in `--deps` mode, `jmods/`).
- **Go** (https://go.dev/dl/) — used to compile the final self-contained executable.

### Input types

| 类型 | 支持 | 说明 |
| --- | --- | --- |
| Executable JAR (有 `Main-Class`) | ✅ | `java -jar app.jar` 可运行 |
| Spring Boot executable JAR | ✅ | 自动识别 `BOOT-INF/` + loader |
| Executable WAR (有 `Main-Class`) | ✅ | `java -jar app.war` 可运行 |
| 普通 Servlet WAR | ❌ 明确报错 | jar2native 不内置 Servlet 容器,请部署到 Tomcat/Jetty |

### Java 8

Java 8 has no `jlink`/`jdeps`. The default mode copies the JDK's full JRE as-is. `--deps` is explicitly rejected with a clear error instead of pretending to work.

---

## 运行时行为 / Runtime behavior

The generated executable:

1. **Atomically extracts** the embedded runtime to a cache directory on first run (`~/Library/Caches/jar2native/<hash>` on macOS, `~/.cache/jar2native/<hash>` on Linux). Concurrent starts are coordinated with a lock file; extraction happens in a temp directory and becomes visible via a single atomic rename — no process can ever observe a partial JRE.
2. **Verifies completeness** via an embedded `manifest.json` (runtime platform, mode, hashes) plus a layout check (`jre/bin/java`, application JAR).
3. **Forwards signals** (`SIGINT`/`SIGTERM`/`SIGHUP`) to the JVM, so Docker/Kubernetes/systemd stop requests reach your application (e.g. Spring graceful shutdown).
4. **Propagates the exit code** verbatim: if your app exits with 42, the executable exits with 42.

### Argument layering

```bash
./app -- foo bar          # "foo bar" are application args
./app foo bar             # equivalent
```

JVM arguments are never mixed with application arguments:

| 层级 | 来源 |
| --- | --- |
| 1. JVM args(构建期) | `--jvm-arg "-Xmx2g"` |
| 2. JVM args(运行期) | `JAR2NATIVE_JVM_OPTS="-Xmx1g -Dfoo=bar"`(空格分隔,不做 shell 解析) |
| 3. 应用 args | 命令行参数(开头的 `--` 会被剥离) |

最终命令形如:`java [JVM args] -jar app.jar [app args]`

### Environment variables

- `JAR2NATIVE_JVM_OPTS` — extra JVM options at runtime (space-separated)
- `JAR2NATIVE_CACHE_DIR` — override the runtime cache directory

---

## 工作原理 / How it works

```text
Java JAR/WAR
  → inspect MANIFEST.MF (executable JAR / Spring Boot / executable WAR)
  → detect + strictly validate a local JDK (never downloaded)
  → full runtime by default (jlink ALL-MODULE-PATH; Java 8: full JRE copy)
    or --deps: jdeps → jlink minimal runtime
  → stream application + runtime + manifest.json into payload.zip
  → embed payload into a Go binary and compile
  → smoke test: run the binary, extract, verify bundled java -version
```

Priority for the default (full) mode: **Correctness > Compatibility > Size**.
`--deps` mode optimizes for size — with the documented trade-offs.

---

## Build & install

```bash
make build     # build the jar2native tool itself → ./jar2native
make install   # install to $GOPATH/bin
make test      # run unit tests
make e2e       # real-app end-to-end test with Halo (downloads on demand)
```

### Real-application end-to-end test

`make e2e` (or `scripts/e2e_halo.sh`) packages two official [Halo](https://github.com/halo-dev/halo) releases — `v1.2.0` (Spring Boot 2.2) and `v2.26.0` (Spring Boot 4.1) — and verifies the full lifecycle of each generated binary:

1. **package** — full-JRE mode with an auto-detected local JDK
2. **start** — run the self-contained executable
3. **serve** — poll HTTP until the web server answers (302/200)
4. **stop** — SIGTERM → graceful shutdown (Spring shutdown hooks run, process exits cleanly)

JARs are downloaded from GitHub releases and cached in `.e2e-cache/` (a copy already present in `~/Downloads` is reused). Override JDK selection with `J2N_JDK17` / `J2N_JDK21`.

---

## License

GPL-3.0 (see [LICENSE](LICENSE)).
