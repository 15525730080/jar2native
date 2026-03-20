# jar2native 🚀

Package any JAR/WAR into a **single self-contained binary** — no Java, no JRE, no unzipping needed on the target machine. Just download and run.

将任意 JAR/WAR 打包成**单文件可执行二进制**，目标机器无需安装 Java，下载即用。

---

## How it works ⚙️

```
jar2native myapp.jar
     │
     ├─ 1. jdeps   → detect required Java modules
     ├─ 2. jlink   → build minimal JRE (only needed modules)
     ├─ 3. zip     → pack JAR + JRE into an in-memory zip
     ├─ 4. embed   → write a Go source file with //go:embed payload.zip
     └─ 5. go build → compile a single self-contained binary  ✅
```

On first launch, the binary extracts itself to `~/.cache/jar2native/<hash>/` and runs `jre/bin/java -jar app.jar`. Subsequent launches skip extraction (50 ms startup).

---

## Prerequisites 📋

**Build machine** (where you run `jar2native`):
- JDK 9+ (provides `jlink` and `jdeps`)
- Go 1.21+

**Target machine** (where the output binary runs):
- Nothing. Zero dependencies.

---

## Build jar2native 🔨

```bash
# Build for current platform
make build          # → ./jar2native

# Build for all platforms
make release        # → dist/jar2native-{os}-{arch}[.exe]

# Install to $GOPATH/bin
make install
```

---

## Usage 🚀

```
jar2native [options] <file.jar|file.war>

Options:
  -jdk-path string      Custom JDK installation path
  -extra-module value   Additional Java module to include (repeatable)
  -all-modules          Include all JDK modules (maximum compatibility)
  -os string            Target OS: linux, darwin, windows (default: current)
  -arch string          Target arch: amd64, arm64 (default: current)
  -version              Print version and exit
```

### Examples

```bash
# Basic — produces dist/myapp (macOS/Linux) or dist/myapp.exe (Windows)
jar2native myapp.jar

# Cross-compile for Linux amd64 (run on macOS, get a Linux binary)
jar2native myapp.jar --os linux --arch amd64

# WAR file with extra modules
jar2native myapp.war --extra-module java.sql --extra-module java.naming

# Custom JDK path
jar2native myapp.jar --jdk-path /usr/lib/jvm/java-17-openjdk

# Include all JDK modules (larger output, maximum compatibility)
jar2native myapp.jar --all-modules
```

---

## Output 📦

A single binary in `dist/`:

```
dist/myapp          ← macOS / Linux  (~15–80 MB depending on JRE size)
dist/myapp.exe      ← Windows
```

**To run on the target machine:**

```bash
# macOS / Linux — just run it
chmod +x myapp && ./myapp

# Windows — just double-click or run
myapp.exe
```

First launch extracts the embedded JRE to `~/.cache/jar2native/<hash>/` (one-time, a few seconds). All subsequent launches start in ~50 ms.

---

## Real-world Example: Halo Blog 🌰

[Halo](https://github.com/halo-dev/halo) is a full-featured Java blog platform. Its release artifact is a single Spring Boot fat JAR (~119 MB) that bundles the backend, 218 dependencies, and the entire Vue-based admin console — making it a perfect real-world test case.

**Step 1 — Download the official release JAR:**

```bash
curl -L -O https://github.com/halo-dev/halo/releases/download/v2.23.1/halo-2.23.1.jar
```

**Step 2 — Package into a self-contained binary:**

```bash
# --all-modules ensures Spring Boot's dynamic class loading works correctly
jar2native --all-modules halo-2.23.1.jar
```

Output:

```
────────────────────────────────────────────────────────────
  jar2native 2.0.0
────────────────────────────────────────────────────────────
[STEP] Analyzing module dependencies
[INFO] Using all JDK modules
[STEP] Building minimal JRE
[OK]   Minimal JRE built successfully
[STEP] Building payload (JAR + JRE)
[INFO] Payload size: 180.4 MB  hash: 4ed6022f2122…
[STEP] Compiling binary: dist/halo-2.23.1
[OK]   Binary compiled: dist/halo-2.23.1

✅ Done in 6.69s
   Output : dist/halo-2.23.1
   Size   : 183.8 MB
```

**Step 3 — Ship and run (no Java required on the target machine):**

```bash
chmod +x halo-2.23.1
./halo-2.23.1
```

```
[jar2native] First run: extracting runtime...

  ██╗  ██╗ █████╗ ██╗      ██████╗
  ██║  ██║██╔══██╗██║     ██╔═══██╗
  ███████║███████║██║     ██║   ██║
  ██╔══██║██╔══██║██║     ██║   ██║
  ██║  ██║██║  ██║███████╗╚██████╔╝
  ╚═╝  ╚═╝╚═╝  ╚═╝╚══════╝ ╚═════╝

  Halo 2.23.1 is starting...
```

Halo starts on `http://localhost:8090` — no Java, no JRE, no `apt install`, nothing.

**What's inside the 183 MB binary:**

```
halo-2.23.1  (single executable)
├── embedded JRE       ~60 MB  (jlink-trimmed, only needed modules)
└── halo-2.23.1.jar   ~119 MB
    ├── BOOT-INF/lib/          218 dependency JARs (Spring Boot, R2DBC, …)
    ├── BOOT-INF/classes/      backend bytecode
    └── BOOT-INF/classes/console/  Vue admin console (HTML + JS + CSS)
```

---

## License

See [LICENSE](LICENSE).
