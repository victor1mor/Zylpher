#!/usr/bin/env bash
set -euo pipefail

dir=$(mktemp -d)
trap 'rm -rf -- "$dir"' EXIT
export RELEASE_TEST_DIR="$dir"
export GH_REPO=example/test GITHUB_SERVER_URL=https://example.invalid
export RELEASE_TEST_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN GITHUB_ENTERPRISE_TOKEN

gh() {
    case "$1 $2" in
        'api repos/example/test/commits/v9.9.9') echo "$RELEASE_TEST_COMMIT" ;;
        'api repos/example/test/releases?per_page=100')
            if [[ ! -f "$RELEASE_TEST_DIR/created" ]]; then
                echo '[[]]'
            elif [[ ! -f "$RELEASE_TEST_DIR/listed" ]]; then
                touch "$RELEASE_TEST_DIR/listed"
                echo '[[]]'
            else
                printf '[[{"id":1,"tag_name":"v9.9.9","draft":true,"body":"<!-- zylpher-release:v9.9.9:%s -->","assets":[]}]]\n' \
                    "$RELEASE_TEST_COMMIT"
            fi ;;
        'release create') touch "$RELEASE_TEST_DIR/created" ;;
        'release upload')
            [[ $# -eq 6 && $3 == v9.9.9 ]] || return 90
            return 23 ;;
        *) return 91 ;;
    esac
}
export -f gh

(
    cd "$dir"
    printf '%s\n' source > zylpher-v9.9.9-source.tar.gz
    printf '%s\n' windows > zylpher-v9.9.9-windows-amd64.zip
    sha256sum ./*.tar.gz ./*.zip > SHA256SUMS
)

status=0
bash "$(dirname "$0")/publish-release.sh" v9.9.9 "$RELEASE_TEST_COMMIT" "$dir" || status=$?
[[ $status -eq 23 ]] || { echo "O publicador deveria retomar o rascunho e chegar ao upload. Status: $status" >&2; exit 1; }
echo "Consulta de rascunho: atraso inicial tratado."
