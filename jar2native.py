#!/usr/bin/env python3
"""Java Application Packaging Tool - Packages JAR/WAR with minimal JRE"""

import os
import sys
import subprocess
import shutil
import zipfile
import time
import argparse
import tempfile
from pathlib import Path
from typing import List, Set


class JDKManager:
    """JDK detection and management"""

    def __init__(self, custom_path: str = None):
        self.jdk_dir = custom_path or self._detect_jdk()
        if not self.jdk_dir:
            raise RuntimeError("JDK not found")

    def _detect_jdk(self) -> str:
        try:
            out = subprocess.check_output(["java", "-version"], stderr=subprocess.STDOUT)
            if b'version "1.' in out:
                raise RuntimeError("JDK version too old (requires 9+)")

            java_path = shutil.which("java")
            if java_path:
                jdk_path = str(Path(java_path).parent.parent)
                print(f"[INFO] JDK detected: {jdk_path}")
                return jdk_path
        except Exception as e:
            print(f"[ERROR] JDK detection failed: {e}")
        return ""

    def get_bin(self, tool: str) -> Path:
        """Get path to JDK binary tool"""
        return Path(self.jdk_dir) / "bin" / tool

    def list_all_modules(self) -> str:
        """List all available modules in JDK"""
        out = subprocess.check_output([str(self.get_bin("java")), "--list-modules"], text=True)
        modules = {line.split("@")[0] for line in out.splitlines() if line.strip()}
        return ",".join(sorted(modules))


class ModuleAnalyzer:
    """Module dependency analyzer for JAR/WAR files"""

    DEFAULT_MODULES = "java.instrument,java.base,java.logging"

    def __init__(self, jdk_manager: JDKManager):
        self.jdk = jdk_manager

    def analyze(self, jar_path: str, use_all: bool = False) -> str:
        """Analyze required modules from target JAR/WAR"""
        if use_all:
            print("[INFO] Using all JDK modules")
            return self.jdk.list_all_modules()

        targets = self._prepare_targets(jar_path)
        return self._run_jdeps(targets)

    def _prepare_targets(self, jar_path: str) -> List[str]:
        """Prepare analysis targets (extract WAR if needed)"""
        return self._extract_war(jar_path) if jar_path.lower().endswith(".war") else [jar_path]

    def _extract_war(self, war_path: str) -> List[str]:
        """Extract WAR file to temporary directory and get analysis targets"""
        with tempfile.TemporaryDirectory() as tmp_dir:
            extract_dir = Path(tmp_dir)
            print(f"[INFO] Extracting WAR file to temporary directory")

            with zipfile.ZipFile(war_path, "r") as zf:
                zf.extractall(extract_dir)

            targets = []
            if (classes_dir := extract_dir / "WEB-INF" / "classes").exists():
                targets.append(str(classes_dir))
            if (lib_dir := extract_dir / "WEB-INF" / "lib").exists():
                targets.extend(str(f) for f in lib_dir.iterdir() if f.suffix == ".jar")

            return targets

    def _run_jdeps(self, targets: List[str]) -> str:
        """Run jdeps to analyze module dependencies"""
        jdeps_bin = self.jdk.get_bin("jdeps")
        try:
            out = subprocess.check_output(
                [str(jdeps_bin), "--print-module-deps"] + targets,
                text=True, stderr=subprocess.DEVNULL
            )
            return out.strip()
        except subprocess.CalledProcessError:
            print("[INFO] Fallback to --list-deps mode")
            out = subprocess.check_output(
                [str(jdeps_bin), "--list-deps"] + targets,
                text=True, stderr=subprocess.DEVNULL
            )
            return self._parse_list_deps(out)
        except Exception as e:
            print(f"[WARN] jdeps execution failed: {e}, using default modules")
            return self.DEFAULT_MODULES

    def _parse_list_deps(self, output: str) -> str:
        """Parse --list-deps output to extract modules"""
        modules = {"java.base"}
        for line in output.splitlines():
            if "[" in line and "]" in line:
                modules.add(line[line.index("[") + 1:line.index("]")])
        return ",".join(sorted(modules))

    def merge_modules(self, analyzed: str, extra: List[str]) -> str:
        """Merge analyzed modules with extra modules"""
        modules: Set[str] = set(analyzed.split(","))
        modules.update(extra)
        return ",".join(sorted(m.strip() for m in modules if m.strip()))


class JREBuilder:
    """Minimal JRE builder using jlink"""

    def __init__(self, jdk_manager: JDKManager):
        self.jdk = jdk_manager

    def build(self, output_dir: Path, modules: str):
        """Build minimal JRE with specified modules"""
        print(f"[STEP] Building minimal JRE: {output_dir}")

        cmd = [
            str(self.jdk.get_bin("jlink")),
            "--module-path", str(Path(self.jdk.jdk_dir) / "jmods"),
            "--add-modules", modules,
            "--output", str(output_dir),
            "--no-header-files",
            "--no-man-pages"
        ]

        subprocess.check_call(cmd)
        print("[OK] Minimal JRE built successfully")


class PackageBuilder:
    """Package builder for creating executable (EXE/Linux binary)"""

    LAUNCHER = '''#!/usr/bin/env python3
import os, sys, subprocess
from pathlib import Path

def get_base():
    return Path(sys._MEIPASS) if hasattr(sys, "_MEIPASS") else Path(__file__).parent.resolve()

base = get_base()
jre = base / "{jre_dir}"
jar = base / "{jar_file}"

if not jre.exists(): raise FileNotFoundError(f"JRE not found: {{jre}}")
if not jar.exists(): raise FileNotFoundError(f"JAR not found: {{jar}}")

java_exe = "java.exe" if os.name == "nt" else "java"
java_bin = jre / "bin" / java_exe

if not java_bin.exists(): raise FileNotFoundError(f"Java executable not found: {{java_bin}}")

print(f"[RUN] {{java_bin}} -jar {{jar}}")
subprocess.run([str(java_bin), "-jar", str(jar)], check=True)
'''

    def __init__(self, work_dir: Path):
        self.work_dir = work_dir
        self.work_dir.mkdir(exist_ok=True)

    def prepare(self, jar_path: Path, jre_dir: Path) -> Path:
        """Prepare source files (JAR, JRE, launcher) for packaging"""
        src_dir = self.work_dir / "src"
        src_dir.mkdir(parents=True, exist_ok=True)

        # Copy JAR file
        shutil.copy2(jar_path, src_dir / jar_path.name)

        # Copy JRE directory (support symlinks and handle conflicts)
        jre_dest = src_dir / jre_dir.name
        try:
            shutil.copytree(jre_dir, jre_dest, symlinks=True)
        except FileExistsError:
            shutil.rmtree(jre_dest, ignore_errors=True)
            shutil.copytree(jre_dir, jre_dest, symlinks=True)
        except PermissionError as e:
            raise RuntimeError(f"Permission denied while copying JRE: {e}")

        # Generate launcher script
        launcher_path = src_dir / "launcher.py"
        launcher_path.write_text(
            self.LAUNCHER.format(jre_dir=jre_dir.name, jar_file=jar_path.name),
            encoding="utf-8"
        )

        return launcher_path

    def build_exe(self, launcher: Path, output: str, jre_name: str, jar_name: str):
        """Build executable using PyInstaller"""
        try:
            import PyInstaller.__main__
        except ImportError:
            print("[INFO] Installing PyInstaller dependency")
            subprocess.check_call([sys.executable, "-m", "pip", "install", "pyinstaller"])

        print(f"[STEP] Building executable: {output}")

        jre_path = launcher.parent / jre_name
        jar_path = launcher.parent / jar_name

        # PyInstaller arguments
        pyinstaller_args = [
            "--onefile",
            "--name", output,
            str(launcher),
            "--add-data", f"{jre_path}{os.pathsep}{jre_name}",
            "--add-data", f"{jar_path}{os.pathsep}.",
            "--clean"
        ]

        # Run PyInstaller
        PyInstaller.__main__.run(pyinstaller_args)


class JavaPackager:
    """Main packaging orchestrator"""

    def __init__(self, args):
        self.args = args
        self.jar_path = Path(args.jar_file).resolve()
        self.jdk = JDKManager(args.jdk_path)
        self.analyzer = ModuleAnalyzer(self.jdk)
        self.jre_builder = JREBuilder(self.jdk)

    def validate(self):
        """Validate input file and prerequisites"""
        if not self.jar_path.exists():
            raise FileNotFoundError(f"Input file not found: {self.jar_path}")

        if self.jar_path.suffix.lower() not in ['.jar', '.war']:
            raise ValueError("Only JAR and WAR files are supported")

        if self.jar_path.suffix.lower() == '.jar' and not self._has_main_class():
            raise ValueError("JAR file missing Main-Class attribute in MANIFEST.MF")

    def _has_main_class(self) -> bool:
        """Check if JAR has Main-Class attribute"""
        with tempfile.TemporaryDirectory() as tmp_dir:
            tmp_dir = Path(tmp_dir)
            # Extract MANIFEST.MF
            subprocess.run(
                [str(self.jdk.get_bin("jar")), "xf", str(self.jar_path), "META-INF/MANIFEST.MF"],
                cwd=tmp_dir, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
            )

            mf_path = tmp_dir / "META-INF" / "MANIFEST.MF"
            return mf_path.exists() and "Main-Class" in mf_path.read_text(encoding="utf-8", errors="ignore")

    def package(self):
        """Main packaging workflow"""
        start_time = time.time()

        # Use temporary working directory (auto-cleaned)
        with tempfile.TemporaryDirectory() as tmp_work_dir:
            work_dir = Path(tmp_work_dir)
            try:
                self.validate()

                print("[STEP] Analyzing module dependencies")
                analyzed_modules = self.analyzer.analyze(str(self.jar_path), self.args.all_modules)
                final_modules = self.analyzer.merge_modules(analyzed_modules, self.args.extra_modules or [])
                print(f"[INFO] Final modules: {final_modules}")

                # Build minimal JRE
                jre_dir = work_dir / "jre"
                self.jre_builder.build(jre_dir, final_modules)

                # Prepare packaging sources
                builder = PackageBuilder(work_dir)
                launcher_script = builder.prepare(self.jar_path, jre_dir)

                # Build executable
                output_name = self.jar_path.stem + (".exe" if os.name == "nt" else "")
                builder.build_exe(launcher_script, output_name, jre_dir.name, self.jar_path.name)

                # Calculate and print elapsed time
                elapsed_time = time.time() - start_time
                print(f"✅ Packaging completed in {elapsed_time:.2f}s: dist/{output_name}")

            except Exception as e:
                print(f"[ERROR] Packaging failed: {e}")
                raise


def main():
    """Entry point of the application"""
    parser = argparse.ArgumentParser(description="Java Application Packaging Tool - Packages JAR/WAR with minimal JRE")
    parser.add_argument("jar_file", help="Path to JAR/WAR file")
    parser.add_argument("--jdk-path", help="Custom JDK installation path (optional)")
    parser.add_argument("--extra-modules", nargs="+", help="Additional modules to include (optional)")
    parser.add_argument("--all-modules", action="store_true", help="Include all JDK modules (optional)")

    args = parser.parse_args()

    # Print header
    print("=" * 60)
    print("Java Application Packaging Tool")
    print("=" * 60)

    # Run packaging
    packager = JavaPackager(args)
    packager.package()


if __name__ == "__main__":
    main()
