#!/usr/bin/env bash
# sync-upstream.sh refreshes what this port copies from upstream
# microsoft/mcp main and reruns the parity checks that need no Fabric tenant.
#
# It updates internal/tools/docs/resources, internal/parity/testdata/ref-tools.json
# and internal/parity/testdata/UPSTREAM, and writes a Markdown report of what
# changed upstream and how the checks went to $WORK/report.md. It exits 0 when
# upstream hasn't moved or every check passed, 1 when a check failed.
#
# Needs git, rsync, Go and the .NET 10 SDK. Environment:
#   WORK          scratch directory (default: a new temporary directory)
#   UPSTREAM_DIR  an existing microsoft/mcp checkout to update instead of cloning
#   FORCE=1       run even when upstream hasn't moved
set -euo pipefail

root=$(git rev-parse --show-toplevel)
pins=$root/internal/parity/testdata/UPSTREAM
work=${WORK:-$(mktemp -d)}
mkdir -p "$work"
report=$work/report.md

old_sha=$(awk '$1 == "microsoft/mcp" {print $2}' "$pins")
old_df=$(awk '$1 == "Microsoft.DataFactory.MCP.Core" {print $2}' "$pins")

upstream=${UPSTREAM_DIR:-$work/mcp}
if [[ -d $upstream/.git ]]; then
  git -C "$upstream" fetch -q --filter=blob:none origin main
  git -C "$upstream" checkout -q --detach FETCH_HEAD
else
  git clone -q --filter=blob:none https://github.com/microsoft/mcp.git "$upstream"
fi
new_sha=$(git -C "$upstream" rev-parse HEAD)
if ! git -C "$upstream" cat-file -e "$old_sha^{commit}" 2>/dev/null; then
  git -C "$upstream" fetch -q --filter=blob:none origin "$old_sha"
fi

if [[ $new_sha == "$old_sha" && ${FORCE:-} != 1 ]]; then
  echo "upstream is still at $old_sha"
  exit 0
fi

new_df=$(sed -n 's/.*"Microsoft.DataFactory.MCP.Core" Version="\([^"]*\)".*/\1/p' "$upstream/Directory.Packages.props")

echo "building upstream $new_sha"
dotnet build "$upstream/servers/Fabric.Mcp.Server/src/Fabric.Mcp.Server.csproj" -c Release -v quiet -nologo >"$work/dotnet-build.log" 2>&1 ||
  { tail -50 "$work/dotnet-build.log"; exit 1; }
export FABMCP_REF=$upstream/servers/Fabric.Mcp.Server/src/bin/Release/fabmcp

rsync -a --delete "$upstream/tools/Fabric.Mcp.Tools.Docs/src/Resources/" "$root/internal/tools/docs/resources/"
(cd "$root" && go test -count=1 ./internal/parity/ -run TestParityToolsList -update >/dev/null)
cat >"$pins" <<EOF
# The upstream versions this port matches; scripts/sync-upstream.sh updates it.
microsoft/mcp $new_sha
Microsoft.DataFactory.MCP.Core $new_df
EOF

# Areas upstream has that this port doesn't.
new_areas=$(comm -13 \
  <(printf '%s\n' Core DataFactory Docs OneLake) \
  <(ls "$upstream/tools" | sed -n 's/^Fabric\.Mcp\.Tools\.//p' | sort))

# check NAME CMD... runs a gate and records its outcome.
status=0
results=
check() {
  local name=$1
  shift
  if (cd "$root" && "$@") >"$work/$name.log" 2>&1; then
    results+="| $name | pass |"$'\n'
  else
    results+="| $name | **fail** |"$'\n'
    status=1
  fi
}
check unit go test -count=1 ./...
check tool-list go test -count=1 ./internal/parity/ -run TestParityToolsList
check namespace-mode go test -count=1 ./internal/parity/ -run TestParityNamespaceMode
check docs-calls go test -count=1 ./internal/parity/ -run TestParityCalls -parity.only '^docs_'

{
  echo "Syncs with upstream [microsoft/mcp@${new_sha:0:7}](https://github.com/microsoft/mcp/commit/$new_sha), up from [${old_sha:0:7}](https://github.com/microsoft/mcp/compare/$old_sha...$new_sha)."
  echo
  echo "This refreshes the embedded docs resources and the tool-list snapshot. Port any tool changes listed below by hand, then rerun the parity tests with \`FABMCP_REF\` against a tenant."
  if [[ $new_df != "$old_df" ]]; then
    echo
    echo "Upstream moved \`Microsoft.DataFactory.MCP.Core\` from $old_df to $new_df; compare [microsoft/DataFactory.MCP](https://github.com/microsoft/DataFactory.MCP) between those versions for datafactory changes."
  fi
  if [[ -n $new_areas ]]; then
    echo
    echo "New upstream tool areas not in this port: $(echo $new_areas | sed 's/ /, /g')."
  fi
  echo
  echo "## Checks"
  echo
  echo "These ran in the sync job; CI doesn't run on pull requests opened by GitHub Actions."
  echo
  echo "| Check | Result |"
  echo "|---|---|"
  printf '%s' "$results"
  for f in unit tool-list namespace-mode docs-calls; do
    if grep -q -- '--- FAIL\|^FAIL' "$work/$f.log"; then
      echo
      echo "<details><summary>$f failures</summary>"
      echo
      echo '```'
      grep -E -- '--- FAIL|_test.go:[0-9]+:|^FAIL' "$work/$f.log" | head -60
      echo '```'
      echo "</details>"
    fi
  done
  echo
  echo "## Upstream changes to port"
  echo
  echo '```'
  git -C "$upstream" diff --stat=120 "$old_sha" "$new_sha" -- \
    servers/Fabric.Mcp.Server tools/Fabric.Mcp.Tools.* core/Fabric.Mcp.Core \
    core/Microsoft.Mcp.Core/src/Areas/Server core/Microsoft.Mcp.Core/src/Commands \
    ':!tools/Fabric.Mcp.Tools.Docs/src/Resources' | tail -80
  echo '```'
} >"$report"

echo "report: $report"
exit $status
