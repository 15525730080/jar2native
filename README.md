# jar2native

Turn any executable JAR or WAR into a standalone native binary — no Java required at runtime.

jar2native packages a Java JAR/WAR into a self-contained executable with an embedded JRE. The output is a single binary file that runs anywhere the target platform supports, with zero external dependencies.

## Use Cases

- **Distribute Java apps as native binaries** — Ship a single executable instead of requiring users to install Java.
- **Simplify deployment** — One file to copy, `chmod +x`, and run. No `java -jar`, no CLASSPATH, no JRE setup.
- **Cross-platform builds** — Package for Linux, macOS, or Windows from a single command.
- **Embed in containers** — Smaller footprint than a full JDK image; just copy the binary in.

## How It Works

1. **Inspect** — Parse `MANIFEST.MF`, detect Spring Boot, validate `Main-Class`.
2. **Resolve JDK** — From `--jdk`, `JAVA_HOME`, or auto-search standard platform locations.
3. **Build runtime** — Full JRE via `jlink` (JDK 9+) or copy legacy JRE (Java 8). Use `-analyze` to run `jdeps` for module trimming.
4. **Assemble payload** — Deterministic `payload.zip` (app + JRE + `manifest.json`) with fixed timestamps and content hashing.
5. **Generate runner** — A Go project that embeds the payload and launches `java -jar` with forwarded arguments and signals.
6. **Compile** — `go build` produces the final single binary.

## Quick Start

```bash
# Build the tool
go build -o jar2native .

# Package a JAR — produces ./myapp
./jar2native -jar app.jar -o myapp

# Package a WAR (must be executable — have Main-Class)
./jar2native -jar app.war -o myapp

# Cross-compile for Linux
./jar2native -jar app.jar -o myapp --platform linux/amd64

# With JVM arguments
./jar2native -jar app.jar -o myapp --jvm-args "-Xmx2g -Dfile.encoding=UTF-8"

# Run the result — that's it
./myapp
```

## Requirements

- **Build time:** JDK 9+ (for `jlink`) or JDK 8 (for legacy JRE copy), Go 1.23+
- **Runtime:** None. The output binary is fully self-contained — no Java, no JRE, no DLLs.

## Platform Support

| Platform | JDK Source | Runtime Image | Status |
|----------|-----------|---------------|--------|
| macOS (amd64/arm64) | Homebrew, `.jdks`, `/Library/Java/...` | `jlink` or legacy copy | ✅ |
| Linux (amd64/arm64) | `/usr/lib/jvm`, `/usr/java`, `.jdks` | `jlink` or legacy copy | ✅ |
| Windows (amd64) | `Program Files\Java`, `.jdks` | `jlink` or legacy copy | ✅ |

The tool auto-detects the JDK from standard locations per OS. Override with `--jdk` or `JAVA_HOME`.

## CLI Options

```
  -jar            Path to JAR or WAR (required)
  -o, --output     Output binary name (default: same as input without extension)
      --jdk        JDK home path (default: JAVA_HOME or auto-detect)
      --platform   Target os/arch (default: host, e.g. linux/amd64)
      --jre-mode   JRE build mode: auto, jlink, copy (default: auto)
      -analyze      Run jdeps to trim unused modules (default: full JRE)
      --modules    Extra JDK modules, comma-separated (use with -analyze)
      --jvm-args   JVM args passed to runner (e.g. "-Xmx2g -Dfile.encoding=UTF-8")
      --verbose    Verbose output
```

## Key Design

- **Full JRE by default** — `jdeps` often fails on real-world WARs (obfuscated classes, new bytecode tags). Default to full JRE; jdeps is opt-in via `-analyze`.
- **Deterministic builds** — Fixed timestamp (1980-01-01), normalized permissions (0755/0644), deflate compression. Same input always produces byte-identical output.
- **PID-based cache lock** — Stale locks from killed processes are automatically reclaimed. Atomic extract to temp dir + rename.
- **Single source of truth** — `runner/shared.go` handles zip-slip protection, cache management, and manifest verification. It's compiled normally by the tool AND written verbatim into each generated runner, eliminating build-time/runtime drift.

## Project Structure

```
jar2native/
├── main.go                 # CLI entry, config, platform, logging, orchestration
├── go.mod
├── Makefile
├── jdk/jdk.go              # JDK discovery, version detection, validation, jlink compress
├── runtime/builder.go      # jlink full build + module trim + legacy JRE copy
├── payload/payload.go     # Artifact inspection, manifest, deterministic payload.zip, zip-slip extraction
├── analyzer/analyzer.go   # jdeps module dependency analysis (opt-in)
├── runner/runner.go       # Runner template generation + go build
├── runner/shared.go       # Shared source (zip-slip, cache, manifest) — embedded into generated runners
└── tests/e2e/run.sh       # End-to-end test (3-line shell script)
```

## Testing

```bash
# Build the tool first
make build

# Run e2e — downloads Jenkins WAR, packages it, runs the binary
bash tests/e2e/run.sh
```

## License

MIT
