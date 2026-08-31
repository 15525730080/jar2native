package main

import (
  "archive/zip"
  "fmt"
  "io"
  "path"
  "strings"
)

// AppKind classifies the input artifact.
type AppKind int

const (
  // AppExecutableJAR is a JAR with a Main-Class attribute (java -jar works).
  AppExecutableJAR AppKind = iota
  // AppSpringBootJAR is a Spring Boot fat JAR (BOOT-INF/ + loader + Main-Class).
  AppSpringBootJAR
  // AppExecutableWAR is a WAR whose MANIFEST declares a Main-Class, so
  // `java -jar app.war` starts it directly (e.g. Spring Boot executable WAR).
  AppExecutableWAR
)

// AppInfo describes the inspected input artifact.
type AppInfo struct {
  Kind      AppKind
  MainClass string
  IsWAR     bool
  SpringBoot bool
}

// parseManifest parses the main section of a JAR manifest according to the
// JAR File Specification:
//
//   - physical lines are limited to 72 bytes; a logical attribute value is
//     continued on the next physical line when that line starts with a space
//   - attribute lines have the form "Name: value" (name case-insensitive)
//   - the main section ends at the first blank line
func parseManifest(data []byte) map[string]string {
  attrs := map[string]string{}

  var lastName, value string
  flush := func() {
    if lastName != "" {
      attrs[strings.ToLower(lastName)] = value
    }
    lastName, value = "", ""
  }

  for _, rawLine := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
    line := strings.TrimSuffix(rawLine, "\r")

    if line == "" {
      // Blank line: end of the main section.
      flush()
      break
    }

    if strings.HasPrefix(line, " ") {
      // Continuation line: strip exactly one leading space.
      if lastName == "" {
        continue
      }
      value += line[1:]
      continue
    }

    flush()

    idx := strings.Index(line, ":")
    if idx <= 0 {
      // Malformed line — skip it rather than aborting the whole parse.
      continue
    }
    lastName = line[:idx]
    value = strings.TrimLeft(line[idx+1:], " ")
  }
  flush()

  return attrs
}

// manifestAttr returns the value of an attribute using case-insensitive
// lookup (attribute names are case-insensitive per the JAR spec).
func manifestAttr(attrs map[string]string, name string) string {
  return attrs[strings.ToLower(name)]
}

// inspectApp opens a JAR/WAR, parses META-INF/MANIFEST.MF and classifies the
// artifact. Non-executable WARs are rejected with an explicit error because
// jar2native does not bundle a Servlet container.
func inspectApp(appPath string) (*AppInfo, error) {
  ext := strings.ToLower(path.Ext(appPath))
  if ext != ".jar" && ext != ".war" {
    return nil, fmt.Errorf("unsupported input %q: only .jar and .war files are supported", appPath)
  }

  r, err := zip.OpenReader(appPath)
  if err != nil {
    return nil, fmt.Errorf("open %s (is it a valid zip/jar archive?): %w", path.Base(appPath), err)
  }
  defer r.Close()

  var manifestData []byte
  hasBootInf := false
  hasSpringLoader := false
  for _, f := range r.File {
    name := f.Name
    switch {
    case strings.EqualFold(name, "META-INF/MANIFEST.MF"):
      rc, err := f.Open()
      if err != nil {
        return nil, fmt.Errorf("read MANIFEST.MF: %w", err)
      }
      data, err := io.ReadAll(rc)
      rc.Close()
      if err != nil {
        return nil, fmt.Errorf("read MANIFEST.MF: %w", err)
      }
      manifestData = data
    case name == "BOOT-INF/" || strings.HasPrefix(name, "BOOT-INF/"):
      hasBootInf = true
    case strings.HasPrefix(name, "org/springframework/boot/loader/"):
      hasSpringLoader = true
    }
  }

  attrs := parseManifest(manifestData)
  mainClass := strings.TrimSpace(manifestAttr(attrs, "Main-Class"))

  isWAR := ext == ".war"
  springBoot := hasBootInf && hasSpringLoader

  if isWAR {
    if mainClass == "" {
      return nil, fmt.Errorf(`This WAR is not an executable WAR.

%s has no Main-Class attribute in META-INF/MANIFEST.MF, so `+"`java -jar`"+` cannot
run it. jar2native currently supports executable JARs and executable WARs,
but does not package a standalone Servlet container for arbitrary WAR files.

Deploy a plain WAR to a Servlet container (Tomcat/Jetty/...) instead.`,
        path.Base(appPath))
    }
    return &AppInfo{Kind: AppExecutableWAR, MainClass: mainClass, IsWAR: true, SpringBoot: springBoot}, nil
  }

  // JAR
  if mainClass == "" {
    return nil, fmt.Errorf(`JAR file is not executable: %s has no Main-Class attribute in META-INF/MANIFEST.MF.

jar2native requires a JAR that can be started with `+"`java -jar app.jar`"+`.
Rebuild your artifact with the Main-Class attribute (e.g. via the
maven-jar-plugin, the Spring Boot plugin, or `+"`jar cfe app.jar MainClass`"+`).`,
      path.Base(appPath))
  }

  kind := AppExecutableJAR
  if springBoot {
    kind = AppSpringBootJAR
  }
  return &AppInfo{Kind: kind, MainClass: mainClass, IsWAR: false, SpringBoot: springBoot}, nil
}
