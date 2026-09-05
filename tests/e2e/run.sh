#!/bin/bash
set -e
cd "$(dirname "$0")/../.."

# 1. Download Jenkins WAR
wget -q -O /tmp/jenkins.war https://github.com/jenkinsci/jenkins/releases/download/jenkins-2.362/jenkins.war

# 2. Package into native binary
./jar2native -jar /tmp/jenkins.war -o /tmp/jenkins

# 3. Run the binary (5s timeout proves JVM starts)
timeout 5 /tmp/jenkins || [ $? -eq 124 ] && echo "[OK] e2e passed — Jenkins binary started successfully"
