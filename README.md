# JavaPackager 🚀

A cross-platform tool to package JAR/WAR files into standalone executables with a minimal JRE — no Java installation needed!  
一款跨平台工具，可将 JAR/WAR 文件打包为独立可执行文件（EXE/Linux 二进制），内置最小 JRE，用户无需安装 Java！

---

## 功能特点 ✨ / Features ✨
- 📦 打包 JAR/WAR 为单文件可执行程序（无外部依赖）
- 🛠️ 自动检测 JDK（9+），构建**最小化 JRE**（减小文件体积）
- 🌍 跨平台支持（Windows 10+、Linux；macOS 即将支持）
- 🚀 一键打包（通过 `jdeps` 自动处理模块依赖）
- 🎯 支持自定义 JDK 路径和额外模块
- 📚 兼容模块化/非模块化 Java 应用
- 📦 Packages JAR/WAR into single standalone executable (no external dependencies)
- 🛠️ Auto-detects JDK (9+) and builds a **minimal JRE** (reduces file size)
- 🌍 Cross-platform support (Windows 10+, Linux; macOS coming soon)
- 🚀 One-click packaging (auto-handles module dependencies via `jdeps`)
- 🎯 Supports custom JDK paths and extra modules
- 📚 Works with both modular and non-modular Java apps

---

## 前置要求 📋 / Prerequisites 📋
使用前请确保安装以下依赖：
Before using JavaPackager, ensure you have these installed:
- [Python 3.8+](https://www.python.org/downloads/)（Python 环境）
- [JDK 9+](https://adoptium.net/)（需 JDK 9+，依赖 `jlink` 和 `jdeps` 工具）
- Pip（Python 包管理器，Python 3.4+ 自带）
- [Python 3.8+](https://www.python.org/downloads/)
- [JDK 9+](https://adoptium.net/) (required for `jlink` and `jdeps` tools)
- Pip (included with Python 3.4+)

---

## 使用方法 🚀 / Usage 🚀
python jar2native.py <your-file.jar/war> [options]


---

## 输出文件 📁 / Output 📁
可执行文件会生成在 dist/ 文件夹中（例：Windows 下为 myapp.exe，Linux 下为 myapp）
可执行文件为自包含格式：内置你的 JAR/WAR + 最小 JRE + 启动器
无临时文件残留（通过 Python tempfile 模块自动清理）
The executable will be generated in the dist/ folder (e.g., myapp.exe on Windows, myapp on Linux)
The executable is self-contained: includes your JAR/WAR + minimal JRE + launcher
No temporary files left behind (auto-cleaned via Python's tempfile module)

---

### 工作原理 ⚙️ / How It Works ⚙️
JDK 检测：自动查找有效 JDK 9+（或使用自定义路径）
依赖分析：通过 jdeps 检测 Java 应用所需模块
最小 JRE 构建：使用 jlink 生成仅包含必要模块的精简 JRE
打包：通过 PyInstaller 捆绑 JRE、应用文件和 Python 启动器为单文件可执行程序
清理：自动删除临时文件（无残留）
JDK Detection: Finds a valid JDK 9+ (or uses your custom path)
Dependency Analysis: Runs jdeps to detect required Java modules
Minimal JRE Build: Uses jlink to create a tiny JRE with only needed modules
Packaging: Uses PyInstaller to bundle the JRE, your app, and a Python launcher into a single executable
Cleanup: Auto-deletes temporary files (no residue!)

