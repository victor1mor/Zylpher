#!/usr/bin/env bash
set -euo pipefail

version=${1:-}
mode=${2:---check}
if [[ ! $version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ||
      ( $mode != --check && $mode != --tag ) || $# -gt 2 ]]; then
    echo "Uso: bash .github/scripts/release-readme.sh vX.Y.Z [--check|--tag]" >&2
    exit 1
fi

rendered=$(mktemp)
trap 'rm -f -- "$rendered"' EXIT
awk -v version="$version" '
    /^\[release v[0-9]/ || /^versao=v[0-9]/ || /^\$versao = \047v[0-9]/ ||
    /^git clone --branch v[0-9]/ || /^Para instalar a v[0-9]/ ||
    /^go get github\.com\/victor1mor\/Zylpher\/client@v[0-9]/ {
        count += gsub(/v[0-9]+\.[0-9]+\.[0-9]+/, version)
    }
    { print }
    END {
        if (count != 8) {
            print "As oito referências de instalação do README não foram encontradas." > "/dev/stderr"
            exit 1
        }
    }
' README.md > "$rendered"

if [[ $mode == --check ]]; then
    cmp -s README.md "$rendered" || { echo "As instruções do README precisam apontar para $version." >&2; exit 1; }
    exit 0
fi

[[ $(git branch --show-current) == main && -z $(git status --porcelain) ]] ||
    { echo "Prepare a release em um checkout limpo do main." >&2; exit 1; }
tag="refs/tags/$version"
git fetch --no-tags origin main
git merge-base --is-ancestor FETCH_HEAD HEAD ||
    { echo "Sincronize o main local com origin/main antes de criar a tag." >&2; exit 1; }
if git show-ref --verify --quiet "$tag"; then
    # ponytail: uma falha no push atômico deixa o commit e a tag locais
    # disponíveis para repetir o mesmo comando sem duplicar a preparação.
    [[ $(git rev-parse "$tag^{commit}") == "$(git rev-parse HEAD)" ]] && cmp -s README.md "$rendered" ||
        { echo "A tag local já existe e difere do main preparado." >&2; exit 1; }
else
    existing=$(git ls-remote --tags --refs origin "$tag")
    [[ -z $existing ]] || { echo "A tag $version já existe no origin." >&2; exit 1; }
    if ! cmp -s README.md "$rendered"; then
        cat "$rendered" > README.md
        git add -- README.md
        git commit -m "Atualizar instalação para o Zylpher $version"
    fi
    git tag -a "$version" -m "Zylpher $version"
fi

git push --atomic --no-follow-tags origin HEAD:refs/heads/main "$tag:$tag"
