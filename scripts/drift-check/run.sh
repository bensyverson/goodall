#!/bin/zsh
# Runs the /drift-check Skill unattended, from launchd. launchd starts with a bare
# environment, so this names the paths claude and go live in. The log stays out of
# git, under local/.
set -u
repo="${0:A:h:h:h}"
cd "$repo" || exit 1
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/bin:/bin"
mkdir -p local
{
  echo "=== $(date -u +%FT%TZ)"
  claude -p "/drift-check" --output-format text
  echo "=== exit $?"
} >> local/drift-check.log 2>&1
